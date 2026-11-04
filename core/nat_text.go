package core

// NATDescription is shared by CLI and JSON hosts that prefer preformatted text.
func NATDescription(s NATSnapshot) string {
	s = s.Current()
	status := map[string]string{"probing": "探测中", "insufficient": "信息不足", "expired": "已过期", "not_applicable": "IPv4 不适用"}[s.Status]
	if s.Status == "" {
		return "对端未上报"
	}
	typ := map[string]string{"open": "公网直连", "full_cone": "完全锥型", "restricted_cone": "地址受限锥型", "port_restricted_cone": "端口受限锥型", "symmetric": "对称型", "unknown": "未知"}[s.Type]
	predict := map[string]string{"stable": "映射稳定", "incremental": "可能可预测（递增）", "decremental": "可能可预测（递减）", "irregular": "未观察到规律", "unknown": "可预测性未知"}[s.Predictability]
	if status != "" {
		return status + " · " + typ + " · " + predict
	}
	return typ + " · " + predict
}
