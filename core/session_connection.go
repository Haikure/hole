package core

import (
	"context"
	"github.com/quic-go/quic-go"
	"net"
)

// A legacy QUIC connection or one authorized channel of a shared peer QUIC
// connection. Closing a multiplexed channel never closes unrelated mappings.
type sessionConnection interface {
	Context() context.Context
	OpenStreamSync(context.Context) (*quic.Stream, error)
	SendDatagram([]byte) error
	ReceiveDatagram(context.Context) ([]byte, error)
	CloseWithError(quic.ApplicationErrorCode, string) error
	ConnectionState() quic.ConnectionState
	RemoteAddr() net.Addr
	LocalAddr() net.Addr
}
type sessionPacketReceiver interface {
	ReceiveSessionPacket(context.Context) (sessionID, []byte, error)
}
