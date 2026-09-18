package mobile

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"hole/core"
)

type fixtureNetworkProvider struct {
	text  string
	err   error
	calls atomic.Int32
}

func (p *fixtureNetworkProvider) InterfacesJSON() (string, error) {
	p.calls.Add(1)
	return p.text, p.err
}

func TestAndroidCandidatesUseHostAndRefreshWithoutNetlink(t *testing.T) {
	source := &fixtureNetworkProvider{text: `[{"name":"wlan0","up":true,"addresses":["2001:db8::1","fe80::1","10.0.0.2"]}]`}
	p := &networkPlatform{provider: source}
	for _, address := range []string{"2001:db8::1", "2001:db8::2"} {
		source.text = strings.ReplaceAll(source.text, "2001:db8::1", address)
		got, err := p.Candidates(context.Background(), core.Config{}, 12345)
		if err != nil || len(got) != 1 || got[0] != (core.Candidate{IP: address, Port: 12345}) {
			t.Fatalf("host candidates = %+v, %v", got, err)
		}
	}
	if source.calls.Load() != 2 {
		t.Fatal("candidate refresh reused a stale snapshot")
	}
	source.text = `[]`
	got, err := p.Candidates(context.Background(), core.Config{}, 12345)
	if err != nil || len(got) != 0 {
		t.Fatal("lost network did not return an empty snapshot")
	}
}

func TestAndroidProviderErrorsDoNotFallBackToGoInterfaces(t *testing.T) {
	source := &fixtureNetworkProvider{err: errors.New("fixture platform failure")}
	p := &networkPlatform{provider: source}
	_, err := p.Candidates(context.Background(), core.Config{}, 55140)
	var fault *core.Fault
	if !errors.As(err, &fault) || fault.Code != "network_interfaces_failed" || !strings.Contains(fault.Message, "fixture platform failure") {
		t.Fatalf("host error was lost or replaced by netlink: %v", err)
	}
	source.err = nil
	for _, text := range []string{"", "null", `{}`, `[] {}`, strings.Repeat(" ", 128*1024+1)} {
		source.text = text
		_, err = p.Candidates(context.Background(), core.Config{}, 55140)
		if !errors.As(err, &fault) || fault.Code != "network_interfaces_invalid" {
			t.Fatalf("accepted invalid snapshot: %v", err)
		}
	}
}

func TestAndroidExplicitCandidatesAndCancellationSkipProvider(t *testing.T) {
	source := &fixtureNetworkProvider{err: errors.New("must not be called")}
	p := &networkPlatform{provider: source}
	got, err := p.Candidates(context.Background(), core.Config{CandidateAddresses: []string{"2001:db8::9"}}, 55140)
	if err != nil || len(got) != 1 || got[0].IP != "2001:db8::9" {
		t.Fatalf("manual candidate = %+v, %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Candidates(ctx, core.Config{}, 55140); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled candidate read: %v", err)
	}
	if source.calls.Load() != 0 {
		t.Fatal("manual/canceled call enumerated interfaces")
	}
}

func TestAndroidFacadeOfflineConstructionAndNoIPv6(t *testing.T) {
	if _, err := NewEngineWithNetworkProvider(nil, nil); err == nil {
		t.Fatal("accepted a missing Android network provider")
	}
	source := &fixtureNetworkProvider{text: `[]`}
	e, err := NewEngineWithNetworkProvider(nil, source)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if err := e.ApplyConfig(requestJSON); err != nil {
		t.Fatal(err)
	}
	if source.calls.Load() != 0 {
		t.Fatal("stopped construction/configuration accessed networks")
	}
	if err := e.Start(requestJSON); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = e.engine.Wait(ctx)
	if s := e.engine.Snapshot(); s.Error == nil || s.Error.Code != "no_ipv6" || s.SignalState != "disconnected" {
		t.Fatalf("wrong unavailable network state: %+v", s)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if e.network.provider != nil {
		t.Fatal("Close retained the host network callback")
	}
}
