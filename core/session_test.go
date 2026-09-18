package core

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
)

func TestSessionWireAndConfig(t *testing.T) {
	id, err := newSessionID()
	if err != nil {
		t.Fatal(err)
	}
	if parsed, err := parseSessionID(id.String()); err != nil || parsed != id {
		t.Fatalf("session ID round trip: %v", err)
	}
	for _, value := range []string{"", "00000000000000000000000000000000", "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"} {
		if _, err := parseSessionID(value); err == nil {
			t.Fatalf("accepted malformed ID %q", value)
		}
	}
	want := sessionHello{Version: sessionProtocol, MappingID: "service", Protocol: "tcp", SessionID: id.String(), Generation: 7, Resume: true, ReceiveOffset: 65536, ReceiveFIN: true}
	var wire bytes.Buffer
	if err := writeSessionJSON(&wire, want); err != nil {
		t.Fatal(err)
	}
	var got sessionHello
	if err := readSessionJSON(&wire, &got); err != nil || got != want {
		t.Fatalf("handshake round trip: %+v %v", got, err)
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], maxSessionHello+1)
	if err := readSessionJSON(bytes.NewReader(header[:]), &got); err == nil {
		t.Fatal("accepted oversized handshake")
	}
	cfg, err := ParseConfig([]byte("session_timeout: 3m\n"))
	if err != nil || cfg.SessionTimeout != 3*time.Minute {
		t.Fatalf("session_timeout: %+v %v", cfg, err)
	}
	cfg, err = ParseConfig([]byte("room: r\n"))
	if err != nil || cfg.SessionTimeout != defaultSessionTimeout {
		t.Fatalf("default session_timeout: %+v %v", cfg, err)
	}
}

func TestSessionTCPAcknowledgements(t *testing.T) {
	s := &tcpSession{tx: []byte("abcdef"), changed: make(chan struct{})}
	if err := s.ackLocked(3, false); err != nil || string(s.tx) != "def" || s.txBase != 3 {
		t.Fatalf("ACK did not retain unacknowledged suffix: %v", err)
	}
	if err := s.ackLocked(2, false); err != nil || string(s.tx) != "def" {
		t.Fatal("delayed ACK moved the replay window backwards")
	}
	if err := s.ackLocked(7, false); !errors.Is(err, errSessionProtocol) {
		t.Fatal("accepted ACK beyond sent data")
	}
	if err := s.ackLocked(6, true); !errors.Is(err, errSessionProtocol) {
		t.Fatal("accepted FIN ACK before EOF")
	}
	s.txEOF = true
	if err := s.ackLocked(6, true); err != nil || !s.txFINACK || len(s.tx) != 0 {
		t.Fatalf("FIN acknowledgement: %v", err)
	}
}

func udpTestFragment(id sessionID, packet uint64, total, offset int, payload []byte) udpFragment {
	return udpFragment{key: udpPacketKey{session: id, packet: packet}, total: total, offset: offset, data: payload}
}

func TestUDPWireReassembly(t *testing.T) {
	idA, _ := newSessionID()
	idB, _ := newSessionID()
	data := make([]byte, maxUDPDatagram)
	for i := range data {
		data[i] = byte(i % 251)
	}
	now := time.Now()
	var assembler udpReassembler
	for _, id := range []sessionID{idA, idB} {
		for end := len(data); end > 0; {
			start := max(0, end-777)
			fragment := udpTestFragment(id, 1, len(data), start, data[start:end])
			got, complete, err := assembler.push(fragment, now)
			if err != nil {
				t.Fatal(err)
			}
			if complete != (start == 0) || (complete && !bytes.Equal(got, data)) {
				t.Fatal("out-of-order large datagram was corrupted")
			}
			end = start
		}
	}
	if _, complete, err := assembler.push(udpTestFragment(idA, 1, len(data), 0, data), now); err != nil || complete {
		t.Fatal("duplicate complete datagram was delivered again")
	}
	if got, complete, err := assembler.push(udpTestFragment(idA, 2, 0, 0, nil), now); err != nil || !complete || len(got) != 0 {
		t.Fatal("zero-length UDP datagram lost")
	}
	_, _, _ = assembler.push(udpTestFragment(idA, 3, 2000, 0, []byte("a")), now)
	assembler.prune(now.Add(udpFragmentTimeout))
	if assembler.bytes != 0 || len(assembler.pending) != 0 {
		t.Fatal("expired fragments retained")
	}
	for packet := uint64(10); packet < 200; packet++ {
		_, _, _ = assembler.push(udpTestFragment(idA, packet, maxUDPDatagram, 0, []byte("a")), now)
	}
	if assembler.bytes > udpAssemblyBudget || len(assembler.pending) > maxUDPAssemblies {
		t.Fatal("reassembly budget exceeded")
	}
}

