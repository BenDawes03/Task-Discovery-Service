package dht

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"
)

func TestHandleMessagePingJoinAndBadPayloads(t *testing.T) {
	d := mustNewDHTForTest(t, "127.0.0.1:0")
	d.AddPeer("127.0.0.1:9001")
	dn := NewDHTNetwork(d, nil)

	pong := dn.handleMessage(&Message{Type: MsgPing, Sender: "127.0.0.1:1"})
	if pong == nil || pong.Type != MsgPong {
		t.Fatalf("expected PONG response, got %+v", pong)
	}

	join := dn.handleMessage(&Message{Type: MsgJoin, Sender: "127.0.0.1:2"})
	if join == nil || join.Type != MsgPeerList {
		t.Fatalf("expected PEERLIST response, got %+v", join)
	}

	if dn.handleMessage(&Message{Type: MsgStore, Payload: []byte("{bad")}) != nil {
		t.Fatal("expected nil response for malformed STORE payload")
	}
	if dn.handleMessage(&Message{Type: MsgFind, Payload: []byte("{bad")}) != nil {
		t.Fatal("expected nil response for malformed FIND payload")
	}
}

func TestHandleMessageStoreAndFindPaths(t *testing.T) {
	oldK := ReplicationFactor
	ReplicationFactor = 3
	defer func() { ReplicationFactor = oldK }()

	d := mustNewDHTForTest(t, "127.0.0.1:0")
	dn := NewDHTNetwork(d, nil)

	sp, _ := json.Marshal(StorePayload{Task: "t1", Address: "10.0.0.1:1"})
	storeResp := dn.handleMessage(&Message{Type: MsgStore, Payload: sp})
	if storeResp == nil || storeResp.Type != MsgOK {
		t.Fatalf("expected MsgOK for store, got %+v", storeResp)
	}
	if got := d.LookupTask("t1"); len(got) != 1 || got[0] != "10.0.0.1:1" {
		t.Fatalf("expected local store result, got %v", got)
	}

	fp, _ := json.Marshal(FindPayload{Task: "t1"})
	foundResp := dn.handleMessage(&Message{Type: MsgFind, Payload: fp})
	if foundResp == nil || foundResp.Type != MsgFoundData {
		t.Fatalf("expected MsgFoundData, got %+v", foundResp)
	}

	fpMissing, _ := json.Marshal(FindPayload{Task: "missing"})
	notFoundResp := dn.handleMessage(&Message{Type: MsgFind, Payload: fpMissing})
	if notFoundResp == nil || notFoundResp.Type != MsgNotFound {
		t.Fatalf("expected MsgNotFound, got %+v", notFoundResp)
	}
}

func TestHandleMessageStoreNotResponsible(t *testing.T) {
	oldK := ReplicationFactor
	ReplicationFactor = 1
	defer func() { ReplicationFactor = oldK }()

	d := mustNewDHTForTest(t, "127.0.0.1:0")
	peerAddr := "127.0.0.1:9101"
	d.AddPeer(peerAddr)
	dn := NewDHTNetwork(d, nil)

	task := forceTaskCloserTo("not-resp", HashAddress(peerAddr), d.self.ID)
	sp, _ := json.Marshal(StorePayload{Task: task, Address: "10.0.0.3:3"})
	resp := dn.handleMessage(&Message{Type: MsgStore, Payload: sp})
	if resp == nil || resp.Type != MsgNotResponsible {
		t.Fatalf("expected NOT_RESPONSIBLE, got %+v", resp)
	}

	var nr NotResponsiblePayload
	if err := json.Unmarshal(resp.Payload, &nr); err != nil {
		t.Fatalf("unmarshal NotResponsible payload: %v", err)
	}
	if len(nr.Closest) == 0 {
		t.Fatal("expected closest node suggestions in NOT_RESPONSIBLE payload")
	}
}

func TestGetFarthestDistance(t *testing.T) {
	d := mustNewDHTForTest(t, "127.0.0.1:0")
	dn := NewDHTNetwork(d, nil)
	taskHash := HashTask("farthest")

	empty := dn.getFarthestDistance(taskHash, map[string]struct{}{})
	max := new(big.Int).Lsh(big.NewInt(1), 256)
	if empty.Cmp(max) != 0 {
		t.Fatalf("expected max distance for empty attempted set")
	}

	attempted := map[string]struct{}{
		"127.0.0.1:9201": {},
		"127.0.0.1:9202": {},
	}
	f := dn.getFarthestDistance(taskHash, attempted)
	if f.Sign() <= 0 {
		t.Fatalf("expected positive farthest distance, got %s", f.String())
	}
}

