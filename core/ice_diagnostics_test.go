package core

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestICEDiagnosticsLevelsContextAndRedaction(t *testing.T) {
	t.Setenv("PION_LOG_TRACE", "all")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan Event, 100)
	cfg := Config{Password: "old-signal-secret", Token: "old-room-secret", TURN: TURNConfig{Username: "manual-user", Credential: "manual-credential"}}
	ready := iceSignalMessage{SignalMessage: SignalMessage{PeerDevice: "peer"}, Phase: "relay_tls", TransportGeneration: 7}
	d := newICEDiagnostics(ctx, ready, cfg, func(e Event) { events <- e })
	secrets := []string{cfg.Password, cfg.Token, cfg.TURN.Username, cfg.TURN.Credential, "worker-user:/+", "worker-key:/+", "local-ufrag", "local-password", "remote-ufrag", "remote-password"}
	d.addSecrets(secrets[4:]...)
	logger := d.NewLogger("ice")
	logger.Trace("不能输出报文")
	logger.Tracef("不能输出 %s", "报文")
	logger.Debug("不能输出报文")
	logger.Debugf("不能输出 %s", "报文")
	logger.Info("不能输出报文")
	logger.Infof("不能输出 %s", "报文")
	if len(events) != 0 {
		t.Fatal("启用了 info/debug/trace")
	}
	var message strings.Builder
	message.WriteString("TURN TLS handshake: x509: certificate signed by unknown authority ")
	for _, secret := range secrets {
		fmt.Fprintf(&message, "%s %s %s %s ", secret, url.QueryEscape(secret), url.PathEscape(secret), hex.EncodeToString([]byte(secret)))
	}
	logger.Warn(message.String())
	e := <-events
	if e.Kind != "log" || e.State != "warning" || e.Stage != "ice" || e.Peer != "peer" || e.Phase != "relay_tls" || e.TransportGeneration != 7 {
		t.Fatalf("诊断上下文不完整：%+v", e)
	}
	if !strings.Contains(e.Message, "x509: certificate signed by unknown authority") || !strings.Contains(e.Message, "[redacted]") {
		t.Fatal("错误原因或脱敏标记丢失", e.Message)
	}
	for _, secret := range secrets {
		for _, spelling := range []string{secret, url.QueryEscape(secret), url.PathEscape(secret), hex.EncodeToString([]byte(secret))} {
			if strings.Contains(e.Message, spelling) {
				t.Fatal("泄露了凭据", e.Message)
			}
		}
	}
	logger.Warnf("dial tcp: %s", "connection refused")
	logger.Error("TURN 401 Unauthorized")
	logger.Errorf("TURN allocation: %d", 403)
	for _, state := range []string{"warning", "error", "error"} {
		if e = <-events; e.State != state {
			t.Fatal(e)
		}
	}
	cancel()
	logger.Error("已取消的尝试")
	d.failed(errors.New("已取消的尝试"))
	if len(events) != 0 {
		t.Fatal("已取消的尝试仍输出日志")
	}
}

func TestICEDiagnosticsBoundedConcurrentLogsKeepFinalCause(t *testing.T) {
	events := make(chan Event, maxICEDiagnosticLogs+10)
	d := newICEDiagnostics(context.Background(), iceSignalMessage{}, Config{}, func(e Event) { events <- e })
	logger := d.NewLogger("turn")
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.addSecrets("worker-credential")
			logger.Warn("TURN timeout worker-credential")
		}()
	}
	wg.Wait()
	if len(events) != maxICEDiagnosticLogs+1 {
		t.Fatal("底层日志数量未受限", len(events))
	}
	d.failed(errors.New("final allocation failed: worker-credential"))
	limited, final := false, false
	for len(events) > 0 {
		e := <-events
		data, _ := json.Marshal(e)
		if strings.Contains(string(data), "worker-credential") {
			t.Fatal("并发日志泄露凭据")
		}
		limited = limited || strings.Contains(e.Message, "已达上限")
		final = final || (e.Error != nil && strings.Contains(e.Error.Message, "final allocation failed"))
	}
	if !limited || !final {
		t.Fatal("缺少限流提示或最终失败原因")
	}
}

