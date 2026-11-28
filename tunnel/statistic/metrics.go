package statistic

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	C "github.com/metacubex/mihomo/constant"
)

// MetricsConfig controls the Prometheus traffic counters served at /metrics.
type MetricsConfig struct {
	Enable bool
	// NodeLabel adds the node name as a label. Off by default: node names change
	// with every subscription refresh and multiply the series count.
	NodeLabel     bool
	FlushInterval time.Duration
	// Sources maps a node-name prefix to a subscription source label, first match wins.
	Sources []SourcePrefix
}

type SourcePrefix struct {
	Prefix string
	Source string
}

const (
	unknownProcess = "unknown"
	ipOnlyHost     = "(ip)"
)

type metricKey struct {
	host, process, source, group, node string
}

type byteCounter struct {
	up, down atomic.Int64
}

// Metrics accumulates per-connection byte deltas. Each tracker reports only the
// bytes it moved since its last report, so periodic flushes of long-lived
// connections and the final flush on close never count a byte twice.
type Metrics struct {
	mu     sync.RWMutex
	cfg    MetricsConfig
	series map[metricKey]*byteCounter

	unknownUp, unknownDown atomic.Int64
	innerUp, innerDown     atomic.Int64

	stop chan struct{}
}

func NewMetrics() *Metrics {
	return &Metrics{series: map[metricKey]*byteCounter{}}
}

func (m *Metrics) Enabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.Enable
}

// observe records the bytes the tracker moved since it was last observed.
func (m *Metrics) observe(info *TrackerInfo) {
	m.mu.RLock()
	cfg := m.cfg
	m.mu.RUnlock()
	if !cfg.Enable {
		return
	}
	up := advance(&info.reportedUp, info.UploadTotal.Load())
	down := advance(&info.reportedDown, info.DownloadTotal.Load())
	if up == 0 && down == 0 {
		return
	}
	md := info.Metadata
	// An Inner connection is the dialer-proxy hop of a connection that is already
	// tracked; counting it as well would count the same bytes twice.
	if md != nil && md.Type == C.INNER {
		m.innerUp.Add(up)
		m.innerDown.Add(down)
		return
	}
	key := cfg.key(info)
	if key.process == unknownProcess {
		m.unknownUp.Add(up)
		m.unknownDown.Add(down)
	}
	m.mu.RLock()
	c := m.series[key]
	m.mu.RUnlock()
	if c == nil {
		m.mu.Lock()
		if c = m.series[key]; c == nil {
			c = &byteCounter{}
			m.series[key] = c
		}
		m.mu.Unlock()
	}
	c.up.Add(up)
	c.down.Add(down)
}

// advance moves the reported mark to total and returns the bytes in between.
func advance(reported *atomic.Int64, total int64) int64 {
	for {
		prev := reported.Load()
		if total <= prev {
			return 0
		}
		if reported.CompareAndSwap(prev, total) {
			return total - prev
		}
	}
}

func (cfg MetricsConfig) key(info *TrackerInfo) metricKey {
	k := metricKey{host: ipOnlyHost, process: unknownProcess}
	if md := info.Metadata; md != nil {
		if md.Host != "" {
			k.host = md.Host
		} else if md.SniffHost != "" {
			k.host = md.SniffHost
		}
		if md.Process != "" {
			k.process = md.Process
		}
	}
	node := ""
	if len(info.Chain) > 0 {
		node = info.Chain[0]
		k.group = info.Chain[len(info.Chain)-1]
	}
	k.source = cfg.source(node, info.ProviderChain)
	if cfg.NodeLabel {
		k.node = node
	}
	return k
}

// source names where the node came from: the configured prefix, else its proxy
// provider, else the node itself (DIRECT, REJECT and other built-in proxies).
func (cfg MetricsConfig) source(node string, providers C.Chain) string {
	for _, s := range cfg.Sources {
		if strings.HasPrefix(node, s.Prefix) {
			return s.Source
		}
	}
	if len(providers) > 0 && providers[0] != "" {
		return providers[0]
	}
	return node
}

// WriteTo writes the counters in the Prometheus text exposition format.
func (m *Metrics) WriteTo(w io.Writer) (int64, error) {
	m.mu.RLock()
	nodeLabel := m.cfg.NodeLabel
	keys := make([]metricKey, 0, len(m.series))
	for k := range m.series {
		keys = append(keys, k)
	}
	m.mu.RUnlock()
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.host != b.host {
			return a.host < b.host
		}
		if a.process != b.process {
			return a.process < b.process
		}
		if a.source != b.source {
			return a.source < b.source
		}
		if a.group != b.group {
			return a.group < b.group
		}
		return a.node < b.node
	})

	var sb strings.Builder
	sb.WriteString("# HELP mihomo_traffic_bytes_total Bytes proxied, by destination host, process, subscription source and proxy group. Inner connections are excluded.\n")
	sb.WriteString("# TYPE mihomo_traffic_bytes_total counter\n")
	for _, k := range keys {
		m.mu.RLock()
		c := m.series[k]
		m.mu.RUnlock()
		labels := fmt.Sprintf(`host="%s",process="%s",source="%s",proxy_group="%s"`,
			escapeLabel(k.host), escapeLabel(k.process), escapeLabel(k.source), escapeLabel(k.group))
		if nodeLabel {
			labels += fmt.Sprintf(`,node="%s"`, escapeLabel(k.node))
		}
		fmt.Fprintf(&sb, "mihomo_traffic_bytes_total{%s,direction=\"upload\"} %d\n", labels, c.up.Load())
		fmt.Fprintf(&sb, "mihomo_traffic_bytes_total{%s,direction=\"download\"} %d\n", labels, c.down.Load())
	}
	writePair(&sb, "mihomo_traffic_unknown_process_bytes_total",
		"Bytes of connections whose process could not be identified (also in mihomo_traffic_bytes_total with process=\"unknown\").",
		m.unknownUp.Load(), m.unknownDown.Load())
	writePair(&sb, "mihomo_traffic_inner_bytes_total",
		"Bytes of mihomo's own Inner connections (dialer-proxy hops, provider and health-check dials), not in mihomo_traffic_bytes_total.",
		m.innerUp.Load(), m.innerDown.Load())
	n, err := io.WriteString(w, sb.String())
	return int64(n), err
}

func writePair(sb *strings.Builder, name, help string, up, down int64) {
	fmt.Fprintf(sb, "# HELP %s %s\n# TYPE %s counter\n", name, help, name)
	fmt.Fprintf(sb, "%s{direction=\"upload\"} %d\n%s{direction=\"download\"} %d\n", name, up, name, down)
}

var labelEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

func escapeLabel(s string) string {
	return labelEscaper.Replace(s)
}

// SetMetricsConfig applies cfg. Counters survive config reloads; they reset only
// when the process restarts, which Prometheus handles as a counter reset.
func (m *Manager) SetMetricsConfig(cfg MetricsConfig) {
	mt := m.metrics
	mt.mu.Lock()
	mt.cfg = cfg
	if mt.stop != nil {
		close(mt.stop)
		mt.stop = nil
	}
	if cfg.Enable {
		interval := cfg.FlushInterval
		if interval <= 0 {
			interval = 15 * time.Second
		}
		mt.stop = make(chan struct{})
		go m.flushLoop(interval, mt.stop)
	}
	mt.mu.Unlock()
}

func (m *Manager) Metrics() *Metrics {
	return m.metrics
}

func (m *Manager) flushLoop(interval time.Duration, stop chan struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			m.Range(func(c Tracker) bool {
				m.metrics.observe(c.Info())
				return true
			})
		}
	}
}
