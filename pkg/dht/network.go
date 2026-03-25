package dht

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// NotResponsiblePayload is the payload for NOT_RESPONSIBLE replies
type NotResponsiblePayload struct {
	Closest []string `json:"closest"`
}

var netLogger = log.New(os.Stdout, "[dht] ", log.LstdFlags)

// ReplicationFactor controls how many k-closest nodes are used for store/find.
// Default is 3 and can be overridden at process startup.
var ReplicationFactor = 3

// EnableBootstrapPeerDiscovery controls whether nodes periodically poll bootstrap
// peers for updated peer lists after the initial join.
var EnableBootstrapPeerDiscovery = true

// Message types for DHT communication
const (
	MsgPing           = "PING"
	MsgPong           = "PONG"
	MsgJoin           = "JOIN"
	MsgPeerList       = "PEERLIST"
	MsgStore          = "STORE"
	MsgFind           = "FIND"
	MsgFoundData      = "FOUND"
	MsgNotFound       = "NOTFOUND"
	MsgOK             = "OK"
	MsgNotResponsible = "NOT_RESPONSIBLE"
)

// Message represents a DHT protocol message
type Message struct {
	Type    string          `json:"type"`
	Sender  string          `json:"sender"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// StorePayload is the payload for STORE messages
type StorePayload struct {
	Task    string `json:"task"`
	Address string `json:"address"`
}

// FindPayload is the payload for FIND messages
type FindPayload struct {
	Task string `json:"task"`
}

// FoundPayload is the payload for FOUND messages
type FoundPayload struct {
	Task      string   `json:"task"`
	Addresses []string `json:"addresses"`
}

// PeerListPayload is the payload for PEERLIST messages
type PeerListPayload struct {
	Peers []string `json:"peers"`
}

// DHTNetwork handles network communication for the DHT
type DHTNetwork struct {
	dht        *DHT
	listener   net.Listener
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	bootstraps []string // bootstrap nodes to join
}

// NewDHTNetwork creates a new DHT network layer
func NewDHTNetwork(dht *DHT, bootstrapNodes []string) *DHTNetwork {
	ctx, cancel := context.WithCancel(context.Background())
	return &DHTNetwork{
		dht:        dht,
		ctx:        ctx,
		cancel:     cancel,
		bootstraps: bootstrapNodes,
	}
}

// Start begins listening for DHT messages
func (dn *DHTNetwork) Start() error {
	ln, err := net.Listen("tcp", dn.dht.listenAddr)
	if err != nil {
		return fmt.Errorf("dht listen: %w", err)
	}
	dn.listener = ln

	// Update DHT's self address with the runtime bound port while keeping a
	// stable, dialable host to avoid wildcard forms like [::]:port.
	actualAddr := ln.Addr().String()
	advertiseHost := "127.0.0.1"
	if h, _, err := net.SplitHostPort(dn.dht.self.Address); err == nil {
		switch strings.TrimSpace(h) {
		case "", "0.0.0.0", "::", "::1", "localhost":
			advertiseHost = "127.0.0.1"
		default:
			advertiseHost = h
		}
	}
	_, actualPort, splitErr := net.SplitHostPort(actualAddr)
	if splitErr == nil && actualPort != "" {
		actualAddr = net.JoinHostPort(advertiseHost, actualPort)
	}
	dn.dht.mutex.Lock()
	dn.dht.self.Address = actualAddr
	dn.dht.self.ID = HashAddress(actualAddr)
	// Rebuild ring with updated self ID
	dn.dht.ring = make([]NodeID, 0, len(dn.dht.peers)+1)
	dn.dht.ring = append(dn.dht.ring, dn.dht.self.ID)
	for id := range dn.dht.peers {
		dn.dht.ring = append(dn.dht.ring, id)
	}
	sort.Slice(dn.dht.ring, func(i, j int) bool {
		return CompareNodeID(dn.dht.ring[i], dn.dht.ring[j]) < 0
	})
	dn.dht.mutex.Unlock()

	// Set network reference on DHT for query forwarding
	dn.dht.SetNetwork(dn)

	netLogger.Printf("DHT listening on %s (advertise %s, node ID: %s)",
		dn.dht.listenAddr, actualAddr, NodeIDToString(dn.dht.self.ID))

	// start accepting connections
	dn.wg.Add(1)
	go dn.acceptLoop()

	// join the network if we have bootstrap nodes
	if len(dn.bootstraps) > 0 {
		dn.wg.Add(1)
		go dn.joinNetwork()
		if EnableBootstrapPeerDiscovery {
			// Also start periodic peer discovery in case new nodes join after we bootstrap.
			dn.wg.Add(1)
			go dn.peerDiscoveryLoop()
		}
	}

	// periodic peer maintenance
	dn.wg.Add(1)
	go dn.maintenanceLoop()

	return nil
}

// Stop shuts down the DHT network
func (dn *DHTNetwork) Stop() error {
	dn.cancel()
	if dn.listener != nil {
		dn.listener.Close()
	}
	dn.wg.Wait()
	return nil
}

// acceptLoop accepts incoming connections
func (dn *DHTNetwork) acceptLoop() {
	defer dn.wg.Done()

	for {
		conn, err := dn.listener.Accept()
		if err != nil {
			select {
			case <-dn.ctx.Done():
				return
			default:
				netLogger.Printf("accept error: %v", err)
				continue
			}
		}
		go dn.handleConnection(conn)
	}
}

// handleConnection processes an incoming connection
func (dn *DHTNetwork) handleConnection(conn net.Conn) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(30 * time.Second))

	decoder := json.NewDecoder(conn)
	var msg Message
	if err := decoder.Decode(&msg); err != nil {
		netLogger.Printf("decode error: %v", err)
		return
	}

	response := dn.handleMessage(&msg)
	if response != nil {
		encoder := json.NewEncoder(conn)
		if err := encoder.Encode(response); err != nil {
			netLogger.Printf("encode error: %v", err)
		}
	}
}

// handleMessage processes a DHT message and returns a response
func (dn *DHTNetwork) handleMessage(msg *Message) *Message {
	// add sender as a peer
	if msg.Sender != "" && msg.Sender != dn.dht.self.Address {
		dn.dht.AddPeer(msg.Sender)
	}

	switch msg.Type {
	case MsgPing:
		return &Message{
			Type:   MsgPong,
			Sender: dn.dht.self.Address,
		}

	case MsgJoin:
		// someone is joining the network, send them our peer list
		peers := dn.dht.GetPeers()
		peerAddrs := make([]string, 0, len(peers))
		for _, p := range peers {
			peerAddrs = append(peerAddrs, p.Address)
		}
		payload, _ := json.Marshal(PeerListPayload{Peers: peerAddrs})
		return &Message{
			Type:    MsgPeerList,
			Sender:  dn.dht.self.Address,
			Payload: payload,
		}

	case MsgStore:
		var sp StorePayload
		if err := json.Unmarshal(msg.Payload, &sp); err != nil {
			netLogger.Printf("store unmarshal error: %v", err)
			return nil
		}

		source := msg.Sender
		if strings.TrimSpace(source) == "" {
			source = "unknown"
		}

		// If ring size <= k, all nodes are in k-closest by definition
		ringSize := dn.dht.GetRingSize()
		if ringSize <= ReplicationFactor {
			dn.dht.StoreTask(sp.Task, sp.Address)
			netLogger.Printf("accepted STORE from %s: %s -> %s", source, sp.Task, sp.Address)
			netLogger.Printf("stored %s -> %s (ring size %d <= k=%d)", sp.Task, sp.Address, ringSize, ReplicationFactor)
			return &Message{
				Type:   MsgOK,
				Sender: dn.dht.self.Address,
			}
		}

		// Check if we're in the k-closest nodes for this task
		if dn.dht.AmIInKClosest(sp.Task, ReplicationFactor) {
			dn.dht.StoreTask(sp.Task, sp.Address)
			netLogger.Printf("accepted STORE from %s: %s -> %s", source, sp.Task, sp.Address)
			netLogger.Printf("stored %s -> %s (in k-closest)", sp.Task, sp.Address)
			return &Message{
				Type:   MsgOK,
				Sender: dn.dht.self.Address,
			}
		}

		// Not in k-closest, reject but suggest closest known nodes
		closestNodes := dn.dht.FindKClosestNodes(sp.Task, ReplicationFactor)
		closestAddrs := make([]string, 0, len(closestNodes))
		for _, n := range closestNodes {
			if n != nil {
				closestAddrs = append(closestAddrs, n.Address)
			}
		}
		payload, _ := json.Marshal(NotResponsiblePayload{Closest: closestAddrs})
		netLogger.Printf("rejected store for %s (not in k-closest), suggesting: %v", sp.Task, closestAddrs)
		return &Message{
			Type:    MsgNotResponsible,
			Sender:  dn.dht.self.Address,
			Payload: payload,
		}

	case MsgFind:
		var fp FindPayload
		if err := json.Unmarshal(msg.Payload, &fp); err != nil {
			netLogger.Printf("find unmarshal error: %v", err)
			return nil
		}

		// If ring size <= k, all nodes are in k-closest by definition
		ringSize := dn.dht.GetRingSize()
		inKClosest := (ringSize <= ReplicationFactor) || dn.dht.AmIInKClosest(fp.Task, ReplicationFactor)

		// Check if we're in k-closest for this task
		if inKClosest {
			addrs := dn.dht.LookupTask(fp.Task)
			if len(addrs) > 0 {
				payload, _ := json.Marshal(FoundPayload{
					Task:      fp.Task,
					Addresses: addrs,
				})
				return &Message{
					Type:    MsgFoundData,
					Sender:  dn.dht.self.Address,
					Payload: payload,
				}
			}
			return &Message{
				Type:   MsgNotFound,
				Sender: dn.dht.self.Address,
			}
		}

		// Not in k-closest, return not found (client will try other k-closest nodes)
		return &Message{
			Type:   MsgNotFound,
			Sender: dn.dht.self.Address,
		}
	}

	return nil
}

// joinNetwork contacts bootstrap nodes to join the DHT
func (dn *DHTNetwork) joinNetwork() {
	defer dn.wg.Done()

	// try each bootstrap node
	for _, bootstrap := range dn.bootstraps {
		if bootstrap == dn.dht.self.Address {
			continue // don't join ourselves
		}

		netLogger.Printf("joining via bootstrap node %s", bootstrap)

		msg := Message{
			Type:   MsgJoin,
			Sender: dn.dht.self.Address,
		}

		resp, err := dn.sendMessage(bootstrap, &msg)
		if err != nil {
			netLogger.Printf("join error with %s: %v", bootstrap, err)
			continue
		}

		if resp.Type == MsgPeerList {
			var pl PeerListPayload
			if err := json.Unmarshal(resp.Payload, &pl); err != nil {
				netLogger.Printf("peer list unmarshal error: %v", err)
				continue
			}

			netLogger.Printf("received %d peers from %s", len(pl.Peers), bootstrap)
			for _, peer := range pl.Peers {
				if peer != dn.dht.self.Address {
					dn.dht.AddPeer(peer)
				}
			}
		}

		// add the bootstrap node itself
		dn.dht.AddPeer(bootstrap)
	}

	netLogger.Printf("joined network, %d peers known", dn.dht.GetRingSize()-1)
}

// peerDiscoveryLoop periodically queries bootstrap nodes for updated peer lists
// This allows nodes to discover peers that joined after the initial bootstrap
func (dn *DHTNetwork) peerDiscoveryLoop() {
	defer dn.wg.Done()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-dn.ctx.Done():
			return
		case <-ticker.C:
			// query bootstrap nodes for updated peer lists
			for _, bootstrap := range dn.bootstraps {
				if bootstrap == dn.dht.self.Address {
					continue
				}

				msg := Message{
					Type:   MsgJoin,
					Sender: dn.dht.self.Address,
				}

				resp, err := dn.sendMessage(bootstrap, &msg)
				if err != nil {
					// bootstrap node may be down, skip
					continue
				}

				if resp.Type == MsgPeerList {
					var pl PeerListPayload
					if err := json.Unmarshal(resp.Payload, &pl); err != nil {
						continue
					}

					// add any new peers we discover
					for _, peer := range pl.Peers {
						if peer != dn.dht.self.Address {
							dn.dht.AddPeer(peer)
						}
					}
				}
			}
		}
	}
}

// maintenanceLoop performs periodic DHT maintenance
func (dn *DHTNetwork) maintenanceLoop() {
	defer dn.wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-dn.ctx.Done():
			return
		case <-ticker.C:
			// ping all peers to check liveness
			peers := dn.dht.GetPeers()
			for _, peer := range peers {
				go dn.pingPeer(peer.Address)
			}

			// cleanup expired registrations (no heartbeat/REGISTER refresh)
			expired := dn.dht.CleanupExpiredRegistrations(ServiceHeartbeatTimeout)
			// cleanup stale data
			removed := dn.dht.CleanupStaleData()
			totalRemoved := expired + removed
			if totalRemoved > 0 {
				netLogger.Printf("cleanup removed %d entries (expired=%d, stale-task=%d, heartbeat-timeout=%s)", totalRemoved, expired, removed, ServiceHeartbeatTimeout)
			}
		}
	}
}

// pingPeer sends a ping to a peer and removes it if unreachable
func (dn *DHTNetwork) pingPeer(addr string) {
	msg := Message{
		Type:   MsgPing,
		Sender: dn.dht.self.Address,
	}

	_, err := dn.sendMessage(addr, &msg)
	if err != nil {
		// peer is unreachable, remove it
		nodeID := HashAddress(addr)
		dn.dht.RemovePeer(nodeID)
		netLogger.Printf("removed unreachable peer %s", addr)
	}
}

// sendMessage sends a message to a peer and waits for response
func (dn *DHTNetwork) sendMessage(addr string, msg *Message) (*Message, error) {
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(10 * time.Second))

	encoder := json.NewEncoder(conn)
	if err := encoder.Encode(msg); err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}

	decoder := json.NewDecoder(conn)
	var resp Message
	if err := decoder.Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	return &resp, nil
}

// Store sends a STORE request to the k-closest nodes
// If a node responds NOT_RESPONSIBLE, only retry on suggested nodes that are closer
func (dn *DHTNetwork) Store(task, address string) error {
	// Track attempted nodes and their distances
	attempted := make(map[string]struct{})
	taskHash := HashTask(task)

	// Priority queue: start with k-closest from local view
	queue := make([]string, 0)
	kClosest := dn.dht.FindKClosestNodes(task, ReplicationFactor)
	for _, node := range kClosest {
		if node != nil {
			queue = append(queue, node.Address)
		}
	}

	successCount := 0
	var lastErr error

	for len(queue) > 0 {
		addr := queue[0]
		queue = queue[1:]

		if _, seen := attempted[addr]; seen {
			continue
		}
		attempted[addr] = struct{}{}

		if addr == dn.dht.self.Address {
			dn.dht.StoreTask(task, address)
			netLogger.Printf("stored locally (k=%d): %s -> %s", ReplicationFactor, task, address)
			successCount++
			continue
		}

		payload, _ := json.Marshal(StorePayload{
			Task:    task,
			Address: address,
		})
		msg := Message{
			Type:    MsgStore,
			Sender:  dn.dht.self.Address,
			Payload: payload,
		}

		resp, err := dn.sendMessage(addr, &msg)
		if err != nil {
			netLogger.Printf("failed to store on %s: %v", addr, err)
			lastErr = err
			continue
		}

		if resp.Type == MsgOK {
			netLogger.Printf("stored on %s (k=%d): %s -> %s", addr, ReplicationFactor, task, address)
			successCount++
		} else if resp.Type == MsgNotResponsible && len(resp.Payload) > 0 {
			var nr NotResponsiblePayload
			if err := json.Unmarshal(resp.Payload, &nr); err == nil {
				// Only queue suggested nodes that are closer than ANY node we've tried
				farthestAttempted := dn.getFarthestDistance(taskHash, attempted)

				for _, newAddr := range nr.Closest {
					dn.dht.AddPeer(newAddr)

					if _, seen := attempted[newAddr]; seen {
						continue
					}

					// Check if this node is closer than the farthest we've tried
					newDist := Distance(taskHash, HashAddress(newAddr))
					if newDist.Cmp(farthestAttempted) < 0 {
						queue = append(queue, newAddr)
						netLogger.Printf("queuing closer node %s (from NOT_RESPONSIBLE)", newAddr)
					}
				}
			}
		} else {
			netLogger.Printf("unexpected response from %s: %s", addr, resp.Type)
		}
	}

	if successCount == 0 {
		if lastErr != nil {
			return fmt.Errorf("failed to store on any node: %w", lastErr)
		}
		return fmt.Errorf("failed to store on any node: no replicas succeeded")
	}

	netLogger.Printf("store complete: %d pool members for %s", successCount, task)
	return nil
}

// getFarthestDistance returns the distance to the farthest attempted node
func (dn *DHTNetwork) getFarthestDistance(taskHash NodeID, attempted map[string]struct{}) *big.Int {
	var farthest *big.Int
	for addr := range attempted {
		dist := Distance(taskHash, HashAddress(addr))
		if farthest == nil || dist.Cmp(farthest) > 0 {
			farthest = dist
		}
	}
	if farthest == nil {
		// Return max distance if nothing attempted yet
		farthest = new(big.Int).Lsh(big.NewInt(1), 256)
	}
	return farthest
}

// Find sends a FIND request to retrieve task addresses from k-closest nodes
func (dn *DHTNetwork) Find(task string) ([]string, error) {
	kClosest := dn.dht.FindKClosestNodes(task, ReplicationFactor)

	// Check locally first if we're in k-closest
	for _, node := range kClosest {
		if node.ID == dn.dht.self.ID {
			addrs := dn.dht.LookupTask(task)
			if len(addrs) > 0 {
				netLogger.Printf("found locally: %s -> %d addresses", task, len(addrs))
				return addrs, nil
			}
			break
		}
	}

	// Query k-closest peers until we get a result
	payload, _ := json.Marshal(FindPayload{Task: task})
	msg := Message{
		Type:    MsgFind,
		Sender:  dn.dht.self.Address,
		Payload: payload,
	}

	for _, node := range kClosest {
		if node.ID == dn.dht.self.ID {
			continue // Already checked locally
		}

		resp, err := dn.sendMessage(node.Address, &msg)
		if err != nil {
			netLogger.Printf("find from %s failed: %v", node.Address, err)
			continue
		}

		if resp.Type == MsgFoundData {
			var fp FoundPayload
			if err := json.Unmarshal(resp.Payload, &fp); err != nil {
				netLogger.Printf("unmarshal found error: %v", err)
				continue
			}
			if len(fp.Addresses) > 0 {
				netLogger.Printf("found on %s: %s -> %d addresses", node.Address, task, len(fp.Addresses))
				return fp.Addresses, nil
			}
		}
	}

	netLogger.Printf("not found on any k-closest node: %s", task)
	return nil, nil
}

// QueryPeerForTask queries a specific peer for a task's addresses
// Used by LookupTask for query forwarding when local node is not in k-nearest
func (dn *DHTNetwork) QueryPeerForTask(peerAddr string, task string) []string {
	payload, err := json.Marshal(FindPayload{Task: task})
	if err != nil {
		return nil
	}

	msg := Message{
		Type:    MsgFind,
		Sender:  dn.dht.self.Address,
		Payload: payload,
	}

	resp, err := dn.sendMessage(peerAddr, &msg)
	if err != nil {
		netLogger.Printf("query peer %s for task %s failed: %v", peerAddr, task, err)
		return nil
	}

	if resp.Type == MsgFoundData {
		var fp FoundPayload
		if err := json.Unmarshal(resp.Payload, &fp); err != nil {
			netLogger.Printf("unmarshal found error: %v", err)
			return nil
		}
		if len(fp.Addresses) > 0 {
			netLogger.Printf("query forwarding: found %s on %s -> %d addresses", task, peerAddr, len(fp.Addresses))
			return fp.Addresses
		}
	}

	return nil
}

// ListenForBroadcasts listens for server broadcasts on the discovery port
// and adds them as potential peers
func (dn *DHTNetwork) ListenForBroadcasts(port int) {
	addr := net.UDPAddr{Port: port}
	conn, err := net.ListenUDP("udp", &addr)
	if err != nil {
		netLogger.Printf("listen broadcasts: %v", err)
		return
	}
	defer conn.Close()

	netLogger.Printf("listening for broadcasts on UDP %d", port)
	buf := make([]byte, 4096)

	for {
		select {
		case <-dn.ctx.Done():
			return
		default:
		}

		conn.SetReadDeadline(time.Now().Add(1 * time.Second))
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			netLogger.Printf("read broadcast: %v", err)
			continue
		}

		// try to parse as JSON
		var announcement map[string]interface{}
		if err := json.Unmarshal(buf[:n], &announcement); err != nil {
			continue
		}

		// check if it's a TDS server announcement
		if typ, ok := announcement["type"].(string); ok && typ == "tds_server" {
			if addr, ok := announcement["address"].(string); ok {
				if port, ok := announcement["port"].(float64); ok {
					peerAddr := fmt.Sprintf("%s:%d", addr, int(port))
					netLogger.Printf("discovered server via broadcast: %s", peerAddr)
					// could add as peer or use for other purposes
				}
			}
		}
	}
}
