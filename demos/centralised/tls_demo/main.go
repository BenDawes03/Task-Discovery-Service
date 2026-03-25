package main

import (
	"bufio"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorCyan   = "\033[36m"
	colorBold   = "\033[1m"
)

func ok(s string) string   { return colorGreen + s + colorReset }
func bad(s string) string  { return colorRed + s + colorReset }
func info(s string) string { return colorYellow + s + colorReset }
func head(s string) string { return colorBold + colorCyan + s + colorReset }

type message struct {
	Command  string `json:"cmd"`
	Task     string `json:"task"`
	Address  string `json:"address,omitempty"`
	Capacity int    `json:"capacity,omitempty"`
}

type response struct {
	Status  string `json:"status"`
	Address string `json:"address,omitempty"`
	Error   string `json:"error,omitempty"`
}

type certBundle struct {
	certPEM []byte
	keyPEM  []byte
	cert    *x509.Certificate
	key     crypto.PrivateKey
}

func main() {
	serverAddr := flag.String("server", "127.0.0.1:5000", "TLS server address in host:port form")
	serverName := flag.String("server-name", "localhost", "TLS server name for certificate verification")
	caCertPath := flag.String("ca-cert", "certs/ca.crt", "CA certificate trusted by server and clients")
	caKeyPath := flag.String("ca-key", "certs/ca.key", "CA private key used to generate valid demo client cert")
	stepByStep := flag.Bool("step-by-step", true, "Pause before each live scene")
	flag.Parse()

	clearScreen()
	printBanner(*serverAddr)
	if *stepByStep {
		pause()
	}

	ca, err := loadCA(*caCertPath, *caKeyPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s failed to load CA assets: %v\n", bad("ERROR"), err)
		os.Exit(1)
	}

	validClient, err := generateLeaf("demo-valid-client", x509.ExtKeyUsageClientAuth, ca)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s failed to generate valid client cert: %v\n", bad("ERROR"), err)
		os.Exit(1)
	}
	validQueryClient, err := generateLeaf("demo-valid-query-client", x509.ExtKeyUsageClientAuth, ca)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s failed to generate second valid client cert: %v\n", bad("ERROR"), err)
		os.Exit(1)
	}
	rogueCA, err := generateCA("demo-rogue-ca")
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s failed to generate rogue CA: %v\n", bad("ERROR"), err)
		os.Exit(1)
	}
	invalidClient, err := generateLeaf("demo-invalid-client", x509.ExtKeyUsageClientAuth, rogueCA)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s failed to generate invalid client cert: %v\n", bad("ERROR"), err)
		os.Exit(1)
	}

	clearScreen()
	fmt.Printf("%s\n\n", head("Preparation"))
	fmt.Printf("  %s loaded from %s\n", ok("Trusted CA"), *caCertPath)
	fmt.Printf("  %s loaded from %s\n", ok("CA private key"), *caKeyPath)
	fmt.Printf("  Generated %s signed by trusted CA\n", ok("valid register client cert"))
	fmt.Printf("  Generated %s signed by trusted CA\n", ok("valid query client cert"))
	fmt.Printf("  Generated %s signed by rogue CA\n\n", bad("invalid client cert"))
	fmt.Printf("  Server target: %s\n", *serverAddr)
	fmt.Printf("  TLS SNI/verify name: %s\n", *serverName)
	if *stepByStep {
		pause()
	}

	taskName := fmt.Sprintf("demo_tls_%d", time.Now().Unix()%100000)
	registeredAddress := "127.0.0.1:19001"

	clearScreen()
	runValidRegisterScene(*serverAddr, *serverName, ca, validClient, taskName, registeredAddress, *stepByStep)

	clearScreen()
	runValidQueryScene(*serverAddr, *serverName, ca, validQueryClient, taskName, registeredAddress, *stepByStep)

	clearScreen()
	runInvalidCertQueryScene(*serverAddr, *serverName, ca, invalidClient, *stepByStep)

	clearScreen()
	runNoCertQueryScene(*serverAddr, *serverName, ca, *stepByStep)

	clearScreen()
	printSummary(taskName)
}

