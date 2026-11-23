package proxydialer_test

import (
	"context"
	"net/netip"
	"testing"

	"github.com/metacubex/mihomo/adapter"
	"github.com/metacubex/mihomo/adapter/outbound"
	"github.com/metacubex/mihomo/adapter/outboundgroup"
	"github.com/metacubex/mihomo/adapter/provider"
	"github.com/metacubex/mihomo/component/dialer"
	"github.com/metacubex/mihomo/component/proxydialer"
	C "github.com/metacubex/mihomo/constant"
	P "github.com/metacubex/mihomo/constant/provider"

	"github.com/stretchr/testify/require"
)

type fakeTunnel struct {
	C.Tunnel
	proxies map[string]C.Proxy
}

func (t fakeTunnel) Proxies() map[string]C.Proxy { return t.proxies }

func urlTestGroup(t *testing.T, name string, emptyFallback C.Proxy, members ...C.Proxy) C.Proxy {
	t.Helper()
	// An inline provider, unlike a compatible one, may be empty, like a subscription
	// that has not loaded yet.
	parser := func([]byte) ([]C.Proxy, error) { return members, nil }
	pd, err := provider.NewInlineProvider(name, nil, parser, provider.NewHealthCheck(nil, "", 5000, 0, true, nil))
	require.NoError(t, err)
	group, err := outboundgroup.NewURLTest(outboundgroup.GroupCommonOption{Name: name}, outboundgroup.URLTestOption{}, emptyFallback, []P.ProxyProvider{pd})
	require.NoError(t, err)
	return adapter.NewProxy(group)
}

func node(name string) C.Proxy {
	return adapter.NewProxy(outbound.NewRejectWithOption(outbound.RejectOption{Name: name}))
}

func TestByNameRefusesEmptyGroup(t *testing.T) {
	compatible := adapter.NewProxy(outbound.NewCompatible())
	tunnel := fakeTunnel{proxies: map[string]C.Proxy{
		"empty": urlTestGroup(t, "empty", compatible),
		// A group whose only member is an empty group still ends at COMPATIBLE.
		"outer": urlTestGroup(t, "outer", compatible, urlTestGroup(t, "inner", compatible)),
	}}

	for _, name := range []string{"empty", "outer"} {
		d := proxydialer.NewByName(name, tunnel)
		require.ErrorContains(t, proxydialer.Ready(d), "has no available proxy")

		_, err := d.DialContext(context.Background(), "tcp", "1.1.1.1:443")
		require.ErrorContains(t, err, "has no available proxy")

		_, err = d.ListenPacket(context.Background(), "udp4", ":0", netip.AddrPort{})
		require.ErrorContains(t, err, "has no available proxy")
	}
}

func TestByNameUsesRealProxy(t *testing.T) {
	compatible := adapter.NewProxy(outbound.NewCompatible())
	direct := adapter.NewProxy(outbound.NewDirect())
	tunnel := fakeTunnel{proxies: map[string]C.Proxy{
		"nl": urlTestGroup(t, "nl", compatible, node("NL")),
		// An explicit empty-fallback is the user's choice, not the placeholder.
		"explicit": urlTestGroup(t, "explicit", direct),
	}}

	d := proxydialer.NewByName("nl", tunnel)
	require.NoError(t, proxydialer.Ready(d))
	conn, err := d.DialContext(context.Background(), "tcp", "1.1.1.1:443")
	require.NoError(t, err)
	require.Equal(t, "NL", conn.(C.Connection).Chains().Last())
	_ = conn.Close()

	require.NoError(t, proxydialer.Ready(proxydialer.NewByName("explicit", tunnel)))
}

func TestReadyIgnoresPlainDialer(t *testing.T) {
	require.NoError(t, proxydialer.Ready(dialer.NewDialer()))
}
