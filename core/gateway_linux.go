//go:build linux

package core

import (
	"context"
	"encoding/binary"
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
)

func (DefaultPlatform) DefaultGateway(ctx context.Context) (netip.Addr, error) {
	if err := ctx.Err(); err != nil {
		return netip.Addr{}, err
	}
	data, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return netip.Addr{}, err
	}
	metric := uint64(^uint32(0))
	var gateway netip.Addr
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) < 8 || f[1] != "00000000" || f[7] != "00000000" {
			continue
		}
		flags, e := strconv.ParseUint(f[3], 16, 32)
		if e != nil || flags&3 != 3 {
			continue
		}
		m, e := strconv.ParseUint(f[6], 10, 32)
		if e != nil || m > metric {
			continue
		}
		x, e := strconv.ParseUint(f[2], 16, 32)
		if e != nil || x == 0 {
			continue
		}
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], uint32(x))
		gateway = netip.AddrFrom4(b)
		metric = m
	}
	if !gateway.IsValid() {
		return gateway, fmt.Errorf("系统路由没有 IPv4 默认网关")
	}
	return gateway, nil
}
