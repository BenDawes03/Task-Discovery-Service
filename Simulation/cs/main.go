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
	var journeyInterval time.Duration
	var ticketInterval time.Duration
	var distributorTask string
	var seedTickets bool
	var seedTicketStations string

	flag.StringVar(&listen, "listen", ":9101", "listen address")
	flag.StringVar(&proxyAddr, "proxy", "localhost:5100", "client proxy address host:port")
	flag.StringVar(&proxyProto, "proxy-proto", "udp", "client proxy transport: udp or tcp")
	flag.StringVar(&taskName, "task", "sim.cs", "task name to register under")
	flag.StringVar(&advertise, "advertise", "", "address to register (default derives from -listen; should be reachable by other VMs)")
	flag.StringVar(&dsn, "db", "", "Postgres DSN (or set CS_DB_DSN / DATABASE_URL)")
	flag.IntVar(&seedWorkingCards, "seed-working-cards", 0, "seed N additional working cards (active, unblocked, balance>0) into the cards table")
	flag.IntVar(&seedStart, "seed-start", 10000, "starting card_id number for seeded working cards")
	flag.Int64Var(&seedBalanceCents, "seed-balance-cents", 500, "balance_cents for seeded working cards")
	flag.DurationVar(&journeyInterval, "journey-interval", 5*time.Minute, "how often to run journey construction (set smaller for testing)")
	flag.DurationVar(&ticketInterval, "ticket-interval", 5*time.Minute, "how often to generate ticket manifests and notify distributor")
	flag.StringVar(&distributorTask, "distributor-task", "sim.ticketdistributor", "task name for ticket distributor to notify via proxy")
	flag.BoolVar(&seedTickets, "seed-tickets", true, "seed initial tickets for next 15 minutes on startup")
	flag.StringVar(&seedTicketStations, "seed-ticket-stations", "1,2,3", "comma-separated list of station IDs to seed tickets for")
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
	if err := store.EnsureTicketTables(ctx); err != nil {
		cancel()
		log.Fatalf("ensure ticket tables: %v", err)
	}
	if err := store.EnsureManifestTables(ctx); err != nil {
		cancel()
		log.Fatalf("ensure manifest tables: %v", err)
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
	seedCosts := []carddb.StationCost{
		{FromStation: "1", ToStation: "1", CostCents: 0},
		{FromStation: "1", ToStation: "2", CostCents: 150},
		{FromStation: "2", ToStation: "3", CostCents: 200},
		{FromStation: "1", ToStation: "3", CostCents: 300},
	}
	if err := store.SeedStationCosts(ctx, seedCosts); err != nil {
		cancel()
		log.Fatalf("seed costs: %v", err)
	}

	if seedTickets {
		stations := strings.Split(strings.TrimSpace(seedTicketStations), ",")
		var validStations []string
		for _, s := range stations {
			if trimmed := strings.TrimSpace(s); trimmed != "" {
				validStations = append(validStations, trimmed)
			}
		}
		if len(validStations) > 0 {
			if err := store.SeedTickets(ctx, validStations); err != nil {
				cancel()
				log.Fatalf("seed tickets: %v", err)
			}
			logger.Printf("seeded tickets for next 15 minutes: stations=%v", validStations)
		}
	}
	cancel()

	if seedWorkingCards > 0 {
		if seedStart < 0 {
			seedStart = 0
		}
		if seedBalanceCents <= 0 {
			seedBalanceCents = 500
		}
		seedCtx, seedCancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer seedCancel()
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
			if err := store.Seed(seedCtx, batch); err != nil {
				log.Fatalf("seed working cards: %v", err)
			}
			seeded += n
		}
		logger.Printf("seeded working cards: count=%d start=%d", seedWorkingCards, seedStart)
	}

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

	// Periodically run JourneyConstruction.
	go func() {
		if journeyInterval <= 0 {
			journeyInterval = 5 * time.Minute
		}
		ticker := time.NewTicker(journeyInterval)
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

	// Periodically generate ticket manifests and store in DB for distributors to poll.
	go func() {
		if ticketInterval <= 0 {
			ticketInterval = 5 * time.Minute
		}
		ticker := time.NewTicker(ticketInterval)
		defer ticker.Stop()
		var lastManifestTime time.Time
		for range ticker.C {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			// Find tickets added since last manifest
			newTickets, err := store.GetTicketsCreatedSince(ctx, lastManifestTime)
			if err != nil {
				cancel()
				logger.Printf("ticket query error: %v", err)
				continue
			}
			if len(newTickets) == 0 {
				cancel()
				continue
			}
			// Build manifest of active tickets departing in the next 15 minutes
			now := time.Now().UTC()
			activeTickets, err := store.GetActiveTicketsWithin(ctx, now, 15*time.Minute)
			if err != nil {
				cancel()
				logger.Printf("active tickets query error: %v", err)
				continue
			}
			if len(activeTickets) == 0 {
				// Nothing to send
				lastManifestTime = time.Now().UTC()
				cancel()
				continue
			}

			manifest := struct {
				ID         string            `json:"id"`
				Generated  time.Time         `json:"generated_at"`
				Tickets    []map[string]any  `json:"tickets"`
			}{
				ID:        fmt.Sprintf("m-%d", time.Now().UTC().Unix()),
				Generated: time.Now().UTC(),
				Tickets:   make([]map[string]any, 0, len(activeTickets)),
			}
			for _, t := range activeTickets {
				manifest.Tickets = append(manifest.Tickets, map[string]any{
					"id": t.ID,
					"station_id": t.StationID,
					"train_time": t.TrainTime.Format(time.RFC3339Nano),
					"passenger": t.Passenger,
				})
			}

			// Store manifest in DB for distributors to poll via HTTP
			body, _ := json.Marshal(manifest)
			if err := store.InsertManifest(ctx, manifest.ID, manifest.Generated, string(body)); err != nil {
				cancel()
				logger.Printf("store manifest failed: %v", err)
				continue
			}
			lastManifestTime = time.Now().UTC()
			logger.Printf("manifest stored: id=%s tickets=%d", manifest.ID, len(manifest.Tickets))
			cancel()
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

	// Distributors poll these endpoints to discover new manifests and fetch them.
	mux.HandleFunc("/manifests", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		sinceStr := r.URL.Query().Get("since")
		var since time.Time
		if sinceStr != "" {
			if t, err := time.Parse(time.RFC3339Nano, sinceStr); err == nil {
				since = t
			}
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		manifests, err := store.GetManifestsSince(ctx, since)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "db error"})
			return
		}
		out := make([]map[string]any, 0, len(manifests))
		for _, m := range manifests {
			var payload any
			_ = json.Unmarshal([]byte(m.Payload), &payload)
			out = append(out, map[string]any{"id": m.ID, "generated_at": m.Generated, "payload": payload})
		}
		writeJSON(w, http.StatusOK, out)
	})

	mux.HandleFunc("/manifest", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		id := r.URL.Query().Get("id")
		if id == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing id"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		m, err := store.GetManifestByID(ctx, id)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "manifest not found"})
			return
		}
		var payload any
		_ = json.Unmarshal([]byte(m.Payload), &payload)
		writeJSON(w, http.StatusOK, map[string]any{"id": m.ID, "generated_at": m.Generated, "payload": payload})
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

	mux.HandleFunc("/ticket", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		var req struct {
			StationID string `json:"station_id"`
			TrainTime string `json:"train_time"`
			Passenger string `json:"passenger"`
		}
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if strings.TrimSpace(req.StationID) == "" || strings.TrimSpace(req.TrainTime) == "" || strings.TrimSpace(req.Passenger) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing fields"})
			return
		}
		tt, err := time.Parse(time.RFC3339Nano, req.TrainTime)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid train_time"})
			return
		}
		t := carddb.Ticket{StationID: strings.TrimSpace(req.StationID), TrainTime: tt, Passenger: strings.TrimSpace(req.Passenger)}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		id, err := store.InsertTicket(ctx, t)
		if err != nil {
			logger.Printf("insert ticket failed: %v", err)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "db error"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
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
