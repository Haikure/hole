package core

import (
	"context"
	"fmt"
	"net"
	"runtime"
	"slices"
	"strconv"
	"testing"
	"time"
)

func TestHoleDualStackAndEmptyBatch(t *testing.T) {
	socket, err := net.ListenPacket("udp", "[::]:0")
	if err != nil {
		t.Fatal(err)
	}
	relay := newHoleForwarder(context.Background(), socket, RelayServeConfig{MaxPairs: 1})
	defer relay.Close()
	if n, err := relay.backend.WriteBatch(nil); n != 0 || err != nil {
		t.Fatal(n, err)
	}
	// ReadBatch is owned exclusively by receive, including an empty batch.
	grant := testHoleGrant()
	ip := "127.0.0.1"
	if runtime.GOOS == "linux" {
		ip = "127.0.0.2"
	}
	grant.Endpoint = net.JoinHostPort(ip, strconv.Itoa(socket.LocalAddr().(*net.UDPAddr).Port))
	if err := relay.install(iceSignalMessage{TransportID: "pair", TransportGeneration: 1, Hole: grant, LeaseUntil: time.Now().Add(time.Minute).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	a, err := openHolePath(ctx, DefaultPlatform{}, grant, 0)
	if err != nil {
		t.Fatal("IPv4 registration on dual-stack listener", err)
	}
	defer a.Close()
	b, err := openHolePath(ctx, DefaultPlatform{}, grant, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if _, err = a.WriteTo([]byte("dual-stack"), b.PeerAddr()); err != nil {
		t.Fatal(err)
	}
	_ = b.SetReadDeadline(time.Now().Add(time.Second))
	var buf [100]byte
	n, _, err := b.ReadFrom(buf[:])
	if err != nil || string(buf[:n]) != "dual-stack" {
		t.Fatal(n, err)
	}
}

// Compare the ordinary and batched syscalls using real loopback delivery.
func TestHoleClosedBackendDoesNotReuseSendCount(t *testing.T) {
	sender, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	receiver, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer receiver.Close()
	backend := newForwardingBackend(sender)
	frames := []*holeFrame{{n: 100, address: receiver.LocalAddr().(*net.UDPAddr).AddrPort()}}
	if n, err := backend.WriteBatch(frames); n != 1 || err != nil {
		t.Fatal(n, err)
	}
	_ = sender.Close()
	if n, err := backend.WriteBatch(frames); n != 0 || err == nil {
		t.Fatal("closed backend reused previous send count", n, err)
	}
}

// Compare the ordinary and batched syscalls using real loopback delivery.
// A sample includes submission and receipt of one batch, not an Internet RTT.
func BenchmarkHoleBackendLoopback(b *testing.B) {
	for _, portable := range []bool{true, false} {
		for _, batchSize := range []int{1, holeBatchSize} {
			b.Run(fmt.Sprintf("portable=%v/batch=%d", portable, batchSize), func(b *testing.B) {
				sender, err := net.ListenPacket("udp4", "127.0.0.1:0")
				if err != nil {
					b.Fatal(err)
				}
				defer sender.Close()
				receiver, err := net.ListenPacket("udp4", "127.0.0.1:0")
				if err != nil {
					b.Fatal(err)
				}
				defer receiver.Close()
				writer, reader := newForwardingBackend(sender), newForwardingBackend(receiver)
				if portable {
					writer = &portableForwarder{sender}
					reader = &portableForwarder{receiver}
				}
				_ = receiver.SetReadDeadline(time.Now().Add(time.Minute))
				addr := receiver.LocalAddr().(*net.UDPAddr).AddrPort()
				out, in := make([]*holeFrame, batchSize), make([]*holeFrame, batchSize)
				for i := range out {
					out[i] = &holeFrame{n: 1246, address: addr}
					in[i] = new(holeFrame)
				}
				var samples [4096]int64
				b.ReportAllocs()
				b.SetBytes(int64(batchSize * 1200))
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					start := time.Now()
					for sent := 0; sent < batchSize; {
						n, err := writer.WriteBatch(out[sent:])
						if err != nil || n == 0 {
							b.Fatal("send", n, err)
						}
						sent += n
					}
					for received := 0; received < batchSize; {
						n, err := reader.ReadBatch(in[received:])
						if err != nil || n == 0 {
							b.Fatal("receive", n, err)
						}
						received += n
					}
					samples[i%len(samples)] = time.Since(start).Nanoseconds()
				}
				b.StopTimer()
				b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*batchSize), "ns/datagram")
				b.ReportMetric(float64(b.N*batchSize)/b.Elapsed().Seconds(), "datagrams/s")
				values := samples[:min(b.N, len(samples))]
				slices.Sort(values)
				for _, percent := range []int{50, 95, 99} {
					b.ReportMetric(float64(values[(len(values)-1)*percent/100]), fmt.Sprintf("p%d-batch-ns", percent))
				}
			})
		}
	}
}
