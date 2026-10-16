package core

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
)

func TestVoiceFiveParticipantsRealOpusAndMute(t *testing.T) {
	server, _ := actualWorkerFixture(t)
	names := []string{"alpha", "beta", "gamma", "delta", "epsilon"}
	engines := make([]*Engine, len(names))
	for i, name := range names {
		engines[i] = NewEngine(Options{RetryNetwork: true})
		t.Cleanup(func() { _ = engines[i].Close() })
		request := iceFixtureRequest(server, name)
		request.Config.Voice.Enabled = true
		if err := engines[i].Start(request); err != nil {
			t.Fatal(err)
		}
	}
	for _, engine := range engines {
		waitSnapshot(t, engine, func(s Snapshot) bool {
			if s.SignalState != "joined" || len(s.Voice.Peers) != len(names)-1 {
				return false
			}
			for _, peer := range s.Voice.Peers {
				if peer.State != "active" {
					return false
				}
			}
			return true
		})
	}
	tones := make([][]byte, len(names))
	for i := range tones {
		tones[i] = make([]byte, 960*2)
		for sample := 0; sample < 960; sample++ {
			value := int16(8000 * math.Sin(2*math.Pi*float64((i+1)*300)*float64(sample)/48000))
			binary.LittleEndian.PutUint16(tones[i][sample*2:], uint16(value))
		}
	}
	type phaseResult struct {
		label   string
		muted   bool
		before  []map[string]VoicePeerSnapshot
		after   []VoiceSnapshot
		audible []int
	}
	var results []phaseResult
	sequence := int64(0)
	const period = 20 * time.Millisecond
	started := time.Now()
	ticker := time.NewTicker(period)
	defer ticker.Stop()
	runPhase := func(label string, muted bool) {
		t.Helper()
		if err := engines[0].SetVoiceMuted(muted); err != nil {
			t.Fatal(err)
		}
		before := make([]map[string]VoicePeerSnapshot, len(engines))
		for i, engine := range engines {
			before[i] = map[string]VoicePeerSnapshot{}
			for _, peer := range engine.Snapshot().Voice.Peers {
				before[i][peer.PeerID] = peer
			}
		}
		audible := make([]int, len(names))
		for frame := 0; frame < 45; frame++ {
			tick := <-ticker.C
			// A delayed ticker can skip capture slots under -race. Preserve
			// their elapsed sampling time rather than slowing the audio clock.
			timestamp := int64(tick.Sub(started)/period) * 960
			for i, engine := range engines {
				if err := engine.PushVoicePCM(sequence, timestamp, tones[i]); err != nil {
					t.Fatal(err)
				}
				playback := engine.PullVoicePCM(4)
				var energy int64
				for offset := 0; offset+1 < len(playback); offset += 2 {
					value := int64(int16(binary.LittleEndian.Uint16(playback[offset:])))
					energy += value * value
				}
				if len(playback) > 0 && energy/int64(len(playback)/2) > 10000 {
					audible[i]++
				}
			}
			sequence++
		}
		after := make([]VoiceSnapshot, len(engines))
		for i, engine := range engines {
			after[i] = engine.Snapshot().Voice
		}
		results = append(results, phaseResult{label, muted, before, after, audible})
	}
	runPhase("simultaneous", false)
	runPhase("alpha_muted", true)
	runPhase("resumed", false)
	// Report only after continuous capture finishes; logging between phases
	// would introduce an artificial source pause while playback keeps ticking.
	for _, result := range results {
		for i, snapshot := range result.after {
			if result.audible[i] < 25 {
				t.Errorf("%s/%s: only %d audible output batches", result.label, names[i], result.audible[i])
			}
			for _, peer := range snapshot.Peers {
				old := result.before[i][peer.PeerID]
				sent, decoded := peer.SentFrames-old.SentFrames, peer.DecodedFrames-old.DecodedFrames
				t.Logf("%s %s->%s: sent=%d decoded=%d error=%v", result.label, names[i], peer.PeerID, sent, decoded, peer.Error)
				if peer.Error != nil {
					t.Error(peer.Error)
				}
				if !(i == 0 && result.muted) && sent < 35 {
					t.Errorf("%s/%s: sender stalled", result.label, names[i])
				}
				if i == 0 && result.muted && sent > 2 {
					t.Errorf("%s/%s: muted sender transmitted %d frames", result.label, names[i], sent)
				}
				if !(peer.PeerID == names[0] && result.muted) && decoded < 30 {
					t.Errorf("%s/%s: decoder from %s stalled", result.label, names[i], peer.PeerID)
				}
			}
		}
		t.Logf("%s: audible output batches=%v", result.label, result.audible)
	}
}

