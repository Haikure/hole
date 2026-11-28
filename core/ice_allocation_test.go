package core

import (
	"context"
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"github.com/pion/transport/v5"
)

type allocationPacketOnly struct{ net.PacketConn }

func allocationDeadlineFixture(t *testing.T, packetOnly, connected bool) (net.PacketConn, *net.UDPAddr, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	address := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}
	receiver, err := net.ListenUDP("udp4", address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = receiver.Close() })
	remote := receiver.LocalAddr().(*net.UDPAddr)
	var udp *net.UDPConn
	if connected {
		udp, err = net.DialUDP("udp4", address, remote)
	} else {
		udp, err = net.ListenUDP("udp4", address)
	}
	if err != nil {
		t.Fatal(err)
	}
	var packet net.PacketConn = udp
	if packetOnly {
		packet = allocationPacketOnly{packet}
	}
	network := &iceNetwork{ctx: ctx, allocation: &natAllocationGate{}}
	conn := network.guardAllocation(packet)
	t.Cleanup(func() { _ = conn.Close() })
	release, err := network.allocation.acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	return conn, remote, release
}

func awaitAllocationWrite(t *testing.T, done <-chan error, timeout bool) {
	t.Helper()
	select {
	case err := <-done:
		if !timeout && err == nil {
			return
		}
		var netErr net.Error
		if !timeout || !errors.Is(err, os.ErrDeadlineExceeded) || !errors.As(err, &netErr) || !netErr.Timeout() {
			t.Fatalf("write error = %v, timeout = %v", err, timeout)
		}
	case <-time.After(time.Second):
		t.Fatal("allocation wait ignored the write deadline")
	}
}

func TestICEAllocationWriteDeadlines(t *testing.T) {
	for _, method := range []string{"packet", "udp_packet", "udp", "message", "connected"} {
		for _, both := range []bool{false, true} {
			name := method + "/write"
			if both {
				name = method + "/both"
			}
			t.Run(name, func(t *testing.T) {
				conn, remote, release := allocationDeadlineFixture(t, method == "packet", method == "connected")
				set := conn.SetWriteDeadline
				if both {
					set = conn.SetDeadline
				}
				if err := set(time.Now().Add(25 * time.Millisecond)); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				write := func() error {
					var err error
					switch method {
					case "packet", "udp_packet":
						_, err = conn.WriteTo([]byte("first"), remote)
					case "udp":
						_, err = conn.(transport.UDPConn).WriteToUDP([]byte("first"), remote)
					case "message":
						_, _, err = conn.(transport.UDPConn).WriteMsgUDP([]byte("first"), nil, remote)
					case "connected":
						_, err = conn.(transport.UDPConn).Write([]byte("first"))
					}
					return err
				}
				go func() { done <- write() }()
				awaitAllocationWrite(t, done, true)
				if err := set(time.Time{}); err != nil {
					t.Fatal(err)
				}
				release()
				if err := write(); err != nil {
					t.Fatal("expired deadline survived reset", err)
				}
			})
		}
	}
}

func TestICEAllocationUpdatesPendingWriteDeadline(t *testing.T) {
	for _, action := range []string{"set", "extend", "clear", "read_only"} {
		t.Run(action, func(t *testing.T) {
			conn, remote, release := allocationDeadlineFixture(t, false, false)
			if action == "extend" || action == "clear" {
				if err := conn.SetWriteDeadline(time.Now().Add(time.Hour)); err != nil {
					t.Fatal(err)
				}
			}
			done := make(chan error, 1)
			go func() { _, err := conn.WriteTo([]byte("pending"), remote); done <- err }()
			// The allocator stays reserved while deadlines change in flight.
			select {
			case err := <-done:
				t.Fatal("write bypassed the allocation reservation", err)
			case <-time.After(10 * time.Millisecond):
			}
			switch action {
			case "set":
				if err := conn.SetDeadline(time.Now()); err != nil {
					t.Fatal(err)
				}
				awaitAllocationWrite(t, done, true)
				return
			case "extend", "clear":
				if err := conn.SetWriteDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
					t.Fatal(err)
				}
				next := time.Time{}
				if action == "extend" {
					next = time.Now().Add(time.Hour)
				}
				if err := conn.SetWriteDeadline(next); err != nil {
					t.Fatal(err)
				}
			case "read_only":
				if err := conn.SetReadDeadline(time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-done:
				t.Fatal("updated deadline incorrectly interrupted the write", err)
			case <-time.After(80 * time.Millisecond):
			}
			release()
			awaitAllocationWrite(t, done, false)
		})
	}
}
