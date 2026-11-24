package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"yingce/backend/internal/auth"
	"yingce/backend/internal/database"
	"yingce/backend/internal/handler"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
	"yingce/backend/internal/service"

	"github.com/gin-gonic/gin"
)

func TestServerDrainClosesSubscriptionsAndFinishesOrdinaryRequest(t *testing.T) {
	status := newSystemStatus(nil, nil)
	router := gin.New()
	router.Use(status.drainEventStreams)
	for _, path := range []string{"/internal-agent/events", "/api/agent/runs/:id/events", "/api/tasks/:id/text-events"} {
		router.GET(path, func(ctx *gin.Context) {
			ctx.Header("Content-Type", "text/event-stream")
			ctx.Writer.Flush()
			<-ctx.Request.Context().Done()
		})
	}
	started, release := make(chan struct{}), make(chan struct{})
	router.POST("/write", func(ctx *gin.Context) {
		close(started)
		select {
		case <-release:
			ctx.Status(http.StatusCreated)
		case <-ctx.Request.Context().Done():
			ctx.Status(http.StatusServiceUnavailable)
		}
	})
	server := httptest.NewServer(router)
	defer server.Close()
	client := server.Client()
	client.Timeout = 5 * time.Second
	for _, path := range []string{"/internal-agent/events", "/api/agent/runs/run/events", "/api/tasks/task/text-events"} {
		resp, err := client.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("stream %s: %s", path, resp.Status)
		}
	}
	writeStatus := make(chan int, 1)
	go func() {
		resp, err := client.Post(server.URL+"/write", "text/plain", nil)
		if err != nil {
			writeStatus <- 0
			return
		}
		defer resp.Body.Close()
		writeStatus <- resp.StatusCode
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("ordinary write did not start")
	}
	status.beginDrain()
	late, err := client.Get(server.URL + "/internal-agent/events")
	if err != nil {
		t.Fatal(err)
	}
	defer late.Body.Close()
	_, _ = io.Copy(io.Discard, late.Body)
	if late.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("stream admitted during shutdown: %s", late.Status)
	}
	close(release)
	if got := <-writeStatus; got != http.StatusCreated {
		t.Fatalf("drain interrupted ordinary write: %d", got)
	}
	shutdown, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if err := server.Config.Shutdown(shutdown); err != nil {
		t.Fatalf("subscriptions held HTTP shutdown open: %v", err)
	}
}

