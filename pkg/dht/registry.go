package dht

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync/atomic"
)

var registryLogger = log.New(os.Stdout, "[dht-registry] ", log.LstdFlags)

// DHTRegistry wraps DHT functionality to match the proxy's expected interface
type DHTRegistry struct {
	network       *DHTNetwork
	regCount      uint64
	queryCount    uint64
	errorCount    uint64
}

func normalizeListenAddr(listenAddr string) string {
	listenAddr = strings.TrimSpace(listenAddr)
	if listenAddr == "" {
		return listenAddr
	}
	// Allow users to input just a port number (e.g. "6001").
	allDigits := true
	for _, r := range listenAddr {
		if r < '0' || r > '9' {
			allDigits = false
			break
		}
	}
	if allDigits {
		return ":" + listenAddr
	}
	return listenAddr
}

// NewDHTRegistry creates a new DHT-based registry
func NewDHTRegistry(listenAddr string, bootstrapNodes []string) (*DHTRegistry, error) {
	listenAddr = normalizeListenAddr(listenAddr)
	dht, err := NewDHT(listenAddr)
	if err != nil {
		return nil, fmt.Errorf("create dht: %w", err)
	}
	
	network := NewDHTNetwork(dht, bootstrapNodes)
	
	return &DHTRegistry{
		network: network,
	}, nil
}

// Start initializes the DHT network
func (dr *DHTRegistry) Start() error {
	if err := dr.network.Start(); err != nil {
		return fmt.Errorf("start network: %w", err)
	}
	registryLogger.Println("DHT registry started")
	return nil
}

// Stop shuts down the DHT network
func (dr *DHTRegistry) Stop() error {
	return dr.network.Stop()
}

// Register stores a task->address mapping in the DHT
func (dr *DHTRegistry) Register(task, address string) error {
	registryLogger.Printf("registering %s -> %s", task, address)
	
	if err := dr.network.Store(task, address); err != nil {
		atomic.AddUint64(&dr.errorCount, 1)
		return fmt.Errorf("dht store: %w", err)
	}
	
	atomic.AddUint64(&dr.regCount, 1)
	return nil
}

// Query retrieves an address for a task from the DHT
// Returns the first available address (round-robin could be added)
func (dr *DHTRegistry) Query(task string) (string, error) {
	registryLogger.Printf("querying %s", task)
	
	addrs, err := dr.network.Find(task)
	if err != nil {
		atomic.AddUint64(&dr.errorCount, 1)
		return "", fmt.Errorf("dht find: %w", err)
	}
	
	atomic.AddUint64(&dr.queryCount, 1)
	
	if len(addrs) == 0 {
		return "", nil
	}
	
	// return the first address (could implement round-robin here)
	return addrs[0], nil
}

// QueryAll retrieves all addresses for a task from the DHT
func (dr *DHTRegistry) QueryAll(task string) ([]string, error) {
	registryLogger.Printf("querying all for %s", task)
	
	addrs, err := dr.network.Find(task)
	if err != nil {
		atomic.AddUint64(&dr.errorCount, 1)
		return nil, fmt.Errorf("dht find: %w", err)
	}
	
	atomic.AddUint64(&dr.queryCount, 1)
	return addrs, nil
}

// Stats returns DHT registry statistics
func (dr *DHTRegistry) Stats() (regs, queries, errs uint64) {
	regs = atomic.LoadUint64(&dr.regCount)
	queries = atomic.LoadUint64(&dr.queryCount)
	errs = atomic.LoadUint64(&dr.errorCount)
	return
}

// GetDHTInfo returns information about the DHT state
func (dr *DHTRegistry) GetDHTInfo() map[string]interface{} {
	self := dr.network.dht.GetSelf()
	peers := dr.network.dht.GetPeers()
	
	peerAddrs := make([]string, len(peers))
	for i, p := range peers {
		peerAddrs[i] = p.Address
	}
	
	return map[string]interface{}{
		"node_id":      NodeIDToString(self.ID),
		"address":      self.Address,
		"ring_size":    dr.network.dht.GetRingSize(),
		"storage_size": dr.network.dht.GetStorageSize(),
		"peers":        peerAddrs,
	}
}

// RunDHTPeerListener listens for other DHT peers and handles P2P requests
// This is separate from the proxy listener which handles client requests
func RunDHTPeerListener(ctx context.Context, dhtRegistry *DHTRegistry) error {
	// The network layer already handles peer connections via Start()
	// This function just waits for context cancellation
	<-ctx.Done()
	return dhtRegistry.Stop()
}
