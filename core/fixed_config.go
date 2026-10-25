package core

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Validate without resolving: the selected platform resolves on every attempt.
func validateFixedAddress(address string, listen bool) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" {
		return fmt.Errorf("需要 host:port，IPv6 使用 [地址]:端口")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 || strings.Trim(port, "0123456789") != "" {
		return fmt.Errorf("端口需要 1–65535")
	}
	ip := net.ParseIP(host)
	if listen {
		if ip == nil {
			return fmt.Errorf("监听地址需要 IPv4 / IPv6 字面地址")
		}
		return nil
	}
	if ip != nil {
		if ip.IsUnspecified() {
			return fmt.Errorf("公布地址不能是通配地址")
		}
		return nil
	}
	if strings.Trim(host, "0123456789.") == "" {
		return fmt.Errorf("IP 地址无效")
	}
	name := strings.TrimSuffix(host, ".")
	if len(name) == 0 || len(name) > 253 {
		return fmt.Errorf("域名无效")
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("域名无效")
		}
		for _, ch := range label {
			if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-') {
				return fmt.Errorf("域名无效")
			}
		}
	}
	return nil
}
