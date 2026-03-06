package dht

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math/big"
	"net"
	"sort"
	"strings"
	"sync"
	"time"
)

// NodeID is a 256-bit identifier derived from hashing the node's address
type NodeID [32]byte

// Node represents a peer in the DHT ring
type Node struct {
	ID       NodeID    // SHA256 hash of the node's address
	Address  string    // Network address (e.g., "192.168.1.100:6000")
	LastSeen time.Time // Last time this node was seen (for liveness tracking)
}

// DHT implements a distributed hash table using consistent hashing.
// It maintains a sorted ring of node IDs and stores task-to-address mappings
// for tasks that hash to positions near this node's ID.
type DHT struct {
	mutex      sync.RWMutex
	self       *Node               // This node's identity
	peers      map[NodeID]*Node    // Known peers in the DHT
	ring       []NodeID            // Sorted ring of all node IDs (including self)
	storage    map[string][]string // task -> addresses (data stored on this node)
	listenAddr string              // Address this node listens on
	network    *DHTNetwork         // Network layer reference (set after creation)
}

// NewDHT creates a new DHT node
func NewDHT(listenAddr string) (*DHT, error) {
	advertiseAddr := strings.TrimSpace(listenAddr)
	// ":port" is a valid listen address but not a dialable peer address.
	// For local demos, advertise on loopback by default.
	if strings.HasPrefix(advertiseAddr, ":") {
		advertiseAddr = "127.0.0.1" + advertiseAddr
	}

	nodeID := HashAddress(advertiseAddr)
	self := &Node{
		ID:       nodeID,
		Address:  advertiseAddr,
		LastSeen: time.Now(),
	}

	dht := &DHT{
		self:       self,
		peers:      make(map[NodeID]*Node),
		ring:       []NodeID{nodeID},
		storage:    make(map[string][]string),
		listenAddr: listenAddr,
		network:    nil, // Set by DHTNetwork after creation
	}

	return dht, nil
}

// SetNetwork sets the network layer reference (called by DHTNetwork)
func (dht *DHT) SetNetwork(network *DHTNetwork) {
	dht.network = network
}

// HashAddress computes a SHA256 hash of an address string to create a NodeID.
// This places the node at a specific position in the 256-bit keyspace.
func HashAddress(addr string) NodeID {
	hash := sha256.Sum256([]byte(addr))
	return hash
}

// HashTask computes a SHA256 hash of a task name to find its position in the ring.
// Tasks are stored on the k-closest nodes to this hash position.
func HashTask(task string) NodeID {
	hash := sha256.Sum256([]byte(task))
	return hash
}

// AddPeer adds a peer to the DHT ring
func (dht *DHT) AddPeer(address string) {
	nodeID := HashAddress(address)
	dht.mutex.Lock()
	defer dht.mutex.Unlock()

	if _, exists := dht.peers[nodeID]; exists {
		dht.peers[nodeID].LastSeen = time.Now()
		return
	}

	peer := &Node{
		ID:       nodeID,
		Address:  address,
		LastSeen: time.Now(),
	}
	dht.peers[nodeID] = peer
	dht.rebuildRing()
}

// RemovePeer removes a peer from the DHT ring
func (dht *DHT) RemovePeer(nodeID NodeID) {
	dht.mutex.Lock()
	defer dht.mutex.Unlock()

	delete(dht.peers, nodeID)
	dht.rebuildRing()
}

// rebuildRing reconstructs the sorted ring of node IDs (must hold lock)
func (dht *DHT) rebuildRing() {
	dht.ring = make([]NodeID, 0, len(dht.peers)+1)
	dht.ring = append(dht.ring, dht.self.ID)
	for id := range dht.peers {
		dht.ring = append(dht.ring, id)
	}
	sort.Slice(dht.ring, func(i, j int) bool {
		return CompareNodeID(dht.ring[i], dht.ring[j]) < 0
	})
}

