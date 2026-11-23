package proxydialer

import (
	"context"
	"fmt"
	"net"
	"net/netip"

	C "github.com/metacubex/mihomo/constant"
)

type Tunnel interface {
	C.Tunnel
	Proxies() map[string]C.Proxy
}

type byNameProxyDialer struct {
	proxyName string
	tunnel    C.Tunnel
}

func (d byNameProxyDialer) resolve(metadata *C.Metadata) (C.Proxy, error) {
	tunnel, _ := d.tunnel.(Tunnel)
	if tunnel == nil {
		return nil, fmt.Errorf("tunnel is invalid, must be proxydialer.Tunnel, but got: %T", d.tunnel)
	}
	proxies := tunnel.Proxies()
	proxy, ok := proxies[d.proxyName]
	if !ok {
		return nil, fmt.Errorf("proxyName[%s] not found", d.proxyName)
	}
	// A group with no proxies (its providers are empty or not loaded yet) selects the
	// COMPATIBLE placeholder, which dials directly. For a dialer-proxy that silently
	// bypasses the proxy the user asked for, so fail instead. An explicit
	// empty-fallback such as DIRECT is a different type and still goes through.
	for p := proxy; p != nil; p = p.Unwrap(metadata, false) {
		if p.Type() == C.Compatible {
			return nil, fmt.Errorf("dialer-proxy %s has no available proxy", d.proxyName)
		}
	}
	return proxy, nil
}

func (d byNameProxyDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	proxy, err := d.resolve(&C.Metadata{Type: C.INNER})
	if err != nil {
		return nil, err
	}
	return New(proxy, true).DialContext(ctx, network, address)
}

func (d byNameProxyDialer) ListenPacket(ctx context.Context, network, address string, rAddrPort netip.AddrPort) (net.PacketConn, error) {
	proxy, err := d.resolve(&C.Metadata{Type: C.INNER})
	if err != nil {
		return nil, err
	}
	return New(proxy, true).ListenPacket(ctx, network, address, rAddrPort)
}

func NewByName(proxyName string, tunnel C.Tunnel) C.Dialer {
	return byNameProxyDialer{proxyName: proxyName, tunnel: tunnel}
}

// Ready returns the error a dial through d would currently fail with before reaching
// any proxy, e.g. because its dialer-proxy group is empty. Dialers that are not
// dialer-proxy references are always ready.
func Ready(d C.Dialer) error {
	if d, ok := d.(byNameProxyDialer); ok {
		_, err := d.resolve(&C.Metadata{Type: C.INNER})
		return err
	}
	return nil
}
