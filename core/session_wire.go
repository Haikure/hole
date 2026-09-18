package core

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

const (
	sessionProtocol         = 2
	defaultSessionTimeout   = 10 * time.Minute
	sessionHandshakeTimeout = 10 * time.Second
	tcpInitialOpenTimeout   = 15 * time.Second
	maxSessionHello         = 4096
	maxTCPSessions          = 256
	tcpReplayBuffer         = 256 * 1024
	tcpFramePayload         = 32 * 1024
)

// session_id identifies an application socket, not an IP address or a QUIC
// connection. The peer certificate and mapping ID are also part of its key.
type sessionID [16]byte

func newSessionID() (sessionID, error) {
	var id sessionID
	for id == (sessionID{}) {
		if _, err := rand.Read(id[:]); err != nil {
			return id, err
		}
	}
	return id, nil
}

func (id sessionID) String() string { return hex.EncodeToString(id[:]) }

func parseSessionID(value string) (sessionID, error) {
	var id sessionID
	if len(value) != 32 {
		return id, errors.New("invalid session_id")
	}
	_, err := hex.Decode(id[:], []byte(value))
	if err != nil || id == (sessionID{}) {
		return id, errors.New("invalid session_id")
	}
	return id, nil
}

type sessionHello struct {
	Version       int    `json:"version"`
	MappingID     string `json:"mapping_id"`
	Protocol      string `json:"protocol"`
	SessionID     string `json:"session_id"`
	Generation    uint64 `json:"generation"`
	Resume        bool   `json:"resume,omitempty"`
	ReceiveOffset uint64 `json:"receive_offset,omitempty"`
	ReceiveFIN    bool   `json:"receive_fin,omitempty"`
}

type sessionReply struct {
	Version       int    `json:"version"`
	Status        string `json:"status"`
	Message       string `json:"message,omitempty"`
	ReceiveOffset uint64 `json:"receive_offset,omitempty"`
	ReceiveFIN    bool   `json:"receive_fin,omitempty"`
}

// Length-prefixed JSON is used only for the bounded, authenticated handshake.
// Data uses binary frames; there is no unbounded line scanner on a public port.
func writeSessionJSON(w io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(data) > maxSessionHello {
		return errors.New("session handshake too large")
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(data)))
	if err := writeFull(w, header[:]); err != nil {
		return err
	}
	return writeFull(w, data)
}

func readSessionJSON(r io.Reader, value any) error {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(header[:])
	if n == 0 || n > maxSessionHello {
		return errors.New("invalid session handshake length")
	}
	data := make([]byte, int(n))
	if _, err := io.ReadFull(r, data); err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

func writeFull(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		data = data[n:]
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

const (
	tcpData byte = iota + 1
	tcpACK
	tcpACKFIN
	tcpFIN
	tcpReset
)

var errSessionProtocol = errors.New("invalid session protocol")

type tcpFrame struct {
	kind   byte
	offset uint64
	data   []byte
}

func writeTCPFrame(w io.Writer, frame tcpFrame) error {
	var header [13]byte
	header[0] = frame.kind
	binary.BigEndian.PutUint64(header[1:9], frame.offset)
	binary.BigEndian.PutUint32(header[9:13], uint32(len(frame.data)))
	if err := writeFull(w, header[:]); err != nil {
		return err
	}
	return writeFull(w, frame.data)
}

func readTCPFrame(r io.Reader) (tcpFrame, error) {
	var header [13]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return tcpFrame{}, err
	}
	f := tcpFrame{kind: header[0], offset: binary.BigEndian.Uint64(header[1:9])}
	n := binary.BigEndian.Uint32(header[9:13])
	if f.kind < tcpData || f.kind > tcpReset || n > tcpFramePayload || (f.kind != tcpData && n != 0) || (f.kind == tcpData && n == 0) {
		return f, fmt.Errorf("%w: frame header", errSessionProtocol)
	}
	f.data = make([]byte, int(n))
	_, err := io.ReadFull(r, f.data)
	return f, err
}
