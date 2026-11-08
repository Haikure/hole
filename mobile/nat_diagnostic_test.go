package mobile

import (
	"context"
	"hole/core"
	"net"
	"strings"
	"testing"
	"time"
)

type natEventSink struct{ events chan string }

func (s natEventSink) OnEvent(event string) { s.events <- event }

type waitingNATPlatform struct{ core.DefaultPlatform }

func (waitingNATPlatform) InterfaceSnapshots(context.Context) ([]core.InterfaceSnapshot, error) {
	return nil, nil
}
func (waitingNATPlatform) ListenPacket(ctx context.Context, _, _ string) (net.PacketConn, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestManualNATEventsWhileStopped(t *testing.T) {
	sink := natEventSink{events: make(chan string, 32)}
	e := newEngine(sink, waitingNATPlatform{})
	defer e.Close()
	if err := e.StartNATProbe(`{"nat_stun_urls":["stun:127.0.0.1:9"]}`); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-sink.events:
		if !strings.Contains(event, "nat_diagnostic") {
			t.Fatal(event)
		}
	case <-time.After(time.Second):
		t.Fatal("stopped facade suppressed diagnostic event")
	}
	if err := e.CancelNATProbe(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.SnapshotJSON(), `"state":"cancelled"`) {
		t.Fatal(e.SnapshotJSON())
	}
	if err := e.Stop(); err != nil {
		t.Fatal(err)
	}
}
