package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"tds/Simulation/carddb"
	"tds/Simulation/simproxy"
	"tds/pkg/netutil"
)

type ValidateRequest struct {
	CardID    string `json:"card_id"`
	GateID    string `json:"gate_id"`
	StationID string `json:"station_id"`
	TapTime   string `json:"tap_time"`
}

type ValidateResponse struct {
	Valid  bool   `json:"valid"`
	Reason string `json:"reason"`
}

type Transaction struct {
	TransactionID string `json:"transaction_id"`
	CardType      string `json:"card_type"`
	CardID        string `json:"card_id"`
	GateID        string `json:"gate_id"`
	StationID     string `json:"station_id"`
	TapTime       string `json:"tap_time"`
	Allowed       bool   `json:"allowed"`
	Reason        string `json:"reason"`
}

type BatchRequest struct {
	Transactions []Transaction `json:"transactions"`
}

type BatchResponse struct {
	Accepted bool `json:"accepted"`
	Received int  `json:"received"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func main() {
	var listen string
	var proxyAddr string
	var proxyProto string
	var taskName string
	var advertise string
	var dsn string
	var seedWorkingCards int
	var seedStart int
	var seedBalanceCents int64

	flag.StringVar(&listen, "listen", ":9101", "listen address")
	flag.StringVar(&proxyAddr, "proxy", "localhost:5100", "client proxy address host:port")
	flag.StringVar(&proxyProto, "proxy-proto", "udp", "client proxy transport: udp or tcp")
	flag.StringVar(&taskName, "task", "sim.cs", "task name to register under")
	flag.StringVar(&advertise, "advertise", "", "address to register (default derives from -listen; should be reachable by other VMs)")
	flag.StringVar(&dsn, "db", "", "Postgres DSN (or set CS_DB_DSN / DATABASE_URL)")
	flag.IntVar(&seedWorkingCards, "seed-working-cards", 0, "seed N additional working cards (active, unblocked, balance>0) into the cards table")
	flag.IntVar(&seedStart, "seed-start", 10000, "starting card_id number for seeded working cards")
	flag.Int64Var(&seedBalanceCents, "seed-balance-cents", 500, "balance_cents for seeded working cards")
	flag.Parse()

	if strings.TrimSpace(advertise) == "" {
		advertise = strings.TrimSpace(os.Getenv("CS_ADVERTISE"))
	}

	if strings.TrimSpace(dsn) == "" {
		dsn = strings.TrimSpace(os.Getenv("CS_DB_DSN"))
	}
	if strings.TrimSpace(dsn) == "" {
		dsn = strings.TrimSpace(os.Getenv("DATABASE_URL"))
	}
	if strings.TrimSpace(dsn) == "" {
		log.Fatalf("missing postgres DSN: provide -db or set CS_DB_DSN/DATABASE_URL")
	}

	store, err := carddb.Open(dsn)
	if err != nil {
		log.Fatalf("open card db: %v", err)
	}
	defer store.Close()

	logger := log.New(os.Stdout, "[cs] ", log.LstdFlags)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := store.EnsureSchema(ctx); err != nil {
		cancel()
		log.Fatalf("ensure cards schema: %v", err)
	}
	if err := store.EnsureCSTables(ctx); err != nil {
		cancel()
		log.Fatalf("ensure cs tables: %v", err)
	}
	seedCards := []carddb.Record{
		{CardID: "1001", Active: true, Blocked: false, BalanceCents: 500},
		{CardID: "1002", Active: true, Blocked: false, BalanceCents: 0},
		{CardID: "1003", Active: false, Blocked: false, BalanceCents: 250},
		{CardID: "1999", Active: true, Blocked: true, BalanceCents: 999},
	}
	if err := store.Seed(ctx, seedCards); err != nil {
		cancel()
		log.Fatalf("seed cards: %v", err)
	}
	if seedWorkingCards > 0 {
		if seedStart < 0 {
			seedStart = 0
		}
		if seedBalanceCents <= 0 {
			seedBalanceCents = 500
		}
		const batchSize = 1000
		seeded := 0
		for seeded < seedWorkingCards {
			n := seedWorkingCards - seeded
			if n > batchSize {
				n = batchSize
			}
			batch := make([]carddb.Record, 0, n)
			for i := 0; i < n; i++ {
				id := fmt.Sprintf("%d", seedStart+seeded+i)
				batch = append(batch, carddb.Record{CardID: id, Active: true, Blocked: false, BalanceCents: seedBalanceCents})
			}
			if err := store.Seed(ctx, batch); err != nil {
				cancel()
				log.Fatalf("seed working cards: %v", err)
			}
			seeded += n
		}
		logger.Printf("seeded working cards: count=%d start=%d", seedWorkingCards, seedStart)
	}
	seedCosts := []carddb.StationCost{
		{FromStation: "station-1", ToStation: "station-1", CostCents: 0},
		{FromStation: "station-1", ToStation: "station-2", CostCents: 150},
		{FromStation: "station-2", ToStation: "station-3", CostCents: 200},
		{FromStation: "station-1", ToStation: "station-3", CostCents: 300},
	}
	if err := store.SeedStationCosts(ctx, seedCosts); err != nil {
		cancel()
		log.Fatalf("seed costs: %v", err)
	}
	cancel()

	// Register ourselves through the local client proxy (and refresh periodically so we don't age out).
	if advertise == "" {
		advertise = simproxy.DeriveHTTPAdvertise(listen)
	}
	proxyClient := simproxy.Client{Addr: proxyAddr, Proto: proxyProto, Timeout: 5 * time.Second}
	if err := proxyClient.Register(taskName, advertise); err != nil {
		logger.Printf("proxy register failed (task=%s addr=%s): %v", taskName, advertise, err)
	} else {
		logger.Printf("registered via proxy (task=%s addr=%s)", taskName, advertise)
	}
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if err := proxyClient.Register(taskName, advertise); err != nil {
				logger.Printf("proxy re-register failed (task=%s): %v", taskName, err)
			}
		}
	}()

	// Every 5 minutes, run JourneyConstruction.
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			journeys, err := store.RunJourneyConstruction(ctx)
			cancel()
			if err != nil {
				logger.Printf("journey construction error: %v", err)
				continue
			}
			if journeys > 0 {
				logger.Printf("journey construction: processed=%d", journeys)
			}
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := store.EnsureSchema(ctx); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("db unavailable"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("/validate", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, ValidateResponse{Valid: false, Reason: "method not allowed"})
			return
		}

		var req ValidateRequest
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, ValidateResponse{Valid: false, Reason: "invalid json"})
			return
		}

		req.CardID = strings.TrimSpace(req.CardID)
		if req.CardID == "" {
			writeJSON(w, http.StatusBadRequest, ValidateResponse{Valid: false, Reason: "missing card_id"})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		valid, reason, err := store.Validate(ctx, req.CardID)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, ValidateResponse{Valid: false, Reason: "db error"})
			return
		}
		writeJSON(w, http.StatusOK, ValidateResponse{Valid: valid, Reason: reason})
	})

	mux.HandleFunc("/batch", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, BatchResponse{Accepted: false, Received: 0})
			return
		}
		var br BatchRequest
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&br); err != nil {
			writeJSON(w, http.StatusBadRequest, BatchResponse{Accepted: false, Received: 0})
			return
		}
		if len(br.Transactions) == 0 {
			writeJSON(w, http.StatusBadRequest, BatchResponse{Accepted: false, Received: 0})
			return
		}

		toInsert := make([]carddb.CSTransaction, 0, len(br.Transactions))
		for _, tx := range br.Transactions {
			if strings.ToUpper(strings.TrimSpace(tx.CardType)) != "OY" {
				writeJSON(w, http.StatusBadRequest, BatchResponse{Accepted: false, Received: 0})
				return
			}
			tapTime, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(tx.TapTime))
			if err != nil {
				writeJSON(w, http.StatusBadRequest, BatchResponse{Accepted: false, Received: 0})
				return
			}
			toInsert = append(toInsert, carddb.CSTransaction{
				TransactionID: strings.TrimSpace(tx.TransactionID),
				CardID:        strings.TrimSpace(tx.CardID),
				GateID:        strings.TrimSpace(tx.GateID),
				StationID:     strings.TrimSpace(tx.StationID),
				TapTime:       tapTime,
				Allowed:       tx.Allowed,
				Reason:        strings.TrimSpace(tx.Reason),
			})
		}

		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := store.InsertCSTransactions(ctx, toInsert); err != nil {
			logger.Printf("insert transactions failed: %v", err)
			writeJSON(w, http.StatusServiceUnavailable, BatchResponse{Accepted: false, Received: 0})
			return
		}
		logger.Printf("received batch: count=%d", len(toInsert))
		writeJSON(w, http.StatusOK, BatchResponse{Accepted: true, Received: len(toInsert)})
	})

	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	ln, err := netutil.ListenTCP(listen)
	if err != nil {
		logger.Fatalf("listen %s: %v", listen, err)
	}
	defer ln.Close()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = server.Shutdown(ctx)
		cancel()
	}()
	logger.Printf("listening on %s", listen)
	fmt.Println("CS ready")
	if err := server.Serve(ln); err != nil && err != http.ErrServerClosed {
		logger.Fatalf("server error: %v", err)
	}
}