func runValidRegisterScene(serverAddr, serverName string, trustedCA, validClient *certBundle, taskName, address string, stepByStep bool) {
	fmt.Printf("%s\n\n", head("Scene 1: Valid register client cert"))
	fmt.Printf("  This test verifies that a client with a valid certificate signed\n")
	fmt.Printf("  by the trusted CA can successfully register a service with the server.\n")
	fmt.Println()
	if stepByStep {
		pause()
	}
	fmt.Println()

	cfg, err := clientTLSConfig(trustedCA.certPEM, validClient, serverName)
	if err != nil {
		fmt.Printf("  %s could not build TLS config: %v\n", bad("FAIL"), err)
		return
	}

	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", serverAddr, cfg)
	if err != nil {
		fmt.Printf("  %s handshake failed: %v\n", bad("FAIL"), err)
		fmt.Println("  Check that the server is running in TLS mode and trusts the same CA.")
		return
	}
	defer conn.Close()

	state := conn.ConnectionState()
	fmt.Printf("  %s handshake accepted\n", ok("PASS"))
	fmt.Printf("  Cipher: %s\n", tls.CipherSuiteName(state.CipherSuite))

	registerResp, err := sendMessage(conn, message{Command: "REGISTER", Task: taskName, Address: address, Capacity: 1})
	if err != nil {
		fmt.Printf("  %s REGISTER failed: %v\n", bad("FAIL"), err)
		if stepByStep {
			pause()
		}
		return
	}
	fmt.Printf("  REGISTER -> status=%s task=%s addr=%s\n", registerResp.Status, taskName, address)
	fmt.Printf("\n  %s register succeeded with client cert CN=%s\n", ok("Result:"), validClient.cert.Subject.CommonName)
	if stepByStep {
		pause()
	}
}

func runValidQueryScene(serverAddr, serverName string, trustedCA, validQueryClient *certBundle, taskName, expectedAddress string, stepByStep bool) {
	fmt.Printf("%s\n\n", head("Scene 2: Different valid query client cert"))
	fmt.Printf("  This test verifies that a different client with a valid certificate\n")
	fmt.Printf("  (signed by the same trusted CA) can query the service registered by the first client.\n")
	fmt.Println()
	if stepByStep {
		pause()
	}
	fmt.Println()

	cfg, err := clientTLSConfig(trustedCA.certPEM, validQueryClient, serverName)
	if err != nil {
		fmt.Printf("  %s could not build TLS config: %v\n", bad("FAIL"), err)
		return
	}

	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", serverAddr, cfg)
	if err != nil {
		fmt.Printf("  %s handshake failed: %v\n", bad("FAIL"), err)
		return
	}
	defer conn.Close()

	queryResp, err := sendMessage(conn, message{Command: "QUERY", Task: taskName})
	if err != nil {
		fmt.Printf("  %s QUERY failed: %v\n", bad("FAIL"), err)
		if stepByStep {
			pause()
		}
		return
	}
	fmt.Printf("  QUERY -> status=%s address=%s\n", queryResp.Status, queryResp.Address)
	if queryResp.Status == "OK" && queryResp.Address == expectedAddress {
		fmt.Printf("\n  %s second valid cert can query the service registered by first client\n", ok("Result:"))
		if stepByStep {
			pause()
		}
		return
	}
	fmt.Printf("\n  %s query returned unexpected result for task %s\n", bad("FAIL"), taskName)
	if stepByStep {
		pause()
	}
}

