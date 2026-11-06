package nat

import (
	"context"
	"net"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/pion/stun/v4"
	"hole/core/natprobe"
)

func trendSTUNServers(t *testing.T, step int) ([]string, []*net.UDPAddr) {
	t.Helper()
	var mu sync.Mutex
	next := 10000
	var urls []string
	var servers []*net.UDPAddr
	for range 3 {
		pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		t.Cleanup(func() { _ = pc.Close(); <-done })
		urls = append(urls, "stun:"+pc.LocalAddr().String())
		servers = append(servers, pc.LocalAddr().(*net.UDPAddr))
		go func() {
			defer close(done)
			mappings := map[string]int{}
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
				port, ok := mappings[from.String()]
				if !ok {
					mu.Lock()
					port = next
					next += step
					mu.Unlock()
					mappings[from.String()] = port
				}
				reply := stun.MustBuild(stun.NewTransactionIDSetter(request.TransactionID), stun.BindingSuccess,
					&stun.XORMappedAddress{IP: net.ParseIP("198.51.100.10"), Port: port})
				_, _ = pc.WriteTo(reply.Raw, from)
			}
		}()
	}
	return urls, servers
}

func TestCoreProbeMatchesToolPortTrends(t *testing.T) {
	for _, tc := range []struct {
		step int
		want string
	}{
		{1, "predictable_incremental"},
		{-1, "predictable_decremental"},
		{20, "incremental"}, // Wider than the old core's 15-port span.
		{-20, "decremental"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			urls, _ := trendSTUNServers(t, tc.step)
			pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer pc.Close()
			got, mapped := Probe(ctx, &fixtureNetwork{}, pc, urls)
			if got.Status != "complete" || got.Type != "symmetric" || got.Predictability != tc.want || got.Samples != 9 {
				t.Fatalf("core probe = %+v, want %s", got, tc.want)
			}
			_, servers := trendSTUNServers(t, tc.step)
			_, tool, toolMapped := natprobe.ProbeWithListener(ctx, servers, net.ListenPacket)
			got.ProbedAt, tool.ProbedAt = "", ""
			if got != tool || !reflect.DeepEqual(mapped, toolMapped) {
				t.Fatalf("core = %+v %v; tool = %+v %v", got, mapped, tool, toolMapped)
			}
			if len(mapped) != 3 {
				t.Fatalf("unexpected original-socket addresses: %v", mapped)
			}
			if _, err := pc.WriteTo([]byte("handoff"), servers[0]); err != nil {
				t.Fatalf("probe closed the original socket or left its deadline set: %v", err)
			}
		})
	}
}
