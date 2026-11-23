package core

import (
	"errors"
	"net"
	"sync"
	"syscall"
	"testing"
	"time"
)

type faultTCPListener struct {
	*net.TCPListener
	faults []error
	resume <-chan struct{}
	closed chan struct{}
	once   sync.Once
}

func (l *faultTCPListener) AcceptTCP() (*net.TCPConn, error) {
	if len(l.faults) > 0 {
		err := l.faults[0]
		l.faults = l.faults[1:]
		return nil, err
	}
	if l.resume != nil {
		select {
		case <-l.resume:
		case <-l.closed:
			return nil, net.ErrClosed
		}
	}
	return l.TCPListener.AcceptTCP()
}

func (l *faultTCPListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return l.TCPListener.Close()
}

func recoveringListener(t *testing.T, faults []error, resume <-chan struct{}) (*sessionManager, *clientTunnel, <-chan Event) {
	t.Helper()
	events := make(chan Event, 16)
	manager := newSessionManager(time.Minute, sessionOptions{emit: func(e Event) { events <- e }})
	t.Cleanup(manager.close)
	tun := tunnel{id: "local", protocol: "tcp", expose: HostPort{Addr: "127.0.0.1"}, peer: "peer"}
	client, err := newClientTunnel(manager.ctx, tun, "cert", discardLog, nil)
	if err != nil {
		t.Fatal(err)
	}
	client.tcpListener = &faultTCPListener{TCPListener: client.tcpListener.(*net.TCPListener), faults: faults, resume: resume, closed: make(chan struct{})}
	client.emit = manager.emit
	client.listenerChanged = func(err error, retrying bool) { manager.clientListenerChanged(client, err, retrying) }
	manager.mu.Lock()
	manager.clients[tun.id] = client
	manager.listenerEventLocked(tun, "ready", client.tcpListener.Addr().String(), nil)
	manager.mu.Unlock()
	go client.acceptTCP()
	return manager, client, events
}

func awaitListenerEvent(t *testing.T, events <-chan Event, state string) Event {
	t.Helper()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		select {
		case event := <-events:
			if event.Kind == "listener" && event.State == state {
				return event
			}
		case <-timer.C:
			t.Fatalf("missing listener state %s", state)
		}
	}
}

func awaitAcceptedTCP(t *testing.T, client *clientTunnel) {
	t.Helper()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		client.mu.Lock()
		accepted := len(client.tcp) == 1
		client.mu.Unlock()
		if accepted {
			return
		}
		select {
		case <-ticker.C:
		case <-timer.C:
			t.Fatal("listener did not accept the TCP connection after recovery")
		}
	}
}

func TestTCPListenerRecoversFromAcceptFailure(t *testing.T) {
	t.Run("temporary exhaustion preserves listener", func(t *testing.T) {
		resume := make(chan struct{})
		faults := []error{
			&net.OpError{Op: "accept", Net: "tcp", Err: syscall.EMFILE},
			&net.OpError{Op: "accept", Net: "tcp", Err: syscall.ENFILE},
		}
		manager, client, events := recoveringListener(t, faults, resume)
		if event := awaitListenerEvent(t, events, "error"); event.Error == nil {
			t.Fatal("accept failure lost its cause")
		}
		if status := manager.snapshot()["local"].listener; status.State != "error" || status.Error == nil {
			t.Fatal("failed accept still appeared ready", status)
		}
		if again, err := manager.consumer(client.t, "cert"); err != nil || again != client {
			t.Fatal("temporary exhaustion replaced the listener", err)
		}
		socket, err := net.DialTimeout("tcp", client.tcpListener.Addr().String(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer socket.Close()
		close(resume)
		awaitListenerEvent(t, events, "ready")
		awaitAcceptedTCP(t, client)
		if status := manager.snapshot()["local"].listener; status.State != "ready" || status.Error != nil {
			t.Fatal("recovered listener retained its error", status)
		}
	})
	t.Run("fatal failure retires cached listener", func(t *testing.T) {
		manager, client, events := recoveringListener(t, []error{errors.New("fatal accept failure")}, nil)
		awaitListenerEvent(t, events, "error")
		replacement, err := manager.consumer(client.t, "cert")
		if err != nil || replacement == client || client.ctx.Err() == nil {
			t.Fatal("fatal listener was reused", err)
		}
		manager.clientListenerChanged(client, errors.New("late old failure"), false)
		if status := manager.snapshot()["local"].listener; status.State != "ready" {
			t.Fatal("old failure overwrote replacement readiness", status)
		}
		socket, err := net.DialTimeout("tcp", replacement.tcpListener.Addr().String(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer socket.Close()
		awaitAcceptedTCP(t, replacement)
	})
}
