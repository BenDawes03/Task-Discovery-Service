package main

import (
	"context"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"tds/Simulation/simproxy"
	"tds/pkg/netutil"
)

type Transaction struct {
	TransactionID string `json:"transaction_id"`
	CardType      string `json:"card_type"` // "OY" or "PCTR"
	CardID        string `json:"card_id,omitempty"`
	Token         string `json:"token,omitempty"`
	GateID        string `json:"gate_id"`
	StationID     string `json:"station_id"`
	TapTime       string `json:"tap_time"`
	Allowed       bool   `json:"allowed"`
	Reason        string `json:"reason"`
}

type TransactionAck struct {
	Accepted  bool `json:"accepted"`
	BatchSize int  `json:"batch_size"` // current buffered count for this tx.CardType
}

type BatchRequest struct {
	Transactions []Transaction `json:"transactions"`
}

type BatchResponse struct {
	Accepted bool `json:"accepted"`
	Received int  `json:"received"`
}

type PerTypeBatcher struct {
	mu        sync.Mutex
	batchSize int
	byType    map[string][]Transaction
}

func NewPerTypeBatcher(batchSize int) *PerTypeBatcher {
	return &PerTypeBatcher{batchSize: batchSize, byType: map[string][]Transaction{"OY": {}, "PCTR": {}}}
}

// Add returns: buffered size for this type, and a full batch (size==batchSize) if ready.
func (b *PerTypeBatcher) Add(tx Transaction) (int, []Transaction) {
	b.mu.Lock()
	defer b.mu.Unlock()

	cur := b.byType[tx.CardType]
	cur = append(cur, tx)
	b.byType[tx.CardType] = cur
	if len(cur) < b.batchSize {
		return len(cur), nil
	}

	batch := make([]Transaction, b.batchSize)
	copy(batch, cur[:b.batchSize])
	b.byType[tx.CardType] = cur[b.batchSize:]
	return len(b.byType[tx.CardType]), batch
}