// FindClosestNode finds the single closest node responsible for a task.
// DEPRECATED: Use FindKClosestNodes for k-replication instead.
// This method is kept for backward compatibility with single-node lookups.
func (dht *DHT) FindClosestNode(taskName string) *Node {
	taskID := HashTask(taskName)
	dht.mutex.RLock()
	defer dht.mutex.RUnlock()

	if len(dht.ring) == 0 {
		return dht.self
	}

	// binary search for the first node >= taskID
	idx := sort.Search(len(dht.ring), func(i int) bool {
		return CompareNodeID(dht.ring[i], taskID) >= 0
	})

	// wrap around if we're past the end
	if idx >= len(dht.ring) {
		idx = 0
	}

	closestID := dht.ring[idx]

	// if it's us, return self
	if closestID == dht.self.ID {
		return dht.self
	}

	// otherwise return the peer
	return dht.peers[closestID]
}

// FindKClosestNodes returns the k nodes with the smallest ring distance to a task hash.
// This is used for k-replication: data is stored on these k nodes for fault tolerance.
// Distance is measured as the clockwise distance on the ring (Chord-like DHT metric).
// Returns fewer than k nodes if the ring has fewer than k nodes total.
func (dht *DHT) FindKClosestNodes(taskName string, k int) []*Node {
	taskID := HashTask(taskName)
	dht.mutex.RLock()
	defer dht.mutex.RUnlock()

	if len(dht.ring) == 0 {
		return []*Node{dht.self}
	}

	// Create list with distances
	type nodeDistance struct {
		node *Node
		dist *big.Int
	}

	distances := make([]nodeDistance, 0, len(dht.ring))

	for _, nodeID := range dht.ring {
		dist := Distance(taskID, nodeID)
		if nodeID == dht.self.ID {
			distances = append(distances, nodeDistance{dht.self, dist})
		} else {
			distances = append(distances, nodeDistance{dht.peers[nodeID], dist})
		}
	}

	// Sort by distance (closest first)
	sort.Slice(distances, func(i, j int) bool {
		return distances[i].dist.Cmp(distances[j].dist) < 0
	})

	// Return top k
	result := make([]*Node, 0, k)
	for i := 0; i < k && i < len(distances); i++ {
		result = append(result, distances[i].node)
	}

	return result
}

// AmIInKClosest checks if this node is in the k-closest to a task.
// Used to determine if this node should accept storage/lookup requests for the task.
// Returns true if this node is one of the k nodes responsible for storing the task.
func (dht *DHT) AmIInKClosest(taskName string, k int) bool {
	closest := dht.FindKClosestNodes(taskName, k)
	for _, node := range closest {
		if node.ID == dht.self.ID {
			return true
		}
	}
	return false
}

// IsResponsibleFor checks if this node is the single closest node to a task.
// DEPRECATED: Use AmIInKClosest for k-replication logic instead.
// This method is kept for backward compatibility.
func (dht *DHT) IsResponsibleFor(taskName string) bool {
	closest := dht.FindClosestNode(taskName)
	return closest.ID == dht.self.ID
}

// StoreTask stores a task->address mapping on this node
func (dht *DHT) StoreTask(taskName, address string) {
	dht.mutex.Lock()
	defer dht.mutex.Unlock()

	addrs := dht.storage[taskName]
	// avoid duplicates
	for _, a := range addrs {
		if a == address {
			return
		}
	}
	dht.storage[taskName] = append(addrs, address)
}

// LookupTask retrieves addresses for a task from the DHT
// If not stored locally and this node is not in k-nearest, forwards to k-nearest nodes
func (dht *DHT) LookupTask(taskName string) []string {
	dht.mutex.RLock()

	// Try local storage first
	addrs := dht.storage[taskName]
	if len(addrs) > 0 {
		result := make([]string, len(addrs))
		copy(result, addrs)
		dht.mutex.RUnlock()
		return result
	}

	// Check if we're in k-nearest for this task
	kClosest := dht.findKClosestNodesLocked(taskName, ReplicationFactor)
	isResponsible := false
	for _, node := range kClosest {
		if node.ID == dht.self.ID {
			isResponsible = true
			break
		}
	}

	// If not responsible and we have a network layer, forward the query
	if !isResponsible && dht.network != nil {
		// Release read lock before network IO
		dht.mutex.RUnlock()
		return dht.forwardLookup(taskName, kClosest)
	}

	dht.mutex.RUnlock()
	return nil
}

