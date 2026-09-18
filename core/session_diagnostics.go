package core

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
)

// Preserve the distinction between a local socket failure and a session-state
// rejection. Neither should be reported as an ICE path failure.
func serviceSessionFault(err error, target string, resume bool) *Fault {
	code := "service_unavailable"
	switch err.Error() {
	case "session_expired", "session_limit":
		code = err.Error()
	default:
		var network net.Error
		switch {
		case errors.Is(err, context.Canceled):
			code = "session_canceled"
		case errors.Is(err, syscall.ECONNREFUSED):
			code = "service_refused"
		case errors.As(err, &network) && network.Timeout():
			code = "service_timeout"
		}
	}
	stage := "连接提供端服务失败"
	if resume {
		stage = "恢复应用会话失败"
	}
	message := fmt.Sprintf("%s（目标 %s）：%v", stage, target, err)
	// Replies use the existing bounded JSON handshake. Keep diagnostic details
	// small enough even when a platform returns an unusually long error.
	if len(message) > 2048 {
		message = strings.ToValidUTF8(message[:2048], "") + "…"
	}
	return &Fault{Code: code, Message: message}
}

func isSessionFault(f *Fault) bool {
	return f != nil && (strings.HasPrefix(f.Code, "service_") || strings.HasPrefix(f.Code, "session_"))
}

func (a *Agent) sessionEvent(conn sessionConnection, hello sessionHello, target, state, stage string, fault *Fault) {
	if a.emit == nil {
		return
	}
	fingerprint := connectionFingerprint(conn)
	peer := ""
	a.peerFingerprintMu.Lock()
	for key, value := range a.peerFingerprints {
		if key.mappingID == hello.MappingID && value == fingerprint {
			peer = key.device
			break
		}
	}
	a.peerFingerprintMu.Unlock()
	a.emit(Event{Kind: "session", MappingID: hello.MappingID, Protocol: hello.Protocol, Peer: peer,
		SessionID: hello.SessionID, Target: target, Resume: hello.Resume, Stage: stage, State: state, Error: fault})
}

func (c *clientTunnel) sessionEvent(session *tcpSession, state, stage string, fault *Fault) {
	if c.emit == nil {
		return
	}
	c.emit(Event{Kind: "session", MappingID: c.t.id, Protocol: "tcp", Peer: c.t.peer,
		SessionID: session.id.String(), Resume: session.wasOpened(), State: state, Stage: stage, Error: fault})
}
