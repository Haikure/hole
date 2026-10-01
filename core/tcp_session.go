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
	tx             tcpReplay
	txBase         uint64
	txEOF          bool
	txFINACK       bool
	rxNext         uint64
	rxFIN          bool
	opened         bool
	closed         bool
	aborted        bool
	closedAt       time.Time
	detached       time.Time
	generation     uint64
	nextGeneration uint64
	link           *tcpSessionLink
	traffic        *tcpTraffic

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

func newTCPSession(ctx context.Context, id sessionID, socket tcpSocket, traffic ...*tcpTraffic) *tcpSession {
	var counters *tcpTraffic
	if len(traffic) > 0 {
		counters = traffic[0]
	}
	ctx, cancel := context.WithCancel(ctx)
	s := &tcpSession{
		id: id, socket: socket, ctx: ctx, cancel: cancel,
		detached: time.Now(), traffic: counters,
	}
	if counters != nil {
		s.tx.budget = counters.budget
	}
	go s.readSocket()
	return s
}

func (s *tcpSession) notifyLocked() {
	if s.changed != nil {
		close(s.changed)
		s.changed = nil
	}
}

func (s *tcpSession) waitLocked() <-chan struct{} {
	if s.changed == nil {
		s.changed = make(chan struct{})
	}
	return s.changed
}

// A QUIC stream reset with this application code ends the application session.
// Ordinary stream cancellation and path loss remain recoverable.
const tcpSessionAborted quic.StreamErrorCode = 0x485302

func (s *tcpSession) close() { s.closeWithCode(0) }
func (s *tcpSession) abort() { s.closeWithCode(tcpSessionAborted) }

func (s *tcpSession) closeWithCode(code quic.StreamErrorCode) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.aborted = code == tcpSessionAborted
	s.closedAt = time.Now()
	s.tx.clear()
	link := s.link
	// Publish the terminal reset before waking workers which would otherwise
	// race to cancel the same stream with the recoverable code zero.
	if code != 0 && link != nil && link.stream != nil {
		link.stream.CancelWrite(code)
		link.stream.CancelRead(code)
	}
	s.notifyLocked()
	s.mu.Unlock()
	if link != nil {
		if link.stream != nil {
			link.stream.CancelWrite(code)
			link.stream.CancelRead(code)
		}
		link.cancel()
	}
	s.cancel()
	_ = s.socket.Close()
}

func (s *tcpSession) cancelStream(stream tcpSessionStream) {
	s.mu.Lock()
	code := quic.StreamErrorCode(0)
	if s.aborted {
		code = tcpSessionAborted
	}
	s.mu.Unlock()
	stream.CancelWrite(code)
	stream.CancelRead(code)
}

func remoteSessionAbort(err error) bool {
	var reset *quic.StreamError
	return errors.As(err, &reset) && reset.Remote && reset.ErrorCode == tcpSessionAborted
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
		space := tcpReplayBuffer - s.tx.size
		var changed <-chan struct{}
		if space == 0 {
			changed = s.waitLocked()
		}
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
		for n > 0 && !s.closed {
			ok, available := s.tx.append(buffer[:n])
			if ok {
				break
			}
			s.mu.Unlock()
			select {
			case <-s.ctx.Done():
				return
			case <-available:
			}
			s.mu.Lock()
		}
		if s.closed {
			s.mu.Unlock()
			return
		}
		if n > 0 {
			if s.traffic != nil {
				s.traffic.read.Add(uint64(n))
			}
		}
		if errors.Is(err, io.EOF) {
			s.txEOF = true
		}
		s.notifyLocked()
		s.mu.Unlock()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				s.abort()
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
	end := s.txBase + uint64(s.tx.size)
	if offset > end || (fin && (!s.txEOF || offset != end)) {
		return fmt.Errorf("%w: acknowledgement outside replay window", errSessionProtocol)
	}
	if offset > s.txBase {
		s.tx.discard(int(offset - s.txBase))
		s.txBase = offset
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
	if remoteSessionAbort(err) || remoteSessionAbort(other) {
		s.close()
	} else if errors.Is(err, errSessionProtocol) || errors.Is(other, errSessionProtocol) {
		s.abort()
	}
	return err
}

func (s *tcpSession) sendFrames(link *tcpSessionLink, next uint64) error {
	buffer := make([]byte, 13+tcpFramePayload)
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
		end := s.txBase + uint64(s.tx.size)
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
			n := s.tx.copyAt(buffer[13:], start)
			frame = tcpFrame{kind: tcpData, offset: next, data: buffer[13 : 13+n]}
		case s.txEOF && !s.txFINACK && !finSent:
			frame = tcpFrame{kind: tcpFIN, offset: end}
		}
		var changed <-chan struct{}
		if frame.kind == 0 && !complete {
			changed = s.waitLocked()
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
		encodeTCPFrameHeader(buffer[:13], frame)
		if err := writeFull(link.stream, buffer[:13+len(frame.data)]); err != nil {
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
	buffer := make([]byte, 13+tcpFramePayload)
	for {
		frame, err := readTCPFrameInto(link.stream, buffer[:13], buffer[13:])
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
					s.abort()
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
		if n > 0 && s.traffic != nil {
			s.traffic.written.Add(uint64(n))
		}
		s.notifyLocked()
		s.mu.Unlock()
		if err != nil {
			var timeout net.Error
			if errors.As(err, &timeout) && timeout.Timeout() {
				continue
			}
			s.abort()
			return err
		}
		if n == 0 {
			s.abort()
			return io.ErrShortWrite
		}
		if n == len(data) {
			return nil
		}
	}
}
