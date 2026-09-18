package core

import (
	"bytes"
	"errors"
	"fmt"
	"gopkg.in/yaml.v3"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	// CLI documents carry the signaling URL; hosts pass it to Request.ServerURL.
	ServerURL           string          `yaml:"server_url"`
	Room                string          `yaml:"room"`
	Token               string          `yaml:"token"`
	Password            string          `yaml:"password"`
	DeviceName          string          `yaml:"device_name"`
	CandidateInterfaces []string        `yaml:"candidate_interfaces"`
	CandidateAddresses  []string        `yaml:"candidate_addresses"`
	Provide             []Provide       `yaml:"provide"`
	Consume             []Consume       `yaml:"consume"`
	SessionTimeout      time.Duration   `yaml:"session_timeout"`
	Transport           TransportConfig `yaml:"transport"`
	ICE                 ICEConfig       `yaml:"ice"`
	TURN                TURNConfig      `yaml:"turn"`
}

// Provide 声明本机向房间内其他设备提供的服务。
type Provide struct {
	ID      string          `yaml:"id" json:"id"`
	Service ServiceEndpoint `yaml:"service" json:"service"`
}

// Consume 声明本机要使用的服务：id 对应某台设备 provide 的映射（id 在房间内
// 唯一，提供者由信令配对得出），expose 是本机的监听地址。
type Consume struct {
	ID     string   `yaml:"id" json:"id"`
	Expose HostPort `yaml:"expose" json:"expose"`
}

// ServiceEndpoint 形如 tcp://127.0.0.1:22，必须显式写出协议。
type ServiceEndpoint struct {
	Protocol string `json:"protocol"`
	Addr     string `json:"addr"`
	Port     int    `json:"port"`
}

func (e *ServiceEndpoint) UnmarshalYAML(node *yaml.Node) error {
	var text string
	if err := node.Decode(&text); err != nil {
		return errors.New("service 必须写成 protocol://host:port 形式，例如 tcp://127.0.0.1:22")
	}
	protocol, addrPort, ok := strings.Cut(strings.TrimSpace(text), "://")
	if !ok || addrPort == "" {
		return fmt.Errorf("service %q 不是合法的 protocol://host:port 形式", text)
	}
	protocol = strings.ToLower(protocol)
	if protocol != "tcp" && protocol != "udp" {
		return fmt.Errorf("service %q 的协议 %q 只支持 tcp 或 udp", text, protocol)
	}
	addr, portText, err := net.SplitHostPort(addrPort)
	if err != nil {
		return fmt.Errorf("service %q 缺少端口（IPv6 地址要写成 [::1]:22 形式）", text)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("service %q 的端口 %q 无效", text, portText)
	}
	if addr == "" {
		return fmt.Errorf("service %q 缺少地址", text)
	}
	e.Protocol, e.Addr, e.Port = protocol, addr, port
	return nil
}

// HostPort 形如 127.0.0.1:22，IPv6 写成 [::1]:22。
type HostPort struct {
	Addr string `json:"addr"`
	Port int    `json:"port"`
}

func (e *HostPort) UnmarshalYAML(node *yaml.Node) error {
	var text string
	if err := node.Decode(&text); err != nil {
		return errors.New("地址必须写成 host:port 形式")
	}
	addr, portText, err := net.SplitHostPort(strings.TrimSpace(text))
	if err != nil {
		return fmt.Errorf("%q 不是合法的 host:port 形式（IPv6 地址要写成 [::1]:22 形式）", text)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("%q 的端口 %q 无效", text, portText)
	}
	if addr == "" {
		return fmt.Errorf("%q 缺少地址", text)
	}
	e.Addr, e.Port = addr, port
	return nil
}