func (b *PerTypeBatcher) Size(cardType string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.byType[cardType])
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
	var stationID string
	var proxyAddr string
	var proxyProto string
	var taskName string
	var advertise string
	var oyboTask string
	var pctrboTask string
	var batchSize int
	flag.StringVar(&listen, "listen", ":9100", "listen address")
	flag.StringVar(&stationID, "station-id", "station-1", "station identifier")
	flag.StringVar(&proxyAddr, "proxy", "localhost:5100", "client proxy address host:port")
	flag.StringVar(&proxyProto, "proxy-proto", "udp", "client proxy transport: udp or tcp")
	flag.StringVar(&taskName, "task", "", "task name to register under (default: sim.station_computer.<station-id>)")
	flag.StringVar(&advertise, "advertise", "", "address to register (default derives from -listen; should be reachable by other VMs)")
	flag.StringVar(&oyboTask, "oybo-task", "sim.cs", "task name to query for CS (OY cards)")
	flag.StringVar(&pctrboTask, "pctrbo-task", "sim.pctrbo", "task name to query for PCTRBO")
	flag.IntVar(&batchSize, "batch-size", 5, "number of taps per card type before forwarding to BO")
	flag.Parse()
	if strings.TrimSpace(taskName) == "" {
		taskName = fmt.Sprintf("sim.station_computer.%s", strings.TrimSpace(stationID))
	}
	if batchSize <= 0 {
		batchSize = 5
	}

	logger := log.New(os.Stdout, "[station] ", log.LstdFlags)
	batcher := NewPerTypeBatcher(batchSize)

	// Register ourselves through the local client proxy (and refresh periodically so we don't age out).
	if advertise == "" {
		if strings.HasPrefix(listen, ":") {
			advertise = "http://localhost" + listen
		} else if strings.HasPrefix(listen, "http://") || strings.HasPrefix(listen, "https://") {
			advertise = listen
		} else {
			advertise = "http://" + listen
		}
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

	// Periodically resolve expected services via proxy and call them (health check).
	httpClient := &http.Client{Timeout: 2 * time.Second}
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			for _, depTask := range []string{oyboTask, pctrboTask} {
				addr, err := proxyClient.Query(depTask)
				if err != nil {
					continue
				}
				base := simproxy.EnsureHTTPBase(addr)
				url := strings.TrimRight(base, "/") + "/health"
				req, _ := http.NewRequest(http.MethodGet, url, nil)
				resp, err := httpClient.Do(req)
				if err != nil {
					logger.Printf("health check failed (task=%s url=%s): %v", depTask, url, err)
					continue
				}
				_ = resp.Body.Close()
			}
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("/transaction", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, TransactionAck{Accepted: false, BatchSize: 0})
			return
		}

		var tx Transaction
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&tx); err != nil {
			writeJSON(w, http.StatusBadRequest, TransactionAck{Accepted: false, BatchSize: 0})
			return
		}

		tx.CardType = strings.TrimSpace(tx.CardType)
		tx.CardType = strings.ToUpper(tx.CardType)
		tx.CardID = strings.TrimSpace(tx.CardID)
		tx.Token = strings.TrimSpace(tx.Token)
		tx.GateID = strings.TrimSpace(tx.GateID)
		// Append station number to every transaction we handle/forward.
		tx.StationID = stationID

		if tx.TransactionID == "" {
			tx.TransactionID = fmt.Sprintf("%s-%d", tx.GateID, time.Now().UnixNano())
		}
		if tx.TapTime == "" {
			tx.TapTime = time.Now().UTC().Format(time.RFC3339Nano)
		}
		if tx.CardType == "" || tx.GateID == "" {
			writeJSON(w, http.StatusBadRequest, TransactionAck{Accepted: false, BatchSize: 0})
			return
		}
		if tx.CardType != "OY" && tx.CardType != "PCTR" {
			writeJSON(w, http.StatusBadRequest, TransactionAck{Accepted: false, BatchSize: 0})
			return
		}
		if tx.CardType == "OY" && strings.TrimSpace(tx.CardID) == "" {
			writeJSON(w, http.StatusBadRequest, TransactionAck{Accepted: false, BatchSize: 0})
			return
		}
		if tx.CardType == "PCTR" && strings.TrimSpace(tx.Token) == "" {
			writeJSON(w, http.StatusBadRequest, TransactionAck{Accepted: false, BatchSize: 0})
			return
		}
		if !tx.Allowed {
			writeJSON(w, http.StatusBadRequest, TransactionAck{Accepted: false, BatchSize: 0})
			return
		}

		// Add to per-type buffer; if this makes a full batch (5) forward to BO.
		remaining, fullBatch := batcher.Add(tx)
		writeJSON(w, http.StatusOK, TransactionAck{Accepted: true, BatchSize: remaining})
		if len(fullBatch) == 0 {
			return
		}

		// Determine which BO gets this batch.
		boTask := oyboTask
		if tx.CardType == "PCTR" {
			boTask = pctrboTask
		}

		go func(batch []Transaction, task string) {
			addr, err := proxyClient.Query(task)
			if err != nil {
				logger.Printf("batch forward failed: resolve task=%s: %v", task, err)
				// Put the batch back into the buffer.
				batcher.mu.Lock()
				batcher.byType[batch[0].CardType] = append(batch, batcher.byType[batch[0].CardType]...)
				batcher.mu.Unlock()
				return
			}
			base := simproxy.EnsureHTTPBase(addr)
			url := strings.TrimRight(base, "/") + "/batch"
			var resp BatchResponse
			req := BatchRequest{Transactions: batch}
			if err := httpPostJSON(httpClient, url, req, &resp); err != nil {
				logger.Printf("batch forward failed: task=%s url=%s: %v", task, url, err)
				batcher.mu.Lock()
				batcher.byType[batch[0].CardType] = append(batch, batcher.byType[batch[0].CardType]...)
				batcher.mu.Unlock()
				return
			}
			logger.Printf("forwarded batch: type=%s count=%d -> %s (accepted=%v)", batch[0].CardType, len(batch), task, resp.Accepted)
		}(fullBatch, boTask)
	})

	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
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

	logger.Printf("listening on %s (station-id=%s batchSize=%d)", listen, stationID, batchSize)
	fmt.Println("Station Computer ready")
	if err := server.Serve(ln); err != nil && err != http.ErrServerClosed {
		logger.Fatalf("server error: %v", err)
	}
}
