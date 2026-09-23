package main

import (
	"bytes"
	"fmt"
	"log"
	"net/url"
	"os"
	"regexp"
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
	return newCLIReporter(log.New(&output, "", log.Ltime), r), &output
}

func TestCLILogFormatAndCauseRules(t *testing.T) {
	if logDuration(999) != "999ms" || logDuration(1000) != "1.0s" || logDuration(5100) != "5.1s" {
		t.Fatal("duration formatting changed")
	}
	if logIdent("desktop") != "desktop" || logIdent("a b") != `"a b"` || logIdent("a:b") != `"a:b"` || logIdent("") != `""` {
		t.Fatal("identifier formatting changed")
	}
	r, output := testReporter(t)
	r.startup()
	r.event(core.Event{Kind: "signal", State: "joined"})
	r.event(core.Event{Kind: "mapping", MappingID: "ssh", State: "active", Peer: "laptop", Protocol: "tcp"})
	r.event(core.Event{Kind: "session", MappingID: "ssh", Peer: "laptop", State: "error", Target: "127.0.0.1:22",
		Error: &core.Fault{Code: "custom_code", Message: "raw\nfailure"}})
	pattern := regexp.MustCompile(`^\d{2}:\d{2}:\d{2} (INFO |WARN |ERROR|DEBUG) `)
	for _, line := range strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n") {
		if !pattern.MatchString(line) {
			t.Fatalf("invalid log line: %q", line)
		}
	}
	text := output.String()
	for _, forbidden := range []string{"\u3010", "\u300c", "\u00b7", "（", "）", "可加 " + "-debug"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("legacy presentation remains: %s", text)
		}
	}
	if !strings.Contains(text, "custom_code: raw\\nfailure") {
		t.Fatalf("unknown code and raw message missing: %s", text)
	}
}

