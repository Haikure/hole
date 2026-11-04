package core

import (
	"context"
	"errors"
	"net/netip"
	"unsafe"

	"golang.org/x/sys/windows"
)

func (DefaultPlatform) DefaultGateway(ctx context.Context) (netip.Addr, error) {
	if err := ctx.Err(); err != nil {
		return netip.Addr{}, err
	}
	var table *windows.MibIpForwardTable2
	if err := windows.GetIpForwardTable2(windows.AF_INET, &table); err != nil {
		return netip.Addr{}, err
	}
	defer windows.FreeMibTable(unsafe.Pointer(table))
	var gateway netip.Addr
	best := ^uint64(0)
	for _, row := range table.Rows() {
		if row.DestinationPrefix.PrefixLength != 0 || row.NextHop.Family != windows.AF_INET || row.Loopback != 0 {
			continue
		}
		ip := netip.AddrFrom4((*windows.RawSockaddrInet4)(unsafe.Pointer(&row.NextHop)).Addr)
		if ip.IsUnspecified() {
			continue
		}
		iface := windows.MibIpInterfaceRow{Family: windows.AF_INET, InterfaceLuid: row.InterfaceLuid, InterfaceIndex: row.InterfaceIndex}
		if windows.GetIpInterfaceEntry(&iface) != nil || iface.Connected == 0 {
			continue
		}
		metric := uint64(row.Metric) + uint64(iface.Metric)
		if metric < best {
			best = metric
			gateway = ip
		}
	}
	if !gateway.IsValid() {
		return gateway, errors.New("系统路由没有 IPv4 默认网关")
	}
	return gateway, nil
}