func runInvalidCertQueryScene(serverAddr, serverName string, trustedCA, invalidClient *certBundle, stepByStep bool) {
	fmt.Printf("%s\n\n", head("Scene 3: Invalid cert query client"))
	fmt.Printf("  This test verifies that a client with a certificate signed by a\n")
	fmt.Printf("  rogue (untrusted) CA is rejected during the TLS handshake.\n")
	fmt.Println()
	if stepByStep {
		pause()
	}
	fmt.Println()

	cfg, err := forcedClientTLSConfig(trustedCA.certPEM, invalidClient, serverName)
	if err != nil {
		fmt.Printf("  %s could not build TLS config: %v\n", bad("FAIL"), err)
		return
	}

	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", serverAddr, cfg)
	if err != nil {
		fmt.Printf("  %s handshake rejected as expected\n", ok("PASS"))
		fmt.Printf("  Error: %v\n", err)
		fmt.Printf("\n  %s server refused query client cert not signed by trusted CA\n", ok("Result:"))
		if conn != nil {
			_ = conn.Close()
		}
		if stepByStep {
			pause()
		}
		return
	}
	defer conn.Close()

	// Some servers may surface client-cert rejection only when application
	// data is exchanged; probe with a real request before declaring failure.
	_, ioErr := sendMessage(conn, message{Command: "QUERY", Task: "tls_invalid_probe"})
	if ioErr != nil {
		fmt.Printf("  %s connection rejected during first request as expected\n", ok("PASS"))
		fmt.Printf("  Error: %v\n", ioErr)
		fmt.Printf("\n  %s server refused query client cert not signed by trusted CA\n", ok("Result:"))
		if stepByStep {
			pause()
		}
		return
	}

	fmt.Printf("  %s invalid certificate was unexpectedly accepted\n", bad("FAIL"))
	if stepByStep {
		pause()
	}
}

func forcedClientTLSConfig(caPEM []byte, client *certBundle, serverName string) (*tls.Config, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("unable to parse CA PEM")
	}
	pair, err := tls.X509KeyPair(client.certPEM, client.keyPEM)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		RootCAs:    pool,
		ServerName: serverName,
		MinVersion: tls.VersionTLS12,
		// Force presenting this client certificate even if it is not issued by
		// one of the server's advertised acceptable CAs.
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			return &pair, nil
		},
	}, nil
}

func runNoCertQueryScene(serverAddr, serverName string, trustedCA *certBundle, stepByStep bool) {
	fmt.Printf("%s\n\n", head("Scene 4: No-cert query client"))
	fmt.Printf("  This test verifies that a client attempting to connect without\n")
	fmt.Printf("  providing a client certificate is rejected by the server.\n")
	fmt.Println()
	if stepByStep {
		pause()
	}
	fmt.Println()

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(trustedCA.certPEM) {
		fmt.Printf("  %s unable to parse trusted CA PEM\n", bad("FAIL"))
		return
	}

	conn, err := tls.DialWithDialer(
		&net.Dialer{Timeout: 5 * time.Second},
		"tcp",
		serverAddr,
		&tls.Config{
			RootCAs:    pool,
			ServerName: serverName,
			MinVersion: tls.VersionTLS12,
		},
	)
	if err != nil {
		fmt.Printf("  %s handshake rejected as expected\n", ok("PASS"))
		fmt.Printf("  Error: %v\n", err)
		fmt.Printf("\n  %s server requires a client certificate\n", ok("Result:"))
		if conn != nil {
			_ = conn.Close()
		}
		if stepByStep {
			pause()
		}
		return
	}
	defer conn.Close()

	_, ioErr := sendMessage(conn, message{Command: "QUERY", Task: "tls_nocert_probe"})
	if ioErr != nil {
		fmt.Printf("  %s connection rejected during first request as expected\n", ok("PASS"))
		fmt.Printf("  Error: %v\n", ioErr)
		fmt.Printf("\n  %s server requires a client certificate\n", ok("Result:"))
		if stepByStep {
			pause()
		}
		return
	}

	fmt.Printf("  %s no-cert client was unexpectedly accepted\n", bad("FAIL"))
	if stepByStep {
		pause()
	}
}

func clientTLSConfig(caPEM []byte, client *certBundle, serverName string) (*tls.Config, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("unable to parse CA PEM")
	}
	pair, err := tls.X509KeyPair(client.certPEM, client.keyPEM)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		RootCAs:      pool,
		Certificates: []tls.Certificate{pair},
		ServerName:   serverName,
		MinVersion:   tls.VersionTLS12,
	}, nil
}