func TestStoreAndFindSingleNodeAndFailurePath(t *testing.T) {
	oldK := ReplicationFactor
	ReplicationFactor = 3
	defer func() { ReplicationFactor = oldK }()

	d := mustNewDHTForTest(t, "127.0.0.1:0")
	dn := NewDHTNetwork(d, nil)

	if err := dn.Store("task-solo", "10.1.1.1:1"); err != nil {
		t.Fatalf("single-node store failed: %v", err)
	}
	got, err := dn.Find("task-solo")
	if err != nil {
		t.Fatalf("single-node find failed: %v", err)
	}
	if len(got) != 1 || got[0] != "10.1.1.1:1" {
		t.Fatalf("unexpected find result: %v", got)
	}

	// Force a failure path by setting replication to 1 and making only peer candidate unreachable.
	ReplicationFactor = 1
	d2 := mustNewDHTForTest(t, "127.0.0.1:0")
	d2.AddPeer("127.0.0.1:65530")
	task := forceTaskCloserTo("store-fail", HashAddress("127.0.0.1:65530"), d2.self.ID)
	dn2 := NewDHTNetwork(d2, nil)
	err = dn2.Store(task, "10.2.2.2:2")
	if err == nil {
		t.Fatal("expected store failure when all remote replicas fail")
	}
}

func TestQueryPeerForTaskAndSendMessageDecodeError(t *testing.T) {
	d := mustNewDHTForTest(t, "127.0.0.1:0")
	dn := NewDHTNetwork(d, nil)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen mock peer: %v", err)
	}
	defer ln.Close()

	peerAddr := ln.Addr().String()
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
		if dec.Decode(&msg) != nil {
			return
		}

		payload, _ := json.Marshal(FoundPayload{Task: "x", Addresses: []string{"9.9.9.9:9"}})
		_ = json.NewEncoder(conn).Encode(&Message{Type: MsgFoundData, Sender: peerAddr, Payload: payload})
	}()

	out := dn.QueryPeerForTask(peerAddr, "x")
	if len(out) != 1 || out[0] != "9.9.9.9:9" {
		t.Fatalf("unexpected QueryPeerForTask output: %v", out)
	}
	<-done

	lnBad, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen bad peer: %v", err)
	}
	defer lnBad.Close()

	go func() {
		conn, err := lnBad.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = conn.Write([]byte("not-json\n"))
	}()

	_, err = dn.sendMessage(lnBad.Addr().String(), &Message{Type: MsgPing, Sender: "s"})
	if err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("expected decode error, got %v", err)
	}
}

func TestStartStopAndRegistryFlow(t *testing.T) {
	reg, err := NewDHTRegistry("0", nil)
	if err != nil {
		t.Fatalf("NewDHTRegistry failed: %v", err)
	}

	if err := reg.Start(); err != nil {
		t.Fatalf("registry start failed: %v", err)
	}
	t.Cleanup(func() { _ = reg.Stop() })

	if err := reg.Register("service.task", "127.0.0.1:9900"); err != nil {
		t.Fatalf("register failed: %v", err)
	}

	addr, err := reg.Query("service.task")
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if addr != "127.0.0.1:9900" {
		t.Fatalf("unexpected query result: %s", addr)
	}

	all, err := reg.QueryAll("service.task")
	if err != nil {
		t.Fatalf("query all failed: %v", err)
	}
	if len(all) != 1 || all[0] != "127.0.0.1:9900" {
		t.Fatalf("unexpected query all result: %v", all)
	}

	regs, queries, errs := reg.Stats()
	if regs != 1 || queries != 2 || errs != 0 {
		t.Fatalf("unexpected stats: regs=%d queries=%d errs=%d", regs, queries, errs)
	}

	snap := reg.Snapshot()
	if snap.RingSize < 1 || snap.StorageSize < 1 || snap.Address == "" || snap.NodeID == "" {
		t.Fatalf("invalid snapshot: %+v", snap)
	}

	info := reg.GetDHTInfo()
	if info["ring_size"] == nil || info["address"] == nil {
		t.Fatalf("unexpected dht info: %+v", info)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- RunDHTPeerListener(ctx, reg)
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("RunDHTPeerListener returned error: %v", err)
	}
}

