// SPDX-License-Identifier: GPL-3.0-only
package nat

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type MappingSnapshot struct {
	Status       string `json:"status"`
	Protocol     string `json:"protocol,omitempty"`
	Address      string `json:"address,omitempty"`
	LeaseSeconds uint32 `json:"lease_seconds,omitempty"`
	Reason       string `json:"reason,omitempty"`
}
type Mapping struct {
	Snapshot MappingSnapshot
	update   func(context.Context, uint32) (netip.AddrPort, uint32, error)
}

// Maintain renews according to the granted lease. Zero means a permanent IGD
// lease; cancellation still attempts deletion through the pinned platform.
func (m *Mapping) Maintain(ctx context.Context, changed func()) {
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_, _, _ = m.update(cleanup, 0)
	}()
	lease := m.Snapshot.LeaseSeconds
	for {
		if lease == 0 {
			<-ctx.Done()
			return
		}
		timer := time.NewTimer(max(time.Second, time.Duration(lease)*time.Second*2/3))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		renew, cancel := context.WithTimeout(ctx, 2*time.Second)
		a, next, err := m.update(renew, 300)
		cancel()
		if err != nil || a.String() != m.Snapshot.Address {
			changed()
			// The active transport retains its mapping while its replacement
			// authenticates. Its owner cancels ctx after the path is retired.
			<-ctx.Done()
			return
		}
		lease = next
	}
}

type MappingNetwork interface {
	Network
	DialSignal(context.Context, string, string) (net.Conn, error)
	DialUDP(context.Context, *net.UDPAddr) (*net.UDPConn, error)
}

func MapPort(ctx context.Context, n MappingNetwork, gateway netip.Addr, port uint16) (*Mapping, error) {
	if !gateway.Is4() || gateway.IsUnspecified() {
		return nil, errors.New("当前网络没有 IPv4 默认网关")
	}
	igdCtx, cancel := context.WithTimeout(ctx, 600*time.Millisecond)
	m, err := mapIGD(igdCtx, n, gateway, port)
	cancel()
	if err == nil {
		return m, nil
	}
	return mapPMP(ctx, n, gateway, port)
}

func pmp(ctx context.Context, n Network, gateway netip.Addr, request []byte, size int) ([]byte, error) {
	pc, err := n.ListenPacket(ctx, "udp4", "0.0.0.0:0")
	if err != nil {
		return nil, err
	}
	defer pc.Close()
	deadline := time.Now().Add(450 * time.Millisecond)
	if t, ok := ctx.Deadline(); ok && t.Before(deadline) {
		deadline = t
	}
	_ = pc.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = pc.Close() })
	defer stop()
	server := net.UDPAddrFromAddrPort(netip.AddrPortFrom(gateway, 5351))
	if _, err = pc.WriteTo(request, server); err != nil {
		return nil, err
	}
	b := make([]byte, 64)
	for {
		count, from, e := pc.ReadFrom(b)
		if e != nil {
			return nil, e
		}
		if from.String() != server.String() || count != size || b[0] != 0 || b[1] != request[1]+128 {
			continue
		}
		if binary.BigEndian.Uint16(b[2:4]) != 0 {
			return nil, errors.New("NAT-PMP 拒绝映射")
		}
		return b[:count], nil
	}
}
func mapPMP(ctx context.Context, n Network, gateway netip.Addr, port uint16) (*Mapping, error) {
	external := port
	update := func(ctx context.Context, seconds uint32) (netip.AddrPort, uint32, error) {
		public, err := pmp(ctx, n, gateway, []byte{0, 0}, 12)
		if err != nil {
			return netip.AddrPort{}, 0, err
		}
		request := make([]byte, 12)
		request[1] = 1
		binary.BigEndian.PutUint16(request[4:6], port)
		binary.BigEndian.PutUint16(request[6:8], external)
		binary.BigEndian.PutUint32(request[8:12], seconds)
		response, err := pmp(ctx, n, gateway, request, 16)
		if err != nil {
			return netip.AddrPort{}, 0, err
		}
		if binary.BigEndian.Uint16(response[8:10]) != port {
			return netip.AddrPort{}, 0, errors.New("NAT-PMP 内部端口不匹配")
		}
		ip := netip.AddrFrom4([4]byte(public[8:12]))
		external = binary.BigEndian.Uint16(response[10:12])
		return netip.AddrPortFrom(ip, external), binary.BigEndian.Uint32(response[12:16]), nil
	}
	a, lease, err := update(ctx, 300)
	if err != nil {
		return nil, err
	}
	return &Mapping{MappingSnapshot{"mapped", "nat-pmp", a.String(), lease, ""}, update}, nil
}