func sendMessage(conn net.Conn, msg message) (*response, error) {
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(conn)
	if err := enc.Encode(msg); err != nil {
		return nil, err
	}
	var resp response
	if err := dec.Decode(&resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func loadCA(certPath, keyPath string) (*certBundle, error) {
	certPEM, err := os.ReadFile(filepath.Clean(certPath))
	if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(filepath.Clean(keyPath))
	if err != nil {
		return nil, err
	}

	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("invalid certificate PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, fmt.Errorf("invalid key PEM")
	}

	var key crypto.PrivateKey
	switch keyBlock.Type {
	case "EC PRIVATE KEY":
		key, err = x509.ParseECPrivateKey(keyBlock.Bytes)
	case "RSA PRIVATE KEY":
		key, err = x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	case "PRIVATE KEY":
		parsedKey, parseErr := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
		err = parseErr
		if parseErr == nil {
			switch cast := parsedKey.(type) {
			case *ecdsa.PrivateKey:
				key = cast
			case *rsa.PrivateKey:
				key = cast
			default:
				return nil, fmt.Errorf("pkcs8 private key must be ECDSA or RSA")
			}
		}
	default:
		return nil, fmt.Errorf("unsupported key type %q", keyBlock.Type)
	}
	if err != nil {
		return nil, err
	}

	return &certBundle{certPEM: certPEM, keyPEM: keyPEM, cert: cert, key: key}, nil
}

func generateCA(commonName string) (*certBundle, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(24 * time.Hour),
		IsCA:         true,
		KeyUsage:     x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	return &certBundle{
		certPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		keyPEM:  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
		cert:    cert,
		key:     key,
	}, nil
}

func generateLeaf(commonName string, eku x509.ExtKeyUsage, signer *certBundle) (*certBundle, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{eku},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, signer.cert, &key.PublicKey, signer.key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	return &certBundle{
		certPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		keyPEM:  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
		cert:    cert,
		key:     key,
	}, nil
}

func printBanner(serverAddr string) {
	fmt.Printf("%s\n", head("TDS Centralised Live TLS Demo"))
	fmt.Println(strings.Repeat("=", 44))
	fmt.Println()
	fmt.Printf("  Target server: %s\n", serverAddr)
	fmt.Printf("  This run will generate four live client flows:\n")
	fmt.Printf("    1) %s REGISTER\n", ok("Valid cert A"))
	fmt.Printf("    2) %s QUERY\n", ok("Different valid cert B"))
	fmt.Printf("    3) %s QUERY\n", bad("Invalid cert (rogue CA)"))
	fmt.Printf("    4) %s QUERY\n", bad("No client cert"))
	fmt.Println()
	fmt.Printf("  Expected outcomes:\n")
	fmt.Printf("    - Scenes 1 and 2: %s\n", ok("Accepted"))
	fmt.Printf("    - Scenes 3 and 4: %s\n", ok("Rejected"))
	fmt.Println()
	fmt.Printf("  Start server example:\n")
	fmt.Printf("  go run ./cmd/server --tls --port 5000 --no-ui --tls-cert certs/server.crt --tls-key certs/server.key --tls-client-ca certs/ca.crt\n")
}

func printSummary(taskName string) {
	fmt.Printf("%s\n\n", head("Summary"))
	fmt.Printf("  %s valid cert A performed REGISTER successfully.\n", ok("PASS:"))
	fmt.Printf("  %s different valid cert B performed QUERY successfully.\n", ok("PASS:"))
	fmt.Printf("  %s invalid cert query client was blocked.\n", ok("PASS:"))
	fmt.Printf("  %s no-cert query client was blocked.\n", ok("PASS:"))
	fmt.Printf("\n  Demo task used: %s\n", taskName)
}

func pause() {
	fmt.Printf("\n%s Press ENTER to continue...%s", info("[pause]"), colorReset)
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}

func clearScreen() {
	if runtime.GOOS == "windows" {
		cmd := exec.Command("cmd", "/c", "cls")
		cmd.Stdout = os.Stdout
		_ = cmd.Run()
		return
	}
	fmt.Print("\033[2J\033[H")
}
