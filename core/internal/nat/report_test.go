package nat

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

type diagnosticNetwork struct{ fixtureNetwork }

func (n *diagnosticNetwork) ResolveUDP4(ctx context.Context, address string) (*net.UDPAddr, error) {
	if strings.HasPrefix(address, "failure.test:") {
		return nil, fmt.Errorf("fixture DNS failure")
	}
	if strings.HasPrefix(address, "v6.test:") {
		return &net.UDPAddr{IP: net.ParseIP("2001:db8::53"), Port: 3478}, nil
	}
	return n.fixtureNetwork.ResolveUDP(ctx, address)
}

func TestDetailedProbeRetainsFailuresAndCounts(t *testing.T) {
	urls, _ := trendSTUNServers(t, 2)
	urls = append(urls, "stun:failure.test:3478", urls[0], "stun:v6.test:3478")
	network := &diagnosticNetwork{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pc, err := network.ListenPacket(ctx, "udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	result, mappings, targets := ProbeDetailed(ctx, network, pc, urls)
	if result.Samples != 9 || len(mappings) != 3 || len(targets) != 6 {
		t.Fatal(result, mappings, targets)
	}
	for i, target := range targets {
		if target.URL != urls[i] {
			t.Fatal("target order changed", targets)
		}
		if i < 3 && (target.Samples != 3 || len(target.Mappings) != 1 || target.Reason != "") {
			t.Fatal(target)
		}
		if i >= 3 && (target.Samples != 0 || target.Reason == "") {
			t.Fatal("lost failure/dedup reason", target)
		}
	}
}

func TestDetailedProbeRecordsCancellation(t *testing.T) {
	network := &diagnosticNetwork{}
	pc, err := network.ListenPacket(context.Background(), "udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, targets := ProbeDetailed(ctx, network, pc, []string{"stun:failure.test:3478", "stun:127.0.0.1:9"})
	for _, target := range targets {
		if target.Samples != 0 || !strings.Contains(target.Reason, "取消") {
			t.Fatal(target)
		}
	}
}