func TestUDPWireParser(t *testing.T) {
	id, _ := newSessionID()
	frame := make([]byte, udpHeaderSize+3)
	copy(frame[:4], udpWireMagic[:])
	copy(frame[4:20], id[:])
	binary.BigEndian.PutUint64(frame[20:28], 1)
	binary.BigEndian.PutUint16(frame[28:30], 3)
	copy(frame[32:], "abc")
	parsed, err := parseUDPFragment(frame)
	if err != nil || parsed.key.session != id || string(parsed.data) != "abc" {
		t.Fatalf("UDP frame parse: %+v %v", parsed, err)
	}
	binary.BigEndian.PutUint16(frame[30:32], 2)
	if _, err := parseUDPFragment(frame); err == nil {
		t.Fatal("accepted overflowing fragment")
	}
}

func TestUDPWireShrinkingMTU(t *testing.T) {
	id, _ := newSessionID()
	data := bytes.Repeat([]byte("fragmented UDP"), 4000)
	frameSize, limit, count := udpInitialFrameSize, 800, 0
	var assembler udpReassembler
	var result []byte
	send := func(wire []byte) error {
		if count == 2 {
			limit = 500
		}
		if len(wire) > limit {
			return &quic.DatagramTooLargeError{MaxDatagramPayloadSize: int64(limit)}
		}
		count++
		frame, err := parseUDPFragment(wire)
		if err != nil {
			t.Fatal(err)
		}
		packet, complete, err := assembler.push(frame, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if complete {
			result = packet
		}
		return nil
	}
	if err := sendUDPFragments(context.Background(), send, id, 1, &frameSize, data); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(result, data) || frameSize != 500 {
		t.Fatal("MTU shrink corrupted fragmented datagram")
	}
	if err := sendUDPFragments(context.Background(), send, id, 2, &frameSize, make([]byte, maxUDPDatagram+1)); err == nil {
		t.Fatal("accepted oversized UDP message")
	}
	result = nil
	if err := sendUDPFragments(context.Background(), send, id, 3, &frameSize, []byte("next packet")); err != nil || string(result) != "next packet" {
		t.Fatalf("oversized message stopped later messages: %v", err)
	}
}

func TestSessionResumeIdentity(t *testing.T) {
	m := newSessionManager(time.Minute)
	defer m.close()
	id, _ := newSessionID()
	app := newMemoryTCPSocket(nil)
	s := newTCPSession(m.ctx, id, app)
	key := providerSessionKey{mapping: "service", fingerprint: "peer", id: id}
	entry := &providerTCPEntry{ready: make(chan struct{}), session: s}
	close(entry.ready)
	m.mu.Lock()
	m.tcp[key] = entry
	m.mu.Unlock()
	got, err := m.providerTCP(context.Background(), key, true, ServiceEndpoint{})
	if err != nil || got != s || got.socket != app {
		t.Fatalf("resume replaced the application socket: %v", err)
	}
	for _, wrongKey := range []providerSessionKey{
		{mapping: key.mapping, fingerprint: "different-peer", id: id},
		{mapping: "different-service", fingerprint: key.fingerprint, id: id},
	} {
		if _, err := m.providerTCP(context.Background(), wrongKey, true, ServiceEndpoint{}); err == nil {
			t.Fatal("session ID escaped its authenticated peer/mapping scope")
		}
	}
}

// These TCP protocol tests use only in-memory streams and application sockets:
// they open no OS/network socket and do not depend on IPv6 or a Worker.
type memoryTCPSocket struct {
	mu       sync.Mutex
	input    []byte
	output   []byte
	eof      bool
	closed   bool
	writeEOF bool
	changed  chan struct{}
}

func newMemoryTCPSocket(input []byte) *memoryTCPSocket {
	return &memoryTCPSocket{input: append([]byte(nil), input...), changed: make(chan struct{})}
}

func (s *memoryTCPSocket) notify() {
	close(s.changed)
	s.changed = make(chan struct{})
}

func (s *memoryTCPSocket) Read(p []byte) (int, error) {
	for {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return 0, net.ErrClosed
		}
		if len(s.input) > 0 {
			n := copy(p, s.input)
			s.input = s.input[n:]
			s.mu.Unlock()
			return n, nil
		}
		if s.eof {
			s.mu.Unlock()
			return 0, io.EOF
		}
		changed := s.changed
		s.mu.Unlock()
		<-changed
	}
}

func (s *memoryTCPSocket) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.writeEOF {
		return 0, net.ErrClosed
	}
	s.output = append(s.output, p...)
	s.notify()
	return len(p), nil
}

