package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
)

// tcpSession outlives its QUIC streams. Bytes read from the application remain
// in tx until the other endpoint acknowledges writing them to its TCP socket.
// On reconnect, the receive offsets are exchanged and only the unacknowledged
// suffix is replayed. Source reads stop at tcpReplayBuffer (TCP backpressure).
type tcpSession struct {
	id     sessionID
	socket tcpSocket
	ctx    context.Context
	cancel context.CancelFunc

	mu             sync.Mutex
	changed        chan struct{}
	tx             []byte
	txBase         uint64
	txEOF          bool
	txFINACK       bool
	rxNext         uint64
	rxFIN          bool
	opened         bool
	closed         bool
	closedAt       time.Time
	detached       time.Time
	generation     uint64
	nextGeneration uint64
	link           *tcpSessionLink

	// A canceled old stream may still be finishing a socket write. Serialize
	// delivery across generations, and use short write deadlines to interrupt it.
	rxMu sync.Mutex
}

type tcpSessionLink struct {
	conn   sessionConnection
	stream tcpSessionStream
	ctx    context.Context
	cancel context.CancelFunc
}

type tcpSocket interface {
	io.ReadWriteCloser
	CloseWrite() error
	SetWriteDeadline(time.Time) error
}

type tcpSessionStream interface {
	io.ReadWriter
	CancelRead(quic.StreamErrorCode)
	CancelWrite(quic.StreamErrorCode)
}

func newTCPSession(ctx context.Context, id sessionID, socket tcpSocket) *tcpSession {
	ctx, cancel := context.WithCancel(ctx)
	s := &tcpSession{
		id: id, socket: socket, ctx: ctx, cancel: cancel,
		changed: make(chan struct{}), detached: time.Now(),
	}
	go s.readSocket()
	return s
}

func (s *tcpSession) notifyLocked() {
	close(s.changed)
	s.changed = make(chan struct{})
}

func (s *tcpSession) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.closedAt = time.Now()
	s.tx = nil
	link := s.link
	s.notifyLocked()
	s.mu.Unlock()
	s.cancel()
	if link != nil {
		link.cancel()
	}
	_ = s.socket.Close()
}

func (s *tcpSession) expired(now time.Time, timeout time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return now.Sub(s.closedAt) >= timeout
	}
	return s.link == nil && now.Sub(s.detached) >= timeout
}

func (s *tcpSession) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *tcpSession) wasOpened() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.opened
}

func (s *tcpSession) readSocket() {
	buffer := make([]byte, tcpFramePayload)
	for {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return
		}
		space := tcpReplayBuffer - len(s.tx)
		changed := s.changed
		s.mu.Unlock()
		if space == 0 {
			select {
			case <-s.ctx.Done():
				return
			case <-changed:
				continue
			}
		}
		n, err := s.socket.Read(buffer[:min(space, len(buffer))])
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return
		}
		if n > 0 {
			s.tx = append(s.tx, buffer[:n]...)
		}
		if errors.Is(err, io.EOF) {
			s.txEOF = true
		}
		s.notifyLocked()
		s.mu.Unlock()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				s.close()
			}
			return
		}
	}
}

func (s *tcpSession) hello(mappingID string) sessionHello {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextGeneration++
	return sessionHello{
		Version: sessionProtocol, MappingID: mappingID, Protocol: "tcp",
		SessionID: s.id.String(), Generation: s.nextGeneration,
		Resume: s.opened, ReceiveOffset: s.rxNext, ReceiveFIN: s.rxFIN,
	}
}

// ackLocked is monotonic: delayed acknowledgements from an old path are benign.
func (s *tcpSession) ackLocked(offset uint64, fin bool) error {
	end := s.txBase + uint64(len(s.tx))
	if offset > end || (fin && (!s.txEOF || offset != end)) {
		return fmt.Errorf("%w: acknowledgement outside replay window", errSessionProtocol)
	}
	if offset > s.txBase {
		s.tx = s.tx[int(offset-s.txBase):]
		s.txBase = offset
		if len(s.tx) == 0 {
			s.tx = nil
		}
	}
	if fin {
		s.txFINACK = true
	}
	s.notifyLocked()
	return nil
}

func (s *tcpSession) attach(conn sessionConnection, stream tcpSessionStream, generation, ack uint64, fin bool) (*tcpSessionLink, sessionReply, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, sessionReply{}, errors.New("session_closed")
	}
	if generation == 0 || generation <= s.generation {
		s.mu.Unlock()
		return nil, sessionReply{}, errors.New("stale_generation")
	}
	if err := s.ackLocked(ack, fin); err != nil {
		s.mu.Unlock()
		return nil, sessionReply{}, err
	}
	ctx, cancel := context.WithCancel(s.ctx)
	link := &tcpSessionLink{conn: conn, stream: stream, ctx: ctx, cancel: cancel}
	old := s.link
	s.link = link
	s.generation = generation
	s.opened = true
	reply := sessionReply{Version: sessionProtocol, Status: "ok", ReceiveOffset: s.rxNext, ReceiveFIN: s.rxFIN}
	s.notifyLocked()
	s.mu.Unlock()
	if old != nil {
		old.cancel()
	}
	return link, reply, nil
}

