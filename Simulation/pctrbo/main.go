package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
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

type Transaction struct {
	TransactionID string `json:"transaction_id"`
	CardType      string `json:"card_type"`
	Token         string `json:"token"`
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

type DebtsRequest struct {
	Debts []carddb.PADebt `json:"debts"`
}

type DebtsResponse struct {
	Accepted bool `json:"accepted"`
	Received int  `json:"received"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func httpPostJSON(client *http.Client, url string, req any, resp any) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpResp, err := client.Do(httpReq)
	if err != nil {
		return err
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		slurp, _ := io.ReadAll(io.LimitReader(httpResp.Body, 4096))
		return fmt.Errorf("http %d: %s", httpResp.StatusCode, strings.TrimSpace(string(slurp)))
	}
	dec := json.NewDecoder(httpResp.Body)
	return dec.Decode(resp)
}

func main() {
	var listen string
	var proxyAddr string
	var proxyProto string
	var taskName string
	var advertise string
	var dsn string
	var paTask string
	var paBase string

	flag.StringVar(&listen, "listen", ":9102", "listen address")
	flag.StringVar(&proxyAddr, "proxy", "localhost:5100", "client proxy address host:port")
	flag.StringVar(&proxyProto, "proxy-proto", "udp", "client proxy transport: udp or tcp")
	flag.StringVar(&taskName, "task", "sim.pctrbo", "task name to register under")
	flag.StringVar(&advertise, "advertise", "", "address to register (default derives from -listen; should be reachable by other VMs)")
	flag.StringVar(&dsn, "db", "", "Postgres DSN (or set PCTRBO_DB_DSN / DATABASE_URL)")
	flag.StringVar(&paTask, "pa-task", "sim.pa", "task name to query for PA")
	flag.StringVar(&paBase, "pa", "", "fallback PA base URL (used if proxy query fails)")
	flag.Parse()

	if strings.TrimSpace(dsn) == "" {
		dsn = strings.TrimSpace(os.Getenv("PCTRBO_DB_DSN"))
	}
	if strings.TrimSpace(dsn) == "" {
		dsn = strings.TrimSpace(os.Getenv("DATABASE_URL"))
	}
	if strings.TrimSpace(dsn) == "" {
		log.Fatalf("missing postgres DSN: provide -db or set PCTRBO_DB_DSN/DATABASE_URL")
	}

	store, err := carddb.Open(dsn)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer store.Close()

	logger := log.New(os.Stdout, "[pctrbo] ", log.LstdFlags)
	proxyClient := simproxy.Client{Addr: proxyAddr, Proto: proxyProto, Timeout: 5 * time.Second}
	httpClient := &http.Client{Timeout: 5 * time.Second}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := store.EnsurePCTRTables(ctx); err != nil {
		cancel()
		log.Fatalf("ensure pctr tables: %v", err)
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

	// Register through proxy.
	if advertise == "" {
		if strings.HasPrefix(listen, ":") {
			advertise = "http://localhost" + listen
		} else if strings.HasPrefix(listen, "http://") || strings.HasPrefix(listen, "https://") {
			advertise = listen
		} else {
			advertise = "http://" + listen
		}
	}
	if err := proxyClient.Register(taskName, advertise); err != nil {
		logger.Printf("proxy register failed (task=%s addr=%s): %v", taskName, advertise, err)
	} else {
		logger.Printf("registered via proxy (task=%s addr=%s)", taskName, advertise)
	}
	go func() {
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		for range t.C {
			_ = proxyClient.Register(taskName, advertise)
		}
	}()

	resolvePA := func() (string, error) {
		addr, err := proxyClient.Query(paTask)
		if err == nil {
			return simproxy.EnsureHTTPBase(addr), nil
		}
		if strings.TrimSpace(paBase) != "" {
			return simproxy.EnsureHTTPBase(paBase), nil
		}
		return "", err
	}

	// Every 5 minutes: build journeys and report debts to PA.
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			journeys, err := store.RunPCTRJourneyConstruction(ctx)
			if err != nil {
				cancel()
				logger.Printf("journey construction error: %v", err)
				continue
			}
			owed, ids, err := store.GetUnreportedDebts(ctx)
			if err != nil {
				cancel()
				logger.Printf("get debts error: %v", err)
				continue
			}
			if len(owed) == 0 {
				cancel()
				if journeys > 0 {
					logger.Printf("journey construction: processed=%d (no debts to report)", journeys)
				}
				continue
			}
			pa, err := resolvePA()
			if err != nil || pa == "" {
				cancel()
				logger.Printf("PA resolve failed: %v", err)
				continue
			}
			debts := make([]carddb.PADebt, 0, len(owed))
			for _, o := range owed {
				debts = append(debts, carddb.PADebt{Token: o.Token, AmountCents: o.AmountCents})
			}
			var resp DebtsResponse
			url := strings.TrimRight(pa, "/") + "/debts"
			if err := httpPostJSON(httpClient, url, DebtsRequest{Debts: debts}, &resp); err != nil {
				cancel()
				logger.Printf("report debts failed: %v", err)
				continue
			}
			_ = store.MarkDebtsReported(ctx, ids)
			cancel()
			logger.Printf("reported debts: tokens=%d (journeys=%d)", len(debts), journeys)
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
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

		toInsert := make([]carddb.PCTRTransaction, 0, len(br.Transactions))
		for _, tx := range br.Transactions {
			if strings.ToUpper(strings.TrimSpace(tx.CardType)) != "PCTR" {
				writeJSON(w, http.StatusBadRequest, BatchResponse{Accepted: false, Received: 0})
				return
			}
			tapTime, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(tx.TapTime))
			if err != nil {
				writeJSON(w, http.StatusBadRequest, BatchResponse{Accepted: false, Received: 0})
				return
			}
			token := strings.TrimSpace(tx.Token)
			if token == "" {
				writeJSON(w, http.StatusBadRequest, BatchResponse{Accepted: false, Received: 0})
				return
			}
			toInsert = append(toInsert, carddb.PCTRTransaction{
				TransactionID: strings.TrimSpace(tx.TransactionID),
				Token:         token,
				GateID:        strings.TrimSpace(tx.GateID),
				StationID:     strings.TrimSpace(tx.StationID),
				TapTime:       tapTime,
				Allowed:       tx.Allowed,
				Reason:        strings.TrimSpace(tx.Reason),
			})
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := store.InsertPCTRTransactions(ctx, toInsert); err != nil {
			logger.Printf("insert transactions failed: %v", err)
			writeJSON(w, http.StatusServiceUnavailable, BatchResponse{Accepted: false, Received: 0})
			return
		}
		logger.Printf("received batch: count=%d", len(toInsert))
		writeJSON(w, http.StatusOK, BatchResponse{Accepted: true, Received: len(toInsert)})
	})

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
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
		_ = srv.Shutdown(ctx)
		cancel()
	}()
	logger.Printf("listening on %s", listen)
	fmt.Println("PCTRBO ready")
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		logger.Fatalf("server error: %v", err)
	}
}
