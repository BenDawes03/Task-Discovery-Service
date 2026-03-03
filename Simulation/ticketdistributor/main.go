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
    var csTask string
    var csBase string
    var pollInterval time.Duration

    flag.StringVar(&listen, "listen", ":9110", "listen address")
    flag.StringVar(&proxyAddr, "proxy", "localhost:5100", "client proxy address host:port")
    flag.StringVar(&proxyProto, "proxy-proto", "udp", "client proxy transport: udp or tcp")
    flag.StringVar(&taskName, "task", "sim.ticketdistributor", "task name to register under")
    flag.StringVar(&advertise, "advertise", "", "address to register (default derives from -listen)")
    flag.StringVar(&outDir, "outdir", ".", "directory to write per-station ticket files")
    flag.StringVar(&csTask, "cs-task", "sim.cs", "task name for CS to query via proxy")
    flag.StringVar(&csBase, "cs-base", "", "direct CS base URL (overrides proxy if set)")
    flag.DurationVar(&pollInterval, "poll-interval", 30*time.Second, "how often to poll CS for new manifests")
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
	// Track latest manifest ID by generated time
	var latestManifestID string
	var latestManifestTime time.Time
    // Poll CS for manifests instead of receiving pushes.
    if pollInterval <= 0 {
        pollInterval = 30 * time.Second
    }
    go func() {
        ticker := time.NewTicker(pollInterval)
        defer ticker.Stop()
        var last time.Time
        client := &http.Client{Timeout: 5 * time.Second}
        for range ticker.C {
            // resolve CS address
            csAddr := strings.TrimSpace(csBase)
            if csAddr == "" {
                a, _ := proxyClient.Query(csTask)
                csAddr = a
            }
            if csAddr == "" {
                // nothing to do
                continue
            }
            url := strings.TrimRight(simproxy.EnsureHTTPBase(csAddr), "/") + "/manifests"
            if !last.IsZero() {
                url = url + "?since=" + last.Format(time.RFC3339Nano)
            }
            req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
            if err != nil {
                logger.Printf("build request failed: %v", err)
                continue
            }
            resp, err := client.Do(req)
            if err != nil {
                logger.Printf("poll cs failed: %v", err)
                continue
            }
            var data []map[string]any
            dec := json.NewDecoder(resp.Body)
            if err := dec.Decode(&data); err != nil {
                _ = resp.Body.Close()
                logger.Printf("decode manifests failed: %v", err)
                continue
            }
            _ = resp.Body.Close()
            if len(data) == 0 {
                continue
            }
            mu.Lock()
            for _, m := range data {
                id, _ := m["id"].(string)
                payload := m["payload"]
                // payload is expected to be an object with tickets array
                ticketsIface, _ := payload.(map[string]any)["tickets"]
                tickets, _ := ticketsIface.([]any)
                byStation := map[string][][]string{}
                for _, ti := range tickets {
                    tmap, _ := ti.(map[string]any)
                    st, _ := tmap["station_id"].(string)
                    if st == "" {
                        continue
                    }
                    tid := fmt.Sprintf("%v", tmap["id"])
                    passenger, _ := tmap["passenger"].(string)
                    trainTime, _ := tmap["train_time"].(string)
                    byStation[st] = append(byStation[st], []string{tid, passenger, trainTime})
                }
                manifests[id] = map[string]string{}
                for st, rows := range byStation {
                    name := fmt.Sprintf("manifest_%s_%s.csv", id, st)
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
                    manifests[id][st] = path
                }
                logger.Printf("manifest polled id=%s stations=%d", id, len(manifests[id]))
                // update last to manifest's generated time if present
                if genRaw, ok := m["generated_at"].(string); ok {
                    if t, err := time.Parse(time.RFC3339Nano, genRaw); err == nil {
                        if t.After(last) {
                            last = t
                        }
                        // Track latest manifest by generated time
                        if t.After(latestManifestTime) {
                            latestManifestTime = t
                            latestManifestID = id
                        }
                    }
                }
            }
            mu.Unlock()
        }
    }()

    mux := http.NewServeMux()
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

	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) {
		// GET /latest?station=...
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		station := r.URL.Query().Get("station")
		if station == "" {
			http.Error(w, "missing station param", http.StatusBadRequest)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if latestManifestID == "" {
			http.Error(w, "no manifests available", http.StatusNotFound)
			return
		}
		m, ok := manifests[latestManifestID]
		if !ok {
			http.Error(w, "latest manifest not found", http.StatusNotFound)
			return
		}
		path, ok := m[station]
		if !ok {
			http.Error(w, "no tickets for this station", http.StatusNotFound)
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
