package handler

import (
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yingce/backend/internal/auth"
	"yingce/backend/internal/database"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
	"yingce/backend/internal/service"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// The TS Agent SDK (Bun) against the real router and service over HTTP.
func TestAgentSDKAgainstRealBackend(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Skip("bun is not on PATH; Agent SDK end-to-end test not run")
	}
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(t.TempDir()+"/agent-sdk-e2e.db?_journal_mode=WAL&_busy_timeout=5000"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := database.MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	for _, row := range []any{
		&model.User{ID: "e2e-a", Username: "e2e-a", Role: model.UserRoleUser, Status: model.UserStatusActive},
		&model.User{ID: "e2e-b", Username: "e2e-b", Role: model.UserRoleUser, Status: model.UserStatusActive},
		&model.AuthSession{ID: "e2e-session-a", UserID: "e2e-a", TokenHash: auth.HashToken("e2e-token-a"), ExpiresAt: time.Now().Add(time.Hour)},
		&model.AuthSession{ID: "e2e-session-b", UserID: "e2e-b", TokenHash: auth.HashToken("e2e-token-b"), ExpiresAt: time.Now().Add(time.Hour)},
		&model.Project{ID: "drama-1", UserID: "e2e-a", Name: "短剧", Type: "short-drama", Status: model.ProjectStatusActive, Revision: 1},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	svc := service.New(repository.New(db), t.TempDir())
	router := gin.New()
	RegisterCanvasAPI(router.Group("/api"), svc)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: router, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	webDir, err := filepath.Abs(filepath.Join("..", "..", "..", "web"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bun, "test", "test/agent-sdk.e2e.test.ts")
	cmd.Dir = webDir
	cmd.Env = append(os.Environ(),
		"CANVAS_AGENT_E2E_URL=http://"+listener.Addr().String()+"/api",
		"CANVAS_AGENT_E2E_COOKIE_A="+service.SessionCookieName+"=e2e-session-a.e2e-token-a",
		"CANVAS_AGENT_E2E_COOKIE_B="+service.SessionCookieName+"=e2e-session-b.e2e-token-b",
	)
	output, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(output), " 1 pass") || strings.Contains(string(output), " skip") {
		t.Fatalf("bun Agent SDK e2e failed: %v\n%s", err, output)
	}
	t.Logf("bun Agent SDK e2e:\n%s", tail(string(output), 6))
}