func (s *tcpSession) detach(link *tcpSessionLink) {
	link.cancel()
	s.mu.Lock()
	if s.link == link {
		s.link = nil
		s.detached = time.Now()
		s.notifyLocked()
	}
	s.mu.Unlock()
}

func (s *tcpSession) runLink(ctx context.Context, link *tcpSessionLink, offset uint64) error {
	stopParent := context.AfterFunc(ctx, link.cancel)
	stopStream := context.AfterFunc(link.ctx, func() {
		link.stream.CancelRead(0)
		link.stream.CancelWrite(0)
	})
	defer stopParent()
	defer stopStream()
	defer s.detach(link)

	results := make(chan error, 2)
	go func() { results <- s.sendFrames(link, offset) }()
	go func() { results <- s.receiveFrames(link) }()
	err := <-results
	link.cancel()
	// Explicit cancellation also covers the race with stopping AfterFunc below.
	link.stream.CancelRead(0)
	link.stream.CancelWrite(0)
	other := <-results
	if errors.Is(err, errSessionProtocol) || errors.Is(other, errSessionProtocol) {
		s.close()
	}
	return err
}

func (s *tcpSession) sendFrames(link *tcpSessionLink, next uint64) error {
	var lastACK uint64
	var lastFIN, ackSent, finSent bool
	for {
		if err := link.ctx.Err(); err != nil {
			return err
		}
		s.mu.Lock()
		if s.closed || s.link != link {
			s.mu.Unlock()
			return context.Canceled
		}
		ack, rxFIN := s.rxNext, s.rxFIN
		next = max(next, s.txBase)
		end := s.txBase + uint64(len(s.tx))
		changed := s.changed
		complete := s.txEOF && s.txFINACK && s.rxFIN
		var frame tcpFrame
		switch {
		case !ackSent || ack != lastACK || rxFIN != lastFIN:
			frame = tcpFrame{kind: tcpACK, offset: ack}
			if rxFIN {
				frame.kind = tcpACKFIN
			}
		case next < end:
			start := int(next - s.txBase)
			frame = tcpFrame{kind: tcpData, offset: next, data: append([]byte(nil), s.tx[start:min(start+tcpFramePayload, len(s.tx))]...)}
		case s.txEOF && !s.txFINACK && !finSent:
			frame = tcpFrame{kind: tcpFIN, offset: end}
		}
		s.mu.Unlock()
		if frame.kind == 0 {
			if complete {
				s.close()
				return nil
			}
			select {
			case <-link.ctx.Done():
				return link.ctx.Err()
			case <-changed:
				continue
			}
		}
		if err := writeTCPFrame(link.stream, frame); err != nil {
			return err
		}
		switch frame.kind {
		case tcpACK, tcpACKFIN:
			lastACK, lastFIN, ackSent = ack, rxFIN, true
		case tcpData:
			next += uint64(len(frame.data))
		case tcpFIN:
			finSent = true
		}
	}
}

func (s *tcpSession) receiveFrames(link *tcpSessionLink) error {
	for {
		frame, err := readTCPFrame(link.stream)
		if err != nil {
			return err
		}
		switch frame.kind {
		case tcpACK, tcpACKFIN:
			s.mu.Lock()
			if s.closed {
				s.mu.Unlock()
				return context.Canceled
			}
			err = s.ackLocked(frame.offset, frame.kind == tcpACKFIN)
			s.mu.Unlock()
		case tcpData, tcpFIN:
			err = s.deliver(link, frame)
		case tcpReset:
			s.close()
			return io.EOF
		}
		if err != nil {
			return err
		}
	}
}

func (s *tcpSession) deliver(link *tcpSessionLink, frame tcpFrame) error {
	s.rxMu.Lock()
	defer s.rxMu.Unlock()
	defer s.socket.SetWriteDeadline(time.Time{})
	for {
		s.mu.Lock()
		if s.closed || s.link != link || link.ctx.Err() != nil {
			s.mu.Unlock()
			return context.Canceled
		}
		next, fin := s.rxNext, s.rxFIN
		s.mu.Unlock()
		if frame.offset > next {
			return fmt.Errorf("%w: receive offset gap", errSessionProtocol)
		}
		if frame.kind == tcpFIN {
			if frame.offset != next {
				return fmt.Errorf("%w: misplaced FIN", errSessionProtocol)
			}
			if !fin {
				if err := s.socket.CloseWrite(); err != nil {
					s.close()
					return err
				}
			}
			s.mu.Lock()
			s.rxFIN = true
			s.notifyLocked()
			s.mu.Unlock()
			return nil
		}
		// Duplicate prefixes are possible when the old ACK or handshake was lost.
		if next-frame.offset >= uint64(len(frame.data)) {
			s.mu.Lock()
			s.notifyLocked()
			s.mu.Unlock()
			return nil
		}
		if fin {
			return fmt.Errorf("%w: data after FIN", errSessionProtocol)
		}
		data := frame.data[int(next-frame.offset):]
		_ = s.socket.SetWriteDeadline(time.Now().Add(time.Second))
		n, err := s.socket.Write(data)
		s.mu.Lock()
		s.rxNext += uint64(n)
		s.notifyLocked()
		s.mu.Unlock()
		if err != nil {
			var timeout net.Error
			if errors.As(err, &timeout) && timeout.Timeout() {
				continue
			}
			s.close()
			return err
		}
		if n == 0 {
			s.close()
			return io.ErrShortWrite
		}
	}
}
