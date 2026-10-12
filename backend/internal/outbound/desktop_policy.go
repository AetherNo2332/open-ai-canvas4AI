package outbound

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// HostPolicy is an immutable desktop policy, independent of website environment flags.
type HostPolicy struct {
	hosts     map[string]bool
	transport *http.Transport
}

func NewHostPolicy(hosts []string) (*HostPolicy, error) {
	if len(hosts) > 32 {
		return nil, errors.New("最多允许 32 个可信上游主机")
	}
	p := &HostPolicy{hosts: map[string]bool{}}
	for _, raw := range hosts {
		host := normalizeOutboundHost(raw)
		if host == "" || strings.ContainsAny(host, "/*?@#\\ \t\r\n") || (strings.Contains(host, ":") && net.ParseIP(host) == nil) {
			return nil, errors.New("可信上游必须是精确域名或 IP 地址")
		}
		if ip := net.ParseIP(host); ip != nil && (ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified()) {
			return nil, errors.New("不能授权链路本地、组播或未指定地址")
		}
		p.hosts[host] = true
	}
	p.transport = newOutboundTransport(p.resolve)
	p.transport.Proxy = nil
	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	p.transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := p.resolve(ctx, host)
		if err != nil {
			return nil, err
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
	}
	return p, nil
}
func (p *HostPolicy) Hosts() []string {
	hosts := make([]string, 0, len(p.hosts))
	for host := range p.hosts {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	return hosts
}
func (p *HostPolicy) resolve(ctx context.Context, host string) ([]net.IP, error) {
	host = normalizeOutboundHost(host)
	ips, err := resolveOutboundHostWithPolicy(ctx, host, p.hosts[host])
	if err != nil {
		return nil, err
	}
	for _, ip := range ips {
		if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
			return nil, errors.New("不能访问链路本地、组播或未指定地址")
		}
		if !p.hosts[host] && blockedCustomRelayIP(ip) {
			return nil, errors.New("不允许访问保留地址")
		}
	}
	return ips, nil
}
func (p *HostPolicy) ValidateURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" || len(raw) > 4096 || u.User != nil || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("模型服务地址无效")
	}
	if u.Scheme == "http" && !p.hosts[normalizeOutboundHost(u.Hostname())] {
		return nil, errors.New("HTTP 模型服务需要先授权精确主机")
	}
	if _, err = p.resolve(context.Background(), u.Hostname()); err != nil {
		return nil, err
	}
	return u, nil
}
func (p *HostPolicy) HTTPClient(timeout time.Duration) *http.Client {
	// Connections resolve through this policy and pin the checked IP; no proxy bypass.
	return &http.Client{Transport: p.transport, Timeout: timeout, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return errors.New("模型服务不允许重定向") }}
}
