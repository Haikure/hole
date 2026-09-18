package main

import (
	"bytes"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"hole/core"
)

func testReporter(t *testing.T) (*cliReporter, *bytes.Buffer) {
	t.Helper()
	r, err := cliRequest([]byte("server_url: wss://fixture.invalid/ws?hidden=PRIVATE_URL_QUERY\n"+cliFixtureConfig), cliOptions{})
	if err != nil {
		t.Fatal(err)
	}
	r.Config.TURN.Username = "PRIVATE_TURN_USER"
	r.Config.TURN.Credential = "PRIVATE_TURN_CREDENTIAL"
	var output bytes.Buffer
	return newCLIReporter(log.New(&output, "", 0), r), &output
}

func TestCLIStartupExplainsEffectivePolicyWithoutCredentials(t *testing.T) {
	r, output := testReporter(t)
	r.startup()
	for _, want := range []string{"【启动】hole", "设备「desktop」", "优先直连，必要时自动中继", "共享 0 项服务，访问 0 项服务"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing %q in %s", want, output)
		}
	}
	for _, hidden := range []string{"PRIVATE_", "STUN", "凭据", "无定时采样", "代次", "会话保留"} {
		if strings.Contains(output.String(), hidden) {
			t.Fatalf("default startup contains %q: %s", hidden, output)
		}
	}
	output.Reset()
	r.debug = true
	r.startup()
	for _, want := range []string{"【调试】", "wss://fixture.invalid/ws", "stun.cloudflare.com:3478", "按需申请", "凭据有效期"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("debug startup missing %q: %s", want, output)
		}
	}
	if strings.Contains(output.String(), "PRIVATE_") {
		t.Fatal("debug startup revealed credentials or URL query", output)
	}
}

func TestCLILogsActualPathChangesButNotStatisticsOrLeases(t *testing.T) {
	r, output := testReporter(t)
	p := core.PeerTransportSnapshot{PeerID: "peer", TransportID: "transport-1", Generation: 1, Profile: core.ProfileICE, State: "connecting", Phase: "direct", MappingCount: 2, RelayState: "requesting"}
	s := core.Snapshot{Configured: true, EngineState: "running", SignalState: "joined", PeerTransports: []core.PeerTransportSnapshot{p}}
	r.snapshot(s)
	if !strings.Contains(output.String(), "正在尝试连接对端「peer」（直连）") {
		t.Fatal(output)
	}
	p.State, p.PathType, p.AddressFamily = "active", "direct", "IPv4"
	p.LocalType, p.RemoteType = "host", "srflx"
	p.LocalAddress, p.RemoteAddress = "192.0.2.1:10000", "198.51.100.2:10001"
	p.ActiveChannels, p.ConnectMS, p.RelayState = 2, 100, "ready"
	s.PeerTransports[0] = p
	r.snapshot(s)
	if !strings.Contains(output.String(), "连接成功（直连，IPv4，建连耗时 100 毫秒）") || strings.Contains(output.String(), "通道") {
		t.Fatal(output)
	}
	output.Reset()
	p.BytesSent, p.BytesReceived, p.RTTMS, p.LeaseUntil, p.LocalCandidates = 10, 20, 5, time.Now().UnixMilli(), 3
	p.TURNExpiresAt, p.RelayState = time.Now().Add(time.Hour).UnixMilli(), "requesting"
	p.Generation++
	p.ActiveChannels, p.MappingCount = 3, 3
	s.PeerTransports[0] = p
	r.snapshot(s)
	if output.Len() != 0 {
		t.Fatal("statistics generated a state-change log", output)
	}
	p.State, p.PendingPhase = "switching", "relay_tls"
	s.PeerTransports[0] = p
	r.snapshot(s)
	if !strings.Contains(output.String(), "建立新连接（TLS 中继）") || !strings.Contains(output.String(), "当前连接继续使用（直连）") {
		t.Fatal(output)
	}
	output.Reset()
	p.State, p.PendingPhase, p.PathType, p.Phase, p.RelayProtocol = "active", "", "relay", "relay_tls", "tls"
	p.Generation++
	s.PeerTransports[0] = p
	r.snapshot(s)
	if !strings.Contains(output.String(), "连接已切换（TLS 中继") {
		t.Fatal(output)
	}
	output.Reset()
	p.State, p.Phase = "reconnecting", "direct"
	s.PeerTransports[0] = p
	r.snapshot(s)
	if !strings.Contains(output.String(), "连接中断，正在重新连接") || strings.Contains(output.String(), "198.51.100.2") {
		t.Fatal("lost path shown as connected", output)
	}
	output.Reset()
	s.PeerTransports = nil
	r.snapshot(s)
	if !strings.Contains(output.String(), "已关闭") {
		t.Fatal(output)
	}
}

