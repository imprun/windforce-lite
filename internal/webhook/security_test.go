package webhook

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
)

type staticResolver struct {
	addresses map[string][]netip.Addr
	calls     int
}

func (resolver *staticResolver) LookupNetIP(_ context.Context, _ string, host string) ([]netip.Addr, error) {
	resolver.calls++
	addresses, ok := resolver.addresses[host]
	if !ok {
		return nil, errors.New("not found")
	}
	return append([]netip.Addr(nil), addresses...), nil
}

func TestEgressPolicyResolveEndpoint(t *testing.T) {
	private := netip.MustParseAddr("10.20.30.40")
	public := netip.MustParseAddr("1.1.1.1")
	linkLocal := netip.MustParseAddr("169.254.169.254")
	metadataV6 := netip.MustParseAddr("fd00:ec2::254")
	metadataShared := netip.MustParseAddr("100.100.100.200")
	documentation := netip.MustParseAddr("192.0.2.10")
	tests := []struct {
		name    string
		raw     string
		policy  EgressPolicy
		wantErr bool
	}{
		{name: "public HTTPS", raw: "https://hooks.example.test/releases", policy: policyWithAddresses("hooks.example.test", public)},
		{name: "private blocked", raw: "https://hooks.internal/releases", policy: policyWithAddresses("hooks.internal", private), wantErr: true},
		{name: "private host allowed", raw: "https://hooks.internal/releases", policy: EgressPolicy{Resolver: resolverFor("hooks.internal", private), AllowedHosts: []string{"hooks.internal"}}},
		{name: "private wildcard host allowed", raw: "https://hooks.ops.internal/releases", policy: EgressPolicy{Resolver: resolverFor("hooks.ops.internal", private), AllowedHosts: []string{"*.ops.internal"}}},
		{name: "private CIDR allowed", raw: "https://10.20.30.40/releases", policy: EgressPolicy{AllowedCIDRs: []netip.Prefix{netip.MustParsePrefix("10.20.0.0/16")}}},
		{name: "mixed DNS blocked", raw: "https://hooks.example.test/releases", policy: policyWithAddresses("hooks.example.test", public, private), wantErr: true},
		{name: "metadata blocked despite host allow", raw: "https://metadata.internal/latest", policy: EgressPolicy{Resolver: resolverFor("metadata.internal", linkLocal), AllowedHosts: []string{"metadata.internal"}}, wantErr: true},
		{name: "IPv6 metadata blocked despite CIDR allow", raw: "https://metadata.internal/latest", policy: EgressPolicy{Resolver: resolverFor("metadata.internal", metadataV6), AllowedCIDRs: []netip.Prefix{netip.MustParsePrefix("fd00::/8")}}, wantErr: true},
		{name: "shared metadata blocked despite host allow", raw: "https://metadata.internal/latest", policy: EgressPolicy{Resolver: resolverFor("metadata.internal", metadataShared), AllowedHosts: []string{"metadata.internal"}}, wantErr: true},
		{name: "reserved address blocked despite host allow", raw: "https://reserved.internal/latest", policy: EgressPolicy{Resolver: resolverFor("reserved.internal", documentation), AllowedHosts: []string{"reserved.internal"}}, wantErr: true},
		{name: "loopback blocked", raw: "http://127.0.0.1:8090/releases", policy: EgressPolicy{}, wantErr: true},
		{name: "loopback explicit development mode", raw: "http://127.0.0.1:8090/releases", policy: EgressPolicy{AllowInsecureLoopback: true}},
		{name: "public HTTP blocked", raw: "http://hooks.example.test/releases", policy: policyWithAddresses("hooks.example.test", public), wantErr: true},
		{name: "private HTTP blocked by default", raw: "http://host.docker.internal:8010/releases", policy: policyWithAddresses("host.docker.internal", private), wantErr: true},
		{name: "private HTTP explicit local host allowed", raw: "http://host.docker.internal:8010/releases", policy: EgressPolicy{Resolver: resolverFor("host.docker.internal", private), AllowedInsecureHTTPHosts: []string{"host.docker.internal"}}},
		{name: "metadata blocked despite insecure HTTP host allow", raw: "http://metadata.internal/latest", policy: EgressPolicy{Resolver: resolverFor("metadata.internal", linkLocal), AllowedInsecureHTTPHosts: []string{"metadata.internal"}}, wantErr: true},
		{name: "userinfo blocked", raw: "https://user:pass@hooks.example.test/releases", policy: policyWithAddresses("hooks.example.test", public), wantErr: true},
		{name: "fragment blocked", raw: "https://hooks.example.test/releases#secret", policy: policyWithAddresses("hooks.example.test", public), wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			endpoint, err := test.policy.ResolveEndpoint(context.Background(), test.raw)
			if test.wantErr {
				if err == nil {
					t.Fatalf("ResolveEndpoint() = %#v, want error", endpoint)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(endpoint.Addresses) == 0 {
				t.Fatal("resolved endpoint has no addresses")
			}
		})
	}
}

func TestEgressPolicyResolvesEveryAttemptAndDialsValidatedAddress(t *testing.T) {
	resolver := resolverFor("hooks.example.test", netip.MustParseAddr("1.1.1.1"))
	var dialed string
	policy := EgressPolicy{
		Resolver: resolver,
		DialContext: func(_ context.Context, _ string, address string) (net.Conn, error) {
			dialed = address
			return nil, errors.New("test dial stop")
		},
	}
	for index := 0; index < 2; index++ {
		endpoint, err := policy.ResolveEndpoint(context.Background(), "https://hooks.example.test/releases")
		if err != nil {
			t.Fatal(err)
		}
		transport := endpoint.NewTransport(policy)
		_, _ = transport.DialContext(context.Background(), "tcp", "hooks.example.test:443")
	}
	if resolver.calls != 2 {
		t.Fatalf("resolver calls = %d, want 2", resolver.calls)
	}
	if dialed != "1.1.1.1:443" {
		t.Fatalf("dialed address = %q", dialed)
	}
}

func TestParseEgressAllowlist(t *testing.T) {
	hosts, err := ParseAllowedHosts("hooks.internal, *.ops.internal;hooks.internal")
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 2 {
		t.Fatalf("hosts = %#v", hosts)
	}
	prefixes, err := ParseAllowedCIDRs("10.0.0.0/8, fd00::/8")
	if err != nil {
		t.Fatal(err)
	}
	if len(prefixes) != 2 {
		t.Fatalf("prefixes = %#v", prefixes)
	}
	if _, err := ParseAllowedHosts("https://hooks.internal"); err == nil {
		t.Fatal("URL accepted as allowed host")
	}
	if _, err := ParseAllowedCIDRs("not-a-cidr"); err == nil {
		t.Fatal("invalid CIDR accepted")
	}
}

func policyWithAddresses(host string, addresses ...netip.Addr) EgressPolicy {
	return EgressPolicy{Resolver: resolverFor(host, addresses...)}
}

func resolverFor(host string, addresses ...netip.Addr) *staticResolver {
	return &staticResolver{addresses: map[string][]netip.Addr{host: addresses}}
}
