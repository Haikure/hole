package core

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/pion/ice/v4"
	"hole/core/internal/nat"
)

// The gateway is entirely local: discovery fails and NAT-PMP assigns a
// different external UDP port, which forwards to the real ICE host socket.
type iceMappingGateway struct {
	DefaultPlatform
	mu       sync.Mutex
	port     uint16
	external uint16
	deleted  bool
}

func (g *iceMappingGateway) DefaultGateway(context.Context) (netip.Addr, error) {
	return netip.MustParseAddr("127.0.0.1"), nil
}

func (g *iceMappingGateway) ListenPacket(context.Context, string, string) (net.PacketConn, error) {
	return &iceMappingPacket{gateway: g, response: make(chan []byte, 1)}, nil
}

type iceMappingPacket struct {
	net.PacketConn
	gateway  *iceMappingGateway
	response chan []byte
}

func (p *iceMappingPacket) Close() error                { return nil }
func (p *iceMappingPacket) SetDeadline(time.Time) error { return nil }
func (p *iceMappingPacket) WriteTo(b []byte, _ net.Addr) (int, error) {
	if len(b) < 2 || b[0] != 0 {
		return 0, errors.New("fixture: no IGD")
	}
	response := make([]byte, 12)
	response[1] = b[1] + 128
	if b[1] == 0 {
		copy(response[8:], []byte{127, 0, 0, 1})
	} else {
		response = make([]byte, 16)
		response[1] = 129
		copy(response[8:10], b[4:6])
		p.gateway.mu.Lock()
		p.gateway.port = binary.BigEndian.Uint16(b[4:6])
		p.gateway.deleted = binary.BigEndian.Uint32(b[8:12]) == 0
		binary.BigEndian.PutUint16(response[10:12], p.gateway.external)
		p.gateway.mu.Unlock()
		binary.BigEndian.PutUint32(response[12:], 300)
	}
	p.response <- response
	return len(b), nil
}
func (p *iceMappingPacket) ReadFrom(b []byte) (int, net.Addr, error) {
	return copy(b, <-p.response), &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5351}, nil
}

func TestICEPortMappingCandidateConnectsAndLeaseIsDeleted(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	proxy, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	gateway := &iceMappingGateway{external: uint16(proxy.LocalAddr().(*net.UDPAddr).Port)}
	go func() {
		buf := make([]byte, 65536)
		var peer net.Addr
		for {
			n, from, err := proxy.ReadFrom(buf)
			if err != nil {
				return
			}
			gateway.mu.Lock()
			port := gateway.port
			gateway.mu.Unlock()
			internal := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: int(port)}
			if from.String() == internal.String() {
				if peer != nil {
					_, _ = proxy.WriteTo(buf[:n], peer)
				}
			} else {
				peer = from
				_, _ = proxy.WriteTo(buf[:n], internal)
			}
		}
	}()
	statuses := make(chan nat.MappingSnapshot, 1)
	mapper := newICEPortMapper(ctx, gateway, func(s nat.MappingSnapshot) { statuses <- s }, func() {})
	defer mapper.close()
	makeAgent := func() *ice.Agent {
		a, err := ice.NewAgentWithOptions(ice.WithNetworkTypes([]ice.NetworkType{ice.NetworkTypeUDP4}), ice.WithCandidateTypes([]ice.CandidateType{ice.CandidateTypeHost}), ice.WithIncludeLoopback(), ice.WithIPFilter(func(ip net.IP) bool { return ip.IsLoopback() }))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = a.Close() })
		return a
	}
	left, right := makeAgent(), makeAgent()
	leftCandidates, rightCandidates := make(chan ice.Candidate, 4), make(chan ice.Candidate, 4)
	_ = left.OnCandidate(func(c ice.Candidate) {
		if c != nil {
			leftCandidates <- c
		}
	})
	_ = right.OnCandidate(func(c ice.Candidate) {
		if c != nil {
			rightCandidates <- c
		}
	})
	if err := left.GatherCandidates(); err != nil {
		t.Fatal(err)
	}
	if err := right.GatherCandidates(); err != nil {
		t.Fatal(err)
	}
	getCandidate := func(ch <-chan ice.Candidate) ice.Candidate {
		select {
		case c := <-ch:
			return c
		case <-ctx.Done():
			t.Fatal(ctx.Err())
			return nil
		}
	}
	host := getCandidate(leftCandidates)
	mapped := mapper.candidate(host)
	if mapped == nil || mapped.Port() != int(gateway.external) || mapped.Port() == host.Port() || mapped.Type() != ice.CandidateTypeServerReflexive {
		t.Fatalf("mapped candidate: %v, host: %v", mapped, host)
	}
	if s := <-statuses; s.Status != "mapped" || s.Protocol != "nat-pmp" {
		t.Fatal(s)
	}
	if mapper.candidate(host) != nil {
		t.Fatal("duplicate mapping")
	}
	// The peer receives only the mapped address, never the original host.
	if err := right.AddRemoteCandidate(mapped); err != nil {
		t.Fatal(err)
	}
	getCandidate(rightCandidates)
	lu, lp, _ := left.GetLocalUserCredentials()
	ru, rp, _ := right.GetLocalUserCredentials()
	accepted := make(chan error, 1)
	go func() {
		conn, err := left.Accept(ctx, ru, rp)
		if err == nil {
			b := make([]byte, 32)
			n, readErr := conn.Read(b)
			err = readErr
			if err == nil && string(b[:n]) != "mapped ICE" {
				err = errors.New("incorrect ICE payload")
			}
		}
		accepted <- err
	}()
	conn, err := right.Dial(ctx, lu, lp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("mapped ICE")); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-accepted:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	mapper.close()
	gateway.mu.Lock()
	deleted := gateway.deleted
	gateway.mu.Unlock()
	if !deleted {
		t.Fatal("lease was not deleted")
	}
	if mapper.candidate(host) != nil {
		t.Fatal("closed mapper published a candidate")
	}
}

func TestICEFailureImmediatelyRequestsRelay(t *testing.T) {
	c, p := lifecycleCoordinator(t)
	p.ready.Profile = ProfileICE
	ctx, cancel := context.WithCancel(c.ctx)
	defer cancel()
	a := &iceAttempt{ctx: ctx, cancel: cancel, ready: p.ready}
	p.pending = a
	p.failed(a, errors.New("ICE failed"))
	select {
	case request := <-c.sink.messages:
		if request.Type != "transport_restart" || request.Phase != "relay_udp" {
			t.Fatal(request)
		}
	default:
		t.Fatal("ICE failure did not immediately request relay")
	}
	if p.pending != nil || ctx.Err() == nil {
		t.Fatal("failed ICE attempt was retained")
	}
}
