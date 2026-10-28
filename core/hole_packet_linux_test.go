//go:build linux

package core

import (
	"context"
	"net"
	"testing"
	"time"

	"golang.org/x/net/ipv4"
	"golang.org/x/sys/unix"
)

func TestHoleFixedPacketInfoIsPreserved(t *testing.T) {
	socket, err := net.ListenPacket("udp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	f := newHoleForwarder(context.Background(), socket, RelayServeConfig{MaxPairs: 1})
	defer f.Close()
	packet := f.quicPacketConn().(*holeQUICPacketConn)
	client, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	dest := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 2), Port: socket.LocalAddr().(*net.UDPAddr).Port}
	_, _ = client.WriteToUDP([]byte("ordinary QUIC"), dest)
	_ = packet.SetReadDeadline(time.Now().Add(time.Second))
	var payload [100]byte
	var control [128]byte
	messages := []ipv4.Message{{Buffers: [][]byte{payload[:]}, OOB: control[:]}}
	if n, err := packet.ReadBatch(messages, 0); n != 1 || err != nil {
		t.Fatal(n, err)
	}
	m := messages[0]
	info := holePacketControl(m.OOB[:m.NN], true)
	if len(info) == 0 {
		t.Fatal("dispatcher lost local address pktinfo")
	}
	h, _, _, err := unix.ParseOneSocketControlMessage(info)
	if err != nil || h.Type != unix.IP_PKTINFO {
		t.Fatal(h, err)
	}
	if _, _, err := packet.WriteMsgUDP(payload[:m.N], info, m.Addr.(*net.UDPAddr)); err != nil {
		t.Fatal(err)
	}
	_ = client.SetReadDeadline(time.Now().Add(time.Second))
	n, from, err := client.ReadFromUDP(payload[:])
	if err != nil || !from.IP.Equal(dest.IP) || string(payload[:n]) != "ordinary QUIC" {
		t.Fatal("reply used wrong local address", from, err)
	}
}
