//go:build with_gvisor && !no_tailscale

package outbound

import (
	"context"
	"net"
	"sync/atomic"
	"testing"

	"github.com/metacubex/tailscale/derp/derphttp"
	"github.com/metacubex/tailscale/net/netmon"
	"github.com/metacubex/tailscale/tailcfg"
)

type closeTrackingConn struct {
	net.Conn
	closed atomic.Bool
}

func (c *closeTrackingConn) Close() error {
	c.closed.Store(true)
	return c.Conn.Close()
}

// The upstream tailscale module leaks the conn when a DERP TLS handshake through
// dialer-proxy fails; every leaked conn stays in /connections as type=Inner. This
// fails if an upstream sync drops the replace in go.mod before upstream fixes it.
func TestTailscaleDERPProbeClosesConnOnHandshakeError(t *testing.T) {
	var dialed *closeTrackingConn
	c := derphttp.NewNetcheckClient(t.Logf, netmon.NewStatic())
	c.SetURLDialer(func(ctx context.Context, network, addr string) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			buf := make([]byte, 4096)
			_, _ = server.Read(buf)
			_ = server.Close()
		}()
		dialed = &closeTrackingConn{Conn: client}
		return dialed, nil
	})
	reg := &tailcfg.DERPRegion{
		RegionID: 900,
		Nodes:    []*tailcfg.DERPNode{{Name: "900a", RegionID: 900, HostName: "derp.invalid", IPv4: "192.0.2.1", IPv6: "none"}},
	}
	if _, _, _, err := c.DialRegionTLS(context.Background(), reg); err == nil {
		t.Fatal("DialRegionTLS succeeded; want handshake error")
	}
	if dialed == nil || !dialed.closed.Load() {
		t.Fatal("dialed conn left open after TLS handshake error")
	}
}