func TestCLIEventsKeepMappingContextAndRedactErrors(t *testing.T) {
	r, output := testReporter(t)
	e := core.Event{Kind: "mapping", MappingID: "ssh", State: "active", Peer: "peer", Protocol: "tcp", Profile: core.ProfileICE}
	r.event(e)
	first := output.String()
	r.event(e)
	r.event(core.Event{Kind: "mapping", MappingID: "ssh", State: "active"})
	if first != output.String() || !strings.Contains(first, "「ssh」映射已就绪") || !strings.Contains(first, "对端「peer」") {
		t.Fatal(output)
	}
	output.Reset()
	e.Kind, e.State = "session", "error"
	e.SessionID, e.Target, e.Stage = "session-fixture", "127.0.0.1:12345", "dial"
	e.Error = &core.Fault{Code: "service_refused", Message: "PRIVATE_SIGNAL_PASSWORD PRIVATE_ROOM_TOKEN PRIVATE_TURN_USER PRIVATE_TURN_CREDENTIAL\nforged line"}
	r.event(e)
	if strings.Contains(output.String(), "PRIVATE_") || strings.Count(output.String(), "\n") != 1 || strings.Contains(output.String(), "service_refused") || !strings.Contains(output.String(), "目标 127.0.0.1:12345") || !strings.Contains(output.String(), "目标服务拒绝连接") {
		t.Fatal(output)
	}
	r.snapshot(core.Snapshot{Configured: true, EventsDropped: 3})
	if !strings.Contains(output.String(), "省略了 3 条事件（累计 3）") {
		t.Fatal(output)
	}
	output.Reset()
	e.State, e.Error = "active", nil
	r.event(e)
	if !strings.Contains(output.String(), "目标服务连接已成功") {
		t.Fatal("service recovery missing", output)
	}
	output.Reset()
	r.event(e)
	if output.Len() != 0 {
		t.Fatal("ordinary successful connection produced noise", output)
	}
}

func TestCLITCPAndTLSPathsRemainDistinct(t *testing.T) {
	r, output := testReporter(t)
	r.peer(core.PeerTransportSnapshot{PeerID: "peer", State: "active", PathType: "relay", RelayProtocol: "tcp", RelaySide: "local", Phase: "relay_tcp_80", Generation: 5})
	if !strings.Contains(output.String(), "连接成功（TCP 中继）") || strings.Contains(output.String(), "TLS 中继") || strings.Contains(output.String(), "代次") {
		t.Fatal(output)
	}
	output.Reset()
	r.peer(core.PeerTransportSnapshot{PeerID: "peer", State: "active", PathType: "relay", RelaySide: "remote", Phase: "relay_tcp_80"})
	if !strings.Contains(output.String(), "对端中继") || strings.Contains(output.String(), "TCP 中继") {
		t.Fatal(output)
	}
	output.Reset()
	r.debug = true
	r.peer(core.PeerTransportSnapshot{PeerID: "peer", State: "active", PathType: "relay", RelaySide: "remote", Phase: "relay_tcp_80", Generation: 5})
	if !strings.Contains(output.String(), "代次 5") || !strings.Contains(output.String(), "接入协议未上报") {
		t.Fatal(output)
	}
}

