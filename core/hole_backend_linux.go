//go:build linux

package core

import (
	"encoding/binary"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Linux mmsghdr is msghdr followed by uint32 msg_len and ABI padding. Using
// the native msghdr keeps this layout correct on ARM32, ARM64 and amd64.
type holeMMsg struct {
	hdr    unix.Msghdr
	length uint32
}
type linuxBatch struct {
	raw                         syscall.RawConn
	ipv6                        bool
	readMsg, writeMsg           [holeBatchSize]holeMMsg
	readIO, writeIO             [holeBatchSize]unix.Iovec
	readAddr, writeAddr         [holeBatchSize]unix.RawSockaddrAny
	writeMu                     sync.Mutex
	readCallback, writeCallback func(uintptr) bool
	readCount, writeCount       int
	readErr, writeErr           error
	readSize, writeSize         int
}

func newForwardingBackend(conn net.PacketConn) forwardingBackend {
	udp, ok := conn.(*net.UDPConn)
	if !ok {
		return &portableForwarder{conn: conn}
	}
	raw, err := udp.SyscallConn()
	if err != nil {
		return &portableForwarder{conn: conn}
	}
	b := &linuxBatch{raw: raw, ipv6: udp.LocalAddr().(*net.UDPAddr).IP.To4() == nil}
	_ = raw.Control(func(fd uintptr) {
		_ = unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_PKTINFO, 1)
		_ = unix.SetsockoptInt(int(fd), unix.IPPROTO_IPV6, unix.IPV6_RECVPKTINFO, 1)
	})
	b.readCallback = b.receive
	b.writeCallback = b.send
	return b
}
func (b *linuxBatch) receive(fd uintptr) bool {
	n, _, errno := unix.Syscall6(unix.SYS_RECVMMSG, fd, uintptr(unsafe.Pointer(&b.readMsg[0])), uintptr(b.readSize), unix.MSG_DONTWAIT, 0, 0)
	if errno == unix.EAGAIN || errno == unix.EWOULDBLOCK || errno == unix.EINTR {
		return false
	}
	b.readCount = 0
	b.readErr = nil
	if errno != 0 {
		b.readErr = errno
	} else {
		b.readCount = int(n)
	}
	return true
}
func (b *linuxBatch) send(fd uintptr) bool {
	n, _, errno := unix.Syscall6(unix.SYS_SENDMMSG, fd, uintptr(unsafe.Pointer(&b.writeMsg[0])), uintptr(b.writeSize), unix.MSG_DONTWAIT, 0, 0)
	if errno == unix.EAGAIN || errno == unix.EWOULDBLOCK || errno == unix.EINTR {
		return false
	}
	b.writeCount = 0
	b.writeErr = nil
	if errno != 0 {
		b.writeErr = errno
	} else {
		b.writeCount = int(n)
	}
	return true
}
func (b *linuxBatch) Name() string { return "linux-mmsg" }
func (b *linuxBatch) ReadBatch(frames []*holeFrame) (int, error) {
	if len(frames) == 0 {
		return 0, nil
	}
	frames = frames[:min(len(frames), holeBatchSize)]
	for i, f := range frames {
		b.readIO[i] = unix.Iovec{Base: &f.data[0]}
		b.readIO[i].SetLen(len(f.data))
		b.readMsg[i] = holeMMsg{hdr: unix.Msghdr{Name: (*byte)(unsafe.Pointer(&b.readAddr[i])), Namelen: uint32(unsafe.Sizeof(b.readAddr[i])), Iov: &b.readIO[i]}}
		b.readMsg[i].hdr.SetIovlen(1)
		b.readMsg[i].hdr.Control = &f.oob[0]
		b.readMsg[i].hdr.SetControllen(len(f.oob))
	}
	b.readSize = len(frames)
	err := b.raw.Read(b.readCallback)
	count, callErr := b.readCount, b.readErr
	if err != nil {
		return 0, err
	}
	if callErr != nil {
		return 0, callErr
	}
	for i := 0; i < count; i++ {
		frames[i].n = int(b.readMsg[i].length)
		frames[i].address = holeSockaddr(&b.readAddr[i])
		frames[i].oobn = min(int(b.readMsg[i].hdr.Controllen), len(frames[i].oob))
		frames[i].flags = int(b.readMsg[i].hdr.Flags)
		if b.readMsg[i].hdr.Flags&unix.MSG_TRUNC != 0 {
			frames[i].n = 0
		}
	}
	return count, nil
}
func holeSockaddr(raw *unix.RawSockaddrAny) netip.AddrPort {
	var ip netip.Addr
	var port uint16
	switch raw.Addr.Family {
	case unix.AF_INET:
		a := (*unix.RawSockaddrInet4)(unsafe.Pointer(raw))
		ip = netip.AddrFrom4(a.Addr)
		port = binary.BigEndian.Uint16(unsafe.Slice((*byte)(unsafe.Pointer(&a.Port)), 2))
	case unix.AF_INET6:
		a := (*unix.RawSockaddrInet6)(unsafe.Pointer(raw))
		ip = netip.AddrFrom16(a.Addr)
		if a.Scope_id != 0 {
			ip = ip.WithZone(strconv.FormatUint(uint64(a.Scope_id), 10))
		}
		port = binary.BigEndian.Uint16(unsafe.Slice((*byte)(unsafe.Pointer(&a.Port)), 2))
	default:
		return netip.AddrPort{}
	}
	return canonicalHoleAddress(netip.AddrPortFrom(ip, port))
}
func (b *linuxBatch) WriteBatch(frames []*holeFrame) (int, error) {
	if len(frames) == 0 {
		return 0, nil
	}
	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	frames = frames[:min(len(frames), holeBatchSize)]
	for i, f := range frames {
		b.writeAddr[i] = unix.RawSockaddrAny{}
		addr := f.address
		if !addr.IsValid() {
			return 0, net.InvalidAddrError("UDP address required")
		}
		var length uint32
		if addr.Addr().Is4() && !b.ipv6 {
			a := (*unix.RawSockaddrInet4)(unsafe.Pointer(&b.writeAddr[i]))
			a.Family = unix.AF_INET
			a.Addr = addr.Addr().As4()
			binary.BigEndian.PutUint16(unsafe.Slice((*byte)(unsafe.Pointer(&a.Port)), 2), addr.Port())
			length = unix.SizeofSockaddrInet4
		} else {
			a := (*unix.RawSockaddrInet6)(unsafe.Pointer(&b.writeAddr[i]))
			a.Family = unix.AF_INET6
			a.Addr = addr.Addr().As16()
			binary.BigEndian.PutUint16(unsafe.Slice((*byte)(unsafe.Pointer(&a.Port)), 2), addr.Port())
			z, _ := strconv.ParseUint(addr.Addr().Zone(), 10, 32)
			a.Scope_id = uint32(z)
			length = unix.SizeofSockaddrInet6
		}
		b.writeIO[i] = unix.Iovec{Base: &f.data[0]}
		b.writeIO[i].SetLen(f.n)
		b.writeMsg[i] = holeMMsg{hdr: unix.Msghdr{Name: (*byte)(unsafe.Pointer(&b.writeAddr[i])), Namelen: length, Iov: &b.writeIO[i]}}
		b.writeMsg[i].hdr.SetIovlen(1)
		control := holePacketControl(f.oob[:f.oobn], addr.Addr().Is4())
		if len(control) > 0 {
			b.writeMsg[i].hdr.Control = &control[0]
			b.writeMsg[i].hdr.SetControllen(len(control))
		}
	}
	b.writeSize = len(frames)
	b.writeCount, b.writeErr = 0, nil
	err := b.raw.Write(b.writeCallback)
	count, callErr := b.writeCount, b.writeErr
	if err != nil {
		return count, err
	}
	return count, callErr
}

// Only the destination endpoint's packet-info is needed for Hole envelopes.
// Ordinary QUIC ancillary data is passed intact by holeQUICPacketConn.
func holePacketControl(control []byte, ipv4 bool) []byte {
	for len(control) > 0 {
		h, _, rest, err := unix.ParseOneSocketControlMessage(control)
		if err != nil {
			return nil
		}
		if (ipv4 && h.Level == unix.IPPROTO_IP && h.Type == unix.IP_PKTINFO) || (!ipv4 && h.Level == unix.IPPROTO_IPV6 && h.Type == unix.IPV6_PKTINFO) {
			return control[:int(h.Len)]
		}
		control = rest
	}
	return nil
}
