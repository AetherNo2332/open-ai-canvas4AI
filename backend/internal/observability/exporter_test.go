package observability

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestExporterUniqueValidSpans(t *testing.T) {
	first := spanPayload(Event{TraceID: "0123456789abcdef0123456789abcdef", Status: StatusFailed})
	second := spanPayload(Event{TraceID: "0123456789abcdef0123456789abcdef"})
	if first["traceId"] != "0123456789abcdef0123456789abcdef" {
		t.Fatal("valid trace ID changed")
	}
	if first["spanId"] == second["spanId"] {
		t.Fatal("different events reused span ID")
	}
	if _, ok := first["startTimeUnixNano"].(string); !ok {
		t.Fatal("OTLP nanoseconds must be decimal strings")
	}
	if first["status"].(map[string]any)["code"] != 2 {
		t.Fatal("failed event requires error span status")
	}
	raw, _ := json.Marshal(spanPayload(Event{Reason: "secret credential", TraceID: strings.Repeat("0", 32)}))
	if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), `"traceId":"00000000000000000000000000000000"`) {
		t.Fatal("unsafe payload")
	}
}

func TestExporterMalformedEndpointAndClosedQueue(t *testing.T) {
	e := NewExporter(1, ":invalid")
	_ = e.Emit(Event{})
	e.Close()
	_ = e.Emit(Event{})
	if got := e.Stats(); got.Failed != 1 || got.Dropped != 2 {
		t.Fatalf("stats: %+v", got)
	}
}

func TestExporterRecoversAfterCollectorFailure(t *testing.T) {
	var calls atomic.Int64
	var spanID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		span := body["resourceSpans"].([]any)[0].(map[string]any)["scopeSpans"].([]any)[0].(map[string]any)["spans"].([]any)[0].(map[string]any)
		if spanID != "" && spanID != span["spanId"] {
			t.Error("retry changed span identity")
		}
		spanID = span["spanId"].(string)
		if calls.Add(1) == 1 {
			w.WriteHeader(503)
		}
	}))
	defer server.Close()
	e := NewExporter(1, server.URL)
	_ = e.Emit(Event{})
	e.Close()
	if got := e.Stats(); got.Exported != 1 || got.Failed != 0 || got.Dropped != 0 || calls.Load() != 2 {
		t.Fatalf("stats: %+v calls: %d", got, calls.Load())
	}
}

func TestExporterCollectorRetries(t *testing.T) {
	for _, tc := range []struct {
		name             string
		status, attempts int
	}{{"permanent", 400, 1}, {"transient", 503, 3}, {"throttled", 429, 3}, {"success", 200, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if r.Header.Get("Content-Type") != "application/json" {
					t.Error("missing JSON content type")
				}
				w.WriteHeader(tc.status)
			}))
			defer server.Close()
			e := NewExporter(2, server.URL)
			_ = e.Emit(Event{})
			e.Close()
			stats := e.Stats()
			if tc.status == 200 {
				if stats.Exported != 1 || stats.Failed != 0 || stats.Dropped != 0 {
					t.Fatalf("success stats: %+v", stats)
				}
			} else if stats.Failed != 1 || stats.Dropped != 1 || stats.Exported != 0 {
				t.Fatalf("failure stats: %+v", stats)
			}
			if calls.Load() != int64(tc.attempts) {
				t.Fatalf("attempts = %d, want %d", calls.Load(), tc.attempts)
			}
		})
	}
}

func TestExporterDropsWhenBoundedQueueIsFull(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-release
	}))
	defer server.Close()
	e := NewExporter(1, server.URL)
	_ = e.Emit(Event{TaskID: "a"})
	<-entered
	_ = e.Emit(Event{TaskID: "b"})
	_ = e.Emit(Event{TaskID: "c"})
	close(release)
	e.Close()
	if e.Dropped() == 0 {
		t.Fatal("expected bounded exporter to drop low priority events")
	}
}