func TestCLICredentialWaitIsDistinctFromConnectionAttempt(t *testing.T) {
	r, output := testReporter(t)
	p := core.PeerTransportSnapshot{PeerID: "peer", TransportID: "pair", Generation: 1, Phase: "relay_udp", State: "waiting_credentials"}
	s := core.Snapshot{Configured: true, PeerTransports: []core.PeerTransportSnapshot{p}}
	r.snapshot(s)
	if !strings.Contains(output.String(), "准备中继连接（UDP 中继），等待中继连接信息") || strings.Contains(output.String(), "正在尝试") {
		t.Fatal(output)
	}
	output.Reset()
	s.PeerTransports[0].State = "connecting"
	r.snapshot(s)
	if !strings.Contains(output.String(), "正在尝试连接对端「peer」（UDP 中继）") {
		t.Fatal("credential arrival did not update the same phase", output)
	}
}

type eventOnlyFixture struct {
	events chan core.Event
	reads  chan struct{}
}

func (f *eventOnlyFixture) Events() <-chan core.Event { return f.events }
func (f *eventOnlyFixture) Snapshot() core.Snapshot {
	f.reads <- struct{}{}
	return core.Snapshot{Configured: true}
}

func TestCLIFollowOnlyReadsSnapshotsOnEvents(t *testing.T) {
	r, output := testReporter(t)
	f := &eventOnlyFixture{events: make(chan core.Event), reads: make(chan struct{}, 4)}
	done := make(chan struct{})
	go func() { r.follow(f); close(done) }()
	select {
	case <-f.reads:
		t.Fatal("snapshot was sampled while idle")
	case <-time.After(40 * time.Millisecond):
	}
	f.events <- core.Event{Kind: "signal", State: "joined"}
	select {
	case <-f.reads:
	case <-time.After(time.Second):
		t.Fatal("event did not refresh state")
	}
	close(f.events)
	<-done
	if !strings.Contains(output.String(), "【连接】已加入房间") {
		t.Fatal(output)
	}
	data, err := os.ReadFile("cli_log.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "NewTicker") || strings.Contains(string(data), "NewTimer") {
		t.Fatal("CLI contains a sampling timer")
	}
}

func TestCLIStartupAndReadyDistinguishServiceRoles(t *testing.T) {
	r, output := testReporter(t)
	r.request.Config.Provide = []core.Provide{{ID: "共享服务", Service: core.ServiceEndpoint{Protocol: "tcp", Addr: "127.0.0.1", Port: 22}}}
	r.request.Config.Consume = []core.Consume{{ID: "SSH", Expose: core.HostPort{Addr: "127.0.0.1", Port: 2222}}}
	r.startup()
	for _, want := range []string{"共享 1 项服务，访问 1 项服务", "【共享】「共享服务」目标服务 127.0.0.1:22（TCP）", "【访问】「SSH」本地入口 127.0.0.1:2222（等待映射就绪）"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing %q: %s", want, output)
		}
	}
	if strings.Contains(output.String(), "已就绪") || strings.Contains(output.String(), "已监听") {
		t.Fatal("startup claimed runtime readiness", output)
	}
	output.Reset()
	r.event(core.Event{Kind: "mapping", MappingID: "共享服务", State: "waiting_peer"})
	r.event(core.Event{Kind: "mapping", MappingID: "SSH", State: "waiting_peer"})
	if !strings.Contains(output.String(), "等待访问方上线") || !strings.Contains(output.String(), "等待服务提供方上线") {
		t.Fatal(output)
	}
	output.Reset()
	e := core.Event{Kind: "mapping", MappingID: "SSH", Peer: "家里电脑", Protocol: "tcp", State: "active", Profile: core.ProfileICE}
	r.event(e)
	if !strings.Contains(output.String(), "本地入口 127.0.0.1:2222 → 「家里电脑」（TCP）") || strings.Contains(output.String(), "目标服务连接已成功") {
		t.Fatal("mapping readiness confused with a service health check", output)
	}
	for i := 0; i < 2; i++ {
		output.Reset()
		e.State = "connecting"
		r.event(e)
		e.State = "active"
		r.event(e)
		if strings.Count(output.String(), "映射已恢复") != 1 {
			t.Fatal("mapping recovery missing", output)
		}
	}
}

