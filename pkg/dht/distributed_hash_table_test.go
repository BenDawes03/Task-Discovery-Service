package dht

import (
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"
)

func mustNewDHTForTest(t *testing.T, listenAddr string) *DHT {
	t.Helper()
	d, err := NewDHT(listenAddr)
	if err != nil {
		t.Fatalf("NewDHT failed: %v", err)
	}
	return d
}

func forceTaskCloserTo(taskPrefix string, closer NodeID, farther NodeID) string {
	for i := 0; i < 200000; i++ {
		task := fmt.Sprintf("%s-%d", taskPrefix, i)
		taskID := HashTask(task)
		if Distance(taskID, closer).Cmp(Distance(taskID, farther)) < 0 {
			return task
		}
	}
	return taskPrefix + "-fallback"
}

func TestCanonicalAddressForHash(t *testing.T) {
	if got := canonicalAddressForHash("localhost:7000"); got != "127.0.0.1:7000" {
		t.Fatalf("unexpected canonical localhost form: %s", got)
	}
	if got := canonicalAddressForHash("[::1]:7000"); got != "127.0.0.1:7000" {
		t.Fatalf("unexpected canonical ::1 form: %s", got)
	}
	if got := canonicalAddressForHash("0.0.0.0:7000"); got != "127.0.0.1:7000" {
		t.Fatalf("unexpected canonical wildcard form: %s", got)
	}
	if got := canonicalAddressForHash("example.com:7000"); got != "example.com:7000" {
		t.Fatalf("unexpected canonical host form: %s", got)
	}
	if got := canonicalAddressForHash("not-an-addr"); got != "not-an-addr" {
		t.Fatalf("expected passthrough invalid addr, got %s", got)
	}
}

func TestHashAddressAliasesMatch(t *testing.T) {
	a := HashAddress("localhost:6010")
	b := HashAddress("127.0.0.1:6010")
	if a != b {
		t.Fatal("expected localhost and 127.0.0.1 to hash identically")
	}
}

func TestNewDHT_PortOnlyListenAddressIsAdvertisedOnLoopback(t *testing.T) {
	d := mustNewDHTForTest(t, ":0")
	self := d.GetSelf()
	if !strings.HasPrefix(self.Address, "127.0.0.1:") {
		t.Fatalf("expected loopback advertised address, got %s", self.Address)
	}
	if d.listenAddr != ":0" {
		t.Fatalf("listenAddr should remain unchanged, got %s", d.listenAddr)
	}
}

func TestAddPeerDeduplicatesAndUpdatesLastSeen(t *testing.T) {
	d := mustNewDHTForTest(t, "127.0.0.1:0")
	peerAddr := "127.0.0.1:7001"
	d.AddPeer(peerAddr)
	if d.GetRingSize() != 2 {
		t.Fatalf("expected ring size 2, got %d", d.GetRingSize())
	}

	peerID := HashAddress(peerAddr)
	first := d.peers[peerID].LastSeen
	time.Sleep(5 * time.Millisecond)
	d.AddPeer(peerAddr)
	second := d.peers[peerID].LastSeen

	if !second.After(first) {
		t.Fatal("expected LastSeen update on duplicate AddPeer")
	}
	if d.GetRingSize() != 2 {
		t.Fatalf("ring size should remain 2 after duplicate add, got %d", d.GetRingSize())
	}
}

func TestRemovePeerRebuildsRing(t *testing.T) {
	d := mustNewDHTForTest(t, "127.0.0.1:0")
	peerAddr := "127.0.0.1:7002"
	peerID := HashAddress(peerAddr)
	d.AddPeer(peerAddr)
	d.RemovePeer(peerID)

	if d.GetRingSize() != 1 {
		t.Fatalf("expected ring size 1 after remove, got %d", d.GetRingSize())
	}
	if _, ok := d.peers[peerID]; ok {
		t.Fatal("peer still exists after RemovePeer")
	}
}

func TestFindClosestNodeAndIsResponsibleFor(t *testing.T) {
	d := mustNewDHTForTest(t, "127.0.0.1:0")
	peerAddr := "127.0.0.1:7003"
	d.AddPeer(peerAddr)

	task := forceTaskCloserTo("closest", HashAddress(peerAddr), d.self.ID)
	closest := d.FindClosestNode(task)
	if closest == nil || closest.Address != peerAddr {
		t.Fatalf("expected closest node %s, got %+v", peerAddr, closest)
	}
	if d.IsResponsibleFor(task) {
		t.Fatal("self should not be responsible for task closer to peer")
	}
}

