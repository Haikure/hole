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
		p.opened <- conn
	}
	return conn, err
}

func TestEngineJoinedAndStopReleasesSocket(t *testing.T) {
	joined := make(chan SignalMessage, 1)
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
		if err := conn.WriteJSON(SignalMessage{Type: "joined"}); err != nil {
			return
		}
		_, _, _ = conn.ReadMessage()
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
	var pc net.PacketConn
	select {
	case pc = <-platform.opened:
	case <-time.After(3 * time.Second):
		t.Fatal("UDP socket not opened")
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
	if _, err := pc.WriteTo([]byte("closed fixture"), pc.LocalAddr()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("socket survived Stop: %v", err)
	}
	e.agentEvent(oldRun, Event{Kind: "signal", State: "joined"})
	if s := e.Snapshot(); s.SignalState != "disconnected" {
		t.Fatal("late event restored a stopped run")
	}
}