func TestCLIStartupUsesEffectiveConnectionPolicy(t *testing.T) {
	for _, tc := range []struct {
		preferred, turn, want string
		relayOnly             bool
	}{
		{core.PreferredIPv6, "worker", "仅 IPv6 直连", false},
		{core.PreferredICE, "off", "本机中继已关闭", false},
		{core.PreferredICE, "manual", "必要时自动中继", false},
		{core.PreferredICE, "manual", "仅使用中继", true},
	} {
		t.Run(tc.want, func(t *testing.T) {
			r, output := testReporter(t)
			r.request.Config.Transport.Preferred = tc.preferred
			r.request.Config.TURN.Mode = tc.turn
			r.request.Config.ICE.RelayOnly = tc.relayOnly
			r.startup()
			if !strings.Contains(output.String(), tc.want) {
				t.Fatal(output)
			}
		})
	}
}

func TestCLIDebugPreservesDiagnosticsAndEscapesAllInputs(t *testing.T) {
	r, output := testReporter(t)
	e := core.Event{Kind: "session", MappingID: "SSH\n伪造", Peer: "设备\x1b[31m", State: "error", Protocol: "tcp", SessionID: "session-fixture", Target: "127.0.0.1:22", Stage: "dial", Phase: "direct", TransportGeneration: 7,
		Error: &core.Fault{Code: "service_refused", Message: "PRIVATE_SIGNAL_PASSWORD PRIVATE_ROOM_TOKEN PRIVATE_TURN_USER PRIVATE_TURN_CREDENTIAL\nforged line " + r.request.ServerURL + " " + url.QueryEscape(r.request.ServerURL)}}
	r.event(e)
	for _, hidden := range []string{"session-fixture", "service_refused", "代次", "forged line", "PRIVATE_", "\x1b"} {
		if strings.Contains(output.String(), hidden) {
			t.Fatalf("default log contains %q: %s", hidden, output)
		}
	}
	if strings.Count(output.String(), "\n") != 1 {
		t.Fatal("untrusted fields injected log lines", output)
	}
	output.Reset()
	r.debug = true
	r.debugEvent(e)
	for _, want := range []string{"【调试】", "session-fixture", "service_refused", "代次 7", "forged line", "[redacted]", "127.0.0.1:22"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("debug log missing %q: %s", want, output)
		}
	}
	if strings.Contains(output.String(), "PRIVATE_") || strings.Contains(output.String(), "\x1b") || strings.Count(output.String(), "\n") != 1 {
		t.Fatal("debug mode bypassed redaction or escaping", output)
	}
	output.Reset()
	r.debug = false
	r.event(core.Event{Kind: "log", Message: "底层实现细节"})
	if output.Len() != 0 {
		t.Fatal("raw core log reached default output", output)
	}
	r.debug = true
	r.event(core.Event{Kind: "log", Message: "底层实现细节"})
	if !strings.Contains(output.String(), "【调试】底层实现细节") {
		t.Fatal(output)
	}
}

func TestCLIRepeatedSessionFailuresAreSummarizedAndRecover(t *testing.T) {
	r, output := testReporter(t)
	e := core.Event{Kind: "session", MappingID: "SSH", Peer: "家里电脑", State: "error", Error: &core.Fault{Code: "service_refused", Message: "raw"}}
	for i := 1; i <= 12; i++ {
		e.SessionID = fmt.Sprintf("session-%d", i)
		r.event(e)
	}
	if strings.Count(output.String(), "\n") != 3 || !strings.Contains(output.String(), "累计 5 次") || !strings.Contains(output.String(), "累计 10 次") {
		t.Fatal("identical failures were not summarized", output)
	}
	output.Reset()
	e.Error = &core.Fault{Code: "service_timeout", Message: "raw timeout"}
	r.event(e)
	if !strings.Contains(output.String(), "连接目标服务超时") {
		t.Fatal("new cause was hidden by suppression", output)
	}
	output.Reset()
	e.State, e.Error = "active", nil
	r.event(e)
	r.event(e)
	if strings.Count(output.String(), "目标服务连接已成功") != 1 {
		t.Fatal(output)
	}
	output.Reset()
	e.State, e.Error = "error", &core.Fault{Code: "service_refused"}
	r.event(e)
	if strings.Count(output.String(), "\n") != 1 || strings.Contains(output.String(), "累计") {
		t.Fatal("suppression was not reset by recovery", output)
	}
}