func TestFindKClosestNodesAndAmIInKClosest(t *testing.T) {
	d := mustNewDHTForTest(t, "127.0.0.1:0")
	d.AddPeer("127.0.0.1:7101")
	d.AddPeer("127.0.0.1:7102")
	d.AddPeer("127.0.0.1:7103")

	task := "kclosest-task"
	k := 3
	nodes := d.FindKClosestNodes(task, k)
	if len(nodes) != k {
		t.Fatalf("expected %d nodes, got %d", k, len(nodes))
	}

	taskID := HashTask(task)
	var prev *big.Int
	for _, n := range nodes {
		dist := Distance(taskID, n.ID)
		if prev != nil && prev.Cmp(dist) > 0 {
			t.Fatalf("distances not sorted ascending: prev=%s current=%s", prev.String(), dist.String())
		}
		prev = dist
	}

	inTopK := false
	for _, n := range nodes {
		if n.ID == d.self.ID {
			inTopK = true
			break
		}
	}
	if d.AmIInKClosest(task, k) != inTopK {
		t.Fatalf("AmIInKClosest mismatch: expected %v", inTopK)
	}
}

func TestStoreTaskAvoidsDuplicatesAndLookupReturnsCopy(t *testing.T) {
	d := mustNewDHTForTest(t, "127.0.0.1:0")
	d.StoreTask("task-a", "10.0.0.1:9000")
	d.StoreTask("task-a", "10.0.0.1:9000")
	d.StoreTask("task-a", "10.0.0.2:9000")

	out := d.LookupTask("task-a")
	if len(out) != 2 {
		t.Fatalf("expected 2 unique addresses, got %d (%v)", len(out), out)
	}

	out[0] = "mutated"
	recheck := d.LookupTask("task-a")
	if recheck[0] == "mutated" {
		t.Fatal("LookupTask must return a copy, not shared underlying slice")
	}
}

func TestStoreTaskDuplicateRefreshesHeartbeat(t *testing.T) {
	d := mustNewDHTForTest(t, "127.0.0.1:0")

	oldTimeout := ServiceHeartbeatTimeout
	ServiceHeartbeatTimeout = 40 * time.Millisecond
	defer func() { ServiceHeartbeatTimeout = oldTimeout }()

	d.StoreTask("task-hb", "10.0.0.1:9000")
	time.Sleep(25 * time.Millisecond)
	// Duplicate REGISTER should refresh heartbeat instead of adding a duplicate entry.
	d.StoreTask("task-hb", "10.0.0.1:9000")
	time.Sleep(25 * time.Millisecond)

	removed := d.CleanupExpiredRegistrations(ServiceHeartbeatTimeout)
	if removed != 0 {
		t.Fatalf("expected no removal after heartbeat refresh, got removed=%d", removed)
	}

	got := d.LookupTask("task-hb")
	if len(got) != 1 || got[0] != "10.0.0.1:9000" {
		t.Fatalf("expected refreshed registration to remain, got %v", got)
	}
}

func TestCleanupExpiredRegistrationsRemovesStaleEntries(t *testing.T) {
	d := mustNewDHTForTest(t, "127.0.0.1:0")

	oldTimeout := ServiceHeartbeatTimeout
	ServiceHeartbeatTimeout = 20 * time.Millisecond
	defer func() { ServiceHeartbeatTimeout = oldTimeout }()

	d.StoreTask("task-expire", "10.0.0.1:9000")
	time.Sleep(30 * time.Millisecond)

	if got := d.LookupTask("task-expire"); got != nil {
		t.Fatalf("expected expired entry to be hidden from lookup, got %v", got)
	}

	removed := d.CleanupExpiredRegistrations(ServiceHeartbeatTimeout)
	if removed != 1 {
		t.Fatalf("expected one stale registration removed, got %d", removed)
	}

	if snap := d.GetStorageSnapshot(); len(snap) != 0 {
		t.Fatalf("expected empty storage snapshot after cleanup, got %v", snap)
	}
}

func TestLookupTaskForwardsWhenNotResponsible(t *testing.T) {
	oldK := ReplicationFactor
	ReplicationFactor = 1
	defer func() { ReplicationFactor = oldK }()

	d := mustNewDHTForTest(t, "127.0.0.1:0")
	selfAddr := d.self.Address

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen mock peer: %v", err)
	}
	defer ln.Close()
	peerAddr := ln.Addr().String()

	d.AddPeer(peerAddr)
	task := forceTaskCloserTo("forward", HashAddress(peerAddr), d.self.ID)

	dn := NewDHTNetwork(d, nil)
	d.SetNetwork(dn)
	d.self.Address = selfAddr

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		dec := json.NewDecoder(conn)
		var msg Message
		if err := dec.Decode(&msg); err != nil || msg.Type != MsgFind {
			return
		}

		payload, _ := json.Marshal(FoundPayload{Task: task, Addresses: []string{"10.10.10.10:80"}})
		_ = json.NewEncoder(conn).Encode(&Message{Type: MsgFoundData, Sender: peerAddr, Payload: payload})
	}()

	out := d.LookupTask(task)
	if len(out) != 1 || out[0] != "10.10.10.10:80" {
		t.Fatalf("expected forwarded lookup result, got %v", out)
	}
	<-done
}

func TestLookupTaskNoForwardWhenResponsibleAndNoData(t *testing.T) {
	oldK := ReplicationFactor
	ReplicationFactor = 1
	defer func() { ReplicationFactor = oldK }()

	d := mustNewDHTForTest(t, "127.0.0.1:0")
	task := forceTaskCloserTo("local", d.self.ID, HashAddress("127.0.0.1:7999"))
	if got := d.LookupTask(task); got != nil {
		t.Fatalf("expected nil for responsible node with no data, got %v", got)
	}
}

