//go:build !linux && !windows

package core

import (
	"context"
	"errors"
	"net/netip"
)

func (DefaultPlatform) DefaultGateway(context.Context) (netip.Addr, error) {
	return netip.Addr{}, errors.New("当前平台尚未提供系统默认网关")
}
