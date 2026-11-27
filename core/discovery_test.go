package core

import (
	"encoding/json"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRoomDiscoveryBeforeMappingAndAfterWithdrawal(t *testing.T) {
	url, _ := actualWorkerFixture(t)
	echo, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = echo.Close() })
	go func() {
		for {
			conn, err := echo.Accept()
			if err != nil {
				return
			}
			go func() { defer conn.Close(); _, _ = io.Copy(conn, conn) }()
		}
	}()
	host, guest := NewEngine(Options{RetryNetwork: true}), NewEngine(Options{RetryNetwork: true})
	t.Cleanup(func() { _ = guest.Close(); _ = host.Close() })
	r := iceFixtureRequest(url, "host")
	r.Config.Provide = []Provide{{ID: "minecraft.test", Service: ServiceEndpoint{"tcp", "127.0.0.1", echo.Addr().(*net.TCPAddr).Port}}}
	r.Config.Consume = nil
	if err := host.Start(r); err != nil {
		t.Fatal(err)
	}
	g := iceFixtureRequest(url, "guest")
	g.Config.Provide, g.Config.Consume = nil, nil
	if err := guest.Start(g); err != nil {
		t.Fatal(err)
	}
	s := waitSnapshot(t, guest, func(s Snapshot) bool { return s.Room != nil && s.Room.DiscoverySupported })
	if len(s.Room.Services) != 0 {
		t.Fatal("ordinary registration was advertised without opt-in")
	}
	r.Config.Provide[0].Discovery = "minecraft"
	r.Config.Provide[0].Minecraft = MinecraftMetadata{Description: "海岛世界", Version: "1.20.1", Online: 1, Max: 20}
	if err := host.ApplyConfig(r); err != nil {
		t.Fatal(err)
	}
	s = waitSnapshot(t, guest, func(s Snapshot) bool { return s.Room != nil && len(s.Room.Services) == 1 })
	if s.Room.Services[0].DeviceName != "host" || s.Room.Services[0].ID != "minecraft.test" || s.Room.Services[0].Kind != "minecraft" || len(s.PeerTransports) != 0 {
		t.Fatalf("discovery opened a transport or lost identity: %+v", s)
	}
	if s.Room.Services[0].Minecraft != r.Config.Provide[0].Minecraft {
		t.Fatal("world metadata lost", s.Room.Services)
	}
	s.Room.Services[0].ID = "modified"
	if guest.Snapshot().Room.Services[0].ID != "minecraft.test" {
		t.Fatal("snapshot aliases directory")
	}
	port := unusedTCPPort(t)
	g.Config.Consume = []Consume{{ID: "minecraft.test", Expose: HostPort{"127.0.0.1", port}}}
	if err := guest.ApplyConfig(g); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, guest, func(s Snapshot) bool {
		return len(s.Mappings) == 1 && s.Mappings[0].State == "active" && s.Mappings[0].Listener != nil && s.Mappings[0].Listener.State == "ready"
	})
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte("minecraft-discovery")); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, len("minecraft-discovery"))
	if _, err := io.ReadFull(conn, data); err != nil || string(data) != "minecraft-discovery" {
		t.Fatalf("discovered TCP service: %q, %v", data, err)
	}
	before := guest.Snapshot().PeerTransports[0].Generation
	r.Config.Provide[0].Minecraft.Online = 2
	if err := host.ApplyConfig(r); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, guest, func(s Snapshot) bool {
		return s.Room != nil && len(s.Room.Services) == 1 && s.Room.Services[0].Minecraft.Online == 2
	})
	if guest.Snapshot().PeerTransports[0].Generation != before {
		t.Fatal("player count update restarted transport")
	}
	r.Config.Provide[0].Discovery = ""
	r.Config.Provide[0].Minecraft = MinecraftMetadata{}
	if err := host.ApplyConfig(r); err != nil {
		t.Fatal(err)
	}
	waitSnapshot(t, guest, func(s Snapshot) bool { return s.Room != nil && len(s.Room.Services) == 0 })
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte("still-registered")); err != nil {
		t.Fatal(err)
	}
	data = make([]byte, len("still-registered"))
	if _, err := io.ReadFull(conn, data); err != nil || string(data) != "still-registered" {
		t.Fatalf("withdrawing discovery broke the mapping: %q, %v", data, err)
	}
}

