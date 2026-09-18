package core

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

type credentialGatePlatform struct {
	DefaultPlatform
	calls chan string
}

func (p *credentialGatePlatform) InterfaceSnapshots(context.Context) ([]InterfaceSnapshot, error) {
	p.calls <- "interfaces"
	return nil, nil // Gathering has no interfaces and opens no real sockets.
}

func (p *credentialGatePlatform) ListenPacket(context.Context, string, string) (net.PacketConn, error) {
	p.calls <- "socket"
	return nil, errors.New("fixture has no sockets")
}

func TestRelayAttemptWaitsForCredentialsBeforeChecking(t *testing.T) {
	c, p := lifecycleCoordinator(t)
	c.desired.Config = c.desired.Config.normalized()
	c.desired.Config.ICE.STUNURLs = []string{}
	c.desired.Config.ICE.GatherTimeout = ConfigDuration(100 * time.Millisecond)
	c.desired.Config.ICE.ConnectivityTimeout = ConfigDuration(100 * time.Millisecond)
	platform := &credentialGatePlatform{calls: make(chan string, 16)}
	c.platform = platform
	events := make(chan Event, 32)
	c.emit = func(e Event) { events <- e }
	ctx, cancel := context.WithCancel(c.ctx)
	p.ready.Phase = "relay_udp"
	a := &iceAttempt{ctx: ctx, cancel: cancel, ready: p.ready, events: make(chan iceSignalMessage, 8), failed: make(chan error, 1)}
	p.pending = a
	done := make(chan struct{})
	go func() { defer close(done); p.attempt(a) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("credential wait did not stop")
		}
	})
	var request iceSignalMessage
	select {
	case request = <-c.sink.messages:
		if request.Type != "turn_request" {
			t.Fatalf("ICE started before requesting credentials: %s", request.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("no credential request")
	}
	for len(events) > 0 {
		if e := <-events; e.Kind == "transport" && e.State == "checking" {
			t.Fatal("checking announced before credentials were available")
		}
	}
	if state := p.snapshot().State; state != "waiting_credentials" {
		t.Fatalf("credential wait displayed as %q", state)
	}
	// A slow credential response must not spend the first (UDP) phase's
	// gather + connectivity budget, nor advance to TCP/TLS generations.
	select {
	case call := <-platform.calls:
		t.Fatalf("network operation before credentials: %s", call)
	case m := <-c.sink.messages:
		t.Fatalf("transport advanced while waiting: %s", m.Type)
	case <-done:
		t.Fatal("attempt ended before credentials arrived")
	case <-time.After(250 * time.Millisecond):
	}
	response := iceSignalMessage{
		SignalMessage: SignalMessage{Type: "turn_servers"}, RequestID: request.RequestID,
		ICEServers: []ICEServer{{URLs: []string{"turn:HOST:3478?transport=udp"}, Username: "fixture", Credential: "fixture"}},
		ExpiresAt:  time.Now().Add(time.Hour).UnixMilli(), RefreshAt: time.Now().Add(45 * time.Minute).UnixMilli(),
	}
	if err := c.handle(response, c.request(), platform); err != nil {
		t.Fatal(err)
	}
	select {
	case m := <-c.sink.messages:
		if m.Type != "ice_description" || m.TransportGeneration != a.ready.TransportGeneration {
			t.Fatalf("credentials did not resume the same UDP attempt: %+v", m)
		}
	case <-time.After(time.Second):
		t.Fatal("ICE did not start after credential arrival")
	}
	checking := false
	for len(events) > 0 {
		e := <-events
		checking = checking || (e.Kind == "transport" && e.State == "checking")
	}
	if !checking || p.snapshot().State != "connecting" {
		t.Fatal("credential-ready attempt did not transition to checking")
	}
}
