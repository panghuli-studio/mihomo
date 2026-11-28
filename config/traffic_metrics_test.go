package config

import (
	"testing"
	"time"
)

func TestParseTrafficMetrics(t *testing.T) {
	raw, err := UnmarshalRawConfig([]byte(`
traffic-metrics:
  enable: true
  flush-interval: 30
  provider-sources:
    - {prefix: "TAG ", source: tag}
    - {prefix: "Jisu ", source: jisu}
`))
	if err != nil {
		t.Fatal(err)
	}
	mc, err := parseTrafficMetrics(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !mc.Enable || mc.NodeLabel || mc.FlushInterval != 30*time.Second || len(mc.Sources) != 2 || mc.Sources[1].Source != "jisu" {
		t.Fatalf("unexpected config: %+v", mc)
	}

	def, err := UnmarshalRawConfig([]byte("mode: rule\n"))
	if err != nil {
		t.Fatal(err)
	}
	if mc, _ := parseTrafficMetrics(def); mc.Enable {
		t.Fatal("traffic-metrics enabled by default")
	}

	bad, _ := UnmarshalRawConfig([]byte("traffic-metrics: {provider-sources: [{prefix: \"TAG \"}]}\n"))
	if _, err := parseTrafficMetrics(bad); err == nil {
		t.Fatal("accepted a provider source without a source name")
	}
}
