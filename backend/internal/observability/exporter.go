package observability

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
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
	failed    int64
	exported  int64
	closed    bool
}

type ExportStats struct {
	Exported int64 `json:"exported"`
	Failed   int64 `json:"failed"`
	Dropped  int64 `json:"dropped"`
}

func (e *Exporter) Stats() ExportStats {
	if e == nil {
		return ExportStats{}
	}
	e.droppedMu.Lock()
	defer e.droppedMu.Unlock()
	return ExportStats{Exported: e.exported, Failed: e.failed, Dropped: e.dropped}
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
	e.droppedMu.Lock()
	defer e.droppedMu.Unlock()
	if e.closed {
		e.dropped++
		return nil
	}
	if e.endpoint == "" {
		return nil
	}
	select {
	case e.queue <- event:
		return nil
	default:
		e.dropped++
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
	e.once.Do(func() {
		e.droppedMu.Lock()
		e.closed = true
		close(e.stop)
		e.droppedMu.Unlock()
	})
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
	raw, err := json.Marshal(body)
	if err == nil {
		for attempt := 0; attempt < 3; attempt++ {
			req, requestErr := http.NewRequest(http.MethodPost, e.endpoint, bytes.NewReader(raw))
			if requestErr != nil {
				break
			}
			req.Header.Set("Content-Type", "application/json")
			resp, sendErr := e.client.Do(req)
			if resp != nil && resp.Body != nil {
				resp.Body.Close()
			}
			if sendErr == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
				e.droppedMu.Lock()
				e.exported++
				e.droppedMu.Unlock()
				return
			}
			if sendErr == nil && resp.StatusCode != 429 && resp.StatusCode < 500 {
				break
			}
			if attempt < 2 {
				time.Sleep(time.Duration(attempt+1) * 25 * time.Millisecond)
			}
		}
	}
	// Delivery failures are counters only: never feed an exporter error back into its queue.
	e.droppedMu.Lock()
	e.failed++
	e.dropped++
	e.droppedMu.Unlock()
}
func spanPayload(event Event) map[string]any {
	trace := strings.ToLower(event.TraceID)
	decoded, err := hex.DecodeString(trace)
	if err != nil || len(decoded) != 16 || trace == strings.Repeat("0", 32) {
		sum := sha256.Sum256([]byte(event.TraceID + ":" + event.RunID))
		trace = hex.EncodeToString(sum[:16])
	}
	var span [8]byte
	if _, err := rand.Read(span[:]); err != nil {
		panic("observability: random span ID unavailable")
	}
	if span == [8]byte{} {
		span[7] = 1
	}
	start, end := event.StartedAt, event.EndedAt
	if start.IsZero() {
		start = time.Now()
	}
	if end.IsZero() || end.Before(start) {
		end = start
	}
	status := 0
	if event.Status == StatusCompleted {
		status = 1
	}
	if event.Status == StatusFailed || event.Status == StatusExpired || event.Status == StatusCancelled {
		status = 2
	}
	return map[string]any{"traceId": trace, "spanId": hex.EncodeToString(span[:]), "name": string(event.Kind), "startTimeUnixNano": strconv.FormatInt(start.UnixNano(), 10), "endTimeUnixNano": strconv.FormatInt(end.UnixNano(), 10), "status": map[string]any{"code": status}, "attributes": []any{attribute("task_id", event.TaskID), attribute("run_id", event.RunID), attribute("status", string(event.Status)), attribute("model", event.Model), attribute("tool_type", event.ToolType)}}
}
func attribute(key, value string) map[string]any {
	return map[string]any{"key": key, "value": map[string]any{"stringValue": value}}
}
