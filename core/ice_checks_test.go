package core

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/pion/ice/v4"
)

type gatedICEPlatform struct {
	DefaultPlatform
	open   atomic.Bool
	writes atomic.Int32
}

type gatedICEPacket struct {
	*net.UDPConn
	gate *gatedICEPlatform
}

func (p *gatedICEPlatform) ListenPacket(ctx context.Context, network, address string) (net.PacketConn, error) {
	conn, err := p.DefaultPlatform.ListenPacket(ctx, network, address)
	if err != nil {
		return nil, err
	}
	return &gatedICEPacket{conn.(*net.UDPConn), p}, nil
}

func (p *gatedICEPacket) WriteTo(data []byte, addr net.Addr) (int, error) {
	if !p.gate.open.Load() {
		p.gate.writes.Add(1)
		return 0, &net.OpError{Op: "write", Net: "udp", Err: syscall.EAGAIN}
	}
	return p.UDPConn.WriteTo(data, addr)
}

func (p *gatedICEPacket) WriteToUDP(data []byte, addr *net.UDPAddr) (int, error) {
	return p.WriteTo(data, addr)
}

func checkFixtureAgent(t *testing.T, platform Platform, options ...ice.AgentOption) (*ice.Agent, ice.Candidate) {
	t.Helper()
	network, err := newICENetwork(context.Background(), platform)
	if err != nil {
		t.Fatal(err)
	}
	base := []ice.AgentOption{ice.WithNet(network), ice.WithNetworkTypes([]ice.NetworkType{ice.NetworkTypeUDP4}),
		ice.WithCandidateTypes([]ice.CandidateType{ice.CandidateTypeHost}), ice.WithIncludeLoopback(),
		ice.WithInterfaceFilter(func(name string) bool { return name == "lo" }),
		ice.WithMulticastDNSMode(ice.MulticastDNSModeDisabled),
		ice.WithLoggerFactory(newICEDiagnostics(context.Background(), iceSignalMessage{}, Config{}, nil))}
	agent, err := ice.NewAgentWithOptions(append(base, options...)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = agent.Close() })
	gathered := make(chan struct{})
	_ = agent.OnCandidate(func(c ice.Candidate) {
		if c == nil {
			close(gathered)
		}
	})
	if err = agent.GatherCandidates(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-gathered:
	case <-time.After(3 * time.Second):
		t.Fatal("gather timeout")
	}
	candidates, err := agent.GetLocalCandidates()
	if err != nil || len(candidates) != 1 {
		t.Fatalf("candidates: %v %v", candidates, err)
	}
	return agent, candidates[0]
}

func TestICEChecksSurviveCandidateBurstAndTransientWrites(t *testing.T) {
	gate := &gatedICEPlatform{}
	a, ac := checkFixtureAgent(t, gate, iceCheckOptions(6*time.Second)...)
	b, bc := checkFixtureAgent(t, gate, iceCheckOptions(6*time.Second)...)
	if err := a.AddRemoteCandidate(bc); err != nil {
		t.Fatal(err)
	}
	if err := b.AddRemoteCandidate(ac); err != nil {
		t.Fatal(err)
	}
	au, ap, _ := a.GetLocalUserCredentials()
	bu, bp, _ := b.GetLocalUserCredentials()
	ca, err := a.StartDial(bu, bp)
	if err != nil {
		t.Fatal(err)
	}
	cb, err := b.StartAccept(au, ap)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	completed := make(chan error, 2)
	go func() { completed <- a.AwaitConnect(ctx) }()
	go func() { completed <- b.AwaitConnect(ctx) }()
	// Each trickled candidate can force another pass over the existing pair.
	for i := 0; i < 40; i++ {
		candidate, err := ice.NewCandidateHost(&ice.CandidateHostConfig{Network: "udp", Address: "127.0.0.1", Port: 20000 + i, Component: 1})
		if err != nil {
			t.Fatal(err)
		}
		if err = a.AddRemoteCandidate(candidate); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	// Longer than the old default retry exhaustion plus its one-second exit.
	select {
	case err := <-completed:
		t.Fatalf("finished before writes recovered: %v", err)
	case <-time.After(3 * time.Second):
	}
	if gate.writes.Load() < 10 {
		t.Fatal("fixture did not exercise retry exhaustion")
	}
	gate.open.Store(true)
	for i := 0; i < 2; i++ {
		if err := <-completed; err != nil {
			t.Fatal(err)
		}
	}
	if _, err = ca.Write([]byte("recovered")); err != nil {
		t.Fatal(err)
	}
	_ = cb.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 32)
	n, err := cb.Read(buf)
	if err != nil || string(buf[:n]) != "recovered" {
		t.Fatal(n, err)
	}
}

func TestICEFailedChecklistWaitsForDeadline(t *testing.T) {
	gate := &gatedICEPlatform{}
	a, _ := checkFixtureAgent(t, gate, ice.WithMaxBindingRequests(0), ice.WithCheckInterval(10*time.Millisecond))
	b, bc := checkFixtureAgent(t, gate)
	if err := a.AddRemoteCandidate(bc); err != nil {
		t.Fatal(err)
	}
	u, p, _ := b.GetLocalUserCredentials()
	if _, err := a.StartDial(u, p); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		pairs := a.GetCandidatePairsStats()
		if len(pairs) == 1 && pairs[0].State == ice.CandidatePairStateFailed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture did not produce a failed pair")
		}
		time.Sleep(10 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2200*time.Millisecond)
	defer cancel()
	failed := make(chan error, 1)
	done := make(chan struct{})
	go func() { defer close(done); watchICEEmptyChecklist(ctx, a, func(err error) { failed <- err }) }()
	err := a.AwaitConnect(ctx)
	<-done
	if !errors.Is(err, ice.ErrCanceledByCaller) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	select {
	case err := <-failed:
		t.Fatalf("failed pair was prematurely discarded: %v", err)
	default:
	}
}

func TestICEEmptyChecklistCanStillSkipPath(t *testing.T) {
	a, _ := checkFixtureAgent(t, DefaultPlatform{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var failure error
	watchICEEmptyChecklist(ctx, a, func(err error) { failure = err })
	if failure == nil || ctx.Err() != nil {
		t.Fatalf("empty checklist did not skip: %v", failure)
	}
}
