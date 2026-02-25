package dht_test

import (
	"fmt"
	"net"
	"testing"
	"time"

	"tds/pkg/dht"
)

func getFreeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for free port: %v", err)
	}
	addr := ln.Addr().(*net.TCPAddr)
	_ = ln.Close()
	return fmt.Sprintf("127.0.0.1:%d", addr.Port)
}

func startRegistry(t *testing.T, bootstraps []string) (*dht.DHTRegistry, string) {
	t.Helper()
	var lastErr error
	for i := 0; i < 5; i++ {
		addr := getFreeAddr(t)
		reg, err := dht.NewDHTRegistry(addr, bootstraps)
		if err != nil {
			lastErr = err
			continue
		}
		if err := reg.Start(); err != nil {
			lastErr = err
			_ = reg.Stop()
			continue
		}
		t.Cleanup(func() {
			_ = reg.Stop()
		})
		return reg, addr
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("exhausted retries")
	}
	t.Fatalf("start registry: %v", lastErr)
	return nil, ""
}

func ringSizeFromInfo(info map[string]interface{}) (int, bool) {
	v, ok := info["ring_size"]
	if !ok {
		return 0, false
	}
	switch size := v.(type) {
	case int:
		return size, true
	case int64:
		return int(size), true
	case float64:
		return int(size), true
	default:
		return 0, false
	}
}

func waitForRingSize(t *testing.T, reg *dht.DHTRegistry, expected int, timeout time.Duration) {
	t.Helper()
	   deadline := time.Now().Add(timeout)
	   for time.Now().Before(deadline) {
		   info := reg.GetDHTInfo()
		   size, ok := ringSizeFromInfo(info)
		   if ok && size == expected {
			   t.Logf("ring size reached: %d", size)
			   return
		   }
		   t.Logf("waiting for ring size: got %v", info)
		   time.Sleep(100 * time.Millisecond)
	   }
	   info := reg.GetDHTInfo()
	   size, _ := ringSizeFromInfo(info)
	   t.Fatalf("ring size timeout: expected %d, got %d, info: %+v", expected, size, info)
}

func waitForQuery(t *testing.T, reg *dht.DHTRegistry, task, expected string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		addr, err := reg.Query(task)
		if err == nil && addr == expected {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	addr, err := reg.Query(task)
	t.Fatalf("query timeout: got %q (err=%v), want %q", addr, err, expected)
}

func TestP2PIntegration_RegisterAndQueryAcrossNodes(t *testing.T) {
	reg1, addr1 := startRegistry(t, nil)
	t.Logf("Started reg1 at %s", addr1)
	reg2, _ := startRegistry(t, []string{addr1})
	t.Logf("Started reg2, bootstrapped to %s", addr1)
	reg3, _ := startRegistry(t, []string{addr1})
	t.Logf("Started reg3, bootstrapped to %s", addr1)

	   waitForRingSize(t, reg1, 3, 5*time.Second)
	   waitForRingSize(t, reg2, 3, 5*time.Second)
	   waitForRingSize(t, reg3, 3, 5*time.Second)

	   task := "svc-alpha"
	   serviceAddr := "10.0.0.1:1234"
	   t.Logf("Registering %s -> %s on reg1", task, serviceAddr)

	   if err := reg1.Register(task, serviceAddr); err != nil {
		   t.Fatalf("register: %v", err)
	   }

	   t.Logf("Querying for %s on reg2", task)
	   waitForQuery(t, reg2, task, serviceAddr, 2*time.Second)
	   t.Logf("Querying for %s on reg3", task)
	   waitForQuery(t, reg3, task, serviceAddr, 2*time.Second)
}

func TestP2PIntegration_QueryNotFound(t *testing.T) {
	reg1, addr1 := startRegistry(t, nil)
	reg2, _ := startRegistry(t, []string{addr1})

	waitForRingSize(t, reg1, 2, 2*time.Second)
	waitForRingSize(t, reg2, 2, 2*time.Second)

	addr, err := reg2.Query("missing-task")
	if err != nil {
		t.Fatalf("query error: %v", err)
	}
	if addr != "" {
		t.Fatalf("expected empty address, got %q", addr)
	}
}