func TestNormalizeListenAddrAndLogging(t *testing.T) {
	if got := normalizeListenAddr(" 6001 "); got != ":6001" {
		t.Fatalf("expected :6001, got %s", got)
	}
	if got := normalizeListenAddr("127.0.0.1:6001"); got != "127.0.0.1:6001" {
		t.Fatalf("expected unchanged host:port, got %s", got)
	}
	if got := normalizeListenAddr(""); got != "" {
		t.Fatalf("expected empty unchanged, got %s", got)
	}

	var buf bytes.Buffer
	SetLogOutput(&buf)
	registryLogger.Print("registry-log-test")
	netLogger.Print("network-log-test")
	if !strings.Contains(buf.String(), "registry-log-test") || !strings.Contains(buf.String(), "network-log-test") {
		t.Fatalf("expected both logs in output, got: %s", buf.String())
	}
	SetQuiet()
}

func TestNetworkStartAcceptHandleAndJoin(t *testing.T) {
	bootstrap := mustNewDHTForTest(t, "127.0.0.1:0")
	bNet := NewDHTNetwork(bootstrap, nil)
	if err := bNet.Start(); err != nil {
		t.Fatalf("bootstrap start failed: %v", err)
	}
	defer func() { _ = bNet.Stop() }()

	joiner := mustNewDHTForTest(t, "127.0.0.1:0")
	jNet := NewDHTNetwork(joiner, []string{bootstrap.GetSelf().Address})
	if err := jNet.Start(); err != nil {
		t.Fatalf("joiner start failed: %v", err)
	}
	defer func() { _ = jNet.Stop() }()

	// Allow join + periodic discovery to run.
	time.Sleep(1200 * time.Millisecond)

	if joiner.GetRingSize() < 2 {
		t.Fatalf("expected joiner ring size >= 2, got %d", joiner.GetRingSize())
	}

	// Exercise acceptLoop + handleConnection path with a real message exchange.
	resp, err := jNet.sendMessage(bootstrap.GetSelf().Address, &Message{Type: MsgPing, Sender: joiner.GetSelf().Address})
	if err != nil {
		t.Fatalf("ping via sendMessage failed: %v", err)
	}
	if resp.Type != MsgPong {
		t.Fatalf("expected PONG, got %s", resp.Type)
	}
}

func TestPingPeerRemovesUnreachablePeer(t *testing.T) {
	d := mustNewDHTForTest(t, "127.0.0.1:0")
	deadAddr := "127.0.0.1:65531"
	d.AddPeer(deadAddr)

	dn := NewDHTNetwork(d, nil)
	if d.GetRingSize() < 2 {
		t.Fatalf("expected peer in ring before ping, got ring size %d", d.GetRingSize())
	}

	dn.pingPeer(deadAddr)

	if d.GetRingSize() != 1 {
		t.Fatalf("expected unreachable peer removed, ring size=%d", d.GetRingSize())
	}
}

func TestListenForBroadcasts(t *testing.T) {
	d := mustNewDHTForTest(t, "127.0.0.1:0")
	dn := NewDHTNetwork(d, nil)

	ln, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to reserve UDP port: %v", err)
	}
	port := ln.LocalAddr().(*net.UDPAddr).Port
	_ = ln.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		dn.ListenForBroadcasts(port)
	}()

	time.Sleep(50 * time.Millisecond)
	conn, err := net.Dial("udp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("udp dial failed: %v", err)
	}
	announcement := `{"type":"tds_server","address":"127.0.0.1","port":5000}`
	_, _ = conn.Write([]byte(announcement))
	_ = conn.Close()

	time.Sleep(50 * time.Millisecond)
	dn.cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ListenForBroadcasts did not stop after cancellation")
	}
}

func TestFindRemotePeerResult(t *testing.T) {
	oldK := ReplicationFactor
	ReplicationFactor = 1
	defer func() { ReplicationFactor = oldK }()

	owner := mustNewDHTForTest(t, "127.0.0.1:0")
	oNet := NewDHTNetwork(owner, nil)
	if err := oNet.Start(); err != nil {
		t.Fatalf("owner start failed: %v", err)
	}
	defer func() { _ = oNet.Stop() }()

	querier := mustNewDHTForTest(t, "127.0.0.1:0")
	qNet := NewDHTNetwork(querier, nil)

	ownerAddr := owner.GetSelf().Address
	querier.AddPeer(ownerAddr)

	task := forceTaskCloserTo("remote-find", HashAddress(ownerAddr), querier.self.ID)
	owner.StoreTask(task, "10.4.4.4:4")

	out, err := qNet.Find(task)
	if err != nil {
		t.Fatalf("Find returned error: %v", err)
	}
	if len(out) != 1 || out[0] != "10.4.4.4:4" {
		t.Fatalf("unexpected remote find output: %v", out)
	}

	none, err := qNet.Find("missing-remote-task")
	if err != nil {
		t.Fatalf("Find missing returned error: %v", err)
	}
	if none != nil {
		t.Fatalf("expected nil for missing remote task, got %v", none)
	}
}
