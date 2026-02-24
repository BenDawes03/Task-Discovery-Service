package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"tds/Simulation/simproxy"
	"tds/pkg/netutil"
)

type Manifest struct {
	ID        string          `json:"id"`
	Generated time.Time       `json:"generated_at"`
	Tickets   []ManifestTicket `json:"tickets"`
}

type ManifestTicket struct {
	ID        any    `json:"id"`
	StationID string `json:"station_id"`
	TrainTime string `json:"train_time"`
	Passenger string `json:"passenger"`
}

func main() {
	var listen string
	var proxyAddr string
	var proxyProto string
	var taskName string
	var advertise string
	var outDir string

	flag.StringVar(&listen, "listen", ":9110", "listen address")
	flag.StringVar(&proxyAddr, "proxy", "localhost:5100", "client proxy address host:port")
	flag.StringVar(&proxyProto, "proxy-proto", "udp", "client proxy transport: udp or tcp")
	flag.StringVar(&taskName, "task", "sim.ticketdistributor", "task name to register under")
	flag.StringVar(&advertise, "advertise", "", "address to register (default derives from -listen)")
	flag.StringVar(&outDir, "outdir", ".", "directory to write per-station ticket files")
	flag.Parse()

	if advertise == "" {
		advertise = simproxy.DeriveHTTPAdvertise(listen)
	}

	logger := log.New(os.Stdout, "[ticketdist] ", log.LstdFlags)

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

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		logger.Fatalf("create outdir: %v", err)
	}

	var mu sync.Mutex
	// manifestID -> map[station]path
	manifests := map[string]map[string]string{}

	mux := http.NewServeMux()

	mux.HandleFunc("/manifest", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var m Manifest
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&m); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if m.ID == "" {
			http.Error(w, "missing id", http.StatusBadRequest)
			return
		}

		// Group tickets by station and write CSV per station
		byStation := map[string][][]string{}
		for _, t := range m.Tickets {
			st := strings.TrimSpace(t.StationID)
			if st == "" {
				continue
			}
			byStation[st] = append(byStation[st], []string{fmt.Sprintf("%v", t.ID), t.Passenger, t.TrainTime})
		}

		mu.Lock()
		defer mu.Unlock()
		manifests[m.ID] = map[string]string{}
		for st, rows := range byStation {
			name := fmt.Sprintf("manifest_%s_%s.csv", m.ID, st)
			path := filepath.Join(outDir, name)
			f, err := os.Create(path)
			if err != nil {
				logger.Printf("create csv failed: %v", err)
				continue
			}
			cw := csv.NewWriter(f)
			_ = cw.Write([]string{"ticket_id", "passenger", "train_time"})
			for _, r := range rows {
				_ = cw.Write(r)
			}
			cw.Flush()
			_ = f.Close()
			manifests[m.ID][st] = path
		}

		logger.Printf("manifest received id=%s stations=%d", m.ID, len(manifests[m.ID]))
		writeJSON := func(w http.ResponseWriter, status int, v any) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(v)
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": m.ID, "stations": len(manifests[m.ID])})
	})

	mux.HandleFunc("/file", func(w http.ResponseWriter, r *http.Request) {
		// GET /file?manifest_id=...&station=...
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		manifestID := r.URL.Query().Get("manifest_id")
		station := r.URL.Query().Get("station")
		if manifestID == "" || station == "" {
			http.Error(w, "missing params", http.StatusBadRequest)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		m, ok := manifests[manifestID]
		if !ok {
			http.Error(w, "manifest not found", http.StatusNotFound)
			return
		}
		path, ok := m[station]
		if !ok {
			http.Error(w, "station file not found", http.StatusNotFound)
			return
		}
		http.ServeFile(w, r, path)
	})

	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	ln, err := netutil.ListenTCP(listen)
	if err != nil {
		logger.Fatalf("listen %s: %v", listen, err)
	}
	defer ln.Close()

	sigCh := make(chan os.Signal, 1)
	// graceful shutdown
	go func() {
		<-sigCh
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = server.Shutdown(ctx)
		cancel()
	}()
	logger.Printf("listening on %s", listen)
	if err := server.Serve(ln); err != nil && err != http.ErrServerClosed {
		logger.Fatalf("server error: %v", err)
	}
}
