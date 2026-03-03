package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"tds/Simulation/pctrcrypto"
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

type TokenizeRequest struct {
	CiphertextB64 string `json:"ciphertext_b64"`
}

type TokenizeResponse struct {
	OK     bool   `json:"ok"`
	Token  string `json:"token,omitempty"`
	Reason string `json:"reason"`
}

type Transaction struct {
	TransactionID string `json:"transaction_id"`
	CardType      string `json:"card_type"`
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
	BatchSize int  `json:"batch_size"`
}

type TapRequest struct {
	Tap      string `json:"tap"` // e.g. "OY:1001" or "PCTR:2001"
	CardType string `json:"card_type,omitempty"`
	CardID   string `json:"card_id,omitempty"`
}

type TapResponse struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason"`
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

func parseTap(line string) (cardType string, cardID string, err error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", "", errors.New("empty tap")
	}

	// Expected formats:
	//   OY:1001
	//   PCTR:2001
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return "", "", errors.New("expected format <OY|PCTR>:<card_id>")
	}

	ct := strings.ToUpper(strings.TrimSpace(parts[0]))
	cid := strings.TrimSpace(parts[1])
	if ct != "OY" && ct != "PCTR" {
		return "", "", errors.New("card type must be OY or PCTR")
	}
	if cid == "" {
		return "", "", errors.New("missing card id")
	}
	return ct, cid, nil
}

