package core

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

type failedServiceObserver struct {
	DefaultPlatform
	target string
	calls  chan struct{}
}

func (p *failedServiceObserver) DialTCP(ctx context.Context, target string) (*net.TCPConn, error) {
	if target == p.target {
		select {
		case p.calls <- struct{}{}:
		default:
		}
	}
	return p.DefaultPlatform.DialTCP(ctx, target)
}

func TestFailedInitialServiceClosesOnlyItsSocketWithoutRetryLoop(t *testing.T) {
	server, _ := actualWorkerFixture(t)
	badPort := unusedTCPPort(t)
	badTarget := fmt.Sprintf("127.0.0.1:%d", badPort)
	observer := &failedServiceObserver{target: badTarget, calls: make(chan struct{}, 8)}
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			c, e := echo.Accept()
			if e != nil {
				return
			}
			go func() { defer c.Close(); _, _ = io.Copy(c, c) }()
		}
	}()
	provider, consumer := NewEngine(Options{Platform: observer, RetryNetwork: true}), NewEngine(Options{RetryNetwork: true})
	defer provider.Close()
	defer consumer.Close()
	a, b := iceFixtureRequest(server, "alpha"), iceFixtureRequest(server, "beta")
	badExpose, goodExpose := unusedTCPPort(t), unusedTCPPort(t)
	a.Config.Provide = []Provide{{ID: "bad", Service: ServiceEndpoint{"tcp", "127.0.0.1", badPort}}, {ID: "good", Service: ServiceEndpoint{"tcp", "127.0.0.1", echo.Addr().(*net.TCPAddr).Port}}}
	b.Config.Consume = []Consume{{ID: "bad", Expose: HostPort{"127.0.0.1", badExpose}}, {ID: "good", Expose: HostPort{"127.0.0.1", goodExpose}}}
	if err = provider.Start(a); err != nil {
		t.Fatal(err)
	}
	if err = consumer.Start(b); err != nil {
		t.Fatal(err)
	}
	ready := func(s Snapshot) bool {
		return len(s.PeerTransports) == 1 && s.PeerTransports[0].State == "active" && s.PeerTransports[0].ActiveChannels == 2
	}
	waitSnapshot(t, provider, ready)
	waitSnapshot(t, consumer, ready)
	bad, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", badExpose), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer bad.Close()
	_ = bad.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = bad.Write([]byte("initial request"))
	_, err = bad.Read(make([]byte, 1))
	if err == nil {
		t.Fatal("failed service connection stayed open")
	}
	if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("initial failure waited for session retention timeout", err)
	}
	select {
	case <-observer.calls:
	case <-time.After(time.Second):
		t.Fatal("service was never dialed")
	}
	var diagnostic Event
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
find:
	for {
		select {
		case event := <-provider.Events():
			if event.Kind == "session" && event.MappingID == "bad" && event.Error != nil {
				diagnostic = event
				break find
			}
		case <-deadline.C:
			t.Fatal("missing service diagnostic")
		}
	}
	if diagnostic.Error.Code != "service_refused" || diagnostic.Target != badTarget || diagnostic.SessionID == "" || diagnostic.Stage != "dial" || !strings.Contains(diagnostic.Error.Message, "refused") {
		t.Fatalf("lost original cause: %+v", diagnostic)
	}
	good, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", goodExpose), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer good.Close()
	_ = good.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = good.Write([]byte("healthy"))
	reply := make([]byte, 7)
	if _, err = io.ReadFull(good, reply); err != nil || string(reply) != "healthy" {
		t.Fatalf("failed service disrupted healthy mapping: %q %v", reply, err)
	}
	select {
	case <-observer.calls:
		t.Fatal("failed initial socket retried in the background")
	case <-time.After(1200 * time.Millisecond):
	}
	state := provider.Snapshot()
	if !ready(state) {
		t.Fatal("application error closed shared transport", state)
	}
	for _, mapping := range state.Mappings {
		if mapping.ID == "bad" && (mapping.Error == nil || mapping.Error.Code != "service_refused") {
			t.Fatal("snapshot hid application error", mapping)
		}
	}
}
