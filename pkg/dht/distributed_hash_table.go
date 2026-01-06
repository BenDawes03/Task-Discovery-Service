package dht

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math/big"
	"net"
	"sort"
	"sync"
	"time"
)

// NodeID is a 256-bit identifier derived from hashing the node's address
type NodeID [32]byte

// Node represents a peer in the DHT ring
type Node struct {
	ID       NodeID
	Address  string
	LastSeen time.Time
}

// DHT implements a distributed hash table using consistent hashing
type DHT struct {
	mu         sync.RWMutex
	self       *Node
	peers      map[NodeID]*Node
	ring       []NodeID            // sorted ring of node IDs
	storage    map[string][]string // task -> addresses (data stored on this node)
	listenAddr string
}

// NewDHT creates a new DHT node
func NewDHT(listenAddr string) (*DHT, error) {
	nodeID := HashAddress(listenAddr)
	self := &Node{
		ID:       nodeID,
		Address:  listenAddr,
		LastSeen: time.Now(),
	}

	dht := &DHT{
		self:       self,
		peers:      make(map[NodeID]*Node),
		ring:       []NodeID{nodeID},
		storage:    make(map[string][]string),
		listenAddr: listenAddr,
	}

	return dht, nil
}

// HashAddress computes a SHA256 hash of an address string to create a NodeID
func HashAddress(addr string) NodeID {
	hash := sha256.Sum256([]byte(addr))
	return hash
}

// HashTask computes a SHA256 hash of a task name to find its position in the ring
func HashTask(task string) NodeID {
	hash := sha256.Sum256([]byte(task))
	return hash
}

// AddPeer adds a peer to the DHT ring
func (d *DHT) AddPeer(address string) {
	nodeID := HashAddress(address)
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, exists := d.peers[nodeID]; exists {
		d.peers[nodeID].LastSeen = time.Now()
		return
	}

	peer := &Node{
		ID:       nodeID,
		Address:  address,
		LastSeen: time.Now(),
	}
	d.peers[nodeID] = peer
	d.rebuildRing()
}

// RemovePeer removes a peer from the DHT ring
func (d *DHT) RemovePeer(nodeID NodeID) {
	d.mu.Lock()
	defer d.mu.Unlock()

	delete(d.peers, nodeID)
	d.rebuildRing()
}

// rebuildRing reconstructs the sorted ring of node IDs (must hold lock)
func (d *DHT) rebuildRing() {
	d.ring = make([]NodeID, 0, len(d.peers)+1)
	d.ring = append(d.ring, d.self.ID)
	for id := range d.peers {
		d.ring = append(d.ring, id)
	}
	sort.Slice(d.ring, func(i, j int) bool {
		return CompareNodeID(d.ring[i], d.ring[j]) < 0
	})
}

// FindClosestNode finds the node responsible for storing a given task
func (d *DHT) FindClosestNode(taskName string) *Node {
	taskID := HashTask(taskName)
	d.mu.RLock()
	defer d.mu.RUnlock()

	if len(d.ring) == 0 {
		return d.self
	}

	// binary search for the first node >= taskID
	idx := sort.Search(len(d.ring), func(i int) bool {
		return CompareNodeID(d.ring[i], taskID) >= 0
	})

	// wrap around if we're past the end
	if idx >= len(d.ring) {
		idx = 0
	}

	closestID := d.ring[idx]

	// if it's us, return self
	if closestID == d.self.ID {
		return d.self
	}

	// otherwise return the peer
	return d.peers[closestID]
}

// IsResponsibleFor checks if this node is responsible for storing a task
func (d *DHT) IsResponsibleFor(taskName string) bool {
	closest := d.FindClosestNode(taskName)
	return closest.ID == d.self.ID
}

// StoreTask stores a task->address mapping on this node
func (d *DHT) StoreTask(taskName, address string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	addrs := d.storage[taskName]
	// avoid duplicates
	for _, a := range addrs {
		if a == address {
			return
		}
	}
	d.storage[taskName] = append(addrs, address)
}

// LookupTask retrieves addresses for a task stored on this node
func (d *DHT) LookupTask(taskName string) []string {
	d.mu.RLock()
	defer d.mu.RUnlock()

	addrs := d.storage[taskName]
	result := make([]string, len(addrs))
	copy(result, addrs)
	return result
}

// GetPeers returns a copy of all known peers
func (d *DHT) GetPeers() []*Node {
	d.mu.RLock()
	defer d.mu.RUnlock()

	peers := make([]*Node, 0, len(d.peers))
	for _, p := range d.peers {
		peers = append(peers, &Node{
			ID:       p.ID,
			Address:  p.Address,
			LastSeen: p.LastSeen,
		})
	}
	return peers
}

// GetSelf returns this node's info
func (d *DHT) GetSelf() *Node {
	return &Node{
		ID:       d.self.ID,
		Address:  d.self.Address,
		LastSeen: d.self.LastSeen,
	}
}

// CompareNodeID compares two node IDs numerically
// returns -1 if a < b, 0 if a == b, 1 if a > b
func CompareNodeID(a, b NodeID) int {
	aBig := new(big.Int).SetBytes(a[:])
	bBig := new(big.Int).SetBytes(b[:])
	return aBig.Cmp(bBig)
}

// NodeIDToString converts a NodeID to a short hex string for display
func NodeIDToString(id NodeID) string {
	return fmt.Sprintf("%x", id[:4])
}

// GetLocalIP returns the first non-loopback IPv4 address
func GetLocalIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "127.0.0.1"
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			ip = ip.To4()
			if ip != nil {
				return ip.String()
			}
		}
	}
	return "127.0.0.1"
}

// Distance calculates the distance between two node IDs on the ring
func Distance(a, b NodeID) *big.Int {
	aBig := new(big.Int).SetBytes(a[:])
	bBig := new(big.Int).SetBytes(b[:])
	diff := new(big.Int).Sub(bBig, aBig)
	if diff.Sign() < 0 {
		// wrap around: add 2^256
		max := new(big.Int).Lsh(big.NewInt(1), 256)
		diff.Add(diff, max)
	}
	return diff
}

// CleanupStaleData removes task entries (could be enhanced to redistribute)
func (d *DHT) CleanupStaleData() int {
	d.mu.Lock()
	defer d.mu.Unlock()

	// For now, just clear storage for tasks we're no longer responsible for
	// (in a real implementation, we'd transfer them to the new responsible node)
	removed := 0
	for task := range d.storage {
		if !d.isResponsibleForLocked(task) {
			delete(d.storage, task)
			removed++
		}
	}
	return removed
}

// isResponsibleForLocked checks responsibility without taking lock (must hold lock)
func (d *DHT) isResponsibleForLocked(taskName string) bool {
	taskID := HashTask(taskName)
	if len(d.ring) == 0 {
		return true
	}

	idx := sort.Search(len(d.ring), func(i int) bool {
		return CompareNodeID(d.ring[i], taskID) >= 0
	})

	if idx >= len(d.ring) {
		idx = 0
	}

	return d.ring[idx] == d.self.ID
}

// GetRingSize returns the number of nodes in the ring
func (d *DHT) GetRingSize() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.ring)
}

// GetStorageSize returns the number of tasks stored on this node
func (d *DHT) GetStorageSize() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.storage)
}

// NodeIDFromUint64 creates a NodeID from a uint64 (for testing)
func NodeIDFromUint64(n uint64) NodeID {
	var id NodeID
	binary.BigEndian.PutUint64(id[24:], n)
	return id
}
