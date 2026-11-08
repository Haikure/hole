package core

import (
	"github.com/apernet/quic-go/congestion"
	"hole/core/internal/congestion/bbr"
	"net"
)

// Install once after TLS succeeds, before any application traffic is queued.
// QUIC's own starting MTU matters for ICE/TURN paths capped at 1200 bytes.
type bbrConnection interface {
	RemoteAddr() net.Addr
	InitialPacketSize() congestion.ByteCount
	SetCongestionControl(congestion.CongestionControl)
}

func useBBR(conn bbrConnection) {
	size := bbr.GetInitialPacketSize(conn.RemoteAddr())
	if initial := conn.InitialPacketSize(); initial > 0 {
		size = min(size, initial)
	}
	conn.SetCongestionControl(bbr.NewBbrSender(bbr.DefaultClock{}, size, bbr.ProfileStandard))
}