// ParseConfig 严格解析配置：未知字段直接报错（带行号），避免拼写错误或
// 旧格式的残留字段被静默忽略后出现"看起来配了但没生效"的隧道。
func ParseConfig(data []byte) (Config, error) {
	var cfg Config
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Config{}, errors.New("配置必须只包含一个 YAML/JSON 文档")
	}
	// join 消息里 provide/consume 必须以数组出现（空列表也要发 []），
	// 否则信令服务器会把缺字段当成非法 join 拒掉。
	if cfg.Provide == nil {
		cfg.Provide = []Provide{}
	}
	if cfg.Consume == nil {
		cfg.Consume = []Consume{}
	}
	if cfg.SessionTimeout == 0 {
		cfg.SessionTimeout = defaultSessionTimeout
	}
	return cfg.normalized(), nil
}

// validate 检查跨字段的约束。单字段的格式（protocol://host:port、host:port、
// 端口范围）已在 YAML 反序列化时由自定义 UnmarshalYAML 把关并带出出错行号。
func (cfg Config) Validate() error {
	if err := cfg.validateTransport(); err != nil {
		return err
	}
	if cfg.SessionTimeout < 0 {
		return errors.New("session_timeout 必须为正时长，例如 10m")
	}
	if cfg.Room == "" || cfg.Token == "" || cfg.Password == "" || cfg.DeviceName == "" {
		return errors.New("room、token、password 和 device_name 都是必填项")
	}
	for _, value := range cfg.CandidateAddresses {
		ip := net.ParseIP(strings.TrimSpace(value))
		if ip == nil || !isPublicIPv6(ip) {
			return fmt.Errorf("候选地址 %q 需要公网 IPv6 字面地址", value)
		}
	}
	provided := make(map[string]bool, len(cfg.Provide))
	for _, p := range cfg.Provide {
		if p.ID == "" {
			return errors.New("provide 项缺少 id")
		}
		if provided[p.ID] {
			return fmt.Errorf("provide 中存在重复 id %q", p.ID)
		}
		provided[p.ID] = true
		if p.Service.Protocol != "tcp" && p.Service.Protocol != "udp" {
			return fmt.Errorf("provide %q 的协议只支持 tcp 或 udp", p.ID)
		}
		if p.Service.Addr == "" || p.Service.Port < 1 || p.Service.Port > 65535 {
			return fmt.Errorf("provide %q 的服务地址或端口无效", p.ID)
		}
	}
	consumed := make(map[string]bool, len(cfg.Consume))
	for _, c := range cfg.Consume {
		if c.ID == "" {
			return errors.New("consume 项缺少 id")
		}
		if consumed[c.ID] {
			return fmt.Errorf("consume 中存在重复 id %q", c.ID)
		}
		consumed[c.ID] = true
		if c.Expose.Port < 1 || c.Expose.Port > 65535 {
			return fmt.Errorf("consume %q 的端口无效", c.ID)
		}
		// expose.addr 必须是字面 IP：解析失败会退化成 nil，
		// 而 nil 会让监听器绑到所有接口，把本该只监听 127.0.0.1 的服务暴露出去。
		if net.ParseIP(c.Expose.Addr) == nil {
			return fmt.Errorf("consume %q 的 expose %q 不是合法 IP 地址（需要写字面地址，例如 0.0.0.0 或 127.0.0.1）", c.ID, c.Expose.Addr)
		}
		if provided[c.ID] {
			return fmt.Errorf("id %q 不能同时出现在 provide 和 consume 里：同一设备对同一映射只能扮演一个角色", c.ID)
		}
	}
	return nil
}

func (cfg Config) normalized() Config {
	cfg.normalizeTransport()
	cfg.CandidateInterfaces = append([]string(nil), cfg.CandidateInterfaces...)
	cfg.CandidateAddresses = append([]string(nil), cfg.CandidateAddresses...)
	cfg.Provide = append([]Provide{}, cfg.Provide...)
	cfg.Consume = append([]Consume{}, cfg.Consume...)
	if cfg.SessionTimeout == 0 {
		cfg.SessionTimeout = defaultSessionTimeout
	}
	return cfg
}

// Normalized returns a detached effective configuration with migration defaults.
func (cfg Config) Normalized() Config { return cfg.normalized() }
