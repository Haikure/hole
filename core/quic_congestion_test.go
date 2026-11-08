package core

import (
	"net"
	"testing"

	"github.com/apernet/quic-go/congestion"
)

type bbrFixtureConnection struct {
	ip         net.IP
	initial    congestion.ByteCount
	controller congestion.CongestionControl
}

func (c *bbrFixtureConnection) RemoteAddr() net.Addr                    { return &net.UDPAddr{IP: c.ip, Port: 443} }
func (c *bbrFixtureConnection) InitialPacketSize() congestion.ByteCount { return c.initial }
func (c *bbrFixtureConnection) SetCongestionControl(cc congestion.CongestionControl) {
	c.controller = cc
}

func TestBBRRespectsQUICInitialMTUAndUsesIndependentControllers(t *testing.T) {
	for _, ip := range []net.IP{net.IPv4(192, 0, 2, 1), net.ParseIP("2001:db8::1")} {
		a, b := &bbrFixtureConnection{ip: ip, initial: 1200}, &bbrFixtureConnection{ip: ip, initial: 1200}
		useBBR(a)
		useBBR(b)
		if a.controller == nil || a.controller == b.controller {
			t.Fatal("missing or shared controller")
		}
		// Standard BBR starts with 32 datagrams. The address-family default
		// exceeds 1200; seeding from it would make this PMTU increase panic.
		if got := a.controller.GetCongestionWindow(); got != 32*1200 {
			t.Fatalf("initial window %d", got)
		}
		a.controller.SetMaxDatagramSize(1250)
		if a.controller.GetCongestionWindow() != 32*1250 || b.controller.GetCongestionWindow() != 32*1200 {
			t.Fatal("MTU growth changed another connection")
		}
	}
}
