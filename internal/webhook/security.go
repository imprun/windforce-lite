package webhook

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strings"
)

var ErrEgressPolicy = errors.New("webhook egress policy rejected endpoint")

type AddressResolver interface {
	LookupNetIP(ctx context.Context, network string, host string) ([]netip.Addr, error)
}

type DialContextFunc func(ctx context.Context, network string, address string) (net.Conn, error)

type EgressPolicy struct {
	AllowedHosts             []string
	AllowedCIDRs             []netip.Prefix
	AllowedInsecureHTTPHosts []string
	AllowInsecureLoopback    bool
	Resolver                 AddressResolver
	DialContext              DialContextFunc
	TLSConfig                *tls.Config
}

type ResolvedEndpoint struct {
	URL       *url.URL
	Host      string
	Port      string
	Addresses []netip.Addr
}

func ParseAllowedCIDRs(raw string) ([]netip.Prefix, error) {
	values := splitList(raw)
	prefixes := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return nil, fmt.Errorf("invalid webhook allowed CIDR")
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	return prefixes, nil
}

func ParseAllowedHosts(raw string) ([]string, error) {
	values := splitList(raw)
	hosts := make([]string, 0, len(values))
	for _, value := range values {
		host := normalizeHost(value)
		if host == "" || strings.ContainsAny(host, "/:@") || (strings.Contains(host, "*") && !strings.HasPrefix(host, "*.")) {
			return nil, fmt.Errorf("invalid webhook allowed host")
		}
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	return uniqueStrings(hosts), nil
}

func (policy EgressPolicy) ResolveEndpoint(ctx context.Context, raw string) (ResolvedEndpoint, error) {
	parsed, err := validateEndpoint(raw, policy.AllowInsecureLoopback, len(policy.AllowedInsecureHTTPHosts) > 0)
	if err != nil {
		return ResolvedEndpoint{}, ErrEgressPolicy
	}
	host := normalizeHost(parsed.Hostname())
	addresses, err := policy.lookup(ctx, host)
	if err != nil || len(addresses) == 0 {
		return ResolvedEndpoint{}, fmt.Errorf("webhook endpoint resolution failed")
	}
	for _, address := range addresses {
		if !policy.allows(host, parsed.Scheme, address) {
			return ResolvedEndpoint{}, ErrEgressPolicy
		}
	}
	port := parsed.Port()
	if port == "" {
		if parsed.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return ResolvedEndpoint{URL: parsed, Host: host, Port: port, Addresses: addresses}, nil
}

func (policy EgressPolicy) lookup(ctx context.Context, host string) ([]netip.Addr, error) {
	if address, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{address.Unmap()}, nil
	}
	resolver := policy.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	addresses, err := resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	result := make([]netip.Addr, 0, len(addresses))
	seen := map[netip.Addr]struct{}{}
	for _, address := range addresses {
		address = address.Unmap()
		if _, ok := seen[address]; ok {
			continue
		}
		seen[address] = struct{}{}
		result = append(result, address)
	}
	return result, nil
}

func (policy EgressPolicy) allows(host string, scheme string, address netip.Addr) bool {
	address = address.Unmap()
	if isMetadataAddress(address) {
		return false
	}
	if !address.IsValid() || address.IsUnspecified() || address.IsMulticast() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() {
		return false
	}
	if isAlwaysBlockedAddress(address) {
		return false
	}
	if address.IsLoopback() {
		return policy.AllowInsecureLoopback && scheme == "http"
	}
	if scheme == "http" {
		return hostAllowed(host, policy.AllowedInsecureHTTPHosts)
	}
	if scheme != "https" {
		return false
	}
	if !isRestrictedAddress(address) {
		return true
	}
	if hostAllowed(host, policy.AllowedHosts) {
		return true
	}
	for _, prefix := range policy.AllowedCIDRs {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func isMetadataAddress(address netip.Addr) bool {
	for _, blocked := range metadataAddresses {
		if address == blocked {
			return true
		}
	}
	return false
}

var metadataAddresses = []netip.Addr{
	netip.MustParseAddr("169.254.169.254"),
	netip.MustParseAddr("100.100.100.200"),
	netip.MustParseAddr("fd00:ec2::254"),
}

func hostAllowed(host string, candidates []string) bool {
	host = normalizeHost(host)
	for _, candidate := range candidates {
		candidate = normalizeHost(candidate)
		if candidate == host {
			return true
		}
		if strings.HasPrefix(candidate, "*.") {
			suffix := strings.TrimPrefix(candidate, "*")
			if strings.HasSuffix(host, suffix) && host != strings.TrimPrefix(suffix, ".") {
				return true
			}
		}
	}
	return false
}

func (endpoint ResolvedEndpoint) NewTransport(policy EgressPolicy) *http.Transport {
	dial := policy.DialContext
	if dial == nil {
		dialer := &net.Dialer{}
		dial = dialer.DialContext
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: endpoint.Host}
	if policy.TLSConfig != nil {
		tlsConfig = policy.TLSConfig.Clone()
		tlsConfig.ServerName = endpoint.Host
		if tlsConfig.MinVersion == 0 {
			tlsConfig.MinVersion = tls.VersionTLS12
		}
	}
	return &http.Transport{
		Proxy:             nil,
		TLSClientConfig:   tlsConfig,
		ForceAttemptHTTP2: true,
		DialContext: func(ctx context.Context, network string, _ string) (net.Conn, error) {
			if len(endpoint.Addresses) == 0 {
				return nil, ErrEgressPolicy
			}
			var lastErr error
			for _, address := range endpoint.Addresses {
				connection, err := dial(ctx, network, net.JoinHostPort(address.String(), endpoint.Port))
				if err == nil {
					return connection, nil
				}
				lastErr = err
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
			}
			return nil, lastErr
		},
	}
}

func isRestrictedAddress(address netip.Addr) bool {
	if address.IsPrivate() {
		return true
	}
	for _, prefix := range allowlistedNetworkPrefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func isAlwaysBlockedAddress(address netip.Addr) bool {
	for _, prefix := range alwaysBlockedAddressPrefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

var allowlistedNetworkPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
}

var alwaysBlockedAddressPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001:db8::/32"),
}

func splitList(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' })
	result := make([]string, 0, len(fields))
	for _, field := range fields {
		if value := strings.TrimSpace(field); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func normalizeHost(host string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
}

func uniqueStrings(values []string) []string {
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}
