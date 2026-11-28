package statistic

import (
	"strings"
	"testing"

	"github.com/metacubex/mihomo/common/atomic"
	C "github.com/metacubex/mihomo/constant"
)

func newInfo(md *C.Metadata, chain, providers C.Chain) *TrackerInfo {
	return &TrackerInfo{
		Metadata:      md,
		Chain:         chain,
		ProviderChain: providers,
		UploadTotal:   atomic.NewInt64(0),
		DownloadTotal: atomic.NewInt64(0),
	}
}

func enabledMetrics(cfg MetricsConfig) *Metrics {
	m := NewMetrics()
	cfg.Enable = true
	m.cfg = cfg
	return m
}

func scrape(t *testing.T, m *Metrics) string {
	t.Helper()
	var sb strings.Builder
	if _, err := m.WriteTo(&sb); err != nil {
		t.Fatal(err)
	}
	return sb.String()
}

func assertLine(t *testing.T, out, line string) {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if l == line {
			return
		}
	}
	t.Fatalf("missing line %q in:\n%s", line, out)
}

var sources = []SourcePrefix{{Prefix: "TAG ", Source: "tag"}, {Prefix: "Jisu ", Source: "jisu"}, {Prefix: "JMS ", Source: "jms"}}

func TestMetricsCountsEachByteOnce(t *testing.T) {
	m := enabledMetrics(MetricsConfig{Sources: sources})
	info := newInfo(&C.Metadata{Type: C.HTTP, Host: "example.com", Process: "curl"}, C.Chain{"TAG HK 01", "auto"}, C.Chain{"sub-store", ""})

	info.UploadTotal.Add(100)
	info.DownloadTotal.Add(1000)
	m.observe(info) // periodic flush of a live connection
	info.UploadTotal.Add(50)
	info.DownloadTotal.Add(500)
	m.observe(info) // final flush on close
	m.observe(info) // a second close must add nothing

	out := scrape(t, m)
	assertLine(t, out, `mihomo_traffic_bytes_total{host="example.com",process="curl",source="tag",proxy_group="auto",direction="upload"} 150`)
	assertLine(t, out, `mihomo_traffic_bytes_total{host="example.com",process="curl",source="tag",proxy_group="auto",direction="download"} 1500`)
}

func TestMetricsInnerExcluded(t *testing.T) {
	m := enabledMetrics(MetricsConfig{Sources: sources})
	info := newInfo(&C.Metadata{Type: C.INNER}, C.Chain{"JMS NL", "nl"}, C.Chain{"sub-store", ""})
	info.UploadTotal.Add(1513)
	m.observe(info)

	out := scrape(t, m)
	if strings.Contains(out, "mihomo_traffic_bytes_total{") {
		t.Fatalf("Inner connection counted in mihomo_traffic_bytes_total:\n%s", out)
	}
	assertLine(t, out, `mihomo_traffic_inner_bytes_total{direction="upload"} 1513`)
}

func TestMetricsUnknownProcess(t *testing.T) {
	m := enabledMetrics(MetricsConfig{Sources: sources})
	known := newInfo(&C.Metadata{Type: C.TUN, Host: "a.com", Process: "git"}, C.Chain{"Jisu US", "dl"}, nil)
	unknown := newInfo(&C.Metadata{Type: C.TUN, Host: "a.com"}, C.Chain{"Jisu US", "dl"}, nil)
	known.DownloadTotal.Add(10)
	unknown.DownloadTotal.Add(7)
	m.observe(known)
	m.observe(unknown)

	out := scrape(t, m)
	assertLine(t, out, `mihomo_traffic_bytes_total{host="a.com",process="unknown",source="jisu",proxy_group="dl",direction="download"} 7`)
	assertLine(t, out, `mihomo_traffic_bytes_total{host="a.com",process="git",source="jisu",proxy_group="dl",direction="download"} 10`)
	assertLine(t, out, `mihomo_traffic_unknown_process_bytes_total{direction="download"} 7`)
}

