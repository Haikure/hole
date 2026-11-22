package core

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type loopbackPlatform struct {
	DefaultPlatform
	opened chan net.PacketConn
}

func (p *loopbackPlatform) Candidates(context.Context, Config, int) ([]Candidate, error) {
	return []Candidate{{IP: "2001:db8::1", Port: 55140}}, nil
}

func (p *loopbackPlatform) ListenPacket(ctx context.Context, _, _ string) (net.PacketConn, error) {
	conn, err := (&net.ListenConfig{}).ListenPacket(ctx, "udp4", "127.0.0.1:0")
	if err == nil {
		select {
		case p.opened <- conn:
		case <-ctx.Done():
			_ = conn.Close()
			return nil, ctx.Err()
		}
	}
	return conn, err
}

func TestEngineJoinedAndStopReleasesSignal(t *testing.T) {
	joined := make(chan SignalMessage, 1)
	closed := make(chan struct{}, 1)
	allowJoin := make(chan struct{})
	defer func() {
		select {
		case <-allowJoin:
		default:
			close(allowJoin)
		}
	}()
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer PRIVATE_PASSWORD" || request.URL.Query().Get("room") != "zhuang" {
			t.Error("CLI-compatible credentials were not sent")
			http.Error(w, "bad fixture credentials", http.StatusUnauthorized)
			return
		}
		conn, err := upgrader.Upgrade(w, request, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var message SignalMessage
		if err := conn.ReadJSON(&message); err != nil {
			return
		}
		joined <- message
		<-allowJoin
		if err := conn.WriteJSON(iceSignalMessage{SignalMessage: SignalMessage{Type: "joined"}, SignalVersion: 2}); err != nil {
			return
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				closed <- struct{}{}
				return
			}
		}
	}))
	defer server.Close()
	platform := &loopbackPlatform{opened: make(chan net.PacketConn, 1)}
	e := NewEngine(Options{Platform: platform})
	defer e.Close()
	r := engineRequest()
	r.ServerURL = "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?existing=value"
	r.Config.Provide, r.Config.Consume = nil, nil
	if err := e.Start(r); err != nil {
		t.Fatal(err)
	}
	select {
	case join := <-joined:
		if join.Type != "join" || join.Token != r.Config.Token || join.Provide == nil || join.Consume == nil || join.CertFingerprint == "" {
			t.Fatalf("wrong join: %+v", join)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("join not sent")
	}
	if s := e.Snapshot(); s.EngineState != "starting" || s.SignalState == "joined" {
		t.Fatal("declared joined before server acknowledgement")
	}
	close(allowJoin)
	deadline := time.Now().Add(3 * time.Second)
	for e.Snapshot().SignalState != "joined" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s := e.Snapshot(); s.SignalState != "joined" {
		t.Fatalf("joined state missing: %+v", s)
	}
	oldRun := e.run
	if err := e.Stop(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("signaling socket survived Stop")
	}
	for len(platform.opened) > 0 {
		packet := <-platform.opened
		if _, err := packet.WriteTo([]byte("closed fixture"), packet.LocalAddr()); !errors.Is(err, net.ErrClosed) {
			t.Fatalf("network probe socket survived Stop: %v", err)
		}
	}
	e.agentEvent(oldRun, Event{Kind: "signal", State: "joined"})
	if s := e.Snapshot(); s.SignalState != "disconnected" {
		t.Fatal("late event restored a stopped run")
	}
}