// forwardLookup forwards a lookup query to k-nearest nodes
func (dht *DHT) forwardLookup(taskName string, kClosest []*Node) []string {
	for _, node := range kClosest {
		if node.ID == dht.self.ID {
			continue // skip self
		}

		// Query peer via network layer
		if addrs := dht.network.QueryPeerForTask(node.Address, taskName); len(addrs) > 0 {
			return addrs
		}
	}
	return nil
}

// findKClosestNodesLocked finds k-closest nodes without taking lock (must hold lock)
func (dht *DHT) findKClosestNodesLocked(taskName string, k int) []*Node {
	taskID := HashTask(taskName)

	if len(dht.ring) == 0 {
		return []*Node{dht.self}
	}

	type nodeDistance struct {
		node *Node
		dist *big.Int
	}

	distances := make([]nodeDistance, 0, len(dht.ring))
	for _, nodeID := range dht.ring {
		dist := Distance(taskID, nodeID)
		if nodeID == dht.self.ID {
			distances = append(distances, nodeDistance{dht.self, dist})
		} else {
			distances = append(distances, nodeDistance{dht.peers[nodeID], dist})
		}
	}

	sort.Slice(distances, func(i, j int) bool {
		return distances[i].dist.Cmp(distances[j].dist) < 0
	})

	result := make([]*Node, 0, k)
	for i := 0; i < k && i < len(distances); i++ {
		result = append(result, distances[i].node)
	}

	return result
}

// GetPeers returns a copy of all known peers
func (dht *DHT) GetPeers() []*Node {
	dht.mutex.RLock()
	defer dht.mutex.RUnlock()

	peers := make([]*Node, 0, len(dht.peers))
	for _, p := range dht.peers {
		peers = append(peers, &Node{
			ID:       p.ID,
			Address:  p.Address,
			LastSeen: p.LastSeen,
		})
	}
	return peers
}

// GetSelf returns this node's info
func (dht *DHT) GetSelf() *Node {
	return &Node{
		ID:       dht.self.ID,
		Address:  dht.self.Address,
		LastSeen: dht.self.LastSeen,
	}
}

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

// Distance calculates the clockwise ring distance from node a to node b.
// For Chord-like DHT: returns (b - a) mod 2^256, which is the position of b relative to a going clockwise.
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

// CleanupStaleData removes task entries we're no longer responsible for
// TODO: Implement data transfer to correct k-closest nodes instead of deletion
func (dht *DHT) CleanupStaleData() int {
	dht.mutex.Lock()
	defer dht.mutex.Unlock()

	// Remove tasks where we're not in the k-closest nodes
	removed := 0
	for task := range dht.storage {
		if !dht.amIInKClosestLocked(task, ReplicationFactor) {
			delete(dht.storage, task)
			removed++
		}
	}
	return removed
}

// amIInKClosestLocked checks if this node is in k-closest without taking lock (must hold lock)
func (dht *DHT) amIInKClosestLocked(taskName string, k int) bool {
	taskID := HashTask(taskName)
	if len(dht.ring) == 0 {
		return true
	}

	// Create list with distances
	type nodeDistance struct {
		nodeID NodeID
		dist   *big.Int
	}

	distances := make([]nodeDistance, 0, len(dht.ring))
	for _, nodeID := range dht.ring {
		dist := Distance(taskID, nodeID)
		distances = append(distances, nodeDistance{nodeID, dist})
	}

	// Sort by distance
	sort.Slice(distances, func(i, j int) bool {
		return distances[i].dist.Cmp(distances[j].dist) < 0
	})

	// Check if we're in top k
	for i := 0; i < k && i < len(distances); i++ {
		if distances[i].nodeID == dht.self.ID {
			return true
		}
	}
	return false
}

// GetRingSize returns the number of nodes in the ring
func (dht *DHT) GetRingSize() int {
	dht.mutex.RLock()
	defer dht.mutex.RUnlock()
	return len(dht.ring)
}

// GetStorageSize returns the number of tasks stored on this node
func (dht *DHT) GetStorageSize() int {
	dht.mutex.RLock()
	defer dht.mutex.RUnlock()
	return len(dht.storage)
}

// NodeIDFromUint64 creates a NodeID from a uint64 (for testing)
func NodeIDFromUint64(n uint64) NodeID {
	var id NodeID
	binary.BigEndian.PutUint64(id[24:], n)
	return id
}