func TestCLIStartupExplainsEffectivePolicyWithoutCredentials(t *testing.T) {
	r, output := testReporter(t)
	r.startup()
	for _, want := range []string{"hole development", "设备 desktop"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing %q in %s", want, output)
		}
	}
	for _, hidden := range []string{"PRIVATE_", "STUN", "凭据", "无定时采样", "代次", "会话保留", "\u3010", "\u300c", "\u00b7", "DEBUG"} {
		if strings.Contains(output.String(), hidden) {
			t.Fatalf("default startup contains %q: %s", hidden, output)
		}
	}
	output.Reset()
	r.debug = true
	r.startup()
	for _, want := range []string{"DEBUG config", "wss://fixture.invalid/ws", "stun.cloudflare.com:3478", "turn mode=worker", "urls=0"} {
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
	if !strings.Contains(output.String(), "尝试直连") {
		t.Fatal(output)
	}
	p.State, p.PathType, p.AddressFamily = "active", "direct", "IPv4"
	p.LocalType, p.RemoteType = "host", "srflx"
	p.LocalAddress, p.RemoteAddress = "192.0.2.1:10000", "198.51.100.2:10001"
	p.ActiveChannels, p.ConnectMS, p.RelayState = 2, 100, "ready"
	s.PeerTransports[0] = p
	r.snapshot(s)
	if !strings.Contains(output.String(), "已连接 直连/IPv4 100ms") || strings.Contains(output.String(), "通道") {
		t.Fatal(output)
	}
	output.Reset()
	p.BytesSent, p.BytesReceived, p.RTTMS, p.LeaseUntil, p.LocalCandidates = 10, 20, 5, time.Now().UnixMilli(), 3
	p.TURNExpiresAt, p.RelayState = time.Now().Add(time.Hour).UnixMilli(), "requesting"
	p.DatagramLimit = 1400
	p.CandidatePairs = []core.CandidatePairSnapshot{{LocalType: "host", RemoteType: "srflx", LocalAddress: "192.0.2.1:10000", RemoteAddress: "198.51.100.2:10001", State: "succeeded", RTTMS: 5, Selected: true}}
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
	if !strings.Contains(output.String(), "尝试切换到 TLS 中继 (当前 直连/IPv4)") {
		t.Fatal(output)
	}
	output.Reset()
	p.State, p.PendingPhase, p.PathType, p.Phase, p.RelayProtocol = "active", "", "relay", "relay_tls", "tls"
	p.Generation++
	s.PeerTransports[0] = p
	r.snapshot(s)
	if !strings.Contains(output.String(), "已切换 TLS 中继") {
		t.Fatal(output)
	}
	output.Reset()
	p.State, p.Phase = "reconnecting", "direct"
	s.PeerTransports[0] = p
	r.snapshot(s)
	if !strings.Contains(output.String(), "连接中断，重连中") || strings.Contains(output.String(), "198.51.100.2") {
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
	if first != output.String() || !strings.Contains(first, "ssh: 映射已就绪，对端 peer (tcp)") {
		t.Fatal(output)
	}
	output.Reset()
	e.Kind, e.State = "session", "error"
	e.SessionID, e.Target, e.Stage = "session-fixture", "127.0.0.1:12345", "dial"
	e.Error = &core.Fault{Code: "service_refused", Message: "PRIVATE_SIGNAL_PASSWORD PRIVATE_ROOM_TOKEN PRIVATE_TURN_USER PRIVATE_TURN_CREDENTIAL\nforged line"}
	r.event(e)
	if strings.Contains(output.String(), "PRIVATE_") || strings.Count(output.String(), "\n") != 1 || !strings.Contains(output.String(), "service_refused") || !strings.Contains(output.String(), "目标 127.0.0.1:12345") || !strings.Contains(output.String(), "拒绝连接") {
		t.Fatal(output)
	}
	r.snapshot(core.Snapshot{Configured: true, EventsDropped: 3})
	if !strings.Contains(output.String(), "省略事件: 3/3") {
		t.Fatal(output)
	}
	output.Reset()
	e.State, e.Error = "active", nil
	r.event(e)
	if !strings.Contains(output.String(), "目标服务已恢复") {
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
	if !strings.Contains(output.String(), "已连接 TCP 中继") || strings.Contains(output.String(), "TLS 中继") || strings.Contains(output.String(), "代次") {
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
	if !strings.Contains(output.String(), "gen=5") || !strings.Contains(output.String(), "remote_relay_proto=unreported") {
		t.Fatal(output)
	}
}

func TestCLICredentialWaitIsDistinctFromConnectionAttempt(t *testing.T) {
	r, output := testReporter(t)
	p := core.PeerTransportSnapshot{PeerID: "peer", TransportID: "pair", Generation: 1, Phase: "relay_udp", State: "waiting_credentials"}
	s := core.Snapshot{Configured: true, PeerTransports: []core.PeerTransportSnapshot{p}}
	r.snapshot(s)
	if !strings.Contains(output.String(), "等待中继凭据") || strings.Contains(output.String(), "尝试 ") {
		t.Fatal(output)
	}
	output.Reset()
	s.PeerTransports[0].State = "connecting"
	r.snapshot(s)
	if !strings.Contains(output.String(), "尝试UDP 中继") {
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
	if !strings.Contains(output.String(), "已加入房间") {
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
	for _, want := range []string{"共享服务: 共享 tcp://127.0.0.1:22", "SSH: 本地入口 127.0.0.1:2222"} {
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
	if !strings.Contains(output.String(), "等待提供方上线") || !strings.Contains(output.String(), "等待访问方上线") {
		t.Fatal(output)
	}
	output.Reset()
	e := core.Event{Kind: "mapping", MappingID: "SSH", Peer: "家里电脑", Protocol: "tcp", State: "active", Profile: core.ProfileICE}
	r.event(e)
	if !strings.Contains(output.String(), "SSH: 映射已就绪，对端 家里电脑 (tcp)") || strings.Contains(output.String(), "目标服务已恢复") {
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

func TestCLIDebugPreservesDiagnosticsAndEscapesAllInputs(t *testing.T) {
	r, output := testReporter(t)
	e := core.Event{Kind: "session", MappingID: "SSH\n伪造", Peer: "设备\x1b[31m", State: "error", Protocol: "tcp", SessionID: "session-fixture", Target: "127.0.0.1:22", Stage: "dial", Phase: "direct", TransportGeneration: 7,
		Error: &core.Fault{Code: "service_refused", Message: "PRIVATE_SIGNAL_PASSWORD PRIVATE_ROOM_TOKEN PRIVATE_TURN_USER PRIVATE_TURN_CREDENTIAL\nforged line " + r.request.ServerURL + " " + url.QueryEscape(r.request.ServerURL)}}
	r.event(e)
	for _, hidden := range []string{"session-fixture", "代次", "PRIVATE_", "\x1b", "\u3010", "\u300c", "\u00b7"} {
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
	for _, want := range []string{"DEBUG session", "session-fixture", "service_refused", "gen=7", "forged line", "[redacted]", "target=127.0.0.1:22"} {
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
	if !strings.Contains(output.String(), "DEBUG core 底层实现细节") {
		t.Fatal(output)
	}
	output.Reset()
	r.debugPeer(core.PeerTransportSnapshot{PeerID: "peer", State: "active", Phase: "relay_udp", PathType: "relay", RelaySide: "remote", AddressFamily: "IPv4", DatagramLimit: 1400,
		CandidatePairs: []core.CandidatePairSnapshot{{LocalType: "srflx", RemoteType: "relay", LocalAddress: "203.0.113.1:20000", RemoteAddress: "198.51.100.9:41000", State: "succeeded", RelayLegs: 1, RTTMS: 30, Selected: true}, {LocalType: "relay", RemoteType: "relay", State: "failed", RelayLegs: 2}}})
	for _, want := range []string{"datagram_limit=1400", "pair srflx 203.0.113.1:20000 -> relay 198.51.100.9:41000 succeeded legs=1 rtt=30ms selected", "failed legs=2"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("debug peer line missing %q: %s", want, output)
		}
	}
}

func TestCLIRepeatedSessionFailuresAreSummarizedAndRecover(t *testing.T) {
	r, output := testReporter(t)
	e := core.Event{Kind: "session", MappingID: "SSH", Peer: "家里电脑", State: "error", Error: &core.Fault{Code: "service_refused", Message: "raw"}}
	for i := 1; i <= 12; i++ {
		e.SessionID = fmt.Sprintf("session-%d", i)
		r.event(e)
	}
	if strings.Count(output.String(), "\n") != 3 || !strings.Contains(output.String(), " x5") || !strings.Contains(output.String(), " x10") {
		t.Fatal("identical failures were not summarized", output)
	}
	output.Reset()
	e.Error = &core.Fault{Code: "service_timeout", Message: "raw timeout"}
	r.event(e)
	if !strings.Contains(output.String(), "连接超时") {
		t.Fatal("new cause was hidden by suppression", output)
	}
	output.Reset()
	e.State, e.Error = "active", nil
	r.event(e)
	r.event(e)
	if strings.Count(output.String(), "目标服务已恢复") != 1 {
		t.Fatal(output)
	}
	output.Reset()
	e.State, e.Error = "error", &core.Fault{Code: "service_refused"}
	r.event(e)
	if strings.Count(output.String(), "\n") != 1 || strings.Contains(output.String(), " x") {
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
	if strings.Count(output.String(), "\n") != 2 || !strings.Contains(output.String(), "信令连接中断") || strings.Contains(output.String(), "服务中断") {
		t.Fatal(output)
	}
	output.Reset()
	r.event(core.Event{Kind: "signal", State: "joined"})
	r.event(core.Event{Kind: "signal", State: "joined"})
	if strings.Count(output.String(), "\n") != 1 || !strings.Contains(output.String(), "信令已恢复") {
		t.Fatal(output)
	}
	initial, initialOutput := testReporter(t)
	initial.event(core.Event{Kind: "signal", State: "reconnecting", Error: &core.Fault{Code: "signal_auth_failed"}})
	if strings.Contains(initialOutput.String(), "中断") || !strings.Contains(initialOutput.String(), "signal_auth_failed") {
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
	if strings.Count(output.String(), "\n") > 8 || !strings.Contains(output.String(), "累计 5") || !strings.Contains(output.String(), "累计 10") {
		t.Fatal("retry loop still floods output", output)
	}
	for _, hidden := range []string{"代次", "\u3010", "raw", "900"} {
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
	if !strings.Contains(output.String(), "连接中断") || !strings.Contains(output.String(), "已恢复 TLS 中继") {
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
	if !strings.Contains(output.String(), "继续使用 直连") || strings.Contains(output.String(), "切换完成") {
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
				path, mapping := strings.Index(output.String(), "已连接"), strings.Index(output.String(), "映射已就绪")
				if path < 0 || mapping < path {
					t.Fatal("readiness appeared before the device connection", output)
				}
			} else if strings.Contains(output.String(), "已就绪") || strings.Contains(output.String(), "已连接") {
				t.Fatal("stale queued event claimed readiness", output)
			}
		})
	}
}

func TestCLIDroppedEventsReconcileMappingsWithoutClaimingServiceHealth(t *testing.T) {
	r, output := testReporter(t)
	s := core.Snapshot{Configured: true, SignalState: "joined", EventsDropped: 2, Mappings: []core.MappingSnapshot{{ID: "SSH", State: "active", Error: &core.Fault{Code: "service_refused"}}}}
	r.snapshot(s)
	for _, want := range []string{"映射已就绪", "拒绝连接", "省略事件: 2/2"} {
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
	if !strings.HasSuffix(output.String(), "INFO  hole 已停止\n") {
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
		log.Print("an unexpected library diagnostic")
		if strings.Count(output.String(), "\n") != 1 || !strings.Contains(output.String(), "WARN  网络库: an unexpected library diagnostic") {
			t.Fatal(output)
		}
		output.Reset()
		r.debug = true
		log.Print("PRIVATE_ROOM_TOKEN\nraw library diagnostic")
		if !strings.Contains(output.String(), "DEBUG lib ") || !strings.Contains(output.String(), "raw library diagnostic") || strings.Contains(output.String(), "PRIVATE_") || strings.Count(output.String(), "\n") != 1 {
			t.Fatal(output)
		}
	}()
	if log.Writer() != writer || log.Flags() != flags || log.Prefix() != prefix {
		t.Fatal("global logger configuration was not restored")
	}
}
