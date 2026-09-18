package desktop

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
	"hole/core"
)

// This is the portable CLI document, not GUI storage or a runtime Request.
// Its small envelope mirrors mobile's existing exchange contract while core
// remains the sole configuration parser/validator. Cross-facade tests enforce
// parity without changing the existing mobile API or adding a production
// desktop -> mobile dependency (and its Android socket adapters).
type portableProvide struct {
	ID      string `json:"id" yaml:"id"`
	Service string `json:"service" yaml:"service"`
}
type portableConsume struct {
	ID     string `json:"id" yaml:"id"`
	Expose string `json:"expose" yaml:"expose"`
}
type portableConfig struct {
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
	Provide             []portableProvide    `json:"provide" yaml:"provide"`
	Consume             []portableConsume    `json:"consume" yaml:"consume"`
}

func documentFault() *rpcError {
	return rpcFault(invalidParams, "invalid_document", "配置文档无效，请检查格式、大小、字段、服务器与映射")
}

func portableDocument(data []byte) (portableConfig, *rpcError) {
	if len(data) > MaxConfigBytes {
		return portableConfig{}, documentFault()
	}
	cfg, err := core.ParseConfig(data)
	if err != nil {
		return portableConfig{}, documentFault()
	}
	cfg.ServerURL = strings.TrimSpace(cfg.ServerURL)
	validation := cfg
	// Like Android, allow a redacted or incomplete connection identity to be
	// previewed as a stopped draft. Placeholders never enter the returned data.
	for _, field := range []*string{&validation.Room, &validation.Password, &validation.Token, &validation.DeviceName} {
		if *field == "" {
			*field = "pending"
		}
	}
	if validation.TURN.Mode == "manual" {
		if validation.TURN.Username == "" {
			validation.TURN.Username = "pending"
		}
		if validation.TURN.Credential == "" {
			validation.TURN.Credential = "pending"
		}
	}
	if validation.Validate() != nil {
		return portableConfig{}, documentFault()
	}
	if cfg.ServerURL != "" && (core.Request{ServerURL: cfg.ServerURL, Config: validation}).Validate() != nil {
		return portableConfig{}, documentFault()
	}
	doc := portableConfig{
		ServerURL: cfg.ServerURL, Transport: cfg.Transport, ICE: cfg.ICE, TURN: cfg.TURN,
		Room: cfg.Room, Password: cfg.Password, Token: cfg.Token, DeviceName: cfg.DeviceName,
		SessionTimeout: cfg.SessionTimeout.String(), CandidateInterfaces: append([]string{}, cfg.CandidateInterfaces...),
		CandidateAddresses: append([]string{}, cfg.CandidateAddresses...), Provide: []portableProvide{}, Consume: []portableConsume{},
	}
	for _, p := range cfg.Provide {
		doc.Provide = append(doc.Provide, portableProvide{p.ID, fmt.Sprintf("%s://%s", p.Service.Protocol, net.JoinHostPort(p.Service.Addr, strconv.Itoa(p.Service.Port)))})
	}
	for _, c := range cfg.Consume {
		doc.Consume = append(doc.Consume, portableConsume{c.ID, net.JoinHostPort(c.Expose.Addr, strconv.Itoa(c.Expose.Port))})
	}
	return doc, nil
}

func exchangeConfig(method string, raw json.RawMessage) (any, *rpcError) {
	if method == "decode_cli_config" {
		var params struct {
			Text string `json:"text"`
		}
		if !onlyFields(raw, "text") || decodeStrict(raw, &params) != nil {
			return nil, documentFault()
		}
		doc, fault := portableDocument([]byte(params.Text))
		if fault != nil {
			return nil, fault
		}
		return struct {
			Config portableConfig `json:"config"`
		}{doc}, nil
	}
	var params struct {
		Config         json.RawMessage `json:"config"`
		IncludeSecrets bool            `json:"include_secrets"`
	}
	if !onlyFields(raw, "config", "include_secrets") || decodeStrict(raw, &params) != nil {
		return nil, documentFault()
	}
	data := strings.TrimSpace(string(params.Config))
	if len(data) == 0 || data[0] != '{' {
		return nil, documentFault()
	}
	doc, fault := portableDocument(params.Config)
	if fault != nil {
		return nil, fault
	}
	if !params.IncludeSecrets {
		doc.Password, doc.Token, doc.TURN.Credential = "", "", ""
	}
	encoded, err := yaml.Marshal(doc)
	if err != nil || len(encoded) > MaxConfigBytes {
		return nil, documentFault()
	}
	return struct {
		Text string `json:"text"`
	}{string(encoded)}, nil
}