func TestICEFailedKeepsRawDiagnosticAndFriendlySnapshot(t *testing.T) {
	c, p := lifecycleCoordinator(t)
	c.online = false
	events := make(chan Event, 10)
	c.emit = func(e Event) { events <- e }
	ctx, cancel := context.WithCancel(c.ctx)
	defer cancel()
	a := &iceAttempt{ctx: ctx, cancel: cancel, ready: p.ready}
	a.ready.Phase = "relay_tcp"
	a.diagnostics = newICEDiagnostics(ctx, a.ready, Config{TURN: TURNConfig{Credential: "old-credential"}}, c.emit)
	p.pending = a
	p.failed(a, errors.New("dial tcp 127.0.0.1:3478: connection refused old-credential"))
	if p.stats.Error == nil || p.stats.Error.Code != "ice_path_failed" || strings.Contains(p.stats.Error.Message, "connection refused") {
		t.Fatal("普通快照提示改变", p.stats.Error)
	}
	diagnostic, transport := <-events, <-events
	if diagnostic.Kind != "log" || diagnostic.Error == nil || diagnostic.Error.Code != "network_error" || !strings.Contains(diagnostic.Error.Message, "connection refused") || strings.Contains(diagnostic.Error.Message, "old-credential") {
		t.Fatalf("未保留脱敏后的原始原因：%+v", diagnostic)
	}
	if transport.Kind != "transport" || transport.State != "retrying" || transport.Error.Code != "ice_path_failed" {
		t.Fatal(transport)
	}
	p.failed(a, errors.New("过期尝试"))
	if len(events) != 0 {
		t.Fatal("过期尝试仍输出失败事件")
	}
}

type diagnosticFailurePlatform struct {
	DefaultPlatform
}

func (p diagnosticFailurePlatform) LookupIP(context.Context, string) ([]net.IP, error) {
	return nil, &net.DNSError{Name: "relay.invalid", Err: "fixture DNS failure", IsNotFound: true}
}
func (p diagnosticFailurePlatform) DialSignal(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("fixture TCP connection refused")
}

func TestICEAttemptReportsActualTURNFailures(t *testing.T) {
	for _, kind := range []string{"tcp", "dns", "allocation"} {
		t.Run(kind, func(t *testing.T) {
			c, p := lifecycleCoordinator(t)
			c.online = false
			r := iceFixtureRequest("wss://signal.example/ws", "local")
			r.Config = r.Config.normalized()
			r.Config.ICE.RelayOnly = true
			r.Config.ICE.GatherTimeout = ConfigDuration(time.Second)
			r.Config.ICE.ConnectivityTimeout = ConfigDuration(time.Second)
			r.Config.TURN = TURNConfig{Mode: "manual", URLs: []string{"turn:127.0.0.1:3478?transport=tcp"}, Username: "diagnostic-user", Credential: "diagnostic-credential"}
			phase, want := "relay_tcp", "fixture TCP connection refused"
			if kind == "dns" {
				r.Config.TURN.URLs = []string{"turn:relay.invalid:3478?transport=tcp"}
				want = "fixture DNS failure"
			}
			if kind == "allocation" {
				relayURL, stop := startTURNRelay(t, "udp")
				defer stop()
				r.Config.TURN.URLs = []string{relayURL}
				phase, want = "relay_udp", "failed to allocate on TURN client"
			} else {
				c.platform = diagnosticFailurePlatform{}
			}
			c.desired = r
			events := make(chan Event, 100)
			c.emit = func(e Event) { events <- e }
			ctx, cancel := context.WithCancel(c.ctx)
			defer cancel()
			a := &iceAttempt{ctx: ctx, cancel: cancel, ready: p.ready, events: make(chan iceSignalMessage, 4), failed: make(chan error, 1)}
			a.ready.Phase = phase
			p.pending = a
			p.attempt(a)
			found := false
			for len(events) > 0 {
				e := <-events
				if e.Kind == "log" && strings.Contains(e.Message, want) {
					found = true
					if e.Phase != phase || e.Peer != "peer" || e.TransportGeneration != 1 {
						t.Fatal("底层失败缺少尝试上下文", e)
					}
				}
				data, _ := json.Marshal(e)
				if strings.Contains(string(data), "diagnostic-user") || strings.Contains(string(data), "diagnostic-credential") {
					t.Fatal("底层日志泄露 TURN 凭据", string(data))
				}
			}
			if !found {
				t.Fatalf("实际 TURN 尝试未输出 %q", want)
			}
		})
	}
}
