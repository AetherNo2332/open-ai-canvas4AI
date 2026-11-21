package handler

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"yingce/backend/internal/database"
	"yingce/backend/internal/database/agentwake"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
	"yingce/backend/internal/service"
)

func TestAgentWakeSQLiteDefaultPoll(t *testing.T) {
	t.Setenv("TEST_AGENT_POSTGRES_DSN", "")
	unsetWakeMode(t)
	db := agentSDKOpenDatabase(t)
	server := wakeAcceptanceServer(t, db)
	after := wakeAcceptanceInsert(t, db, model.AgentWakeEvent{UserID: "wake-user", Kind: "initial"})
	assertWakeSSE(t, server, 0, []int64{after})

	var expected []int64
	if err := db.Transaction(func(tx *gorm.DB) error {
		for index := range 270 {
			event := model.AgentWakeEvent{UserID: "wake-user", Kind: "backlog"}
			switch index % 10 {
			case 0:
				event.UserID = ""
			case 1:
				event.UserID, event.RunID = "", "private-run"
			case 2:
				event.UserID, event.TaskID = "", "private-task"
			case 3, 4:
				event.UserID = "other-user"
			}
			if err := agentwake.Append(tx, event); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.AgentWakeEvent{}).
		Where("sequence > ? AND (user_id = ? OR (user_id = '' AND run_id = '' AND task_id = ''))", after, "wake-user").
		Order("sequence").Pluck("sequence", &expected).Error; err != nil {
		t.Fatal(err)
	}
	if len(expected) != 162 {
		t.Fatalf("unexpected visible backlog size %d", len(expected))
	}
	boundary := wakeAcceptanceInsert(t, db, model.AgentWakeEvent{Kind: "broadcast-watermark"})
	assertWakeSSE(t, server, after, append(expected, boundary))
}

func TestAgentWakePostgresDefaultNotify(t *testing.T) {
	if os.Getenv("TEST_AGENT_POSTGRES_DSN") == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	unsetWakeMode(t)
	db := agentSDKOpenDatabase(t)
	server := wakeAcceptanceServer(t, db)
	var triggers int
	if err := db.Raw(`SELECT count(*) FROM pg_trigger WHERE tgrelid='agent_wake_events'::regclass
		AND tgname='canvas_agent_wake_signal' AND tgenabled='O'`).Scan(&triggers).Error; err != nil || triggers != 1 {
		t.Fatalf("enabled wake trigger: count=%d err=%v", triggers, err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		var listeners int
		if err := db.Raw(`SELECT count(*) FROM pg_stat_activity WHERE datname=current_database()
			AND application_name='canvas-agent-wake-listener' AND wait_event='ClientRead'`).Scan(&listeners).Error; err != nil {
			t.Fatal(err)
		}
		if listeners == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected one ready wake listener, got %d", listeners)
		}
		time.Sleep(25 * time.Millisecond)
	}
	stream, closeStream := openWakeSSE(t, server, 0)
	defer closeStream()
	start := time.Now()
	sequence := wakeAcceptanceInsert(t, db, model.AgentWakeEvent{Kind: "notification-probe"})
	rows := wakeSSEThrough(t, stream, sequence, []int64{sequence})
	if elapsed := time.Since(start); elapsed >= time.Second {
		t.Fatalf("expected delivery before the recovery read: %s", elapsed)
	}
	t.Logf("default PostgreSQL notify SSE rows=%v latency=%s", rows, time.Since(start))
}

func unsetWakeMode(t *testing.T) {
	t.Helper()
	t.Setenv("CANVAS_AGENT_WAKE_MODE", "")
	if err := os.Unsetenv("CANVAS_AGENT_WAKE_MODE"); err != nil {
		t.Fatal(err)
	}
}

func wakeAcceptanceServer(t *testing.T, db *gorm.DB) *httptest.Server {
	t.Helper()
	t.Setenv("REDIS_URL", "")
	t.Setenv("CANVAS_AGENT_INTERNAL_TOKEN", strings.Repeat("i", 48))
	t.Setenv("CANVAS_AGENT_WORKER_ID", "wake-default")
	t.Setenv("CANVAS_AGENT_WORKER_TOKEN", strings.Repeat("w", 48))
	t.Setenv("CANVAS_AGENT_WORKER_USER_ID", "wake-user")
	if err := database.MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	svc := service.New(repository.New(db), t.TempDir())
	t.Cleanup(func() { _ = svc.Close() })
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterInternalAgentRoutes(router, svc)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return server
}

func wakeAcceptanceInsert(t *testing.T, db *gorm.DB, event model.AgentWakeEvent) int64 {
	t.Helper()
	var sequence int64
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := agentwake.Append(tx, event); err != nil {
			return err
		}
		return tx.Model(&model.AgentEventCounter{}).Select("value").Where("id = 1").Scan(&sequence).Error
	}); err != nil {
		t.Fatal(err)
	}
	return sequence
}

func openWakeSSE(t *testing.T, server *httptest.Server, after int64) (*bufio.Scanner, func()) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/internal-agent/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Last-Event-ID", strconv.FormatInt(after, 10))
	setWakeWorkerHeaders(request, "wake-default", strings.Repeat("w", 48))
	client := &http.Client{Timeout: 8 * time.Second, Transport: server.Client().Transport}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		t.Fatalf("SSE HTTP %d", response.StatusCode)
	}
	return bufio.NewScanner(response.Body), func() { _ = response.Body.Close() }
}

func assertWakeSSE(t *testing.T, server *httptest.Server, after int64, expected []int64) {
	t.Helper()
	stream, closeStream := openWakeSSE(t, server, after)
	defer closeStream()
	rows := wakeSSEThrough(t, stream, expected[len(expected)-1], expected)
	t.Logf("SSE database comparison after=%d H=%d rows=%v missing=0 extra=0 duplicates=0 unordered=0 leaked=0", after, expected[len(expected)-1], rows)
}

func wakeSSEThrough(t *testing.T, stream *bufio.Scanner, end int64, expected []int64) []int64 {
	t.Helper()
	var id int64
	var event model.AgentWakeEvent
	var rows []int64
	for stream.Scan() {
		line := stream.Text()
		prefix, value, _ := strings.Cut(line, ": ")
		switch {
		case prefix == "id":
			if _, err := fmt.Sscan(value, &id); err != nil {
				t.Fatal(err)
			}
		case prefix == "data":
			event = model.AgentWakeEvent{}
			if strings.Contains(value, `"userId":`) {
				t.Fatal("SSE frame exposed user identifier")
			}
			if err := json.Unmarshal([]byte(value), &event); err != nil {
				t.Fatal(err)
			}
		case line == "" && id != 0:
			if len(rows) >= len(expected) || id != expected[len(rows)] || event.Sequence != id ||
				event.RunID == "private-run" || event.TaskID == "private-task" {
				t.Fatalf("unexpected frame after %d visible deliveries: id=%d event=%+v", len(rows), id, event)
			}
			rows = append(rows, id)
			if id == end {
				return rows
			}
			id = 0
		}
	}
	t.Fatalf("SSE closed before %d: received %d/%d: %v", end, len(rows), len(expected), stream.Err())
	return nil
}