func TestForwardLookupSkipsSelfAndReturnsFirstMatch(t *testing.T) {
	d := mustNewDHTForTest(t, "127.0.0.1:0")
	peer1 := &Node{ID: HashAddress("127.0.0.1:8011"), Address: d.self.Address}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen peer2: %v", err)
	}
	defer ln.Close()
	peer2Addr := ln.Addr().String()
	peer2 := &Node{ID: HashAddress(peer2Addr), Address: peer2Addr}

	dn := NewDHTNetwork(d, nil)
	d.SetNetwork(dn)

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		var msg Message
		if err := json.NewDecoder(conn).Decode(&msg); err != nil || msg.Type != MsgFind {
			return
		}
		payload, _ := json.Marshal(FoundPayload{Task: "task", Addresses: []string{"1.1.1.1:1"}})
		_ = json.NewEncoder(conn).Encode(&Message{Type: MsgFoundData, Sender: peer2Addr, Payload: payload})
	}()

	got := d.forwardLookup("task", []*Node{peer1, peer2})
	if len(got) != 1 || got[0] != "1.1.1.1:1" {
		t.Fatalf("unexpected forwardLookup result: %v", got)
	}
}

func TestCleanupStaleDataRemovesTasksNotInKClosest(t *testing.T) {
	oldK := ReplicationFactor
	ReplicationFactor = 1
	defer func() { ReplicationFactor = oldK }()

	d := mustNewDHTForTest(t, "127.0.0.1:0")
	peerAddr := "127.0.0.1:8201"
	d.AddPeer(peerAddr)

	notSelfTask := forceTaskCloserTo("cleanup-remote", HashAddress(peerAddr), d.self.ID)
	selfTask := forceTaskCloserTo("cleanup-self", d.self.ID, HashAddress(peerAddr))

	d.StoreTask(notSelfTask, "10.0.0.5:5")
	d.StoreTask(selfTask, "10.0.0.6:6")

	removed := d.CleanupStaleData()
	if removed != 1 {
		t.Fatalf("expected 1 removed task, got %d", removed)
	}
	if got := d.LookupTask(notSelfTask); len(got) != 0 {
		t.Fatalf("expected remote task removed, got %v", got)
	}
	if got := d.LookupTask(selfTask); len(got) == 0 {
		t.Fatal("expected self task to remain")
	}
}

func TestGetStorageSnapshotDeepCopy(t *testing.T) {
	d := mustNewDHTForTest(t, "127.0.0.1:0")
	d.StoreTask("task", "addr")

	snap := d.GetStorageSnapshot()
	snap["task"][0] = "changed"
	if got := d.LookupTask("task"); len(got) == 0 || got[0] != "addr" {
		t.Fatalf("expected original storage unchanged, got %v", got)
	}
}

func TestCompareNodeIDAndNodeIDHelpers(t *testing.T) {
	a := NodeIDFromUint64(1)
	b := NodeIDFromUint64(2)
	if CompareNodeID(a, b) >= 0 {
		t.Fatal("expected a < b")
	}
	if CompareNodeID(b, a) <= 0 {
		t.Fatal("expected b > a")
	}
	if CompareNodeID(a, a) != 0 {
		t.Fatal("expected equality for same node ID")
	}
	if s := NodeIDToString(a); len(s) != 8 {
		t.Fatalf("expected 8-char short node string, got %q", s)
	}
}

func TestDistanceWrapAroundAndPositive(t *testing.T) {
	a := NodeIDFromUint64(10)
	b := NodeIDFromUint64(20)
	if Distance(a, b).Cmp(big.NewInt(10)) != 0 {
		t.Fatalf("expected direct distance 10, got %s", Distance(a, b).String())
	}

	wrapped := Distance(b, a)
	if wrapped.Sign() <= 0 {
		t.Fatalf("expected positive wrapped distance, got %s", wrapped.String())
	}
}

func TestGetPeersAndGetSelfReturnCopies(t *testing.T) {
	d := mustNewDHTForTest(t, "127.0.0.1:0")
	addr := "127.0.0.1:8301"
	d.AddPeer(addr)

	peers := d.GetPeers()
	if len(peers) != 1 {
		t.Fatalf("expected one peer, got %d", len(peers))
	}
	peers[0].Address = "mutated"

	peers2 := d.GetPeers()
	if peers2[0].Address != addr {
		t.Fatalf("expected peer copy isolation, got %s", peers2[0].Address)
	}

	self := d.GetSelf()
	orig := d.self.Address
	self.Address = "mutated"
	if d.self.Address != orig {
		t.Fatal("GetSelf should return copy")
	}
}

func TestGetLocalIPReturnsSomething(t *testing.T) {
	ip := GetLocalIP()
	if strings.TrimSpace(ip) == "" {
		t.Fatal("GetLocalIP returned empty string")
	}
}
