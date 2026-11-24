package main

import (
	"bytes"
	"encoding/json"
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
	stamp := time.Date(2026, 11, 8, 7, 59, 59, 123456789, time.Local)
	if got := localNATTime(stamp.UTC().Format(time.RFC3339Nano)); got != "2026-11-08 07:59:59" {
		t.Fatalf("NAT timestamp did not use system time: %q", got)
	}
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

func TestCLIPeerRelayPathShowsTheNode(t *testing.T) {
	r, output := testReporter(t)
	p := core.PeerTransportSnapshot{PeerID: "peer", TransportID: "peer-relay-1", Profile: core.ProfileICE, Phase: "peer_relay", State: "connecting"}
	r.peer(p)
	if !strings.Contains(output.String(), "节点中继") {
		t.Fatal(output)
	}
	output.Reset()
	p.State, p.PathType, p.RelayDevice = "active", "peer_relay", "relay"
	r.peer(p)
	if !strings.Contains(output.String(), "已连接 节点中继") || !strings.Contains(output.String(), "relay") {
		t.Fatal(output)
	}
}

func TestCLIFixedPathShowsEndpointActualAddressAndFailure(t *testing.T) {
	r, output := testReporter(t)
	p := core.PeerTransportSnapshot{PeerID: "public", TransportID: "fixed-1", Profile: core.ProfileFixed, Phase: "fixed", FixedEndpoint: "gateway.example:50000", State: "connecting"}
	r.peer(p)
	if !strings.Contains(output.String(), "固定入口直连") || !strings.Contains(output.String(), p.FixedEndpoint) {
		t.Fatal(output)
	}
	output.Reset()
	p.State, p.PathType, p.RemoteAddress = "active", "fixed", "203.0.113.10:50000"
	r.peer(p)
	if !strings.Contains(output.String(), "已连接 固定入口直连") || !strings.Contains(output.String(), p.RemoteAddress) {
		t.Fatal(output)
	}
	output.Reset()
	p.State = "reconnecting"
	p.Error = &core.Fault{Code: "fixed_path_failed", Message: "dial timeout"}
	r.peer(p)
	if !strings.Contains(output.String(), "fixed_path_failed") || !strings.Contains(output.String(), "dial timeout") || !strings.Contains(output.String(), p.FixedEndpoint) {
		t.Fatal(output)
	}
}

func TestCLIStartupExplainsEffectivePolicyWithoutCredentials(t *testing.T) {
	r, output := testReporter(t)
	r.startup()
	for _, want := range []string{"hole " + core.CoreVersion, "设备 desktop", "房间 fixture", "信令 wss://fixture.invalid/ws", "连接策略: 直连优先"} {
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
	if !strings.Contains(output.String(), "已连接 直连/IPv4，耗时 100ms") || !strings.Contains(output.String(), "对端 "+p.RemoteAddress) || strings.Contains(output.String(), "通道") {
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

func TestCLIEventsKeepMappingContextAndPreserveErrors(t *testing.T) {
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
	if !strings.Contains(output.String(), logText(e.Error.Message)) || strings.Contains(output.String(), "[redacted]") || strings.Count(output.String(), "\n") != 1 || !strings.Contains(output.String(), "service_refused") || !strings.Contains(output.String(), "目标 127.0.0.1:12345") || !strings.Contains(output.String(), "拒绝连接") {
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

func TestCLIConnectionLogIncludesMeasuredQUICRTT(t *testing.T) {
	for _, p := range []core.PeerTransportSnapshot{
		{PeerID: "hole-peer", State: "active", PathType: "hole", RelayDevice: "relay", ConnectMS: 123, RTTMS: 27},
		{PeerID: "fixed-peer", State: "active", Profile: core.ProfileFixed, PathType: "fixed", ConnectMS: 123, RTTMS: 31},
	} {
		t.Run(p.PeerID, func(t *testing.T) {
			r, output := testReporter(t)
			r.peer(p)
			if !strings.Contains(output.String(), "RTT "+logDuration(p.RTTMS)) {
				t.Fatalf("connection log omitted measured RTT: %s", output)
			}
		})
	}
}

func TestCLIConnectedEndpointsAndElapsedTime(t *testing.T) {
	for _, tc := range []struct {
		name string
		peer core.PeerTransportSnapshot
		want string
	}{
		{"IPv4", core.PeerTransportSnapshot{PathType: "direct", RemoteAddress: "203.0.113.5:42117"}, "对端 203.0.113.5:42117"},
		{"IPv6", core.PeerTransportSnapshot{PathType: "direct", RemoteAddress: "[2001:db8::5]:42117"}, "对端 [2001:db8::5]:42117"},
		{"fixed", core.PeerTransportSnapshot{Profile: core.ProfileFixed, PathType: "fixed", FixedEndpoint: "gateway.example:50000", RemoteAddress: "203.0.113.5:50000"}, "对端 203.0.113.5:50000"},
		{"local TURN", core.PeerTransportSnapshot{PathType: "relay", RelaySide: "local", RemoteAddress: "203.0.113.5:42117"}, "对端 203.0.113.5:42117"},
		{"remote TURN", core.PeerTransportSnapshot{PathType: "relay", RelaySide: "remote", RemoteAddress: "198.51.100.8:41000"}, "对端中继 198.51.100.8:41000"},
		{"Hole", core.PeerTransportSnapshot{PathType: "hole", RelayDevice: "relay", RemoteAddress: "198.51.100.8:40000", RelayAccessAddress: "198.51.100.8:40000"}, "中继 198.51.100.8:40000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, output := testReporter(t)
			p := tc.peer
			p.PeerID, p.State, p.ConnectMS, p.RTTMS = "laptop", "active", 1200, 28
			r.peer(p)
			for _, want := range []string{"laptop: 已连接", tc.want, "耗时 1.2s", "RTT 28ms"} {
				if !strings.Contains(output.String(), want) {
					t.Fatalf("missing %q: %s", want, output)
				}
			}
			if strings.Count(output.String(), p.RemoteAddress) != 1 || strings.Contains(output.String(), "建连") {
				t.Fatal("address duplicated or old elapsed label remains", output)
			}
			output.Reset()
			p.State = "paused"
			r.peer(p)
			if !strings.HasSuffix(output.String(), "WARN  设备 laptop: 离线\n") || strings.Count(output.String(), "\n") != 1 {
				t.Fatal("offline log retained old path details", output)
			}
			output.Reset()
			p.State = "active"
			r.peer(p)
			if !strings.Contains(output.String(), "已恢复") || !strings.Contains(output.String(), tc.want) {
				t.Fatal("recovery omitted the current endpoint", output)
			}
		})
	}
}

func TestCLISwitchAndAddressChangeUseCurrentEndpoint(t *testing.T) {
	r, output := testReporter(t)
	p := core.PeerTransportSnapshot{PeerID: "laptop", State: "active", PathType: "direct", Generation: 1, RemoteAddress: "203.0.113.5:42117", ConnectMS: 100}
	r.peer(p)
	p.State, p.PendingPhase = "switching", "direct"
	r.peer(p)
	output.Reset()
	p.State, p.PendingPhase, p.Generation, p.RemoteAddress = "active", "", 2, "203.0.113.6:42118"
	r.peer(p)
	if !strings.Contains(output.String(), "已切换") || !strings.Contains(output.String(), p.RemoteAddress) || strings.Contains(output.String(), "203.0.113.5:42117") {
		t.Fatal("switch reported an old endpoint", output)
	}
	output.Reset()
	p.RemoteAddress = "203.0.113.7:42119"
	r.peer(p)
	r.peer(p)
	if !strings.Contains(output.String(), "连接地址已更新") || !strings.Contains(output.String(), p.RemoteAddress) || strings.Contains(output.String(), "耗时") || strings.Count(output.String(), "\n") != 1 {
		t.Fatal("address update was hidden, repeated, or reused old handshake timing", output)
	}
}

func TestCLIPeerRelayShowsActualOuterEndpoint(t *testing.T) {
	r, output := testReporter(t)
	s := core.Snapshot{Configured: true, PeerTransports: []core.PeerTransportSnapshot{
		{PeerID: "laptop", TransportID: "a-inner", State: "active", PathType: "peer_relay", RelayDevice: "relay", RemoteAddress: "127.0.0.2:1", RelayEndpoint: "old.example:40000"},
		{PeerID: "relay", TransportID: "z-outer", State: "active", PathType: "direct", RemoteAddress: "203.0.113.8:41000"},
	}}
	r.snapshot(s)
	line := strings.Split(output.String(), "\n")[0]
	if !strings.Contains(line, "laptop: 已连接 节点中继") || !strings.Contains(line, "中继 203.0.113.8:41000") || strings.Contains(line, "127.0.0.2:1") || strings.Contains(line, "old.example") {
		t.Fatal("node relay endpoint was virtual, stale, or depended on display order", output)
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
	if !strings.Contains(output.String(), "提供方离线") || !strings.Contains(output.String(), "访问方离线") || strings.Contains(output.String(), "上线") {
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
	for _, hidden := range []string{"session-fixture", "代次", "[redacted]", "\x1b", "\u3010", "\u300c", "\u00b7"} {
		if strings.Contains(output.String(), hidden) {
			t.Fatalf("default log contains %q: %s", hidden, output)
		}
	}
	if strings.Count(output.String(), "\n") != 1 || !strings.Contains(output.String(), logText(e.Error.Message)) {
		t.Fatal("error content was changed or injected log lines", output)
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

func TestCLIICEDiagnosticsAreDebugOnlyAndKeepAttemptContext(t *testing.T) {
	r, output := testReporter(t)
	events := []core.Event{
		{Kind: "log", State: "warning", Stage: "ice", Peer: "peer\nforged", Phase: "relay_tcp", TransportGeneration: 7,
			Message: "Failed to dial TCP address 127.0.0.1:3478: connection refused PRIVATE_TURN_CREDENTIAL\n\x1b[31m"},
		{Kind: "log", State: "failed", Stage: "ice", Peer: "peer", Phase: "relay_tcp", TransportGeneration: 7,
			Error: &core.Fault{Code: "network_error", Message: "context deadline exceeded PRIVATE_TURN_CREDENTIAL"}},
	}
	for _, e := range events {
		r.event(e)
	}
	if output.Len() != 0 {
		t.Fatal("底层中继诊断进入普通日志", output)
	}
	r.debug = true
	for _, e := range events {
		r.event(e)
	}
	for _, want := range []string{"DEBUG core warning", "DEBUG core failed", "stage=ice", "phase=relay_tcp", "gen=7", "peer=", "127.0.0.1:3478", "connection refused", "context deadline exceeded", "err=network_error", "[redacted]"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("debug 诊断缺少 %q：%s", want, output)
		}
	}
	if strings.Contains(output.String(), "PRIVATE_") || strings.Contains(output.String(), "\x1b") || strings.Count(output.String(), "\n") != 2 {
		t.Fatal("debug 诊断泄露凭据或允许终端注入", output)
	}
}

func TestCLINATPunchCalibrationDebugLogs(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			r, output := testReporter(t)
			r.format = format
			events := []core.Event{
				{Kind: "log", State: "calibration_local", Stage: "nat_punch", Peer: "nec", Phase: "direct", TransportGeneration: 149,
					Message: `round=1 trial=1 mode=single result=refresh stun="192.0.2.1:3478" mapped="198.51.100.2:40001" model_ip="198.51.100.1" reason="public IP changed PRIVATE_TURN_CREDENTIAL"`},
				{Kind: "log", State: "calibration_peer", Stage: "nat_punch", Peer: "nec", Phase: "direct", TransportGeneration: 149,
					Message: "round=1 trial=1 mode=single result=ok"},
				{Kind: "log", State: "round_finished", Stage: "nat_punch", Peer: "nec", Phase: "direct", TransportGeneration: 149,
					Message: "round=1 mode=single probes_sent=0 acks_verified=0"},
			}
			for _, e := range events {
				r.followEvent(e, core.Snapshot{})
			}
			if output.Len() != 0 {
				t.Fatal("NAT calibration details appeared without -debug", output)
			}
			r.debug = true
			for _, e := range events {
				r.followEvent(e, core.Snapshot{})
			}
			lines := strings.Split(strings.TrimSpace(output.String()), "\n")
			if len(lines) != len(events) || strings.Contains(output.String(), "PRIVATE_") {
				t.Fatal("NAT calibration logs were suppressed or revealed credentials", output)
			}
			for i, line := range lines {
				if format == "json" {
					var record cliLogRecord
					if err := json.Unmarshal([]byte(line), &record); err != nil || record.Level != "DEBUG" || record.State != events[i].State || record.Peer != "nec" || record.Stage != "nat_punch" || record.TransportGeneration != 149 {
						t.Fatalf("NAT calibration JSON context lost: err=%v line=%s", err, line)
					}
					line = record.Message
				}
				want := []string{events[i].State, "round=1"}
				switch i {
				case 0:
					want = append(want, "trial=1", "mode=single", "result=refresh", "192.0.2.1:3478", "198.51.100.2:40001", "198.51.100.1", "public IP changed", "[redacted]")
				case 1:
					want = append(want, "result=ok")
				case 2:
					want = append(want, "probes_sent=0", "acks_verified=0")
				}
				for _, value := range want {
					if !strings.Contains(line, value) {
						t.Fatalf("NAT calibration evidence %q lost: %s", value, line)
					}
				}
			}
		})
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
	if strings.Count(output.String(), "\n") != 4 || !strings.Contains(output.String(), "WARN  信令连接断开 [信令委托]") || !strings.Contains(output.String(), " x5") || strings.Contains(output.String(), "服务中断") {
		t.Fatal(output)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	for i := 0; i < len(lines); i += 2 {
		if !strings.Contains(lines[i], "信令断开原因: network_error: raw") || !strings.Contains(lines[i+1], "WARN  信令连接断开 [信令委托]") {
			t.Fatal("cause must precede each outage summary", output)
		}
	}
	output.Reset()
	r.event(core.Event{Kind: "signal", State: "joined"})
	r.event(core.Event{Kind: "signal", State: "joined"})
	if strings.Count(output.String(), "\n") != 1 || !strings.Contains(output.String(), "信令已恢复") {
		t.Fatal(output)
	}
	initial, initialOutput := testReporter(t)
	initial.event(core.Event{Kind: "signal", State: "reconnecting", Error: &core.Fault{Code: "signal_auth_failed"}})
	if strings.Contains(initialOutput.String(), "信令连接断开") || !strings.Contains(initialOutput.String(), "signal_auth_failed") {
		t.Fatal("first connection failure described as an outage", initialOutput)
	}
}

func TestCLISignalDisconnectSeverityUsesCurrentConnections(t *testing.T) {
	for _, tc := range []struct {
		name, peerState, room, want string
	}{
		{"live peer", "active", `{"signal_via":["peer"]}`, "WARN  信令连接断开 [信令委托]"},
		{"switching peer", "switching", `{"signal_via":["peer"]}`, "WARN  信令连接断开 [信令委托]"},
		{"last signal despite live data peer", "active", `{"groups":[{"members":["desktop","peer"],"signaling":["desktop"]}]}`, "ERROR 信令连接断开"},
		{"reachable room route", "", `{"signal_via":["peer"]}`, "WARN  信令连接断开 [信令委托]"},
		{"last signal connection", "", `{"groups":[{"members":["desktop"],"signaling":["desktop"]}]}`, "ERROR 信令连接断开"},
		{"pending is not connected", "connecting", `{"groups":[{"members":["desktop"],"signaling":["desktop"]}]}`, "ERROR 信令连接断开"},
		{"other group cannot delegate", "paused", `{"groups":[{"members":["desktop"],"signaling":["desktop"]},{"members":["other"],"signaling":["other"]}]}`, "ERROR 信令连接断开"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, output := testReporter(t)
			r.event(core.Event{Kind: "signal", State: "joined"})
			// A cached connection must not override the current snapshot.
			r.peer(core.PeerTransportSnapshot{PeerID: "old", State: "active", PathType: "direct"})
			output.Reset()
			s := core.Snapshot{Configured: true, SignalState: "reconnecting", Room: testCLIRoom(t, tc.room)}
			if tc.peerState != "" {
				s.PeerTransports = []core.PeerTransportSnapshot{{PeerID: "peer", State: tc.peerState}}
			}
			r.eventWithSnapshot(core.Event{Kind: "signal", State: "reconnecting", Error: &core.Fault{Code: "network_error", Message: "EOF PRIVATE_ROOM_TOKEN\n\x1b[31m"}}, s)
			lines := strings.Split(strings.TrimSpace(output.String()), "\n")
			if len(lines) != 2 || !strings.Contains(lines[0], "信令断开原因: network_error: EOF PRIVATE_ROOM_TOKEN") || !strings.HasSuffix(lines[1], tc.want) {
				t.Fatal("wrong severity, missing cause, or reversed output order", output)
			}
			if strings.Contains(output.String(), "[redacted]") || strings.Contains(output.String(), "\x1b") {
				t.Fatal("signal error was changed or bypassed terminal escaping", output)
			}
			stamp := regexp.MustCompile(`^\d{2}:\d{2}:\d{2} `)
			for _, line := range lines {
				if !stamp.MatchString(line) {
					t.Fatal("time prefix was removed", output)
				}
			}
		})
	}
}

func TestCLIDelegatedOutageEscalatesWhenLastConnectionIsLost(t *testing.T) {
	r, output := testReporter(t)
	s := core.Snapshot{Configured: true, SignalState: "joined", PeerTransports: []core.PeerTransportSnapshot{{PeerID: "peer", State: "active", PathType: "direct"}}, Room: testCLIRoom(t, `{"signal_via":["peer"]}`)}
	r.followEvent(core.Event{Kind: "signal", State: "joined"}, s)
	s.SignalState = "delegated"
	r.followEvent(core.Event{Kind: "signal", State: "reconnecting", Error: &core.Fault{Code: "network_error", Message: "EOF"}}, s)
	output.Reset()
	r.followEvent(core.Event{Kind: "signal", State: "delegated"}, s)
	if output.Len() != 0 {
		t.Fatal("delegation repeated the outage or claimed recovery", output)
	}
	s.PeerTransports, s.Room = nil, testCLIRoom(t, `{"groups":[{"members":["desktop"],"signaling":["desktop"]}]}`)
	r.followEvent(core.Event{Kind: "room", State: "changed"}, s)
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) < 2 || !strings.HasSuffix(lines[len(lines)-2], "ERROR 信令断开原因: network_error: EOF") || !strings.HasSuffix(lines[len(lines)-1], "ERROR 信令连接断开") {
		t.Fatal("last connection loss did not escalate with its cause", output)
	}
	output.Reset()
	for i := 0; i < 6; i++ {
		r.snapshot(s)
	}
	if output.Len() != 0 {
		t.Fatal("snapshot reads counted as signal retries", output)
	}
	s.SignalState = "joined"
	r.followEvent(core.Event{Kind: "signal", State: "joined"}, s)
	if strings.Count(output.String(), "信令已恢复") != 1 || strings.Contains(output.String(), "信令连接断开") {
		t.Fatal("direct signaling recovery retained the outage", output)
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

func testCLIRoom(t *testing.T, data string) *core.RoomSnapshot {
	t.Helper()
	var room core.RoomSnapshot
	if err := json.Unmarshal([]byte(data), &room); err != nil {
		t.Fatal(err)
	}
	return &room
}

func TestCLIRoomPresenceReportsOfflineAndRecovery(t *testing.T) {
	r, output := testReporter(t)
	p := core.PeerTransportSnapshot{PeerID: "huawei", TransportID: "pair", State: "active", PathType: "direct"}
	s := core.Snapshot{Configured: true, EngineState: "running", SignalState: "joined", PeerTransports: []core.PeerTransportSnapshot{p},
		Room: testCLIRoom(t, `{"members":[{"device_name":"desktop"},{"device_name":"huawei"}],"groups":[{"group_id":"desktop","members":["desktop","huawei"],"signaling":["desktop"]}]}`)}
	r.snapshot(s)
	output.Reset()

	p.State, p.RetryCount = "reconnecting", 5
	s.PeerTransports[0] = p
	s.Room = testCLIRoom(t, `{"members":[{"device_name":"desktop"}],"groups":[{"group_id":"desktop","members":["desktop"],"signaling":["desktop"]}]}`)
	r.snapshot(s)
	p.RetryCount = 10
	s.PeerTransports[0] = p
	r.snapshot(s)
	if got := output.String(); strings.Count(got, "WARN  设备 huawei: 离线\n") != 1 || strings.Contains(got, "重连中") || strings.Contains(got, "重试中") || strings.Contains(got, "等待") {
		t.Fatalf("offline peer reported as retrying: %s", got)
	}

	output.Reset()
	s.Room = testCLIRoom(t, `{"members":[{"device_name":"desktop"}],"groups":[{"group_id":"desktop","members":["desktop"],"signaling":["desktop"]},{"group_id":"huawei","members":["huawei"],"signaling":["huawei"]}]}`)
	r.snapshot(s)
	if !strings.Contains(output.String(), "huawei: 重试中") || strings.Contains(output.String(), "离线") {
		t.Fatalf("returning peer was still shown offline: %s", output)
	}
	output.Reset()
	p.State, p.PathType = "active", "direct"
	s.PeerTransports[0] = p
	r.snapshot(s)
	if !strings.Contains(output.String(), "huawei: 已恢复 直连") {
		t.Fatalf("connection recovery missing: %s", output)
	}
}

func TestCLIRoomPresenceWaitsForTopologyAfterConnectionLoss(t *testing.T) {
	r, output := testReporter(t)
	p := core.PeerTransportSnapshot{PeerID: "huawei", TransportID: "pair", State: "active", PathType: "direct"}
	s := core.Snapshot{Configured: true, EngineState: "running", SignalState: "joined", PeerTransports: []core.PeerTransportSnapshot{p},
		Room: testCLIRoom(t, `{"members":[{"device_name":"desktop"},{"device_name":"huawei"}],"groups":[{"group_id":"desktop","members":["desktop","huawei"],"signaling":["desktop"]}]}`)}
	r.snapshot(s)
	output.Reset()

	// The local edge is gone, while the Worker's previous graph still contains it.
	p.State = "reconnecting"
	s.PeerTransports[0] = p
	s.Room = testCLIRoom(t, `{"members":[{"device_name":"desktop"}],"groups":[{"group_id":"desktop","members":["desktop","huawei"],"signaling":["desktop"]}]}`)
	r.snapshot(s)
	r.snapshot(s)
	if got := output.String(); strings.Count(got, "连接中断，对端状态未知") != 1 || strings.Contains(got, "离线") || strings.Contains(got, "重试中") || strings.Contains(got, "重连中") {
		t.Fatalf("stale graph was treated as confirmed presence: %s", got)
	}
	output.Reset()
	s.Room = testCLIRoom(t, `{"members":[{"device_name":"desktop"}],"groups":[{"group_id":"desktop","members":["desktop"],"signaling":["desktop"]}]}`)
	r.snapshot(s)
	if !strings.Contains(output.String(), "WARN  设备 huawei: 离线\n") {
		t.Fatalf("confirmed offline state missing: %s", output)
	}
}

func TestCLIRoomPresenceDoesNotMistakeOnlineOrUnknownForOffline(t *testing.T) {
	for _, tc := range []struct {
		name, signal, room, want string
	}{
		{"reachable through room", "delegated", `{"signal_via":["relay"],"members":[{"device_name":"desktop"},{"device_name":"huawei"}],"groups":[{"members":["desktop","huawei"],"signaling":["relay"]}]}`, "重连中"},
		{"direct signaling", "joined", `{"members":[{"device_name":"desktop"}],"groups":[{"members":["desktop","huawei"],"signaling":["desktop","huawei"]}]}`, "重连中"},
		{"different group", "joined", `{"members":[{"device_name":"desktop"}],"groups":[{"members":["desktop"],"signaling":["desktop"]},{"members":["huawei"],"signaling":["huawei"]}]}`, "重连中"},
		{"local signaling unavailable", "reconnecting", `{"members":[{"device_name":"desktop"}],"groups":[{"members":["desktop"],"signaling":["desktop"]}]}`, "对端状态未知"},
		{"delegated signal route lost", "delegated", `{"members":[{"device_name":"desktop"}],"groups":[{"members":["desktop"],"signaling":["desktop"]}]}`, "对端状态未知"},
		{"own group missing", "joined", `{"members":[{"device_name":"desktop"}],"groups":[{"members":["other"],"signaling":["other"]}]}`, "对端状态未知"},
		{"no room graph", "joined", `{"members":[{"device_name":"desktop"}],"groups":[]}`, "重连中"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, output := testReporter(t)
			p := core.PeerTransportSnapshot{PeerID: "huawei", TransportID: "pair", State: "active", PathType: "direct"}
			s := core.Snapshot{Configured: true, EngineState: "running", SignalState: tc.signal, PeerTransports: []core.PeerTransportSnapshot{p}, Room: testCLIRoom(t, tc.room)}
			r.snapshot(s)
			output.Reset()
			p.State = "reconnecting"
			s.PeerTransports[0] = p
			r.snapshot(s)
			if got := output.String(); !strings.Contains(got, tc.want) || strings.Contains(got, "离线") {
				t.Fatalf("incorrect presence: %s", got)
			}
		})
	}
}

func TestCLIRoomPresenceKeepsActivePathsAndErrorsVisible(t *testing.T) {
	r, output := testReporter(t)
	p := core.PeerTransportSnapshot{PeerID: "huawei", TransportID: "pair", State: "active", PathType: "direct"}
	s := core.Snapshot{Configured: true, EngineState: "running", SignalState: "joined", PeerTransports: []core.PeerTransportSnapshot{p},
		Room: testCLIRoom(t, `{"members":[{"device_name":"desktop"}],"groups":[{"members":["desktop"],"signaling":["desktop"]}]}`)}
	r.snapshot(s)
	if !strings.Contains(output.String(), "已连接 直连") || strings.Contains(output.String(), "离线") {
		t.Fatalf("live path masked by stale Worker state: %s", output)
	}
	output.Reset()
	p.State, p.Error = "error", &core.Fault{Code: "peer_identity_mismatch"}
	s.PeerTransports[0] = p
	r.snapshot(s)
	if !strings.Contains(output.String(), "连接失败") || strings.Contains(output.String(), "离线") {
		t.Fatalf("terminal error masked by room presence: %s", output)
	}
}

func TestCLIRoomPresenceDoesNotShowStaleFixedPathErrors(t *testing.T) {
	r, output := testReporter(t)
	p := core.PeerTransportSnapshot{PeerID: "huawei", TransportID: "pair", Profile: core.ProfileFixed, FixedEndpoint: "127.0.0.1:1234", State: "active", PathType: "fixed"}
	s := core.Snapshot{Configured: true, EngineState: "running", SignalState: "joined", PeerTransports: []core.PeerTransportSnapshot{p},
		Room: testCLIRoom(t, `{"members":[{"device_name":"desktop"},{"device_name":"huawei"}],"groups":[{"members":["desktop","huawei"],"signaling":["desktop"]}]}`)}
	r.snapshot(s)
	output.Reset()

	p.State = "reconnecting"
	p.Error = &core.Fault{Code: "fixed_path_failed", Message: "固定入口连接已断开，正在重试"}
	s.PeerTransports[0] = p
	s.Room = testCLIRoom(t, `{"members":[{"device_name":"desktop"}],"groups":[{"members":["desktop","huawei"],"signaling":["desktop"]}]}`)
	r.snapshot(s)
	s.Room = testCLIRoom(t, `{"members":[{"device_name":"desktop"}],"groups":[{"members":["desktop"],"signaling":["desktop"]}]}`)
	r.snapshot(s)
	if got := output.String(); !strings.Contains(got, "对端状态未知，固定入口 127.0.0.1:1234") || !strings.Contains(got, "WARN  设备 huawei: 离线\n") || strings.Contains(got, "正在重试") || strings.Contains(got, "fixed_path_failed") {
		t.Fatalf("outdated fixed path error contradicted room presence: %s", got)
	}
}

func TestCLIRoomPresenceOnlineWaitDoesNotClaimPriorConnection(t *testing.T) {
	r, output := testReporter(t)
	p := core.PeerTransportSnapshot{PeerID: "huawei", TransportID: "pair", State: "waiting_peer"}
	s := core.Snapshot{Configured: true, EngineState: "running", SignalState: "joined", PeerTransports: []core.PeerTransportSnapshot{p},
		Room: testCLIRoom(t, `{"groups":[{"members":["desktop"],"signaling":["desktop"]},{"members":["huawei"],"signaling":["huawei"]}]}`)}
	r.snapshot(s)
	if got := output.String(); !strings.Contains(got, "huawei: 连接中") || strings.Contains(got, "连接中断") || strings.Contains(got, "离线") {
		t.Fatalf("online peer incorrectly reported as disconnected: %s", got)
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
		if output.Len() != 0 {
			t.Fatal("raw library diagnostic reached default logs", output)
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

func TestCLIListenerTimelineUsesCurrentStateAndRealServiceEvents(t *testing.T) {
	r, output := testReporter(t)
	fault := &core.Fault{Code: "address_in_use", Message: "listen tcp 127.0.0.1:2222: bind: address already in use"}
	s := core.Snapshot{Configured: true, EngineState: "running", SignalState: "joined",
		PeerTransports: []core.PeerTransportSnapshot{{PeerID: "laptop", State: "active", PathType: "direct", RemoteAddress: "203.0.113.1:45678"}},
		Mappings: []core.MappingSnapshot{{ID: "SSH", State: "error", Protocol: "tcp", Peer: "laptop", Error: fault,
			Listener: &core.ListenerSnapshot{State: "error", Protocol: "tcp", Address: "127.0.0.1:2222", Error: fault}}}}
	// A queued success must not hide the bind failure in the current snapshot.
	r.followEvent(core.Event{Kind: "listener", MappingID: "SSH", State: "ready", Target: "old:2222"}, s)
	if !strings.Contains(output.String(), "ERROR 映射 SSH: 本地端口被占用 tcp://127.0.0.1:2222") || strings.Count(output.String(), "address_in_use") != 1 || strings.Contains(output.String(), "已监听") {
		t.Fatal("listener failure missing or repeated as a mapping failure", output)
	}
	output.Reset()
	r.snapshot(s)
	if output.Len() != 0 {
		t.Fatal("unchanged listener snapshot generated logs", output)
	}
	s.Mappings[0].State, s.Mappings[0].Error = "active", nil
	s.Mappings[0].Listener = &core.ListenerSnapshot{State: "ready", Protocol: "tcp", Address: "127.0.0.1:2222"}
	r.followEvent(core.Event{Kind: "listener", MappingID: "SSH", State: "error", Error: fault}, s)
	for _, want := range []string{"INFO  映射 SSH: 监听已恢复 tcp://127.0.0.1:2222", "映射已就绪"} {
		if !strings.Contains(output.String(), want) {
			t.Fatal("listener recovery missing", want, output)
		}
	}
	if strings.Contains(output.String(), "目标服务") {
		t.Fatal("listening was mistaken for a target health check", output)
	}
	output.Reset()
	e := core.Event{Kind: "session", MappingID: "SSH", Peer: "laptop", State: "active", Target: "127.0.0.1:22"}
	for i := 0; i < 3; i++ {
		e.SessionID = fmt.Sprint(i)
		r.event(e)
	}
	if strings.Count(output.String(), "INFO  服务 SSH: 目标服务已连通，目标 127.0.0.1:22") != 1 {
		t.Fatal("first service success missing or repeated per session", output)
	}
	output.Reset()
	s.PeerTransports[0].State, s.Mappings[0].State = "waiting_peer", "waiting_peer"
	r.snapshot(s)
	if strings.Contains(output.String(), "监听已关闭") {
		t.Fatal("device loss incorrectly closed a retained listener", output)
	}
	output.Reset()
	s.Mappings = nil
	r.snapshot(s)
	r.snapshot(s)
	if strings.Count(output.String(), "监听已关闭 tcp://127.0.0.1:2222") != 1 {
		t.Fatal("removed listener closure missing or repeated", output)
	}
}

func TestCLIJSONRecordsKeepContextAndFiltering(t *testing.T) {
	if err := run("missing-config.yaml", cliOptions{logFormat: "yaml"}); err == nil || !strings.Contains(err.Error(), "日志格式") {
		t.Fatal("invalid log format was not rejected before file access", err)
	}
	r, output := testReporter(t)
	r.format = "json"
	r.startup()
	r.event(core.Event{Kind: "log", Message: "hidden raw diagnostics"})
	r.peer(core.PeerTransportSnapshot{PeerID: "laptop", State: "active", PathType: "direct", Phase: "direct", Generation: 9,
		RemoteAddress: "[2001:db8::1]:45678", AddressFamily: "IPv6", ConnectMS: 123, RTTMS: 45})
	e := core.Event{Kind: "session", MappingID: "SSH", Peer: "laptop", State: "error", Target: "127.0.0.1:22",
		Error: &core.Fault{Code: "service_refused", Message: "quoted \" PRIVATE_ROOM_TOKEN\n" + r.request.ServerURL}}
	for i := 0; i < 5; i++ {
		r.event(e)
	}
	r.event(core.Event{Kind: "signal", State: "joined"})
	r.event(core.Event{Kind: "signal", State: "reconnecting", Error: &core.Fault{Code: "network_error", Message: "EOF"}})
	var records []cliLogRecord
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var record cliLogRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("invalid JSON log: %v: %s", err, line)
		}
		if !regexp.MustCompile(`^\d{2}:\d{2}:\d{2}$`).MatchString(record.Time) || record.Level == "" || record.Component == "" {
			t.Fatal("missing time, severity or component", line)
		}
		records = append(records, record)
	}
	var connected, failure, summary bool
	for _, record := range records {
		switch record.Component {
		case "peer":
			connected = record.Peer == "laptop" && record.RemoteAddress == "[2001:db8::1]:45678" && record.ElapsedMS == 123 && record.RTTMS == 45 && record.TransportGeneration == 9
		case "session":
			if record.ErrorCode != "service_refused" || record.MappingID != "SSH" || record.Target != "127.0.0.1:22" || record.ErrorMessage != e.Error.Message {
				t.Fatal("service context or original error lost", record)
			}
			failure = true
			summary = summary || record.Count == 5
		}
	}
	if !connected || !failure || !summary || strings.Contains(output.String(), "[redacted]") || strings.Contains(output.String(), "hidden raw diagnostics") {
		t.Fatal("JSON context, filtering or original content changed", output)
	}
	last := records[len(records)-2:]
	if !strings.Contains(last[0].Message, "信令断开原因") || last[1].Message != "信令连接断开 [信令委托]" || last[0].Level != "WARN" || last[1].Level != "WARN" {
		t.Fatal("signal cause and summary were separated", last)
	}
	output.Reset()
	r.debug = true
	for i := 0; i < 12; i++ {
		r.event(core.Event{Kind: "log", State: "warning", Peer: "laptop", Stage: "ice", Phase: "direct", TransportGeneration: 9, Message: "repeated diagnostic"})
	}
	if strings.Count(output.String(), "\n") != 3 || !strings.Contains(output.String(), `"count":10`) || !strings.Contains(output.String(), `"level":"DEBUG"`) {
		t.Fatal("debug repeats were not summarized", output)
	}
	output.Reset()
	r.debugEvent(e)
	var diagnostic cliLogRecord
	if err := json.Unmarshal(output.Bytes(), &diagnostic); err != nil || diagnostic.Level != "DEBUG" || !strings.Contains(diagnostic.ErrorMessage, "[redacted]") || strings.Contains(output.String(), "PRIVATE_") {
		t.Fatal("DEBUG JSON diagnostic lost its credential handling", err, output)
	}
}

func TestCLIStatusLogsPreserveNamesMatchingCredentials(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		for _, debug := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/debug=%t", format, debug), func(t *testing.T) {
				base, _ := testReporter(t)
				request := base.request
				request.ServerURL = "wss://hole.bsgm.us.kg/ws"
				request.Config.DeviceName, request.Config.Room = "nec", "zhuang"
				request.Config.Password, request.Config.Token = "hole", "zhuang"
				request.Config.TURN.Username, request.Config.TURN.Credential = "nec", "203.0.113.5"
				var output bytes.Buffer
				r := newCLIReporter(log.New(&output, "", log.Ltime), request)
				r.format, r.debug = format, debug
				r.startup()
				r.peer(core.PeerTransportSnapshot{PeerID: "nec", State: "active", PathType: "direct", RemoteAddress: "203.0.113.5:443"})
				fault := &core.Fault{Code: "service_refused", Message: "hole: connect 203.0.113.5:443 refused"}
				r.event(core.Event{Kind: "session", MappingID: "hole", Peer: "nec", State: "error", Target: "203.0.113.5:443", Error: fault})
				r.event(core.Event{Kind: "engine", State: "stopped"})
				var messages []string
				for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
					if format == "json" {
						var record cliLogRecord
						if err := json.Unmarshal([]byte(line), &record); err != nil {
							t.Fatal(err, line)
						}
						if record.Level == "DEBUG" {
							continue
						}
						if strings.Contains(line, "[redacted]") {
							t.Fatal("ordinary JSON fields were redacted", line)
						}
						messages = append(messages, record.Message)
					} else if !strings.Contains(line, " DEBUG ") {
						messages = append(messages, line)
					}
				}
				ordinary := strings.Join(messages, "\n")
				for _, want := range []string{"hole " + core.CoreVersion + " 设备 nec", "房间 zhuang，信令 wss://hole.bsgm.us.kg/ws", "nec: 已连接", "203.0.113.5:443", fault.Message, "hole 已停止"} {
					if !strings.Contains(ordinary, want) {
						t.Fatalf("ordinary logs changed %q: %s", want, ordinary)
					}
				}
				if strings.Contains(ordinary, "[redacted]") {
					t.Fatal("ordinary logs applied credential substitution", ordinary)
				}
			})
		}
	}
}