func TestServerDrainClosesRealSSERoutesAndFinishesOrdinaryWrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("CANVAS_AGENT_INTERNAL_TOKEN", strings.Repeat("k", 48))
	t.Setenv("CANVAS_AGENT_WORKER_ID", "drain-worker")
	t.Setenv("CANVAS_AGENT_WORKER_TOKEN", strings.Repeat("w", 48))
	t.Setenv("CANVAS_AGENT_WORKER_USER_ID", "drain-user")
	t.Setenv("CANVAS_AGENT_WAKE_MODE", "poll")
	t.Setenv("REDIS_URL", "")
	t.Setenv("CANVAS_CORS_ORIGINS", "")

	db, err := database.Open(database.Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "drain-real-routes.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	now := time.Now().UTC()
	user := model.User{ID: "drain-user", Username: "drain-user", DisplayName: "Drain User", Role: model.UserRoleUser, Status: model.UserStatusActive}
	session := model.AuthSession{ID: "drain-session", UserID: user.ID, TokenHash: auth.HashToken("drain-token"), ExpiresAt: now.Add(time.Hour)}
	run := model.CloudAgentExecution{
		ID: "drain-run", UserID: user.ID, Status: "completed", CleanupPending: true,
		ConversationID: "drain-run", CanvasID: "drain-canvas", Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	task := model.Task{ID: "drain-task", UserID: user.ID, Type: "canvas_text", Status: model.TaskStatusRunning, CreatedAt: now, UpdatedAt: now}
	for _, item := range []any{&user, &session, &run, &task} {
		if err := db.Create(item).Error; err != nil {
			t.Fatal(err)
		}
	}

	svc := service.New(repository.New(db), t.TempDir())
	t.Cleanup(func() { _ = svc.Close() })
	handler.ConfigureRuntime(svc)
	t.Cleanup(func() { handler.ConfigureRuntime(nil) })

	status := newSystemStatus(db, svc)
	router := gin.New()
	router.Use(status.drainEventStreams)
	api := router.Group("/api")
	handler.RegisterCanvasAPI(api, svc)
	handler.RegisterInternalAgentRoutes(router, svc)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	client := server.Client()
	// As the streams finish, a racing keep-alive reuse can leave an unused
	// connection in StateNew for longer than the shutdown budget.
	client.Transport.(*http.Transport).DisableKeepAlives = true
	workerRequest, err := http.NewRequest(http.MethodGet, server.URL+"/internal-agent/events?after=0", nil)
	if err != nil {
		t.Fatal(err)
	}
	workerRequest.Header.Set("Authorization", "Bearer "+strings.Repeat("w", 48))
	workerRequest.Header.Set("X-Agent-Identity", "drain-worker")
	workerRequest.Header.Set("X-Agent-Protocol-Version", "canvas-pi-wire/v7")
	workerRequest.Header.Set("X-Pi-SDK-Version", "0.87.1")
	workerRequest.Header.Set("X-Pi-Session-Format", "3")
	workerResponse, err := client.Do(workerRequest)
	if err != nil {
		t.Fatal(err)
	}
	if workerResponse.StatusCode != http.StatusOK {
		_ = workerResponse.Body.Close()
		t.Fatalf("worker SSE status = %s", workerResponse.Status)
	}
	workerDone := readBodyUntilClose(workerResponse.Body)

	userRequest, err := http.NewRequest(http.MethodGet, server.URL+"/api/agent/runs/drain-run/events?after=0", nil)
	if err != nil {
		t.Fatal(err)
	}
	userRequest.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "drain-session.drain-token"})
	userResponse, err := client.Do(userRequest)
	if err != nil {
		t.Fatal(err)
	}
	if userResponse.StatusCode != http.StatusOK {
		_ = userResponse.Body.Close()
		t.Fatalf("user SSE status = %s", userResponse.Status)
	}
	userDone := readBodyUntilClose(userResponse.Body)

	taskRequest, err := http.NewRequest(http.MethodGet, server.URL+"/api/tasks/drain-task/text-events?after=0", nil)
	if err != nil {
		t.Fatal(err)
	}
	taskRequest.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "drain-session.drain-token"})
	taskResponse, err := client.Do(taskRequest)
	if err != nil {
		t.Fatal(err)
	}
	if taskResponse.StatusCode != http.StatusOK {
		_ = taskResponse.Body.Close()
		t.Fatalf("task SSE status = %s", taskResponse.Status)
	}
	taskDone := readBodyUntilClose(taskResponse.Body)

	writeBody := &drainGatedBody{
		started: make(chan struct{}),
		release: make(chan struct{}),
		data:    []byte(`{"content":"after-drain"}`),
	}
	writeRequest, err := http.NewRequest(http.MethodPost, server.URL+"/api/tasks/drain-task/text-deltas", writeBody)
	if err != nil {
		t.Fatal(err)
	}
	writeRequest.Header.Set("Content-Type", "application/json")
	writeRequest.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "drain-session.drain-token"})
	writeResponse := make(chan *http.Response, 1)
	writeError := make(chan error, 1)
	go func() {
		response, err := client.Do(writeRequest)
		if err != nil {
			writeError <- err
			return
		}
		writeResponse <- response
	}()
	select {
	case <-writeBody.started:
	case <-time.After(2 * time.Second):
		t.Fatal("ordinary write did not start")
	}

	status.beginDrain()
	lateResponse, err := client.Get(server.URL + "/internal-agent/events")
	if err != nil {
		t.Fatal(err)
	}
	defer lateResponse.Body.Close()
	_, _ = io.Copy(io.Discard, lateResponse.Body)
	if lateResponse.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("late stream status = %s, want 503", lateResponse.Status)
	}

	close(writeBody.release)
	var response *http.Response
	select {
	case err := <-writeError:
		t.Fatalf("ordinary write failed: %v", err)
	case response = <-writeResponse:
	case <-time.After(2 * time.Second):
		t.Fatal("ordinary write did not finish")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("ordinary write status = %s, body=%v", response.Status, response.Body)
	}

	for name, done := range map[string]<-chan error{
		"worker": workerDone,
		"user":   userDone,
		"task":   taskDone,
	} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("%s SSE did not exit cleanly: %v", name, err)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s SSE remained open after drain", name)
		}
	}

	shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := server.Config.Shutdown(shutdown); err != nil {
		t.Fatalf("HTTP shutdown remained blocked: %v", err)
	}
}

