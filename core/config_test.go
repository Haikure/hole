package core

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestServiceEndpointParsing(t *testing.T) {
	for _, tc := range []struct {
		text    string
		want    ServiceEndpoint
		wantErr string
	}{
		{text: "tcp://127.0.0.1:22", want: ServiceEndpoint{Protocol: "tcp", Addr: "127.0.0.1", Port: 22}},
		{text: "udp://[::1]:53", want: ServiceEndpoint{Protocol: "udp", Addr: "::1", Port: 53}},
		{text: "TCP://Proxy.local:7890", want: ServiceEndpoint{Protocol: "tcp", Addr: "Proxy.local", Port: 7890}},
		{text: "  tcp://127.0.0.1:22  ", want: ServiceEndpoint{Protocol: "tcp", Addr: "127.0.0.1", Port: 22}},
		{text: "127.0.0.1:22", wantErr: "protocol://host:port"},
		{text: "tcp://127.0.0.1", wantErr: "缺少端口"},
		{text: "tcp://:22", wantErr: "缺少地址"},
		{text: "tcp://127.0.0.1:0", wantErr: "端口"},
		{text: "tcp://127.0.0.1:99999", wantErr: "端口"},
		{text: "quic://127.0.0.1:443", wantErr: "只支持 tcp 或 udp"},
		{text: "tcp://[::1]:22", want: ServiceEndpoint{Protocol: "tcp", Addr: "::1", Port: 22}},
	} {
		var wrapper struct {
			Service ServiceEndpoint `yaml:"service"`
		}
		err := yaml.Unmarshal([]byte("service: \""+tc.text+"\"\n"), &wrapper)
		if tc.wantErr != "" {
			if err == nil {
				t.Fatalf("%q 应当解析失败", tc.text)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("%q 的错误信息 %q 未包含 %q", tc.text, err.Error(), tc.wantErr)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%q 解析失败：%v", tc.text, err)
		}
		if wrapper.Service != tc.want {
			t.Fatalf("%q 解析结果 %+v，期望 %+v", tc.text, wrapper.Service, tc.want)
		}
	}
}

// 旧配置格式（嵌套 addr/port 块）不再兼容，必须报错而不是静默解析出零值。
func TestServiceEndpointRejectsLegacyFormat(t *testing.T) {
	var wrapper struct {
		Service ServiceEndpoint `yaml:"service"`
	}
	legacy := "service:\n  protocol: tcp\n  addr: 127.0.0.1\n  port: 22\n"
	if err := yaml.Unmarshal([]byte(legacy), &wrapper); err == nil {
		t.Fatal("旧的嵌套 service 格式应当解析失败")
	}
}

func TestHostPortParsing(t *testing.T) {
	for _, tc := range []struct {
		text    string
		want    HostPort
		wantErr string
	}{
		{text: "127.0.0.1:22", want: HostPort{Addr: "127.0.0.1", Port: 22}},
		{text: "[::1]:15222", want: HostPort{Addr: "::1", Port: 15222}},
		{text: "localhost:22", want: HostPort{Addr: "localhost", Port: 22}},
		{text: "0.0.0.0:5222", want: HostPort{Addr: "0.0.0.0", Port: 5222}},
		{text: "127.0.0.1", wantErr: "host:port"},
		{text: ":22", wantErr: "缺少地址"},
		{text: "127.0.0.1:", wantErr: "端口"},
		{text: "127.0.0.1:0", wantErr: "端口"},
		{text: "127.0.0.1:65536", wantErr: "端口"},
	} {
		var wrapper struct {
			Endpoint HostPort `yaml:"endpoint"`
		}
		err := yaml.Unmarshal([]byte("endpoint: \""+tc.text+"\"\n"), &wrapper)
		if tc.wantErr != "" {
			if err == nil {
				t.Fatalf("%q 应当解析失败", tc.text)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("%q 的错误信息 %q 未包含 %q", tc.text, err.Error(), tc.wantErr)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%q 解析失败：%v", tc.text, err)
		}
		if wrapper.Endpoint != tc.want {
			t.Fatalf("%q 解析结果 %+v，期望 %+v", tc.text, wrapper.Endpoint, tc.want)
		}
	}
}

func validConfig() Config {
	return Config{
		Room:       "zhuang",
		Token:      "tok",
		Password:   "pass",
		DeviceName: "nec",
		Provide: []Provide{
			{ID: "ssh", Service: ServiceEndpoint{Protocol: "tcp", Addr: "127.0.0.1", Port: 22}},
		},
		Consume: []Consume{
			{ID: "ssh_taotao", Expose: HostPort{Addr: "127.0.0.1", Port: 15222}},
		},
	}
}

func TestConfigValidate(t *testing.T) {
	if err := validConfig().Validate(); err != nil {
		t.Fatalf("合法配置不应报错：%v", err)
	}

	for _, tc := range []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{"缺少 password", func(c *Config) { c.Password = "" }, "password"},
		{"expose 不是字面 IP", func(c *Config) { c.Consume[0].Expose.Addr = "localhost" }, "不是合法 IP"},
		{"provide 与 consume 同 id", func(c *Config) { c.Consume[0].ID = "ssh" }, "只能扮演一个角色"},
		{"consume 重复 id", func(c *Config) { c.Consume = append(c.Consume, c.Consume[0]) }, "重复 id"},
		{"provide 重复 id", func(c *Config) { c.Provide = append(c.Provide, c.Provide[0]) }, "重复 id"},
	} {
		cfg := validConfig()
		tc.mutate(&cfg)
		err := cfg.Validate()
		if err == nil {
			t.Fatalf("%s：应当校验失败", tc.name)
		}
		if !strings.Contains(err.Error(), tc.wantErr) {
			t.Fatalf("%s：错误信息 %q 未包含 %q", tc.name, err.Error(), tc.wantErr)
		}
	}
}

// 严格解析：consume 里的旧字段（provider/service）与任何未知字段都应报错，
// 而不是被静默忽略后配出一条"看起来配了但没生效"的隧道。
func TestParseConfigRejectsUnknownFields(t *testing.T) {
	good := `
room: r
token: t
password: p
device_name: nec
consume:
  - id: x
    expose: 127.0.0.1:1522
`
	if _, err := ParseConfig([]byte(good)); err != nil {
		t.Fatalf("新格式应能解析：%v", err)
	}

	for name, text := range map[string]string{
		"consume 带旧 provider 字段": good + "    provider: taotao\n",
		"consume 带旧 service 字段":  good + "    service: 127.0.0.1:22\n",
		"未知顶层字段":                 strings.Replace(good, "device_name: nec", "device_name: nec\nfoo: bar", 1),
	} {
		if _, err := ParseConfig([]byte(text)); err == nil {
			t.Fatalf("%s：应当解析失败", name)
		}
	}
}
