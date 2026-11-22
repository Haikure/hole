package core

import (
	"context"
	"errors"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestServiceSessionErrorsKeepTheirRealCause(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{
		{errors.New("session_expired"), "session_expired"},
		{errors.New("session_limit"), "session_limit"},
		{context.Canceled, "session_canceled"},
		{context.DeadlineExceeded, "service_timeout"},
		{&net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}, "service_refused"},
		{errors.New("platform-specific socket error"), "service_unavailable"},
	} {
		fault := serviceSessionFault(tc.err, "127.0.0.1:12345", false)
		if fault.Code != tc.code || !strings.Contains(fault.Message, tc.err.Error()) || !strings.Contains(fault.Message, "127.0.0.1:12345") {
			t.Fatal(fault)
		}
	}
	if f := serviceSessionFault(errors.New("session_expired"), "TARGET", true); !strings.Contains(f.Message, "恢复应用会话") {
		t.Fatal(f)
	}
	if f := serviceSessionFault(errors.New(strings.Repeat("x", 10000)), "TARGET", false); len(f.Message) > 2100 {
		t.Fatal("unbounded diagnostic")
	}
}

func TestListenerDiagnosticsFollowSocketLifecycle(t *testing.T) {
	for _, protocol := range []string{"tcp", "udp"} {
		t.Run(protocol, func(t *testing.T) {
			var events []Event
			manager := newSessionManager(time.Minute, sessionOptions{emit: func(e Event) { events = append(events, e) }})
			defer manager.close()
			tun := tunnel{id: "local", protocol: protocol, expose: HostPort{Addr: "127.0.0.1"}, peer: "peer"}
			client, err := manager.consumer(tun, "cert")
			if err != nil {
				t.Fatal(err)
			}
			var address string
			var port int
			if protocol == "tcp" {
				address, port = client.tcpListener.Addr().String(), client.tcpListener.Addr().(*net.TCPAddr).Port
			} else {
				address, port = client.udpListener.LocalAddr().String(), client.udpListener.LocalAddr().(*net.UDPAddr).Port
			}
			if len(events) != 1 || events[0].Kind != "listener" || events[0].State != "ready" || events[0].Target != address || port == 0 {
				t.Fatalf("missing actual bound address: %+v", events)
			}
			if again, err := manager.consumer(tun, "cert"); err != nil || again != client {
				t.Fatal("listener was not reused", err)
			}
			manager.disconnect("local", "cert")
			status := manager.snapshot()["local"].listener
			if status == nil || status.State != "ready" || status.Address != address || len(events) != 1 {
				t.Fatal("path loss changed listener readiness", status, events)
			}
			busy := tunnel{id: "busy", protocol: protocol, expose: HostPort{Addr: "127.0.0.1", Port: port}}
			if _, err := manager.consumer(busy, "cert"); !errors.Is(err, syscall.EADDRINUSE) {
				t.Fatal("occupied port did not fail", err)
			}
			status = manager.snapshot()["busy"].listener
			if status == nil || status.State != "error" || status.Address != address || status.Error == nil || status.Error.Code != "address_in_use" {
				t.Fatal("bind failure missing from snapshot", status)
			}
			if len(events) != 2 || events[1].State != "error" || events[1].Error == nil || events[1].Error.Code != "address_in_use" {
				t.Fatalf("bind failure missing from events: %+v", events)
			}
			status.Error.Message = "mutated snapshot"
			if manager.snapshot()["busy"].listener.Error.Message == "mutated snapshot" {
				t.Fatal("snapshot shares a mutable fault")
			}
			manager.terminate("local", "cert")
			if manager.snapshot()["local"].listener.State != "closed" || len(events) != 3 || events[2].State != "closed" {
				t.Fatalf("listener closure was not reported: %+v", events)
			}
		})
	}
}
