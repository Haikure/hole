package core

import "net"

// InterfaceSnapshot is a network-free description supplied by the host. On
// Android it comes from ConnectivityManager/LinkProperties, not netlink.
type InterfaceSnapshot struct {
	Name      string   `json:"name"`
	Up        bool     `json:"up"`
	Loopback  bool     `json:"loopback"`
	Addresses []string `json:"addresses"`
}

// globalIPv6Candidates retains the desktop interface discovery path. Explicit
// addresses take precedence and must not trigger interface enumeration.
func globalIPv6Candidates(cfg Config, port int) ([]Candidate, error) {
	if len(cfg.CandidateAddresses) > 0 {
		return CandidatesFromInterfaces(cfg, port, nil)
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	wanted := stringSet(cfg.CandidateInterfaces)
	snapshots := make([]InterfaceSnapshot, 0, len(interfaces))
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if len(wanted) > 0 && !wanted[iface.Name] || len(wanted) == 0 && isDefaultExcludedInterface(iface.Name) {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			discardLog("读取接口 %s 地址失败：%v", iface.Name, err)
			continue
		}
		snapshot := InterfaceSnapshot{Name: iface.Name, Up: true}
		for _, raw := range addrs {
			if ip := addrIP(raw); ip != nil {
				snapshot.Addresses = append(snapshot.Addresses, ip.String())
			}
		}
		snapshots = append(snapshots, snapshot)
	}
	return CandidatesFromInterfaces(cfg, port, snapshots)
}