func TestCLISignalRetryDoesNotClaimBusinessDisconnection(t *testing.T) {
	r, output := testReporter(t)
	r.event(core.Event{Kind: "signal", State: "joined"})
	p := core.PeerTransportSnapshot{PeerID: "设备", TransportID: "pair", State: "active", PathType: "direct"}
	r.peer(p)
	output.Reset()
	for i := 0; i < 6; i++ {
		r.event(core.Event{Kind: "signal", State: "reconnecting", Error: &core.Fault{Code: "network_error", Message: "raw"}})
		r.event(core.Event{Kind: "signal", State: "connecting"})
		r.peer(p)
	}
	if strings.Count(output.String(), "\n") != 2 || !strings.Contains(output.String(), "协调服务器连接中断") || strings.Contains(output.String(), "与「设备」的连接中断") || strings.Contains(output.String(), "服务中断") {
		t.Fatal(output)
	}
	output.Reset()
	r.event(core.Event{Kind: "signal", State: "joined"})
	r.event(core.Event{Kind: "signal", State: "joined"})
	if strings.Count(output.String(), "\n") != 1 || !strings.Contains(output.String(), "协调服务器连接已恢复") {
		t.Fatal(output)
	}
	initial, initialOutput := testReporter(t)
	initial.event(core.Event{Kind: "signal", State: "reconnecting", Error: &core.Fault{Code: "signal_auth_failed"}})
	if strings.Contains(initialOutput.String(), "中断") || !strings.Contains(initialOutput.String(), "密码不匹配") {
		t.Fatal("first connection failure described as an outage", initialOutput)
	}
}

func TestCLIPathRetriesUseRetryCountNotGeneration(t *testing.T) {
	r, output := testReporter(t)
	p := core.PeerTransportSnapshot{PeerID: "设备", TransportID: "pair", State: "connecting", Phase: "direct", Generation: 900}
	r.peer(p)
	for i := uint64(1); i <= 12; i++ {
		p.State, p.RetryCount, p.Generation = "reconnecting", i, 900+i*100
		p.Error = &core.Fault{Code: "ice_path_failed", Message: "raw"}
		r.peer(p)
		p.State, p.Phase = "connecting", []string{"direct", "relay_udp", "relay_tcp_80", "relay_tls_443"}[i%4]
		r.peer(p)
	}
	if strings.Count(output.String(), "\n") > 8 || !strings.Contains(output.String(), "发起 5 次重试") || !strings.Contains(output.String(), "发起 10 次重试") {
		t.Fatal("retry loop still floods output", output)
	}
	for _, hidden := range []string{"代次", "连接中断", "【失败】", "raw", "900"} {
		if strings.Contains(output.String(), hidden) {
			t.Fatalf("routine fallback contains %q: %s", hidden, output)
		}
	}
	p.State, p.PathType, p.RelayProtocol, p.Error = "active", "relay", "tls", nil
	r.peer(p)
	output.Reset()
	p.State = "reconnecting"
	r.peer(p)
	p.State = "active"
	r.peer(p)
	if !strings.Contains(output.String(), "连接中断") || !strings.Contains(output.String(), "连接已恢复（TLS 中继）") {
		t.Fatal("actual outage or recovery missing", output)
	}
}

func TestCLIFailedSwitchDoesNotClaimSuccessfulSwitch(t *testing.T) {
	r, output := testReporter(t)
	p := core.PeerTransportSnapshot{PeerID: "设备", TransportID: "pair", State: "active", PathType: "direct", Phase: "direct", Generation: 3, ConnectMS: 120}
	r.peer(p)
	p.State, p.PendingPhase = "switching", "relay_tls"
	r.peer(p)
	output.Reset()
	p.State, p.PendingPhase = "active", ""
	p.Error = &core.Fault{Code: "ice_path_failed"}
	r.peer(p)
	if !strings.Contains(output.String(), "继续使用原连接（直连）") || strings.Contains(output.String(), "切换完成") || strings.Contains(output.String(), "耗时") {
		t.Fatal(output)
	}
	output.Reset()
	r.peer(core.PeerTransportSnapshot{PeerID: "未知路径设备", State: "active", Phase: "relay_tcp_80"})
	if strings.Contains(output.String(), "TCP 中继") || strings.Contains(output.String(), "直连") {
		t.Fatal("attempt phase was presented as the actual path", output)
	}
}