func TestMinecraftMetadataValidationAndValueIsolation(t *testing.T) {
	good := MinecraftMetadata{Description: "海岛", Version: "1.20.1", Online: 2, Max: 20}
	for _, mutate := range []func(*MinecraftMetadata){
		func(m *MinecraftMetadata) { m.Description = strings.Repeat("界", 65) },
		func(m *MinecraftMetadata) { m.Version = "" }, func(m *MinecraftMetadata) { m.Version = strings.Repeat("a", 33) },
		func(m *MinecraftMetadata) { m.Description = "a\nb" }, func(m *MinecraftMetadata) { m.Online = -1 },
		func(m *MinecraftMetadata) { m.Max = 1000001 },
	} {
		m := good
		mutate(&m)
		cfg := validConfig()
		cfg.Provide[0].Discovery = "minecraft"
		cfg.Provide[0].Minecraft = m
		if cfg.Validate() == nil || validServiceAdvertisements([]ServiceAdvertisement{{ID: "world", Protocol: "tcp", Kind: "minecraft", Minecraft: m}}) {
			t.Fatal("invalid metadata accepted", m)
		}
	}
	if validMinecraftMetadata(good, "") {
		t.Fatal("ordinary service accepted world details")
	}
	data, err := json.Marshal(ServiceAdvertisement{ID: "world", Protocol: "tcp", Kind: "minecraft"})
	if err != nil || strings.Contains(string(data), `"minecraft":`) {
		t.Fatal("legacy directory gained metadata", string(data), err)
	}
	c := roomTestCoordinator(t, "a")
	member := c.room.directory["a"]
	member.Services = []ServiceAdvertisement{{ID: "world", Protocol: "tcp", Kind: "minecraft", Minecraft: good}}
	c.room.directory["a"] = member
	s := c.room.snapshot()
	s.Services[0].Minecraft.Online = 99
	if c.room.snapshot().Services[0].Minecraft != good {
		t.Fatal("metadata aliases directory")
	}
}

func TestServiceAdvertisementsRejectInvalidDirectory(t *testing.T) {
	for _, items := range [][]ServiceAdvertisement{
		{{ID: "", Protocol: "tcp"}}, {{ID: "world", Protocol: "http"}},
		{{ID: "world", Protocol: "tcp"}, {ID: "world", Protocol: "udp"}},
		{{ID: "bad id", Protocol: "tcp"}}, {{ID: "world", Protocol: "udp", Kind: "minecraft"}},
		{{ID: "world", Protocol: "tcp", Kind: "unknown"}}, make([]ServiceAdvertisement, 65),
	} {
		if validServiceAdvertisements(items) {
			t.Fatalf("accepted %+v", items)
		}
	}
}

func TestRoomDiscoverySnapshotIsolationAndNewRuntime(t *testing.T) {
	c := roomTestCoordinator(t, "a")
	member := c.room.directory["a"]
	member.Services = []ServiceAdvertisement{{ID: "world", Protocol: "tcp", Kind: "minecraft"}, {ID: "private", Protocol: "tcp"}}
	c.room.directory["a"] = member
	snapshot := c.room.snapshot()
	if len(snapshot.Services) != 1 {
		t.Fatal("unmarked legacy directory entries were exposed")
	}
	snapshot.Members[0].Services[0].ID = "mutated"
	if c.room.snapshot().Services[0].ID != "world" {
		t.Fatal("snapshot mutated directory")
	}
	c.room.bindPeer("a", "new-runtime", "new-fingerprint", CoreVersion)
	if len(c.room.snapshot().Services) != 0 {
		t.Fatal("new runtime inherited stale services")
	}
}