type drainGatedBody struct {
	started chan struct{}
	release chan struct{}
	data    []byte
	once    sync.Once
	sent    bool
}

func (body *drainGatedBody) Read(buffer []byte) (int, error) {
	body.once.Do(func() {
		close(body.started)
		<-body.release
	})
	if body.sent {
		return 0, io.EOF
	}
	body.sent = true
	return copy(buffer, body.data), nil
}

func (body *drainGatedBody) Close() error { return nil }

func readBodyUntilClose(body io.ReadCloser) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, body)
		_ = body.Close()
		done <- err
	}()
	return done
}

func TestServerProcessSIGTERMClosesActiveSSEWithinBudget(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("SIGTERM process acceptance requires Linux")
	}
	if os.Getenv("CANVAS_RUN_SIGTERM_TEST") != "1" {
		t.Skip("set CANVAS_RUN_SIGTERM_TEST=1 to run process-level SIGTERM acceptance")
	}
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "drain-process.db")
	dataDir := filepath.Join(dir, "data")
	prepareDrainProcessDatabase(t, dbPath, dataDir)
	for _, scenario := range []struct {
		name       string
		userStream bool
	}{
		{name: "worker only"},
		{name: "worker and user", userStream: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			runDrainProcessScenario(t, scenario.userStream, dbPath, dataDir)
		})
	}
}

func TestServerDrainProcessHelper(t *testing.T) {
	if os.Getenv("CANVAS_DRAIN_HELPER") != "1" {
		t.Skip("helper process only")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		t.Fatal(err)
	}
}

