package dht

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

var netLogger = log.New(os.Stdout, "[dht] ", log.LstdFlags)

// Message types for DHT communication
const (
	MsgPing        = "PING"
	MsgPong        = "PONG"
	MsgJoin        = "JOIN"
	MsgPeerList    = "PEERLIST"
	MsgStore       = "STORE"
	MsgFind        = "FIND"
	MsgFoundData   = "FOUND"
	MsgNotFound    = "NOTFOUND"
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
	
	netLogger.Printf("DHT listening on %s (node ID: %s)", 
		dn.dht.listenAddr, NodeIDToString(dn.dht.self.ID))
	
	// start accepting connections
	dn.wg.Add(1)
	go dn.acceptLoop()
	
	// join the network if we have bootstrap nodes
	if len(dn.bootstraps) > 0 {
		dn.wg.Add(1)
		go dn.joinNetwork()
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
	if msg.Sender != "" && msg.Sender != dn.dht.listenAddr {
		dn.dht.AddPeer(msg.Sender)
	}
	
	switch msg.Type {
	case MsgPing:
		return &Message{
			Type:   MsgPong,
			Sender: dn.dht.listenAddr,
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
			Sender:  dn.dht.listenAddr,
			Payload: payload,
		}
		
	case MsgStore:
		var sp StorePayload
		if err := json.Unmarshal(msg.Payload, &sp); err != nil {
			netLogger.Printf("store unmarshal error: %v", err)
			return nil
		}
		
		// check if we're responsible for this task
		if dn.dht.IsResponsibleFor(sp.Task) {
			dn.dht.StoreTask(sp.Task, sp.Address)
			netLogger.Printf("stored %s -> %s", sp.Task, sp.Address)
			return &Message{
				Type:   "OK",
				Sender: dn.dht.listenAddr,
			}
		}
		
		// not responsible, forward to correct node
		closest := dn.dht.FindClosestNode(sp.Task)
		if closest.ID != dn.dht.self.ID {
			// forward to the responsible node
			go dn.ForwardStore(closest.Address, sp.Task, sp.Address)
		}
		return &Message{
			Type:   "FORWARDED",
			Sender: dn.dht.listenAddr,
		}
		
	case MsgFind:
		var fp FindPayload
		if err := json.Unmarshal(msg.Payload, &fp); err != nil {
			netLogger.Printf("find unmarshal error: %v", err)
			return nil
		}
		
		// check if we're responsible for this task
		if dn.dht.IsResponsibleFor(fp.Task) {
			addrs := dn.dht.LookupTask(fp.Task)
			if len(addrs) > 0 {
				payload, _ := json.Marshal(FoundPayload{
					Task:      fp.Task,
					Addresses: addrs,
				})
				return &Message{
					Type:    MsgFoundData,
					Sender:  dn.dht.listenAddr,
					Payload: payload,
				}
			}
			return &Message{
				Type:   MsgNotFound,
				Sender: dn.dht.listenAddr,
			}
		}
		
		// not responsible, return the responsible node address
		closest := dn.dht.FindClosestNode(fp.Task)
		payload, _ := json.Marshal(map[string]string{
			"redirect": closest.Address,
		})
		return &Message{
			Type:    "REDIRECT",
			Sender:  dn.dht.listenAddr,
			Payload: payload,
		}
	}
	
	return nil
}

// joinNetwork contacts bootstrap nodes to join the DHT
func (dn *DHTNetwork) joinNetwork() {
	defer dn.wg.Done()
	
	// try each bootstrap node
	for _, bootstrap := range dn.bootstraps {
		if bootstrap == dn.dht.listenAddr {
			continue // don't join ourselves
		}
		
		netLogger.Printf("joining via bootstrap node %s", bootstrap)
		
		msg := Message{
			Type:   MsgJoin,
			Sender: dn.dht.listenAddr,
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
				if peer != dn.dht.listenAddr {
					dn.dht.AddPeer(peer)
				}
			}
		}
		
		// add the bootstrap node itself
		dn.dht.AddPeer(bootstrap)
	}
	
	netLogger.Printf("joined network, %d peers known", dn.dht.GetRingSize()-1)
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
			
			// cleanup stale data
			removed := dn.dht.CleanupStaleData()
			if removed > 0 {
				netLogger.Printf("cleaned up %d stale task entries", removed)
			}
		}
	}
}