type igdService struct {
	Type    string `xml:"serviceType"`
	Control string `xml:"controlURL"`
}
type igdDevice struct {
	Services []igdService `xml:"serviceList>service"`
	Devices  []igdDevice  `xml:"deviceList>device"`
}

func (d igdDevice) service() (igdService, bool) {
	for _, s := range d.Services {
		if strings.Contains(s.Type, ":WANIPConnection:") || strings.Contains(s.Type, ":WANPPPConnection:") {
			return s, true
		}
	}
	for _, child := range d.Devices {
		if s, ok := child.service(); ok {
			return s, true
		}
	}
	return igdService{}, false
}

func mapIGD(ctx context.Context, n MappingNetwork, gateway netip.Addr, port uint16) (*Mapping, error) {
	pc, err := n.ListenPacket(ctx, "udp4", "0.0.0.0:0")
	if err != nil {
		return nil, err
	}
	defer pc.Close()
	deadline := time.Now().Add(250 * time.Millisecond)
	if t, ok := ctx.Deadline(); ok && t.Before(deadline) {
		deadline = t
	}
	_ = pc.SetDeadline(deadline)
	server := net.UDPAddrFromAddrPort(netip.MustParseAddrPort("239.255.255.250:1900"))
	_, err = pc.WriteTo([]byte("M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nMAN: \"ssdp:discover\"\r\nMX: 1\r\nST: urn:schemas-upnp-org:device:InternetGatewayDevice:1\r\n\r\n"), server)
	if err != nil {
		return nil, err
	}
	b := make([]byte, 8192)
	size, from, err := pc.ReadFrom(b)
	if err != nil {
		return nil, err
	}
	if addrPort(from).Addr() != gateway {
		return nil, errors.New("IGD 来源不是默认网关")
	}
	var location string
	for _, line := range strings.Split(string(b[:size]), "\r\n") {
		k, v, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(k, "location") {
			location = strings.TrimSpace(v)
		}
	}
	u, err := url.Parse(location)
	if err != nil || u.Scheme != "http" || u.Hostname() != gateway.String() || u.User != nil {
		return nil, errors.New("IGD 描述地址无效")
	}
	transport := &http.Transport{Proxy: nil, DialContext: n.DialSignal, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("IGD 不接受重定向") }}
	request, _ := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 128*1024))
	_ = response.Body.Close()
	if err != nil {
		return nil, err
	}
	var description struct {
		Device igdDevice `xml:"device"`
	}
	if err = xml.Unmarshal(data, &description); err != nil {
		return nil, err
	}
	service, ok := description.Device.service()
	if !ok {
		return nil, errors.New("网关未提供 IGD 服务")
	}
	ref, err := url.Parse(service.Control)
	if err != nil {
		return nil, err
	}
	control := u.ResolveReference(ref)
	if control.Scheme != "http" || control.Hostname() != gateway.String() || control.User != nil {
		return nil, errors.New("IGD 控制地址无效")
	}
	conn, err := n.DialUDP(ctx, net.UDPAddrFromAddrPort(netip.AddrPortFrom(gateway, 1900)))
	if err != nil {
		return nil, err
	}
	internal := addrPort(conn.LocalAddr()).Addr()
	_ = conn.Close()
	soap := func(ctx context.Context, action, body string) ([]byte, error) {
		var escaped bytes.Buffer
		_ = xml.EscapeText(&escaped, []byte(service.Type))
		x := `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:` + action + ` xmlns:u="` + escaped.String() + `">` + body + `</u:` + action + `></s:Body></s:Envelope>`
		req, e := http.NewRequestWithContext(ctx, "POST", control.String(), strings.NewReader(x))
		if e != nil {
			return nil, e
		}
		req.Header.Set("Content-Type", "text/xml; charset=\"utf-8\"")
		req.Header.Set("SOAPAction", strconv.Quote(service.Type+"#"+action))
		r, e := client.Do(req)
		if e != nil {
			return nil, e
		}
		defer r.Body.Close()
		value, e := io.ReadAll(io.LimitReader(r.Body, 65536))
		if r.StatusCode != 200 {
			return nil, fmt.Errorf("IGD %s HTTP %d", action, r.StatusCode)
		}
		return value, e
	}
	update := func(ctx context.Context, seconds uint32) (netip.AddrPort, uint32, error) {
		body := fmt.Sprintf("<NewRemoteHost></NewRemoteHost><NewExternalPort>%d</NewExternalPort><NewProtocol>UDP</NewProtocol>", port)
		if seconds == 0 {
			_, e := soap(ctx, "DeletePortMapping", body)
			return netip.AddrPort{}, 0, e
		}
		if existing, e := soap(ctx, "GetSpecificPortMappingEntry", body); e == nil {
			decoder := xml.NewDecoder(bytes.NewReader(existing))
			owner, ownerPort := "", ""
			for {
				token, e := decoder.Token()
				if e != nil {
					break
				}
				if start, ok := token.(xml.StartElement); ok {
					switch start.Name.Local {
					case "NewInternalClient":
						_ = decoder.DecodeElement(&owner, &start)
					case "NewInternalPort":
						_ = decoder.DecodeElement(&ownerPort, &start)
					}
				}
			}
			if owner != internal.String() || ownerPort != strconv.Itoa(int(port)) {
				return netip.AddrPort{}, 0, errors.New("IGD 外部端口已有其他映射")
			}
		}
		body += fmt.Sprintf("<NewInternalPort>%d</NewInternalPort><NewInternalClient>%s</NewInternalClient><NewEnabled>1</NewEnabled><NewPortMappingDescription>Hole UDP</NewPortMappingDescription><NewLeaseDuration>%d</NewLeaseDuration>", port, internal, seconds)
		if _, e := soap(ctx, "AddPortMapping", body); e != nil {
			return netip.AddrPort{}, 0, e
		}
		committed := false
		defer func() {
			if !committed {
				cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
				defer cancel()
				_, _ = soap(cleanup, "DeletePortMapping", fmt.Sprintf("<NewRemoteHost></NewRemoteHost><NewExternalPort>%d</NewExternalPort><NewProtocol>UDP</NewProtocol>", port))
			}
		}()
		lease := seconds
		if details, e := soap(ctx, "GetSpecificPortMappingEntry", fmt.Sprintf("<NewRemoteHost></NewRemoteHost><NewExternalPort>%d</NewExternalPort><NewProtocol>UDP</NewProtocol>", port)); e == nil {
			decoder := xml.NewDecoder(bytes.NewReader(details))
			for {
				token, e := decoder.Token()
				if e != nil {
					break
				}
				if start, ok := token.(xml.StartElement); ok && start.Name.Local == "NewLeaseDuration" {
					var text string
					if decoder.DecodeElement(&text, &start) == nil {
						if value, e := strconv.ParseUint(strings.TrimSpace(text), 10, 32); e == nil {
							lease = uint32(value)
						}
					}
				}
			}
		}
		value, e := soap(ctx, "GetExternalIPAddress", "")
		if e != nil {
			return netip.AddrPort{}, 0, e
		}
		decoder := xml.NewDecoder(bytes.NewReader(value))
		for {
			token, e := decoder.Token()
			if e != nil {
				return netip.AddrPort{}, 0, e
			}
			if start, ok := token.(xml.StartElement); ok && start.Name.Local == "NewExternalIPAddress" {
				var text string
				if e = decoder.DecodeElement(&text, &start); e != nil {
					return netip.AddrPort{}, 0, e
				}
				ip, e := netip.ParseAddr(text)
				if e != nil || !ip.Is4() {
					return netip.AddrPort{}, 0, errors.New("IGD 外部 IPv4 地址无效")
				}
				committed = true
				return netip.AddrPortFrom(ip, port), lease, nil
			}
		}
	}
	a, lease, err := update(ctx, 300)
	if err != nil {
		return nil, err
	}
	return &Mapping{MappingSnapshot{"mapped", "upnp", a.String(), lease, ""}, update}, nil
}
