package mobile

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/pion/stun/v4"
	"hole/core"
	"hole/core/natprobe"
)

type natDNSBinding struct {
	*bindingFixture
	answers map[string][]string
}

func (f *natDNSBinding) NetworkJSON() (string, error) {
	return `{"handle":"1","available":true,"interface":"wlan0","addresses":["192.168.1.2","2001:db8::1"]}`, nil
}
func (f *natDNSBinding) LookupIP(host, handle string) (string, error) {
	if handle != "1" {
		return "", fmt.Errorf("wrong network: %s", handle)
	}
	if values, ok := f.answers[host]; ok {
		b, _ := json.Marshal(values)
		return string(b), nil
	}
	return "", fmt.Errorf("fixture DNS failure")
}

// The first four hosts are dual stack with AAAA first, as Android can return.
// The last two are IPv4-only: skipping the first four still finds a symmetric
// NAT but loses enough targets to prevent an allocation model.
func manualNATFixture(t *testing.T) (*natDNSBinding, []string, []*net.UDPAddr) {
	t.Helper()
	binding := &natDNSBinding{bindingFixture: &bindingFixture{handle: "1"}, answers: map[string][]string{}}
	var mu sync.Mutex
	next := 40000
	var urls []string
	var servers []*net.UDPAddr
	for i := 0; i < 6; i++ {
		ip := fmt.Sprintf("127.0.0.%d", i+1)
		pc, err := net.ListenPacket("udp4", ip+":0")
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		t.Cleanup(func() { pc.Close(); <-done })
		server := pc.LocalAddr().(*net.UDPAddr)
		servers = append(servers, server)
		host := fmt.Sprintf("nat%d.test", i)
		binding.answers[host] = []string{ip}
		if i < 4 {
			binding.answers[host] = []string{"2001:db8::53", ip}
		}
		urls = append(urls, fmt.Sprintf("stun:%s:%d", host, server.Port))
		go func() {
			defer close(done)
			ports := map[string]int{}
			buf := make([]byte, 2048)
			for {
				n, from, err := pc.ReadFrom(buf)
				if err != nil {
					return
				}
				request := &stun.Message{Raw: append([]byte(nil), buf[:n]...)}
				if request.Decode() != nil {
					continue
				}
				port, ok := ports[from.String()]
				if !ok {
					mu.Lock()
					port = next
					next += 2
					mu.Unlock()
					ports[from.String()] = port
				}
				reply := stun.MustBuild(stun.NewTransactionIDSetter(request.TransactionID), stun.BindingSuccess,
					&stun.XORMappedAddress{IP: net.ParseIP("198.51.100.1"), Port: port})
				pc.WriteTo(reply.Raw, from)
			}
		}()
	}
	return binding, urls, servers
}

func TestManualNATMatchesToolWithIPv6FirstDNS(t *testing.T) {
	binding, urls, _ := manualNATFixture(t)
	e, err := NewEngineWithNetworkBinding(nil, binding)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	options, _ := json.Marshal(core.NATProbeOptions{NATSTUNURLs: urls})
	if err := e.StartNATProbe(string(options)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	var d core.NATDiagnostic
	for time.Now().Before(deadline) {
		d = e.engine.Snapshot().NATDiagnostic
		if d.FinishedAt != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if d.Result.Type != "symmetric" || d.Result.Samples != 18 || len(d.Mappings) != 6 || d.Result.PortStep != 2 || d.Result.AllocationScope != "cross_socket" || d.Result.LastMappingPort != 40018 {
		t.Fatalf("manual result lost target/model evidence: %+v", d)
	}
	for _, target := range d.Targets {
		if target.Samples != 3 || len(target.Mappings) != 1 || target.Reason != "" {
			t.Fatal(target)
		}
	}
	d.Targets[0].Mappings[0] = "mutated"
	if e.engine.Snapshot().NATDiagnostic.Targets[0].Mappings[0] == "mutated" {
		t.Fatal("aliased target mappings")
	}
	_, _, toolServers := manualNATFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, tool, mapped := natprobe.ProbeWithListener(ctx, toolServers, net.ListenPacket)
	d.Result.ProbedAt, tool.ProbedAt = "", ""
	var wantMappings []string
	for _, address := range mapped {
		wantMappings = append(wantMappings, address.String())
	}
	if !reflect.DeepEqual(d.Result, tool) || !reflect.DeepEqual(d.Mappings, wantMappings) {
		t.Fatalf("manual=%+v %v; tool=%+v %v", d.Result, d.Mappings, tool, wantMappings)
	}
}

func TestBoundNATIPv4ResolutionKeepsSelectedNetwork(t *testing.T) {
	f := &natDNSBinding{bindingFixture: &bindingFixture{handle: "1"}, answers: map[string][]string{
		"dual.test": {"2001:db8::53", "192.0.2.53"}, "v6.test": {"2001:db8::53"},
	}}
	p := &networkPlatform{provider: f, binding: f, bindingEnabled: true, lookupSlots: make(chan struct{}, 4)}
	defer p.close()
	got, err := p.ResolveUDP4(context.Background(), "dual.test:3478")
	if err != nil || got.String() != "192.0.2.53:3478" {
		t.Fatal(got, err)
	}
	for _, host := range []string{"v6.test", "missing.test"} {
		if _, err := p.ResolveUDP4(context.Background(), host+":3478"); err == nil {
			t.Fatal("unexpected fallback for", host)
		}
	}
}
