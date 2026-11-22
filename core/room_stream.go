package core

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/apernet/quic-go"
)

const muxRoom byte = 3
const roomQueueBytes = 512 * 1024

func writeRoomJSON(w io.Writer, frame roomFrame) error {
	data, err := json.Marshal(frame)
	if err != nil || len(data) > maxRoomFrame {
		return errors.New("room frame exceeds limit")
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(data)))
	if err := writeFull(w, header[:]); err != nil {
		return err
	}
	return writeFull(w, data)
}

func readRoomJSON(r io.Reader, frame *roomFrame) error {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(header[:])
	if n == 0 || n > maxRoomFrame {
		return errors.New("invalid room frame length")
	}
	data := make([]byte, int(n))
	if _, err := io.ReadFull(r, data); err != nil {
		return err
	}
	return json.Unmarshal(data, frame)
}

func roomFrameRevision(frame roomFrame) uint64 {
	data, err := base64.StdEncoding.DecodeString(frame.Body)
	if err != nil {
		return 0
	}
	var revision struct {
		Node  uint64 `json:"revision,string"`
		State uint64 `json:"state_revision,string"`
	}
	if json.Unmarshal(data, &revision) != nil {
		return 0
	}
	return max(revision.Node, revision.State)
}

func (p *muxPeer) roomLink() roomLink {
	return roomLink{PeerID: p.ready.PeerDevice, RuntimeID: p.ready.PeerRuntimeID, TransportID: p.ready.TransportID, Generation: p.ready.TransportGeneration}
}

func (p *muxPeer) sendRoomFrame(frame roomFrame) bool {
	if !p.roomEnabled || p.ctx.Err() != nil || !frame.validSize() {
		return false
	}
	key := frame.ID
	if frame.Kind == "node" {
		key = "node/" + frame.Origin + "/" + frame.Runtime
	}
	if frame.Origin == "$worker" && frame.Target == "" {
		key = "directory"
	}
	data, _ := json.Marshal(frame)
	p.roomMu.Lock()
	if previous, ok := p.roomPending[key]; ok {
		if key != frame.ID && roomFrameRevision(frame) <= roomFrameRevision(previous) {
			p.roomMu.Unlock()
			return true
		}
		old, _ := json.Marshal(previous)
		if p.roomBytes-len(old)+len(data) > roomQueueBytes {
			p.roomMu.Unlock()
			return false
		}
		p.roomBytes -= len(old)
	} else {
		if len(p.roomPending) >= 64 || p.roomBytes+len(data) > roomQueueBytes {
			p.roomMu.Unlock()
			return false
		}
		p.roomOrder = append(p.roomOrder, key)
	}
	p.roomPending[key] = frame
	p.roomBytes += len(data)
	p.roomMu.Unlock()
	select {
	case p.roomNotify <- struct{}{}:
	default:
	}
	return true
}

func (p *muxPeer) nextRoomFrame() (roomFrame, bool) {
	p.roomMu.Lock()
	defer p.roomMu.Unlock()
	if len(p.roomOrder) == 0 {
		return roomFrame{}, false
	}
	key := p.roomOrder[0]
	p.roomOrder = p.roomOrder[1:]
	frame := p.roomPending[key]
	delete(p.roomPending, key)
	data, _ := json.Marshal(frame)
	p.roomBytes -= len(data)
	return frame, true
}

func (p *muxPeer) writeRoom() {
	stream, err := p.conn.OpenStreamSync(p.ctx)
	if err != nil {
		p.stop()
		return
	}
	defer stream.CancelRead(0)
	defer stream.CancelWrite(0)
	defer p.stop()
	_ = stream.SetWriteDeadline(time.Now().Add(sessionHandshakeTimeout))
	if writeMuxPrefix(stream, muxRoom, 0) != nil {
		return
	}
	for {
		frame, ready := p.nextRoomFrame()
		if !ready {
			select {
			case <-p.ctx.Done():
				return
			case <-p.roomNotify:
			}
			continue
		}
		_ = stream.SetWriteDeadline(time.Now().Add(sessionHandshakeTimeout))
		if writeRoomJSON(stream, frame) != nil {
			return
		}
	}
}

func (p *muxPeer) readRoom(stream *quic.Stream) {
	if !p.roomReceiving.CompareAndSwap(false, true) {
		stream.CancelRead(1)
		stream.CancelWrite(1)
		return
	}
	defer stream.CancelRead(0)
	defer stream.CancelWrite(0)
	defer p.stop()
	_ = stream.SetDeadline(time.Time{})
	window, count := time.Now(), 0
	for {
		var frame roomFrame
		if readRoomJSON(stream, &frame) != nil {
			return
		}
		if time.Since(window) >= 10*time.Second {
			window, count = time.Now(), 0
		}
		count++
		if count > 512 {
			return
		}
		p.owner.coordinator.room.accept(frame, p)
	}
}
