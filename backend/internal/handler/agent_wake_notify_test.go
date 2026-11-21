package handler

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"yingce/backend/internal/database"
	"yingce/backend/internal/repository"
	"yingce/backend/internal/service"
)

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
