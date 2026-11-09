package core

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

type failingPunchPacketConn struct{ net.PacketConn }

func (failingPunchPacketConn) ReadFrom([]byte) (int, net.Addr, error) {
	return 0, nil, errors.New("fixture receive failure")
}

func TestNATPunchDiagnosticKeepsReceiveLoopFailure(t *testing.T) {
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	a := newNATPunchAttempt([]net.PacketConn{failingPunchPacketConn{pc}})
	defer a.close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, _ = a.run(ctx, true, 1, []byte("key"), []*net.UDPAddr{pc.LocalAddr().(*net.UDPAddr)}, nil)
	if a.diagnostic.ReadErrors != 1 || a.diagnostic.ReadError == "" || a.diagnostic.PacketsReceived != 0 {
		t.Fatalf("receive failure disappeared into a timeout: %+v", a.diagnostic)
	}
	if !a.diagnostic.SocketObserved || a.diagnostic.UDPReadErrors != 1 || a.diagnostic.UDPReadError != "fixture receive failure" {
		t.Fatalf("underlying receive error was replaced by quic-go's closed error: %+v", a.diagnostic)
	}
}

func TestNATPunchDiagnosticDistinguishesRejectedAndAuthenticatedPackets(t *testing.T) {
	open := func() net.PacketConn {
		pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = pc.Close() })
		return pc
	}
	local, remote, unexpected := open(), open(), open()
	key := []byte("diagnostic-fixture")
	a := newNATPunchAttempt([]net.PacketConn{local})
	defer a.close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := a.run(ctx, true, 1, key, []*net.UDPAddr{remote.LocalAddr().(*net.UDPAddr)}, nil)
		done <- err
	}()
	_ = remote.SetReadDeadline(time.Now().Add(time.Second))
	buffer := make([]byte, 2048)
	if _, _, err := remote.ReadFrom(buffer); err != nil {
		t.Fatal(err)
	}
	probe := natPunchPacket{kind: 1, round: 1, socket: 0, sequence: 1}
	for _, message := range []struct {
		pc      net.PacketConn
		payload []byte
	}{
		{unexpected, probe.encode(key)},
		{remote, []byte{0, 'x'}},
		{remote, probe.encode([]byte("wrong-key"))},
		{remote, probe.encode(key)},
		// Authenticated but not echoing this attempt's random challenge.
		{remote, (natPunchPacket{kind: 2, round: 1, sequence: 1}).encode(key)},
	} {
		if _, err := message.pc.WriteTo(message.payload, local.LocalAddr()); err != nil {
			t.Fatal(err)
		}
	}
	// Receiving our ACK proves the valid incoming probe was processed.
	for {
		n, _, err := remote.ReadFrom(buffer)
		if err != nil {
			t.Fatal(err)
		}
		if packet, ok := decodeNATPunch(buffer[:n], key, 1); ok && packet.kind == 2 {
			break
		}
	}
	if err := <-done; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	d := a.diagnostic
	if d.PacketsReceived != 5 || d.SourceRejected != 1 || d.PacketRejected != 2 || d.ProbesReceived != 1 || d.ACKsReceived != 1 {
		t.Fatalf("receive accounting = %+v", d)
	}
	if d.ProbesSent == 0 || d.ACKsSent != 1 || d.ACKsVerified != 0 || d.QUICDialStarted || d.QUICEstablished || d.ReadErrors != 0 {
		t.Fatalf("UDP must not be mistaken for a verified QUIC path: %+v", d)
	}
	if !d.SocketObserved || d.UDPPackets != 5 || d.UDPReadErrors != 0 {
		t.Fatalf("optimized UDP receive accounting = %+v", d)
	}
}

func TestNATPunchDiagnosticsSurviveRelayAndSnapshotDoesNotAlias(t *testing.T) {
	_, p := lifecycleCoordinator(t)
	a := &iceAttempt{ctx: p.ctx, ready: p.ready}
	for round := 1; round <= 8; round++ {
		p.recordPunchDiagnostic(a, NATPunchDiagnostic{Generation: a.ready.TransportGeneration, Round: round})
	}
	p.mu.Lock()
	p.ready.Phase = "relay_udp"
	p.ready.TransportGeneration++
	p.mu.Unlock()
	s := p.snapshot()
	if len(s.PunchDiagnostics) != 6 || s.PunchDiagnostics[0].Round != 3 || s.PunchDiagnostics[5].Round != 8 {
		t.Fatal(s.PunchDiagnostics)
	}
	s.PunchDiagnostics[0].Round = 99
	if p.snapshot().PunchDiagnostics[0].Round != 3 {
		t.Fatal("snapshot aliases live diagnostics")
	}
	ctx, cancel := context.WithCancel(p.ctx)
	cancel()
	a.ctx = ctx
	p.recordPunchDiagnostic(a, NATPunchDiagnostic{Round: 9})
	if p.snapshot().PunchDiagnostics[5].Round != 8 {
		t.Fatal("cancelled generation wrote diagnostics")
	}
}
