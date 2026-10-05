package observability

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"sync"
	"time"
)

type Exporter struct {
	queue     chan Event
	endpoint  string
	client    *http.Client
	stop      chan struct{}
	done      chan struct{}
	once      sync.Once
	droppedMu sync.Mutex
	dropped   int64
}

func NewExporter(capacity int, endpoint string) *Exporter {
	if capacity < 1 {
		capacity = 256
	}
	e := &Exporter{queue: make(chan Event, capacity), endpoint: endpoint, client: &http.Client{Timeout: 2 * time.Second}, stop: make(chan struct{}), done: make(chan struct{})}
	go e.loop()
	return e
}

func NewConfiguredExporter() *Exporter {
	return NewExporter(256, os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"))
}

func (e *Exporter) Emit(event Event) error {
	if e == nil {
		return nil
	}
	select {
	case e.queue <- event:
		return nil
	default:
		e.droppedMu.Lock()
		e.dropped++
		e.droppedMu.Unlock()
		return nil
	}
}
func (e *Exporter) Dropped() int64 {
	if e == nil {
		return 0
	}
	e.droppedMu.Lock()
	defer e.droppedMu.Unlock()
	return e.dropped
}
func (e *Exporter) Close() {
	if e == nil {
		return
	}
	e.once.Do(func() { close(e.stop) })
	<-e.done
}
func (e *Exporter) loop() {
	defer close(e.done)
	for {
		select {
		case event := <-e.queue:
			e.send(event)
		case <-e.stop:
			for {
				select {
				case event := <-e.queue:
					e.send(event)
				default:
					return
				}
			}
		}
	}
}
func (e *Exporter) send(event Event) {
	if e.endpoint == "" {
		return
	}
	body := map[string]any{"resourceSpans": []any{map[string]any{"scopeSpans": []any{map[string]any{"spans": []any{spanPayload(event)}}}}}}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, e.endpoint, bytes.NewReader(raw))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err == nil && resp.Body != nil {
		resp.Body.Close()
	}
}
func spanPayload(event Event) map[string]any {
	trace := hex.EncodeToString([]byte(event.TraceID))
	if len(trace) > 32 {
		trace = trace[:32]
	}
	if len(trace) < 32 {
		trace = trace + "00000000000000000000000000000000"[:32-len(trace)]
	}
	return map[string]any{"traceId": trace, "spanId": trace[:16], "name": string(event.Kind), "startTimeUnixNano": event.StartedAt.UnixNano(), "endTimeUnixNano": event.EndedAt.UnixNano(), "attributes": []any{attribute("task_id", event.TaskID), attribute("run_id", event.RunID), attribute("status", string(event.Status)), attribute("model", event.Model), attribute("tool_type", event.ToolType)}}
}
func attribute(key, value string) map[string]any {
	return map[string]any{"key": key, "value": map[string]any{"stringValue": value}}
}
