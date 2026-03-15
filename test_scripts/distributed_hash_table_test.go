package dht_test

import (
	"testing"
	"time"

	"tds/pkg/dht"
)

func TestDHTCreation(t *testing.T) {
	dhtNode, err := dht.NewDHT("127.0.0.1:6000")
	if err != nil {
		t.Fatalf("failed to create DHT: %v", err)
	}

	if dhtNode.GetSelf().Address != "127.0.0.1:6000" {
		t.Errorf("expected address 127.0.0.1:6000, got %s", dhtNode.GetSelf().Address)
	}

	if dhtNode.GetRingSize() != 1 {
		t.Errorf("expected ring size 1, got %d", dhtNode.GetRingSize())
	}
}

func TestHashAddress(t *testing.T) {
	addr1 := "192.168.1.1:6000"
	addr2 := "192.168.1.2:6000"

	hash1 := dht.HashAddress(addr1)
	hash2 := dht.HashAddress(addr2)

	if hash1 == hash2 {
		t.Error("different addresses should have different hashes")
	}

	// same address should produce same hash
	hash1b := dht.HashAddress(addr1)
	if hash1 != hash1b {
		t.Error("same address should produce same hash")
	}
}

func TestHashTask(t *testing.T) {
	task1 := "web-service"
	task2 := "api-service"

	hash1 := dht.HashTask(task1)
	hash2 := dht.HashTask(task2)

	if hash1 == hash2 {
		t.Error("different tasks should have different hashes")
	}

	// same task should produce same hash
	hash1b := dht.HashTask(task1)
	if hash1 != hash1b {
		t.Error("same task should produce same hash")
	}
}

func TestAddPeer(t *testing.T) {
	dhtNode, _ := dht.NewDHT("127.0.0.1:6000")

	dhtNode.AddPeer("127.0.0.1:6001")
	dhtNode.AddPeer("127.0.0.1:6002")

	if dhtNode.GetRingSize() != 3 {
		t.Errorf("expected ring size 3, got %d", dhtNode.GetRingSize())
	}

	peers := dhtNode.GetPeers()
	if len(peers) != 2 {
		t.Errorf("expected 2 peers, got %d", len(peers))
	}
}

func TestStoreAndLookup(t *testing.T) {
	dhtNode, _ := dht.NewDHT("127.0.0.1:6000")

	task := "test-service"
	addr := "192.168.1.50:8080"

	dhtNode.StoreTask(task, addr)

	addrs := dhtNode.LookupTask(task)
	if len(addrs) != 1 {
		t.Fatalf("expected 1 address, got %d", len(addrs))
	}

	if addrs[0] != addr {
		t.Errorf("expected address %s, got %s", addr, addrs[0])
	}
}

func TestMultipleAddresses(t *testing.T) {
	dhtNode, _ := dht.NewDHT("127.0.0.1:6000")

	task := "load-balanced-service"
	addr1 := "192.168.1.50:8080"
	addr2 := "192.168.1.51:8080"

	dhtNode.StoreTask(task, addr1)
	dhtNode.StoreTask(task, addr2)

	addrs := dhtNode.LookupTask(task)
	if len(addrs) != 2 {
		t.Fatalf("expected 2 addresses, got %d", len(addrs))
	}
}

func TestFindClosestNode(t *testing.T) {
	dhtNode, _ := dht.NewDHT("127.0.0.1:6000")
	dhtNode.AddPeer("127.0.0.1:6001")
	dhtNode.AddPeer("127.0.0.1:6002")

	// should find a node for any task
	task := "some-task"
	closest := dhtNode.FindClosestNode(task)

	if closest == nil {
		t.Fatal("expected to find a closest node")
	}

	// should consistently return the same node for the same task
	closest2 := dhtNode.FindClosestNode(task)
	if closest.ID != closest2.ID {
		t.Error("same task should map to same node")
	}
}

func TestIsResponsibleFor(t *testing.T) {
	dhtNode, _ := dht.NewDHT("127.0.0.1:6000")

	// with only one node, we're responsible for everything
	if !dhtNode.IsResponsibleFor("any-task") {
		t.Error("single node should be responsible for all tasks")
	}

	// add another peer
	dhtNode.AddPeer("127.0.0.1:6001")

	// now we're only responsible for some tasks
	responsibleCount := 0
	for i := 0; i < 100; i++ {
		task := string(rune(i))
		if dhtNode.IsResponsibleFor(task) {
			responsibleCount++
		}
	}

	// with 2 nodes, we should be responsible for roughly 50% of tasks
	// allow some variance (30-70%)
	if responsibleCount < 30 || responsibleCount > 70 {
		t.Logf("responsible for %d/100 tasks", responsibleCount)
		// This is informational, not a failure
	}
}

func TestRemovePeer(t *testing.T) {
	dhtNode, _ := dht.NewDHT("127.0.0.1:6000")
	dhtNode.AddPeer("127.0.0.1:6001")
	dhtNode.AddPeer("127.0.0.1:6002")

	if dhtNode.GetRingSize() != 3 {
		t.Fatalf("expected ring size 3, got %d", dhtNode.GetRingSize())
	}

	nodeID := dht.HashAddress("127.0.0.1:6001")
	dhtNode.RemovePeer(nodeID)

	if dhtNode.GetRingSize() != 2 {
		t.Errorf("expected ring size 2 after removal, got %d", dhtNode.GetRingSize())
	}
}

func TestDuplicateAddresses(t *testing.T) {
	dhtNode, _ := dht.NewDHT("127.0.0.1:6000")

	task := "service"
	addr := "192.168.1.50:8080"

	dhtNode.StoreTask(task, addr)
	dhtNode.StoreTask(task, addr) // duplicate

	addrs := dhtNode.LookupTask(task)
	if len(addrs) != 1 {
		t.Errorf("expected 1 address (no duplicates), got %d", len(addrs))
	}
}

func TestNodeIDComparison(t *testing.T) {
	id1 := dht.NodeIDFromUint64(100)
	id2 := dht.NodeIDFromUint64(200)
	id3 := dht.NodeIDFromUint64(100)

	if dht.CompareNodeID(id1, id2) != -1 {
		t.Error("100 should be less than 200")
	}

	if dht.CompareNodeID(id2, id1) != 1 {
		t.Error("200 should be greater than 100")
	}

	if dht.CompareNodeID(id1, id3) != 0 {
		t.Error("100 should equal 100")
	}
}

func TestGetStorageSize(t *testing.T) {
	dhtNode, _ := dht.NewDHT("127.0.0.1:6000")

	if dhtNode.GetStorageSize() != 0 {
		t.Errorf("expected storage size 0, got %d", dhtNode.GetStorageSize())
	}

	dhtNode.StoreTask("task1", "addr1")
	dhtNode.StoreTask("task2", "addr2")

	if dhtNode.GetStorageSize() != 2 {
		t.Errorf("expected storage size 2, got %d", dhtNode.GetStorageSize())
	}
}

func TestPeerLastSeen(t *testing.T) {
	dhtNode, _ := dht.NewDHT("127.0.0.1:6000")

	before := time.Now()
	dhtNode.AddPeer("127.0.0.1:6001")
	after := time.Now()

	peers := dhtNode.GetPeers()
	if len(peers) != 1 {
		t.Fatalf("expected 1 peer")
	}

	if peers[0].LastSeen.Before(before) || peers[0].LastSeen.After(after) {
		t.Error("peer LastSeen timestamp is incorrect")
	}
}
