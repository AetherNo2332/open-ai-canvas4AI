package handler

import (
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"yingce/backend/internal/database"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
	"yingce/backend/internal/service"
)

func TestAgentWakePostgresDisconnectFallbackAndClose(t *testing.T) {
	if os.Getenv("TEST_AGENT_POSTGRES_DSN") == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	t.Setenv("CANVAS_AGENT_WAKE_MODE", "notify")
	t.Setenv("CANVAS_AGENT_INTERNAL_TOKEN", strings.Repeat("i", 48))
	t.Setenv("CANVAS_AGENT_WORKER_ID", "wake-default")
	t.Setenv("CANVAS_AGENT_WORKER_TOKEN", strings.Repeat("w", 48))
	t.Setenv("CANVAS_AGENT_WORKER_USER_ID", "wake-user")
	t.Setenv("REDIS_URL", "")
	db := agentSDKOpenDatabase(t)
	if err := database.MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	svc := service.New(repository.New(db), t.TempDir())
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterInternalAgentRoutes(router, svc)
	server := httptest.NewServer(router)
	defer server.Close()
	closed := make(chan error, 1)
	closeService := func() { go func() { closed <- svc.Close() }() }
	stopping := false
	t.Cleanup(func() {
		if !stopping {
			closeService()
			select {
			case err := <-closed:
				if err != nil {
					t.Error(err)
				}
			case <-time.After(5 * time.Second):
				t.Error("service cleanup timed out")
			}
		}
	})
	_, _, ready, _ := svc.AgentWakeSubscription("wake-user")
	waitWakeTestSignal(t, ready, "initial listener")
	events, _, _, unsubscribe := svc.AgentWakeSubscription("wake-user")
	if events == nil {
		t.Fatal("expected online subscription")
	}
	defer unsubscribe()
	stream, closeStream := openWakeSSE(t, server, 0)
	defer closeStream()
	first := wakeAcceptanceInsert(t, db, model.AgentWakeEvent{Kind: "before-disconnect"})
	wakeSSEThrough(t, stream, first, []int64{first})

	var terminated bool
	if err := db.Raw(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity
		WHERE datname=current_database() AND application_name='canvas-agent-wake-listener'`).Scan(&terminated).Error; err != nil || !terminated {
		t.Fatalf("terminate isolated listener: terminated=%v err=%v", terminated, err)
	}
	for {
		select {
		case _, ok := <-events:
			if ok {
				continue
			}
		case <-time.After(3 * time.Second):
			t.Fatal("listener did not close its subscription")
		}
		break
	}
	_, _, reconnect, _ := svc.AgentWakeSubscription("wake-user")
	if reconnect == nil {
		t.Fatal("listener already online before fallback probe")
	}
	during := wakeAcceptanceInsert(t, db, model.AgentWakeEvent{Kind: "during-disconnect"})
	wakeSSEThrough(t, stream, during, []int64{during})
	select {
	case <-reconnect:
		t.Fatal("event arrived after reconnect, fallback was not exercised")
	default:
	}
	waitWakeTestSignal(t, reconnect, "listener reconnect")
	after := wakeAcceptanceInsert(t, db, model.AgentWakeEvent{Kind: "after-reconnect"})
	wakeSSEThrough(t, stream, after, []int64{after})

	stopping = true
	closeService()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("service close timed out with listener and SSE request active")
	}
	t.Log("same stream delivered the offline event before reconnect, then the next event without replays; service closed with an active SSE request")
}

func waitWakeTestSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	if signal == nil {
		return
	}
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func TestAgentWakeNotifyUsesOneListenerPostgres(t *testing.T) {
	if os.Getenv("TEST_AGENT_POSTGRES_DSN") == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	t.Setenv("CANVAS_AGENT_WAKE_MODE", "notify")
	t.Setenv("CANVAS_AGENT_INTERNAL_TOKEN", strings.Repeat("i", 48))
	t.Setenv("CANVAS_AGENT_WORKER_ID", "wake-notify-test")
	t.Setenv("CANVAS_AGENT_WORKER_TOKEN", strings.Repeat("n", 48))
	t.Setenv("REDIS_URL", "")
	db := agentSDKOpenDatabase(t)
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(8)
	}
	if err := database.MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	svc := service.New(repository.New(db), t.TempDir())
	t.Cleanup(func() { _ = svc.Close() })
	gin.SetMode(gin.TestMode)
	RegisterInternalAgentRoutes(gin.New(), svc)
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		var listeners int64
		err := db.Raw("SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND application_name = ?", "canvas-agent-wake-listener").Scan(&listeners).Error
		if err != nil || listeners > 1 {
			t.Fatalf("listener count=%d: %v", listeners, err)
		}
		if listeners == 1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("notify mode did not establish its dedicated PostgreSQL listener")
}