// pingPeer sends a ping to a peer and removes it if unreachable
func (dn *DHTNetwork) pingPeer(addr string) {
	msg := Message{
		Type:   MsgPing,
		Sender: dn.dht.listenAddr,
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

// Store sends a STORE request to the appropriate node
func (dn *DHTNetwork) Store(task, address string) error {
	closest := dn.dht.FindClosestNode(task)
	
	// if we're responsible, store locally
	if closest.ID == dn.dht.self.ID {
		dn.dht.StoreTask(task, address)
		netLogger.Printf("stored locally: %s -> %s", task, address)
		return nil
	}
	
	// otherwise, send to the responsible node
	payload, _ := json.Marshal(StorePayload{
		Task:    task,
		Address: address,
	})
	
	msg := Message{
		Type:    MsgStore,
		Sender:  dn.dht.listenAddr,
		Payload: payload,
	}
	
	_, err := dn.sendMessage(closest.Address, &msg)
	if err != nil {
		return fmt.Errorf("store to %s: %w", closest.Address, err)
	}
	
	netLogger.Printf("stored on %s: %s -> %s", closest.Address, task, address)
	return nil
}

// Find sends a FIND request to retrieve task addresses
func (dn *DHTNetwork) Find(task string) ([]string, error) {
	closest := dn.dht.FindClosestNode(task)
	
	// if we're responsible, lookup locally
	if closest.ID == dn.dht.self.ID {
		addrs := dn.dht.LookupTask(task)
		netLogger.Printf("found locally: %s -> %d addresses", task, len(addrs))
		return addrs, nil
	}
	
	// otherwise, query the responsible node
	payload, _ := json.Marshal(FindPayload{Task: task})
	
	msg := Message{
		Type:    MsgFind,
		Sender:  dn.dht.listenAddr,
		Payload: payload,
	}
	
	maxRedirects := 3
	targetAddr := closest.Address
	
	for i := 0; i < maxRedirects; i++ {
		resp, err := dn.sendMessage(targetAddr, &msg)
		if err != nil {
			return nil, fmt.Errorf("find from %s: %w", targetAddr, err)
		}
		
		if resp.Type == MsgFoundData {
			var fp FoundPayload
			if err := json.Unmarshal(resp.Payload, &fp); err != nil {
				return nil, fmt.Errorf("unmarshal found: %w", err)
			}
			netLogger.Printf("found on %s: %s -> %d addresses", targetAddr, task, len(fp.Addresses))
			return fp.Addresses, nil
		}
		
		if resp.Type == MsgNotFound {
			return nil, nil
		}
		
		if resp.Type == "REDIRECT" {
			var redirect map[string]string
			if err := json.Unmarshal(resp.Payload, &redirect); err != nil {
				return nil, fmt.Errorf("unmarshal redirect: %w", err)
			}
			targetAddr = redirect["redirect"]
			continue
		}
		
		return nil, fmt.Errorf("unexpected response: %s", resp.Type)
	}
	
	return nil, fmt.Errorf("too many redirects")
}

// ForwardStore forwards a store request to another node
func (dn *DHTNetwork) ForwardStore(targetAddr, task, address string) error {
	payload, _ := json.Marshal(StorePayload{
		Task:    task,
		Address: address,
	})
	
	msg := Message{
		Type:    MsgStore,
		Sender:  dn.dht.listenAddr,
		Payload: payload,
	}
	
	_, err := dn.sendMessage(targetAddr, &msg)
	return err
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

// SendUDP sends a simple UDP message (for backward compatibility with existing proxy)
func SendUDP(serverAddr, command string) (string, error) {
	conn, err := net.DialTimeout("udp", serverAddr, 2*time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	
	if _, err := fmt.Fprintf(conn, "%s\n", command); err != nil {
		return "", err
	}
	
	buf := make([]byte, 2048)
	n, err := conn.Read(buf)
	if err != nil {
		return "", err
	}
	
	return strings.TrimSpace(string(buf[:n])), nil
}

// DialTCP sends a command over TCP and returns the response
func DialTCP(serverAddr, command string) (string, error) {
	conn, err := net.DialTimeout("tcp", serverAddr, 2*time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	
	fmt.Fprintf(conn, "%s\n", command)
	
	r := bufio.NewReader(conn)
	resp, err := r.ReadString('\n')
	if err != nil {
		return "", err
	}
	
	return strings.TrimSpace(resp), nil
}
