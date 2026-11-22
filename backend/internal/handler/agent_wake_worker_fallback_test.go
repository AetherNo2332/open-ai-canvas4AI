package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"yingce/backend/internal/database"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
	"yingce/backend/internal/service"
)

func TestAgentWakePostgresWorkerClaimsDuringFallback(t *testing.T) {
	if os.Getenv("TEST_AGENT_POSTGRES_DSN") == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	t.Setenv("CANVAS_AGENT_WAKE_MODE", "notify")
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	t.Setenv("CANVAS_AGENT_HARNESS_DIR", "")
	t.Setenv("REDIS_URL", "")
	t.Setenv("CANVAS_AGENT_INTERNAL_TOKEN", strings.Repeat("i", 48))
	t.Setenv("CANVAS_AGENT_WORKER_ID", "fallback-worker")
	t.Setenv("CANVAS_AGENT_WORKER_TOKEN", strings.Repeat("w", 48))
	t.Setenv("CANVAS_AGENT_WORKER_USER_ID", "fallback-user")
	db := agentSDKOpenDatabase(t)
	if err := database.MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	repo := repository.New(db)
	svc := service.New(repo, t.TempDir())
	svc.StartWorker()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := svc.StopWorker(ctx); err != nil {
			t.Error(err)
		}
		_ = svc.Close()
	})
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Fallback run completed.\"}}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":30,\"completion_tokens\":10,\"total_tokens\":40}}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(upstream.Close)
	seedFourCapabilityChannels(t, db, upstream.URL)
	for _, row := range []any{
		&model.User{ID: "fallback-user", Username: "fallback-user", Role: model.UserRoleUser, Status: model.UserStatusActive},
		&model.CreditAccount{UserID: "fallback-user", AvailableMicrocredits: 100_000_000},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.UpsertCanvasProject(&model.CanvasProject{ID: "fallback-canvas", UserID: "fallback-user", PayloadJSON: `{"nodes":[],"connections":[]}`}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SeedAgentWorker(); err != nil {
		t.Fatal(err)
	}

	streamStarted, firstClaim := make(chan struct{}), make(chan struct{})
	var streamOnce, claimOnce sync.Once
	router := gin.New()
	router.Use(func(c *gin.Context) {
		switch c.Request.URL.Path {
		case "/internal-agent/events":
			streamOnce.Do(func() { close(streamStarted) })
		case "/internal-agent/claim":
			c.Next()
			claimOnce.Do(func() { close(firstClaim) })
		}
	})
	RegisterInternalAgentRoutes(router, svc)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	_, _, ready, _ := svc.AgentWakeSubscription("fallback-user")
	waitWakeTestSignal(t, ready, "initial listener")
	events, _, _, unsubscribe := svc.AgentWakeSubscription("fallback-user")
	if events == nil {
		t.Fatal("expected online listener")
	}
	defer unsubscribe()

	cmd := exec.CommandContext(t.Context(), "node", "dist/src/server.js")
	cmd.Dir = "../../../agent"
	cmd.Env = append(os.Environ(), "CANVAS_BACKEND_INTERNAL_URL="+server.URL)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	waitWakeTestSignal(t, streamStarted, "worker event stream")
	waitWakeTestSignal(t, firstClaim, "worker initial empty claim")
	var terminated bool
	if err := db.Raw(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity
		WHERE datname=current_database() AND application_name='canvas-agent-wake-listener'`).Scan(&terminated).Error; err != nil || !terminated {
		t.Fatalf("could not terminate isolated listener: terminated=%v err=%v", terminated, err)
	}
	deadline := time.After(3 * time.Second)
	for events != nil {
		select {
		case _, ok := <-events:
			if !ok {
				events = nil
			}
		case <-deadline:
			t.Fatal("listener did not disconnect")
		}
	}
	_, _, reconnect, _ := svc.AgentWakeSubscription("fallback-user")
	if reconnect == nil {
		t.Fatal("listener already reconnected")
	}
	started := time.Now()
	request := service.CloudAgentRequest{CanvasID: "fallback-canvas", Prompt: "Reply with one sentence.", Model: "text-e2e", ChannelID: "sdk-text", ChannelModelKey: "text-e2e", PermissionMode: "auto", ContextScope: []string{"canvas"}, IdempotencyKey: "worker-disconnect-fallback"}
	request.Budget.MaxCredits = 1
	run, err := svc.CreateCloudAgentRun("fallback-user", request, "")
	if err != nil {
		t.Fatal(err)
	}
	ticker := time.NewTicker(15 * time.Millisecond)
	defer ticker.Stop()
	var execution model.CloudAgentExecution
	for {
		if err := db.First(&execution, "id = ?", run.ID).Error; err != nil {
			t.Fatal(err)
		}
		select {
		case <-reconnect:
			t.Fatalf("listener reconnected before observing the worker's claim: status=%s", execution.Status)
		default:
		}
		if strings.HasPrefix(execution.LeaseOwner, "fallback-worker:") || execution.Status == "completed" {
			break
		}
		select {
		case <-ticker.C:
		case <-t.Context().Done():
			t.Fatal("test context canceled")
		case <-reconnect:
			t.Fatal("listener reconnected before the worker claimed the run")
		}
	}
	t.Logf("production worker claimed the persisted run while listener was offline after %s", time.Since(started))
	waitWakeTestSignal(t, reconnect, "listener reconnect")
	finish := time.After(15 * time.Second)
	for execution.Status != "completed" {
		select {
		case <-ticker.C:
			if err := db.First(&execution, "id = ?", run.ID).Error; err != nil {
				t.Fatal(err)
			}
		case <-finish:
			t.Fatalf("worker did not complete run: status=%s model calls=%d", execution.Status, calls.Load())
		}
	}
	var session model.CloudAgentPiSession
	if err := db.First(&session, "user_id = ? AND conversation_id = ?", execution.UserID, execution.ConversationID).Error; err != nil {
		t.Fatal(err)
	}
	if session.LeaseEpoch != 1 || calls.Load() != 1 {
		t.Fatalf("expected one lease and one model execution: epoch=%d calls=%d", session.LeaseEpoch, calls.Load())
	}
	t.Log("run completed after listener recovery with one lease epoch and one model call")
}