func TestMetricsSourceAndHostFallbacks(t *testing.T) {
	cfg := MetricsConfig{Sources: sources}
	cases := []struct {
		name      string
		md        *C.Metadata
		chain     C.Chain
		providers C.Chain
		want      metricKey
	}{
		{"prefix", &C.Metadata{Host: "h"}, C.Chain{"JMS US", "us"}, C.Chain{"sub-store", ""}, metricKey{host: "h", process: "unknown", source: "jms", group: "us"}},
		{"provider", &C.Metadata{SniffHost: "s"}, C.Chain{"Codeai 1", "anthropic"}, C.Chain{"codeai", ""}, metricKey{host: "s", process: "unknown", source: "codeai", group: "anthropic"}},
		{"builtin", &C.Metadata{}, C.Chain{"DIRECT"}, C.Chain{""}, metricKey{host: "(ip)", process: "unknown", source: "DIRECT", group: "DIRECT"}},
	}
	for _, tc := range cases {
		if got := cfg.key(newInfo(tc.md, tc.chain, tc.providers)); got != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestMetricsNodeLabel(t *testing.T) {
	info := newInfo(&C.Metadata{Host: "h", Process: "p"}, C.Chain{"TAG HK 01", "auto"}, nil)
	info.UploadTotal.Add(1)

	off := enabledMetrics(MetricsConfig{Sources: sources})
	off.observe(info)
	if strings.Contains(scrape(t, off), `node="`) {
		t.Fatal("node label present while node-label is off")
	}

	info.UploadTotal.Add(1)
	on := enabledMetrics(MetricsConfig{Sources: sources, NodeLabel: true})
	on.observe(info)
	assertLine(t, scrape(t, on), `mihomo_traffic_bytes_total{host="h",process="p",source="tag",proxy_group="auto",node="TAG HK 01",direction="upload"} 1`)
}

func TestMetricsDisabledRecordsNothing(t *testing.T) {
	m := NewMetrics()
	info := newInfo(&C.Metadata{Host: "h"}, C.Chain{"DIRECT"}, nil)
	info.UploadTotal.Add(5)
	m.observe(info)
	if strings.Contains(scrape(t, m), "mihomo_traffic_bytes_total{") {
		t.Fatal("disabled metrics recorded a series")
	}
	// Bytes moved while disabled stay unreported and are counted once after enabling.
	m.cfg.Enable = true
	m.observe(info)
	assertLine(t, scrape(t, m), `mihomo_traffic_bytes_total{host="h",process="unknown",source="DIRECT",proxy_group="DIRECT",direction="upload"} 5`)
}

func TestMetricsEscapesLabels(t *testing.T) {
	m := enabledMetrics(MetricsConfig{})
	info := newInfo(&C.Metadata{Host: "h", Process: "a\"b\\c\nd"}, C.Chain{"DIRECT"}, nil)
	info.UploadTotal.Add(1)
	m.observe(info)
	assertLine(t, scrape(t, m), `mihomo_traffic_bytes_total{host="h",process="a\"b\\c\nd",source="DIRECT",proxy_group="DIRECT",direction="upload"} 1`)
}

type fakeTracker struct {
	*TrackerInfo
	C.Connection
}

func (f fakeTracker) ID() string         { return f.UUID.String() }
func (f fakeTracker) Close() error       { return nil }
func (f fakeTracker) Info() *TrackerInfo { return f.TrackerInfo }

func TestManagerLeaveFlushesOnce(t *testing.T) {
	mgr := &Manager{metrics: enabledMetrics(MetricsConfig{})}
	info := newInfo(&C.Metadata{Host: "h", Process: "p"}, C.Chain{"DIRECT"}, nil)
	tr := fakeTracker{TrackerInfo: info}
	mgr.Join(tr)
	info.DownloadTotal.Add(42)
	mgr.Leave(tr)
	info.DownloadTotal.Add(8) // bytes after removal are never reported
	mgr.Leave(tr)
	assertLine(t, scrape(t, mgr.metrics), `mihomo_traffic_bytes_total{host="h",process="p",source="DIRECT",proxy_group="DIRECT",direction="download"} 42`)
}
