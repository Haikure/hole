package core

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func waitSnapshot(t *testing.T, e *Engine, predicate func(Snapshot) bool) Snapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s := e.Snapshot()
		if predicate(s) {
			return s
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("snapshot did not converge: %+v", e.Snapshot())
	return Snapshot{}
}

func recordingSignal(t *testing.T) (string, <-chan SignalMessage) {
	t.Helper()
	joins := make(chan SignalMessage, 128)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var join SignalMessage
		if conn.ReadJSON(&join) != nil {
			return
		}
		joins <- join
		if conn.WriteJSON(SignalMessage{Type: "joined"}) != nil {
			return
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http"), joins
}

func nextJoin(t *testing.T, joins <-chan SignalMessage) SignalMessage {
	t.Helper()
	select {
	case msg := <-joins:
		return msg
	case <-time.After(5 * time.Second):
		t.Fatal("join missing")
		return SignalMessage{}
	}
}

func TestLiveConfigFullRejoinAndNetworkPreserveSessionManager(t *testing.T) {
	url, joins := recordingSignal(t)
	p := &loopbackPlatform{opened: make(chan net.PacketConn, 128)}
	e := NewEngine(Options{Platform: p, RetryNetwork: true})
	defer e.Close()
	r := engineRequest()
	r.ServerURL = url
	r.Config.Provide, r.Config.Consume = nil, nil
	if err := e.Start(r); err != nil {
		t.Fatal(err)
	}
	first := nextJoin(t, joins)
	waitSnapshot(t, e, func(s Snapshot) bool { return s.SignalState == "joined" })
	e.mu.Lock()
	manager, run, oldEpoch := e.run.sessions, e.run, e.run.epoch
	e.mu.Unlock()
	// Identical saves are accepted without closing an established signal.
	if err := e.ApplyConfig(r); err != nil {
		t.Fatal(err)
	}
	select {
	case <-joins:
		t.Fatal("identical config rejoined")
	case <-time.After(30 * time.Millisecond):
	}
	r.Config.Provide = []Provide{{ID: "new", Service: ServiceEndpoint{Protocol: "tcp", Addr: "127.0.0.1", Port: 22}}}
	if err := e.ApplyConfig(r); err != nil {
		t.Fatal(err)
	}
	e.agentEventEpoch(run, &oldEpoch, Event{Kind: "signal", State: "joined"})
	second := nextJoin(t, joins)
	if second.CertFingerprint != first.CertFingerprint || len(second.Provide) != 1 || second.Consume == nil {
		t.Fatalf("bad full join: %+v", second)
	}
	waitSnapshot(t, e, func(s Snapshot) bool { return s.SignalState == "joined" && len(s.Mappings) == 1 })
	if err := e.NetworkChanged(); err != nil {
		t.Fatal(err)
	}
	third := nextJoin(t, joins)
	if third.CertFingerprint != first.CertFingerprint {
		t.Fatal("network change changed runtime identity")
	}
	waitSnapshot(t, e, func(s Snapshot) bool { return s.SignalState == "joined" && s.NetworkChanges == 1 })
	e.mu.Lock()
	same := e.run == run && e.run.sessions == manager
	e.mu.Unlock()
	if !same {
		t.Fatal("network/config changes replaced run/session scope")
	}
	r.Config.Token = "NEW_ROOM_TOKEN"
	if err := e.ApplyConfig(r); err != nil {
		t.Fatal(err)
	}
	changed := nextJoin(t, joins)
	if changed.CertFingerprint == first.CertFingerprint || changed.Token != r.Config.Token {
		t.Fatal("credentials reused old scope")
	}
	waitSnapshot(t, e, func(s Snapshot) bool { return s.SignalState == "joined" })
	manager.mu.Lock()
	closed := manager.closed
	manager.mu.Unlock()
	if !closed {
		t.Fatal("credential change retained old application sessions")
	}
}

func TestReconfigureClosesChangedListenersButRetainsUnchanged(t *testing.T) {
	manager := newSessionManager(time.Minute)
	defer manager.close()
	first, err := manager.consumer(tunnel{id: "keep", protocol: "tcp", expose: HostPort{Addr: "127.0.0.1", Port: 0}, peer: "peer"}, "cert")
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.consumer(tunnel{id: "remove", protocol: "tcp", expose: HostPort{Addr: "127.0.0.1", Port: 0}, peer: "peer"}, "cert")
	if err != nil {
		t.Fatal(err)
	}
	old := Config{SessionTimeout: time.Minute, Consume: []Consume{{ID: "keep", Expose: first.t.expose}, {ID: "remove", Expose: second.t.expose}}}
	next := old
	next.Consume = old.Consume[:1]
	manager.reconfigure(old, next)
	if first.ctx.Err() != nil || second.ctx.Err() == nil {
		t.Fatal("incorrect listener retention")
	}
	conn, err := net.Dial("tcp", first.tcpListener.Addr().String())
	if err != nil {
		t.Fatal("unchanged listener lost", err)
	}
	conn.Close()
	if conn, err := net.DialTimeout("tcp", second.tcpListener.Addr().String(), time.Second); err == nil {
		conn.Close()
		t.Fatal("deleted listener still accepting")
	}
	changed := next
	changed.Consume = []Consume{{ID: "keep", Expose: HostPort{Addr: "127.0.0.1", Port: 9999}}}
	manager.reconfigure(next, changed)
	if first.ctx.Err() == nil {
		t.Fatal("target change retained listener")
	}
}

type recoverablePlatform struct {
	loopbackPlatform
	online atomic.Bool
}

func (p *recoverablePlatform) Candidates(ctx context.Context, cfg Config, port int) ([]Candidate, error) {
	if !p.online.Load() {
		return nil, nil
	}
	return p.loopbackPlatform.Candidates(ctx, cfg, port)
}
func TestNetworkAppearsAfterOfflineStartAndStopCancelsRecovery(t *testing.T) {
	url, joins := recordingSignal(t)
	p := &recoverablePlatform{loopbackPlatform: loopbackPlatform{opened: make(chan net.PacketConn, 128)}}
	e := NewEngine(Options{Platform: p, RetryNetwork: true})
	defer e.Close()
	r := engineRequest()
	r.ServerURL = url
	if err := e.Start(r); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, e, func(s Snapshot) bool {
		return s.EngineState == "recovering" && s.Error != nil && s.Error.Code == "no_ipv6"
	})
	p.online.Store(true)
	_ = e.NetworkChanged()
	nextJoin(t, joins)
	waitSnapshot(t, e, func(s Snapshot) bool { return s.SignalState == "joined" })
	for i := 2; i < 25; i++ {
		r.Config.DeviceName = "revision-" + string(rune('a'+i))
		if err := e.ApplyConfig(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.Stop(); err != nil {
		t.Fatal(err)
	}
	stopped := e.Snapshot().TransportGeneration
	for i := 0; i < 20; i++ {
		_ = e.NetworkChanged()
	}
	time.Sleep(50 * time.Millisecond)
	if s := e.Snapshot(); s.RunRequested || s.EngineState != "stopped" || s.TransportGeneration != stopped {
		t.Fatalf("late work restarted stopped engine: %+v", s)
	}
}
