package app

import "testing"

func TestConfiguredGrafanaURLRejectsUnsafeValues(t *testing.T) {
	for _, value := range []string{"javascript:alert(1)", "//evil.example", "https://user:pass@grafana.example"} {
		t.Setenv("CANVAS_GRAFANA_URL", value)
		if got := configuredGrafanaURL(); got != "" {
			t.Fatalf("unsafe Grafana URL %q accepted as %q", value, got)
		}
	}
}

func TestConfiguredGrafanaURLNormalizesTrustedHTTPURL(t *testing.T) {
	t.Setenv("CANVAS_GRAFANA_URL", "https://grafana.example/d/agent-overview/")
	if got := configuredGrafanaURL(); got != "https://grafana.example/d/agent-overview" {
		t.Fatalf("url = %q", got)
	}
}
