package mobile

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
	"hole/core"
)

func netEndpoint(host string, port int) string { return net.JoinHostPort(host, strconv.Itoa(port)) }

// The CLI format contains effective entries plus its required signaling URL.
// Old URL-less documents remain importable as stopped Android drafts.
type exchangeProvide struct {
	ID      string `json:"id" yaml:"id"`
	Service string `json:"service" yaml:"service"`
}
type exchangeConsume struct {
	ID     string `json:"id" yaml:"id"`
	Expose string `json:"expose" yaml:"expose"`
}
type exchangeConfig struct {
	ServerURL           string               `json:"server_url" yaml:"server_url"`
	Transport           core.TransportConfig `json:"transport" yaml:"transport"`
	ICE                 core.ICEConfig       `json:"ice" yaml:"ice"`
	TURN                core.TURNConfig      `json:"turn" yaml:"turn"`
	Room                string               `json:"room" yaml:"room"`
	Password            string               `json:"password" yaml:"password,omitempty"`
	Token               string               `json:"token" yaml:"token,omitempty"`
	DeviceName          string               `json:"device_name" yaml:"device_name"`
	SessionTimeout      string               `json:"session_timeout" yaml:"session_timeout"`
	CandidateInterfaces []string             `json:"candidate_interfaces" yaml:"candidate_interfaces,omitempty"`
	CandidateAddresses  []string             `json:"candidate_addresses" yaml:"candidate_addresses,omitempty"`
	Provide             []exchangeProvide    `json:"provide" yaml:"provide"`
	Consume             []exchangeConsume    `json:"consume" yaml:"consume"`
}

func exchangeDocument(text string) (exchangeConfig, error) {
	if len(text) > 128*1024 {
		return exchangeConfig{}, errors.New("配置文件超过 128 KiB")
	}
	cfg, err := core.ParseConfig([]byte(text))
	if err != nil {
		return exchangeConfig{}, errors.New("CLI YAML/JSON 格式错误，请检查字段、协议与端口")
	}
	cfg = cfg.Normalized()
	cfg.ServerURL = strings.TrimSpace(cfg.ServerURL)
	validation := cfg
	if validation.TURN.Mode == "manual" {
		if validation.TURN.Credential == "" {
			validation.TURN.Credential = "pending"
		}
		if validation.TURN.Username == "" {
			validation.TURN.Username = "pending"
		}
	}
	// Redacted documents are intentionally importable while stopped.
	if validation.Room == "" {
		validation.Room = "pending"
	}
	if validation.Password == "" {
		validation.Password = "pending"
	}
	if validation.Token == "" {
		validation.Token = "pending"
	}
	if validation.DeviceName == "" {
		validation.DeviceName = "pending"
	}
	if err := validation.Validate(); err != nil {
		return exchangeConfig{}, errors.New("CLI 配置校验失败，请检查映射 ID、地址、协议和会话期限")
	}
	if cfg.ServerURL != "" {
		if err := (core.Request{ServerURL: cfg.ServerURL, Config: validation}).Validate(); err != nil {
			return exchangeConfig{}, errors.New("CLI server_url 需要有效的信令服务器地址，并与连接方式匹配")
		}
	}
	doc := exchangeConfig{ServerURL: cfg.ServerURL, Transport: cfg.Transport, ICE: cfg.ICE, TURN: cfg.TURN,
		Room: cfg.Room, Password: cfg.Password, Token: cfg.Token, DeviceName: cfg.DeviceName,
		SessionTimeout: cfg.SessionTimeout.String(), CandidateInterfaces: append([]string{}, cfg.CandidateInterfaces...),
		CandidateAddresses: append([]string{}, cfg.CandidateAddresses...), Provide: []exchangeProvide{}, Consume: []exchangeConsume{},
	}
	for _, entry := range cfg.Provide {
		doc.Provide = append(doc.Provide, exchangeProvide{entry.ID, fmt.Sprintf("%s://%s", entry.Service.Protocol, netEndpoint(entry.Service.Addr, entry.Service.Port))})
	}
	for _, entry := range cfg.Consume {
		doc.Consume = append(doc.Consume, exchangeConsume{entry.ID, netEndpoint(entry.Expose.Addr, entry.Expose.Port)})
	}
	return doc, nil
}

func DecodeCLIConfig(text string) (string, error) {
	doc, err := exchangeDocument(text)
	if err != nil {
		return "", bridgeError(err)
	}
	data, err := json.Marshal(doc)
	return string(data), bridgeError(err)
}

func EncodeCLIConfig(configJSON string, includeSecrets bool) (string, error) {
	doc, err := exchangeDocument(configJSON)
	if err != nil {
		return "", bridgeError(err)
	}
	if !includeSecrets {
		doc.Password, doc.Token = "", ""
		doc.TURN.Credential = ""
	}
	data, err := yaml.Marshal(doc)
	return string(data), bridgeError(err)
}