type snapshotFixture struct {
	events chan core.Event
	state  core.Snapshot
}

func (f *snapshotFixture) Events() <-chan core.Event { return f.events }
func (f *snapshotFixture) Snapshot() core.Snapshot   { return f.state }

func TestCLIFollowUsesCurrentMappingStateAndPrintsPathFirst(t *testing.T) {
	for _, state := range []string{"active", "connecting"} {
		t.Run(state, func(t *testing.T) {
			r, output := testReporter(t)
			f := &snapshotFixture{events: make(chan core.Event, 1), state: core.Snapshot{Configured: true, SignalState: "joined",
				PeerTransports: []core.PeerTransportSnapshot{{PeerID: "设备", TransportID: "pair", State: state, Phase: "direct", PathType: "direct"}},
				Mappings:       []core.MappingSnapshot{{ID: "SSH", Peer: "设备", State: state, Protocol: "tcp", Profile: core.ProfileICE}},
			}}
			f.events <- core.Event{Kind: "mapping", MappingID: "SSH", State: "active"}
			close(f.events)
			r.follow(f)
			if state == "active" {
				path, mapping := strings.Index(output.String(), "连接成功"), strings.Index(output.String(), "映射已就绪")
				if path < 0 || mapping < path {
					t.Fatal("readiness appeared before the device connection", output)
				}
			} else if strings.Contains(output.String(), "已就绪") || strings.Contains(output.String(), "连接成功") {
				t.Fatal("stale queued event claimed readiness", output)
			}
		})
	}
}

func TestCLIDroppedEventsReconcileMappingsWithoutClaimingServiceHealth(t *testing.T) {
	r, output := testReporter(t)
	s := core.Snapshot{Configured: true, SignalState: "joined", EventsDropped: 2, Mappings: []core.MappingSnapshot{{ID: "SSH", State: "active", Error: &core.Fault{Code: "service_refused"}}}}
	r.snapshot(s)
	for _, want := range []string{"映射已就绪", "目标服务拒绝连接", "省略了 2 条事件"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing %q: %s", want, output)
		}
	}
	output.Reset()
	r.snapshot(s)
	if output.Len() != 0 {
		t.Fatal("unchanged snapshot caused repeat diagnostics", output)
	}
	r.event(core.Event{Kind: "engine", State: "stopped"})
	s.EngineState = "stopped"
	r.snapshot(s)
	if output.String() != "【停止】hole 已停止\n" {
		t.Fatal("shutdown produced misleading per-path failures", output)
	}
}

func TestCLILibraryLogsUseDebugChannelAndRestoreGlobalLogger(t *testing.T) {
	r, output := testReporter(t)
	writer, flags, prefix := log.Writer(), log.Flags(), log.Prefix()
	func() {
		restore := r.captureLibraryLogs()
		defer restore()
		for _, direction := range []string{"receive", "send"} {
			log.Printf("connection doesn't allow setting of %s buffer size. Not a *net.UDPConn?. See documentation.", direction)
		}
		if output.Len() != 0 {
			t.Fatal("expected packet-adapter notice reached default logs", output)
		}
		log.Print("an unexpected library diagnostic")
		log.Print("another diagnostic")
		if strings.Count(output.String(), "\n") != 1 || !strings.Contains(output.String(), "【提示】网络库报告了运行诊断") || strings.Contains(output.String(), "unexpected") {
			t.Fatal(output)
		}
		output.Reset()
		r.debug = true
		log.Print("PRIVATE_ROOM_TOKEN\nraw library diagnostic")
		if !strings.Contains(output.String(), "【调试】网络库：") || !strings.Contains(output.String(), "raw library diagnostic") || strings.Contains(output.String(), "PRIVATE_") || strings.Count(output.String(), "\n") != 1 {
			t.Fatal(output)
		}
	}()
	if log.Writer() != writer || log.Flags() != flags || log.Prefix() != prefix {
		t.Fatal("global logger configuration was not restored")
	}
}
