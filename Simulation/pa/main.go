package main

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"tds/Simulation/carddb"
	"tds/Simulation/pctrcrypto"
	"tds/Simulation/simproxy"
	"tds/pkg/netutil"
)

type TokenizeRequest struct {
	CiphertextB64 string `json:"ciphertext_b64"`
}

type TokenizeResponse struct {
	OK     bool   `json:"ok"`
	Token  string `json:"token,omitempty"`
	Reason string `json:"reason"`
}

type DebtsRequest struct {
	Debts []carddb.PADebt `json:"debts"`
}

type DebtsResponse struct {
	Accepted bool `json:"accepted"`
	Received int  `json:"received"`
}

type RestitutionResponse struct {
	File       string `json:"file"`
	Rows       int    `json:"rows"`
	TotalCents int64  `json:"total_cents"`
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
	var privateKeyPath string
	var minFundsCents int64
	var outDir string
	var seedWorkingCards int
	var seedStart int
	var seedBalanceCents int64

	flag.StringVar(&listen, "listen", ":9103", "listen address")
	flag.StringVar(&proxyAddr, "proxy", "localhost:5100", "client proxy address host:port")
	flag.StringVar(&proxyProto, "proxy-proto", "udp", "client proxy transport: udp or tcp")
	flag.StringVar(&taskName, "task", "sim.pa", "task name to register under")
	flag.StringVar(&advertise, "advertise", "", "address to register (default derives from -listen; should be reachable by other VMs)")
	flag.StringVar(&dsn, "db", "", "Postgres DSN (or set PA_DB_DSN / DATABASE_URL)")
	flag.StringVar(&privateKeyPath, "private-key", "", "RSA private key PEM for decrypting gate-encrypted card data")
	flag.Int64Var(&minFundsCents, "min-funds-cents", 1, "minimum funds required to return token")
	flag.StringVar(&outDir, "outdir", ".", "directory to write restitution files")
	flag.IntVar(&seedWorkingCards, "seed-working-cards", 0, "seed N additional working PCTR cards (active, unblocked, balance>=min-funds) into the cards table")
	flag.IntVar(&seedStart, "seed-start", 20000, "starting card_id number for seeded working PCTR cards")
	flag.Int64Var(&seedBalanceCents, "seed-balance-cents", 300, "balance_cents for seeded working PCTR cards")
	flag.Parse()

	if strings.TrimSpace(advertise) == "" {
		advertise = strings.TrimSpace(os.Getenv("PA_ADVERTISE"))
	}

	if strings.TrimSpace(dsn) == "" {
		dsn = strings.TrimSpace(os.Getenv("PA_DB_DSN"))
	}
	if strings.TrimSpace(dsn) == "" {
		dsn = strings.TrimSpace(os.Getenv("DATABASE_URL"))
	}
	if strings.TrimSpace(dsn) == "" {
		log.Fatalf("missing postgres DSN: provide -db or set PA_DB_DSN/DATABASE_URL")
	}
	if strings.TrimSpace(privateKeyPath) == "" {
		privateKeyPath = strings.TrimSpace(os.Getenv("PA_PRIVATE_KEY"))
	}
	if strings.TrimSpace(privateKeyPath) == "" {
		log.Fatalf("missing PA private key: provide -private-key or set PA_PRIVATE_KEY")
	}
	if minFundsCents <= 0 {
		minFundsCents = 1
	}

	priv, err := pctrcrypto.LoadRSAPrivateKeyFromPEMFile(privateKeyPath)
	if err != nil {
		log.Fatalf("load private key: %v", err)
	}

	store, err := carddb.Open(dsn)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer store.Close()

	logger := log.New(os.Stdout, "[pa] ", log.LstdFlags)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := store.EnsureSchema(ctx); err != nil {
		cancel()
		log.Fatalf("ensure cards schema: %v", err)
	}
	if err := store.EnsurePATables(ctx); err != nil {
		cancel()
		log.Fatalf("ensure pa tables: %v", err)
	}
	seedCards := []carddb.Record{
		{CardID: "2001", Active: true, Blocked: false, BalanceCents: 300},
		{CardID: "2002", Active: true, Blocked: false, BalanceCents: 0},
		{CardID: "2003", Active: true, Blocked: false, BalanceCents: 100},
		{CardID: "2999", Active: true, Blocked: true, BalanceCents: 999},
	}
	if err := store.Seed(ctx, seedCards); err != nil {
		cancel()
		log.Fatalf("seed cards: %v", err)
	}
	cancel()

	if seedWorkingCards > 0 {
		if seedStart < 0 {
			seedStart = 0
		}
		if seedBalanceCents <= 0 {
			seedBalanceCents = 300
		}
		if seedBalanceCents < minFundsCents {
			seedBalanceCents = minFundsCents
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
		logger.Printf("seeded working PCTR cards: count=%d start=%d", seedWorkingCards, seedStart)
	}

	// Register through proxy.
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
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		for range t.C {
			_ = proxyClient.Register(taskName, advertise)
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("/tokenize", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, TokenizeResponse{OK: false, Reason: "method not allowed"})
			return
		}
		var req TokenizeRequest
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, TokenizeResponse{OK: false, Reason: "invalid json"})
			return
		}
		ct, err := base64.StdEncoding.DecodeString(strings.TrimSpace(req.CiphertextB64))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, TokenizeResponse{OK: false, Reason: "invalid ciphertext"})
			return
		}

		cardData, err := decryptCardData(priv, ct)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, TokenizeResponse{OK: false, Reason: "decrypt failed"})
			return
		}
		cardID := strings.TrimSpace(string(cardData))
		if cardID == "" {
			writeJSON(w, http.StatusBadRequest, TokenizeResponse{OK: false, Reason: "empty card"})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		has, reason, err := store.CardHasMinFunds(ctx, cardID, minFundsCents)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, TokenizeResponse{OK: false, Reason: "db error"})
			return
		}
		if !has {
			writeJSON(w, http.StatusOK, TokenizeResponse{OK: false, Reason: reason})
			return
		}
		token, err := store.GetOrCreateToken(ctx, cardID)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, TokenizeResponse{OK: false, Reason: "db error"})
			return
		}
		writeJSON(w, http.StatusOK, TokenizeResponse{OK: true, Token: token, Reason: "ok"})
	})

	mux.HandleFunc("/debts", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, DebtsResponse{Accepted: false, Received: 0})
			return
		}
		var req DebtsRequest
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, DebtsResponse{Accepted: false, Received: 0})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := store.InsertDebts(ctx, req.Debts); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, DebtsResponse{Accepted: false, Received: 0})
			return
		}
		writeJSON(w, http.StatusOK, DebtsResponse{Accepted: true, Received: len(req.Debts)})
	})

	mux.HandleFunc("/restitution", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		rows, ids, err := store.BuildRestitution(ctx)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "db error"})
			return
		}
		if len(rows) == 0 {
			writeJSON(w, http.StatusOK, RestitutionResponse{File: "", Rows: 0, TotalCents: 0})
			return
		}

		if err := os.MkdirAll(outDir, 0o755); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "mkdir failed"})
			return
		}
		name := fmt.Sprintf("restitution_%s.csv", time.Now().UTC().Format("20060102_150405"))
		path := filepath.Join(outDir, name)
		f, err := os.Create(path)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create failed"})
			return
		}
		cw := csv.NewWriter(f)
		_ = cw.Write([]string{"card_id", "amount_cents"})
		var total int64
		for _, row := range rows {
			total += row.AmountCents
			_ = cw.Write([]string{row.CardID, fmt.Sprintf("%d", row.AmountCents)})
		}
		cw.Flush()
		_ = f.Close()

		_ = store.MarkDebtsExported(ctx, ids)
		writeJSON(w, http.StatusOK, RestitutionResponse{File: path, Rows: len(rows), TotalCents: total})
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
	fmt.Println("PA ready")
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		logger.Fatalf("server error: %v", err)
	}
}

func decryptCardData(priv *rsa.PrivateKey, ciphertext []byte) ([]byte, error) {
	return pctrcrypto.DecryptCardData(priv, ciphertext)
}