func main() {
	var gateID string
	var listen string
	var stationBase string
	var oyboBase string
	var paBase string
	var stationID string
	var proxyAddr string
	var proxyProto string
	var stationTask string
	var oyboTask string
	var paTask string
	var pctrPublicKeyPath string

	flag.StringVar(&gateID, "id", "gate-1", "gate identifier")
	flag.StringVar(&listen, "listen", ":9200", "listen address for HTTP tap input")
	flag.StringVar(&stationID, "station-id", "1", "station computer identifier")
	flag.StringVar(&proxyAddr, "proxy", "localhost:5100", "client proxy address host:port")
	flag.StringVar(&proxyProto, "proxy-proto", "udp", "client proxy transport: udp or tcp")
	flag.StringVar(&stationTask, "station-task", "", "task name to query for station computer (default: station-Computer-<station-id>)")
	flag.StringVar(&oyboTask, "oybo-task", "sim.cs", "task name to query for CS (OY cards)")
	flag.StringVar(&paTask, "pa-task", "sim.pa", "task name to query for PA (PCTR tokenization)")
	flag.StringVar(&stationBase, "station", "", "fallback station computer base URL (used if proxy query fails)")
	flag.StringVar(&oyboBase, "oybo", "", "fallback CS base URL (used if proxy query fails)")
	flag.StringVar(&paBase, "pa", "", "fallback PA base URL (used if proxy query fails)")
	flag.StringVar(&pctrPublicKeyPath, "pctr-public-key", "", "RSA public key PEM used to encrypt PCTR card data")
	flag.Parse()

	normalizeStationID := func(v string) string {
		v = strings.TrimSpace(v)
		if v == "" {
			return v
		}
		allDigits := true
		for _, r := range v {
			if r < '0' || r > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			return "station-" + v
		}
		return v
	}
	stationID = normalizeStationID(stationID)

	stationTaskID := strings.TrimSpace(stationID)
	if strings.HasPrefix(stationTaskID, "station-") {
		stationTaskID = strings.TrimPrefix(stationTaskID, "station-")
	}
	if strings.TrimSpace(stationTask) == "" {
		stationTask = fmt.Sprintf("station-Computer-%s", stationTaskID)
	}

	logger := log.New(os.Stdout, "[gate] ", log.LstdFlags)
	httpClient := &http.Client{Timeout: 5 * time.Second}
	proxyClient := simproxy.Client{Addr: proxyAddr, Proto: proxyProto, Timeout: 5 * time.Second}

	type cachedAddr struct {
		value string
		at    time.Time
	}
	var stationCache cachedAddr
	var oyboCache cachedAddr
	var paCache cachedAddr
	var cacheMu sync.Mutex

	var pctrPub *rsa.PublicKey
	keyPath := strings.TrimSpace(pctrPublicKeyPath)
	if keyPath == "" {
		keyPath = strings.TrimSpace(os.Getenv("PCTR_PUBLIC_KEY"))
	}
	keyPath = strings.TrimSpace(keyPath)

	findKeyPath := func(explicit string) string {
		if strings.TrimSpace(explicit) != "" {
			return strings.TrimSpace(explicit)
		}
		candidates := []string{
			"pctr_public.pem",
			filepath.FromSlash("./pctr_public.pem"),
			filepath.FromSlash("./Simulation/gate/pctr_public.pem"),
			filepath.FromSlash("Simulation/gate/pctr_public.pem"),
		}
		for _, c := range candidates {
			if strings.TrimSpace(c) == "" {
				continue
			}
			if _, err := os.Stat(c); err == nil {
				return c
			}
		}
		return ""
	}

	resolvedKeyPath := findKeyPath(keyPath)
	if resolvedKeyPath != "" {
		pk, err := pctrcrypto.LoadRSAPublicKeyFromPEMFile(resolvedKeyPath)
		if err != nil {
			logger.Printf("failed to load PCTR public key from %q (PCTR taps will deny): %v", resolvedKeyPath, err)
		} else {
			pctrPub = pk
			logger.Printf("loaded PCTR public key: %s", resolvedKeyPath)
		}
	} else {
		logger.Printf("PCTR public key not found (PCTR taps will deny). Provide -pctr-public-key or set PCTR_PUBLIC_KEY. Looked in ./pctr_public.pem and ./Simulation/gate/pctr_public.pem")
	}

	resolveBase := func(task string, fallback string, cache *cachedAddr) (string, error) {
		cacheMu.Lock()
		defer cacheMu.Unlock()
		addr, err := proxyClient.Query(task)
		if err == nil {
			cache.value = addr
			cache.at = time.Now()
			return simproxy.EnsureHTTPBase(addr), nil
		}
		if cache.value != "" && time.Since(cache.at) < 2*time.Minute {
			return simproxy.EnsureHTTPBase(cache.value), nil
		}
		if strings.TrimSpace(fallback) != "" {
			return simproxy.EnsureHTTPBase(fallback), nil
		}
		return "", err
	}

	handleTap := func(tapLine string) (bool, string) {
		cardType, cardID, err := parseTap(tapLine)
		if err != nil {
			return false, "invalid tap"
		}

		tapTime := time.Now().UTC().Format(time.RFC3339Nano)
		allowed := false
		reason := ""
		token := ""

		if cardType == "OY" {
			backendBase, err := resolveBase(oyboTask, oyboBase, &oyboCache)
			if err != nil {
				logger.Printf("backend resolve failed (task=%s): %v", oyboTask, err)
			}
			if backendBase == "" {
				allowed = false
				reason = "backend not found"
			} else {
				validateURL := fmt.Sprintf("%s/validate", strings.TrimRight(backendBase, "/"))
				vreq := ValidateRequest{CardID: cardID, GateID: gateID, StationID: stationID, TapTime: tapTime}
				var vresp ValidateResponse
				if err := httpPostJSON(httpClient, validateURL, vreq, &vresp); err != nil {
					allowed = false
					reason = "backend error"
					logger.Printf("validate error (deny): %v", err)
				} else {
					allowed = vresp.Valid
					reason = vresp.Reason
				}
			}
		} else {
			if pctrPub == nil {
				allowed = false
				reason = "missing PCTR public key"
			} else {
				paResolved, err := resolveBase(paTask, paBase, &paCache)
				if err != nil {
					logger.Printf("PA resolve failed (task=%s): %v", paTask, err)
				}
				if paResolved == "" {
					allowed = false
					reason = "PA not found"
				} else {
					cipher, err := pctrcrypto.EncryptCardData(pctrPub, []byte(cardID))
					if err != nil {
						allowed = false
						reason = "encrypt failed"
					} else {
						req := TokenizeRequest{CiphertextB64: base64.StdEncoding.EncodeToString(cipher)}
						var resp TokenizeResponse
						url := strings.TrimRight(paResolved, "/") + "/tokenize"
						if err := httpPostJSON(httpClient, url, req, &resp); err != nil {
							allowed = false
							reason = "PA error"
							logger.Printf("tokenize error (deny): %v", err)
						} else if !resp.OK {
							allowed = false
							reason = resp.Reason
						} else {
							allowed = true
							reason = "ok"
							token = resp.Token
						}
					}
				}
			}
		}

		if !allowed {
			return false, reason
		}

		resolvedStationBase, err := resolveBase(stationTask, stationBase, &stationCache)
		if err != nil {
			logger.Printf("station resolve failed: %v", err)
		}
		if resolvedStationBase == "" {
			return false, "station not found"
		}

		tx := Transaction{
			TransactionID: fmt.Sprintf("%s-%d", gateID, time.Now().UnixNano()),
			CardType:      cardType,
			CardID: func() string {
				if cardType == "OY" {
					return cardID
				}
				return ""
			}(),
			Token:     token,
			GateID:    gateID,
			StationID: stationID,
			TapTime:   tapTime,
			Allowed:   allowed,
			Reason:    reason,
		}

		var ack TransactionAck
		txURL := fmt.Sprintf("%s/transaction", strings.TrimRight(resolvedStationBase, "/"))
		if err := httpPostJSON(httpClient, txURL, tx, &ack); err != nil {
			logger.Printf("failed to send transaction: %v", err)
			return false, "station send failed"
		}
		if !ack.Accepted {
			logger.Printf("station did not accept transaction")
			return false, "station rejected"
		}
		return true, "ok"
	}

	// HTTP server for remote tap input.
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/tap", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, TapResponse{Allowed: false, Reason: "method not allowed"})
			return
		}

		var tapLine string
		ct := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
		if strings.Contains(ct, "application/json") {
			var req TapRequest
			dec := json.NewDecoder(r.Body)
			dec.DisallowUnknownFields()
			if err := dec.Decode(&req); err != nil {
				writeJSON(w, http.StatusBadRequest, TapResponse{Allowed: false, Reason: "invalid json"})
				return
			}
			if strings.TrimSpace(req.Tap) != "" {
				tapLine = req.Tap
			} else if strings.TrimSpace(req.CardType) != "" && strings.TrimSpace(req.CardID) != "" {
				tapLine = fmt.Sprintf("%s:%s", req.CardType, req.CardID)
			} else {
				writeJSON(w, http.StatusBadRequest, TapResponse{Allowed: false, Reason: "missing tap"})
				return
			}
		} else {
			b, _ := io.ReadAll(io.LimitReader(r.Body, 256))
			tapLine = strings.TrimSpace(string(b))
			if tapLine == "" {
				writeJSON(w, http.StatusBadRequest, TapResponse{Allowed: false, Reason: "missing tap"})
				return
			}
		}

		allowed, reason := handleTap(tapLine)
		writeJSON(w, http.StatusOK, TapResponse{Allowed: allowed, Reason: reason})
	})

	ln, err := netutil.ListenTCP(listen)
	if err != nil {
		logger.Fatalf("listen %s: %v", listen, err)
	}
	defer ln.Close()

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	srvErr := make(chan error, 1)
	go func() {
		logger.Printf("HTTP tap listener on %s", listen)
		srvErr <- srv.Serve(ln)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	fmt.Println("Gate ready")
	fmt.Println("Enter taps like: OY:1001 or PCTR:2001")

	lines := make(chan string)
	scanDone := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		defer close(lines)
		for {
			fmt.Print("> ")
			if !scanner.Scan() {
				scanDone <- scanner.Err()
				return
			}
			lines <- scanner.Text()
		}
	}()

	shutdown := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = srv.Shutdown(ctx)
		cancel()
	}

	for {
		select {
		case <-sigCh:
			shutdown()
			logger.Println("exiting")
			return
		case err := <-srvErr:
			if err != nil && err != http.ErrServerClosed {
				logger.Printf("tap listener error: %v", err)
			}
			shutdown()
			logger.Println("exiting")
			return
		case line, ok := <-lines:
			if !ok {
				err := <-scanDone
				if err != nil {
					logger.Printf("stdin error: %v", err)
				}
				shutdown()
				logger.Println("exiting")
				return
			}
			allowed, reason := handleTap(line)
			if allowed {
				fmt.Printf("ALLOW (%s)\n", strings.TrimSpace(line))
			} else {
				fmt.Printf("DENY (%s) reason=%s\n", strings.TrimSpace(line), reason)
			}
		}
	}
}