func runDrainProcessScenario(t *testing.T, userStream bool, dbPath string, dataDir string) {
	t.Helper()
	port := freeDrainTestPort(t)

	command := exec.Command(os.Args[0], "-test.run=^TestServerDrainProcessHelper$", "-test.count=1", "-test.v")
	command.Env = append(os.Environ(),
		"CANVAS_DRAIN_HELPER=1",
		"CANVAS_DATABASE_DRIVER=sqlite",
		"DATABASE_URL="+dbPath,
		"CANVAS_BACKEND_DATA_DIR="+dataDir,
		"CANVAS_BACKEND_ADDR=127.0.0.1:"+strconv.Itoa(port),
		"CANVAS_AUTO_MIGRATE=false",
		"CANVAS_AGENT_INTERNAL_TOKEN="+strings.Repeat("k", 48),
		"CANVAS_AGENT_WORKER_ID=drain-worker",
		"CANVAS_AGENT_WORKER_TOKEN="+strings.Repeat("w", 48),
		"CANVAS_AGENT_WORKER_USER_ID=drain-user",
		"CANVAS_AGENT_WAKE_MODE=poll",
		"CANVAS_CORS_ORIGINS=",
		"REDIS_URL=",
		"GIN_MODE=release",
	)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	processDone := make(chan error, 1)
	go func() { processDone <- command.Wait() }()
	t.Cleanup(func() {
		if command.Process != nil {
			_ = command.Process.Kill()
		}
	})

	baseURL := "http://127.0.0.1:" + strconv.Itoa(port)
	waitForDrainTestServer(t, baseURL, &output, processDone)

	client := &http.Client{}
	workerRequest, err := http.NewRequest(http.MethodGet, baseURL+"/internal-agent/events?after=0", nil)
	if err != nil {
		t.Fatal(err)
	}
	workerRequest.Header.Set("Authorization", "Bearer "+strings.Repeat("w", 48))
	workerRequest.Header.Set("X-Agent-Identity", "drain-worker")
	workerRequest.Header.Set("X-Agent-Protocol-Version", "canvas-pi-wire/v7")
	workerRequest.Header.Set("X-Pi-SDK-Version", "0.87.1")
	workerRequest.Header.Set("X-Pi-Session-Format", "3")
	workerResponse, err := client.Do(workerRequest)
	if err != nil {
		t.Fatal(err)
	}
	if workerResponse.StatusCode != http.StatusOK {
		_ = workerResponse.Body.Close()
		t.Fatalf("worker SSE status = %s", workerResponse.Status)
	}
	workerDone := readBodyUntilClose(workerResponse.Body)
	t.Cleanup(func() { _ = workerResponse.Body.Close() })

	var userDone <-chan error
	var userResponse *http.Response
	if userStream {
		userRequest, err := http.NewRequest(http.MethodGet, baseURL+"/api/agent/runs/drain-run/events?after=0", nil)
		if err != nil {
			t.Fatal(err)
		}
		userRequest.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "drain-session.drain-token"})
		userResponse, err = client.Do(userRequest)
		if err != nil {
			t.Fatal(err)
		}
		if userResponse.StatusCode != http.StatusOK {
			_ = userResponse.Body.Close()
			t.Fatalf("user SSE status = %s", userResponse.Status)
		}
		userDone = readBodyUntilClose(userResponse.Body)
		t.Cleanup(func() { _ = userResponse.Body.Close() })
	}

	started := time.Now()
	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-processDone:
		if err != nil {
			t.Fatalf("server process exited with error: %v\n%s", err, output.String())
		}
	case <-time.After(5 * time.Second):
		_ = command.Process.Kill()
		t.Fatalf("server process did not exit within 5s\n%s", output.String())
	}
	elapsed := time.Since(started)
	t.Logf("SIGTERM process exit: %s", elapsed)
	if elapsed >= 5*time.Second {
		t.Fatalf("shutdown took %s, want under 5s", elapsed)
	}
	if strings.Contains(output.String(), "context deadline exceeded") {
		t.Fatalf("shutdown hit a context deadline:\n%s", output.String())
	}
	if !strings.Contains(output.String(), "backend stopped gracefully") {
		t.Fatalf("missing graceful shutdown log:\n%s", output.String())
	}
	for name, done := range map[string]<-chan error{"worker": workerDone, "user": userDone} {
		if done == nil {
			continue
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("%s SSE did not close cleanly: %v", name, err)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s SSE remained open after process exit", name)
		}
	}
}

func prepareDrainProcessDatabase(t *testing.T, path string, dataDir string) {
	t.Helper()
	db, err := database.Open(database.Config{Driver: "sqlite", DSN: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	svc := service.New(repository.New(db), dataDir)
	if err := initializeBuiltinCatalogs(svc); err != nil {
		_ = svc.Close()
		t.Fatal(err)
	}
	if err := svc.Close(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, item := range []any{
		&model.User{ID: "drain-user", Username: "drain-user", DisplayName: "Drain User", Role: model.UserRoleUser, Status: model.UserStatusActive},
		&model.AuthSession{ID: "drain-session", UserID: "drain-user", TokenHash: auth.HashToken("drain-token"), ExpiresAt: now.Add(time.Hour)},
		&model.CloudAgentExecution{ID: "drain-run", UserID: "drain-user", Status: "completed", CleanupPending: true, ConversationID: "drain-run", CanvasID: "drain-canvas", Revision: 1, CreatedAt: now, UpdatedAt: now},
	} {
		if err := db.Create(item).Error; err != nil {
			t.Fatal(err)
		}
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
}

func waitForDrainTestServer(t *testing.T, baseURL string, output *bytes.Buffer, processDone <-chan error) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-processDone:
			t.Fatalf("server process exited before becoming live: %v\n%s", err, output.String())
		default:
		}
		response, err := http.Get(baseURL + "/api/health/live")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("server did not become live:\n%s", output.String())
}

func freeDrainTestPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}
