package core

import (
	"testing"
	"time"
)

func TestMappingTCPTrafficPersistsAfterSessionClose(t *testing.T) {
	m := newSessionManager(time.Minute)
	defer m.close()

	traffic := &tcpTraffic{}
	m.mu.Lock()
	m.tcpBytes["mapping"] = traffic
	m.mu.Unlock()

	socket := newMemoryTCPSocket([]byte("read"))
	socket.finish()
	id, _ := newSessionID()
	session := newTCPSession(m.ctx, id, socket, traffic)

	deadline := time.Now().Add(time.Second)
	for traffic.read.Load() != 4 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := traffic.read.Load(); got != 4 {
		t.Fatalf("TCP read traffic = %d, want 4", got)
	}

	link, _, err := session.attach(nil, nil, 1, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.deliver(link, tcpFrame{kind: tcpData, data: []byte("written")}); err != nil {
		t.Fatal(err)
	}
	session.close()

	stats := m.snapshot()["mapping"]
	if stats.read != 4 || stats.written != 7 {
		t.Fatalf("mapping TCP traffic = %d/%d, want 4/7", stats.read, stats.written)
	}
}