func (s *memoryTCPSocket) CloseWrite() error {
	s.mu.Lock()
	s.writeEOF = true
	s.notify()
	s.mu.Unlock()
	return nil
}

func (s *memoryTCPSocket) Close() error {
	s.mu.Lock()
	s.closed = true
	s.notify()
	s.mu.Unlock()
	return nil
}

func (s *memoryTCPSocket) SetWriteDeadline(time.Time) error { return nil }

func (s *memoryTCPSocket) finish() {
	s.mu.Lock()
	s.eof = true
	s.notify()
	s.mu.Unlock()
}

func (s *memoryTCPSocket) received() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.output...)
}

type memorySessionStream struct {
	net.Conn
	budget int // negative = unlimited; otherwise interrupt a partial frame
}

func (s *memorySessionStream) CancelRead(quic.StreamErrorCode)  { _ = s.Conn.Close() }
func (s *memorySessionStream) CancelWrite(quic.StreamErrorCode) { _ = s.Conn.Close() }

func (s *memorySessionStream) Write(p []byte) (int, error) {
	if s.budget < 0 {
		return s.Conn.Write(p)
	}
	n, err := s.Conn.Write(p[:min(len(p), s.budget)])
	s.budget -= n
	if s.budget == 0 {
		_ = s.Conn.Close()
		return n, io.ErrUnexpectedEOF
	}
	return n, err
}

func TestTCPResumeMemory(t *testing.T) {
	inputA := bytes.Repeat([]byte("client-to-service-"), 40000)
	inputB := bytes.Repeat([]byte("service-to-client-"), 40000)
	appA, appB := newMemoryTCPSocket(inputA), newMemoryTCPSocket(inputB)
	id, _ := newSessionID()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a, b := newTCPSession(ctx, id, appA), newTCPSession(ctx, id, appB)
	defer a.close()
	defer b.close()
	connect := func(generation uint64, budget int) <-chan error {
		t.Helper()
		left, right := net.Pipe()
		a.mu.Lock()
		aOffset, aFIN := a.rxNext, a.rxFIN
		a.mu.Unlock()
		b.mu.Lock()
		bOffset, bFIN := b.rxNext, b.rxFIN
		b.mu.Unlock()
		la, _, err := a.attach(nil, &memorySessionStream{Conn: left, budget: budget}, generation, bOffset, bFIN)
		if err != nil {
			t.Fatal(err)
		}
		lb, _, err := b.attach(nil, &memorySessionStream{Conn: right, budget: -1}, generation, aOffset, aFIN)
		if err != nil {
			t.Fatal(err)
		}
		results := make(chan error, 2)
		go func() { results <- a.runLink(ctx, la, bOffset) }()
		go func() { results <- b.runLink(ctx, lb, aOffset) }()
		return results
	}
	first := connect(1, 2048)
	for range 2 {
		select {
		case <-first:
		case <-ctx.Done():
			t.Fatal("interrupted transport did not detach")
		}
	}
	if a.isClosed() || b.isClosed() {
		t.Fatal("transport interruption closed application sockets")
	}
	second := connect(2, -1)
	appA.finish()
	appB.finish()
	for range 2 {
		select {
		case <-second:
		case <-ctx.Done():
			t.Fatalf("resume stalled: A=%d/%d B=%d/%d", len(appA.received()), len(inputB), len(appB.received()), len(inputA))
		}
	}
	if !bytes.Equal(appA.received(), inputB) || !bytes.Equal(appB.received(), inputA) {
		t.Fatal("resumed byte stream lost or duplicated data")
	}
}

func TestTCPResumeDuplicatePrefix(t *testing.T) {
	app := newMemoryTCPSocket(nil)
	id, _ := newSessionID()
	s := newTCPSession(context.Background(), id, app)
	defer s.close()
	first, _, err := s.attach(nil, nil, 1, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.deliver(first, tcpFrame{kind: tcpData, data: []byte("abc")}); err != nil {
		t.Fatal(err)
	}
	second, _, err := s.attach(nil, nil, 2, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.deliver(second, tcpFrame{kind: tcpData, data: []byte("abcdef")}); err != nil {
		t.Fatal(err)
	}
	if string(app.received()) != "abcdef" {
		t.Fatal("lost ACK caused duplicate application bytes")
	}
	if _, _, err := s.attach(nil, nil, 1, 0, false); err == nil {
		t.Fatal("old generation replaced the new path")
	}
	s.detach(first)
	s.mu.Lock()
	current := s.link
	s.mu.Unlock()
	if current != second {
		t.Fatal("old cleanup detached the resumed link")
	}
}