// Exercise the existing application socket across a real Worker/ICE/QUIC
// reconnect, with room voice as the only difference from the control case.
func TestVoicePreservesMappedSessionsAcrossReconnect(t *testing.T) {
	for _, voice := range []bool{false, true} {
		for _, transition := range []string{"config", "network"} {
			t.Run(fmt.Sprintf("voice=%v/%s", voice, transition), func(t *testing.T) {
				server, _ := actualWorkerFixture(t)
				echo, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer echo.Close()
				go func() {
					for {
						conn, err := echo.Accept()
						if err != nil {
							return
						}
						go func() { defer conn.Close(); _, _ = io.Copy(conn, conn) }()
					}
				}()
				udpEcho, err := net.ListenPacket("udp4", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer udpEcho.Close()
				udpSources := make(chan string, 4)
				go func() {
					data := make([]byte, 128)
					for {
						n, addr, err := udpEcho.ReadFrom(data)
						if err != nil {
							return
						}
						select {
						case udpSources <- addr.String():
						default:
						}
						_, _ = udpEcho.WriteTo(data[:n], addr)
					}
				}()
				a, b := NewEngine(Options{RetryNetwork: true}), NewEngine(Options{RetryNetwork: true})
				defer a.Close()
				defer b.Close()
				ra, rb := iceFixtureRequest(server, "alpha"), iceFixtureRequest(server, "beta")
				ra.Config.Voice.Enabled, rb.Config.Voice.Enabled = voice, voice
				expose, udpExpose := unusedTCPPort(t), unusedUDPPort(t)
				ra.Config.Provide = []Provide{
					{ID: "echo", Service: ServiceEndpoint{"tcp", "127.0.0.1", echo.Addr().(*net.TCPAddr).Port}},
					{ID: "udp", Service: ServiceEndpoint{"udp", "127.0.0.1", udpEcho.LocalAddr().(*net.UDPAddr).Port}},
				}
				rb.Config.Consume = []Consume{
					{ID: "echo", Expose: HostPort{"127.0.0.1", expose}},
					{ID: "udp", Expose: HostPort{"127.0.0.1", udpExpose}},
				}
				if err := a.Start(ra); err != nil {
					t.Fatal(err)
				}
				if err := b.Start(rb); err != nil {
					t.Fatal(err)
				}
				ready := func(s Snapshot) bool {
					return s.SignalState == "joined" && len(s.PeerTransports) == 1 && s.PeerTransports[0].State == "active" && s.PeerTransports[0].ActiveChannels == 2
				}
				waitSnapshot(t, a, ready)
				waitSnapshot(t, b, ready)
				conn, err := net.Dial("tcp", endpointString("127.0.0.1", expose))
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				exchange := func(text string) error {
					_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
					if _, err := conn.Write([]byte(text)); err != nil {
						return err
					}
					got := make([]byte, len(text))
					_, err := io.ReadFull(conn, got)
					if err == nil && string(got) != text {
						return fmt.Errorf("echo changed: %q", got)
					}
					return err
				}
				if err := exchange("before"); err != nil {
					t.Fatal(err)
				}
				udp, err := net.Dial("udp4", endpointString("127.0.0.1", udpExpose))
				if err != nil {
					t.Fatal(err)
				}
				defer udp.Close()
				exchangeUDP := func(text string) string {
					t.Helper()
					_ = udp.SetDeadline(time.Now().Add(3 * time.Second))
					if _, err := udp.Write([]byte(text)); err != nil {
						t.Fatal(err)
					}
					got := make([]byte, 128)
					n, err := udp.Read(got)
					if err != nil || string(got[:n]) != text {
						t.Fatalf("UDP echo=%q err=%v", got[:n], err)
					}
					return <-udpSources
				}
				udpSource := exchangeUDP("before")
				before := b.Snapshot().PeerTransports[0].Generation
				if transition == "config" {
					rb.Config.SessionTimeout = 91 * time.Second
					err = b.ApplyConfig(rb)
				} else {
					err = b.NetworkChanged()
				}
				if err != nil {
					t.Fatal(err)
				}
				waitSnapshot(t, b, func(s Snapshot) bool { return ready(s) && s.PeerTransports[0].Generation > before })
				waitSnapshot(t, a, ready)
				if err := exchange("after"); err != nil {
					t.Fatalf("existing TCP socket was terminated after %s with voice=%v: %v", transition, voice, err)
				}
				if after := exchangeUDP("after"); after != udpSource {
					t.Fatalf("UDP upstream socket was replaced: %s -> %s", udpSource, after)
				}
			})
		}
	}
}

// A transport replacement keeps sessions; an explicit revocation must still
// terminate them. This guard applies to both voice and ordinary transports.
func TestVoiceOfflineOnlyTerminatesRevokedSessions(t *testing.T) {
	for _, reason := range []string{"signal_offline", "mapping_removed", "peer_runtime_changed", "revoked"} {
		t.Run(reason, func(t *testing.T) {
			c, peer := lifecycleCoordinator(t)
			c.sessions = newSessionManager(time.Minute)
			defer c.sessions.close()
			terminated := false
			key := providerSessionKey{mapping: peer.ready.Mappings[0].ID, fingerprint: peer.ready.PeerFingerprint}
			c.sessions.mu.Lock()
			c.sessions.tcp[key] = &providerTCPEntry{ready: make(chan struct{}), cancel: func() { terminated = true }}
			c.sessions.mu.Unlock()
			peer.offline(reason)
			if terminated != (reason != "signal_offline") {
				t.Fatalf("terminated=%v for %s", terminated, reason)
			}
		})
	}
}

// Model quic-go's context-free blocked DATAGRAM API. Cancellation must close
// the transport to release both media and feedback senders.
type congestedVoiceDatagrams struct {
	entered             chan struct{}
	closed              chan struct{}
	sendOnce, closeOnce sync.Once
}

func (c *congestedVoiceDatagrams) SendDatagram([]byte) error {
	c.sendOnce.Do(func() { close(c.entered) })
	<-c.closed
	return net.ErrClosed
}

func (c *congestedVoiceDatagrams) CloseWithError(quic.ApplicationErrorCode, string) error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

type congestedVoiceLink struct {
	ctx      context.Context
	conn     *congestedVoiceDatagrams
	feedback bool
}

func (l *congestedVoiceLink) Context() context.Context { return l.ctx }
func (l *congestedVoiceLink) SendVoiceDatagram(ctx context.Context, data []byte) error {
	if l.feedback {
		return nil
	}
	return sendVoiceDatagram(ctx, l.conn, data)
}
func (l *congestedVoiceLink) SendVoiceFeedback(ctx context.Context, data []byte) error {
	if !l.feedback {
		return nil
	}
	return sendVoiceDatagram(ctx, l.conn, data)
}
func (l *congestedVoiceLink) ReceiveVoiceDatagram(ctx context.Context) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (l *congestedVoiceLink) ReceiveVoiceFeedback(ctx context.Context) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestVoiceDisableCancelsCongestedSenders(t *testing.T) {
	for _, feedback := range []bool{false, true} {
		t.Run(fmt.Sprintf("feedback=%v", feedback), func(t *testing.T) {
			c, _ := lifecycleCoordinator(t)
			c.desired.Config.Voice.Enabled = true
			r := newVoiceRuntime(c.ctx, nil, nil, fakeVoiceCodec{})
			c.voice = r
			conn := &congestedVoiceDatagrams{entered: make(chan struct{}), closed: make(chan struct{})}
			defer func() { _ = conn.CloseWithError(0, "test cleanup"); r.close() }()
			link := &congestedVoiceLink{ctx: c.ctx, conn: conn, feedback: feedback}
			r.bindPeer("peer", "pair", 1, link)
			peer := r.peers["peer"]
			if feedback {
				peer.mu.Lock()
				peer.receiveWindow.observe(0, time.Now())
				peer.mu.Unlock()
			} else {
				peer.enqueue(PCMFrame{Samples: make([]int16, 960)})
			}
			select {
			case <-conn.entered:
			case <-time.After(2 * time.Second):
				t.Fatal("sender did not reach congestion")
			}
			request := c.request()
			request.Config.Voice.Enabled = false
			done := make(chan struct{})
			go func() { c.submit(request); close(done) }()
			select {
			case <-done:
			case <-time.After(time.Second):
				_ = conn.CloseWithError(0, "unblock failed test")
				<-done
				t.Fatal("voice disable waited for congestion to clear")
			}
			if c.voiceRuntimePtr() != nil {
				t.Fatal("disabled runtime remains bound")
			}
		})
	}
}

func TestVoiceCancelledSendLeavesUnusedTransportOpen(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	conn := &congestedVoiceDatagrams{entered: make(chan struct{}), closed: make(chan struct{})}
	if err := sendVoiceDatagram(ctx, conn, []byte{1}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	select {
	case <-conn.entered:
		t.Fatal("cancelled send reached the transport")
	case <-conn.closed:
		t.Fatal("unused transport was closed")
	default:
	}
}
