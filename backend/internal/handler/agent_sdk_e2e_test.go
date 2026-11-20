package handler

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"yingce/backend/internal/auth"
	"yingce/backend/internal/database"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
	"yingce/backend/internal/service"
)

func TestAgentSDKRealHTTPWorkerRestart(t *testing.T) {
	bun, bunErr := exec.LookPath("bun")
	node, nodeErr := exec.LookPath("node")
	if bunErr != nil || nodeErr != nil {
		t.Skip("bun and node required for cross-process SDK smoke")
	}
	agentDir, _ := filepath.Abs(filepath.Join("..", "..", "..", "agent"))
	if _, err := os.Stat(filepath.Join(agentDir, "dist", "src", "runner.js")); err != nil {
		t.Skip("build agent before cross-process smoke")
	}
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	t.Setenv("CANVAS_AGENT_INTERNAL_TOKEN", strings.Repeat("i", 48))
	t.Setenv("CANVAS_AGENT_WORKER_ID", "sdk-worker")
	t.Setenv("CANVAS_AGENT_WORKER_TOKEN", strings.Repeat("w", 48))
	t.Setenv("REDIS_URL", "")
	gin.SetMode(gin.TestMode)
	var requests, pixels, isolated, leaks atomic.Int32
	upstream := httptest.NewServer(agentSDKUpstream(&requests, &pixels, &isolated, &leaks))
	defer upstream.Close()
	db := agentSDKOpenDatabase(t)
	if err := database.MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	seedFourCapabilityChannels(t, db, upstream.URL)
	profile := service.DefaultModelCapabilityConfigForModel(string(model.ChannelInterfaceChatCompletion), "text-e2e")
	profile.Text.References.MaxImages, profile.Text.References.MaxImageBytes = 2, 1024*1024
	raw, _ := json.Marshal(profile)
	db.Model(&model.ChannelModel{}).Where("id = ?", "sdk-cm-text").Update("capability_config_json", string(raw))
	for _, row := range []any{
		&model.SystemSetting{Key: "inspiration_feed", ValueJSON: `{"enabled":false}`},
		&model.User{ID: "sdk-user", Username: "sdk-user", Role: model.UserRoleUser, Status: model.UserStatusActive},
		&model.AuthSession{ID: "sdk-session", UserID: "sdk-user", TokenHash: auth.HashToken("sdk-token"), ExpiresAt: time.Now().Add(time.Hour)},
		&model.CreditAccount{UserID: "sdk-user", AvailableMicrocredits: 100_000_000},
		&model.Project{ID: "sdk-drama", UserID: "sdk-user", Name: "SDK drama", Status: model.ProjectStatusActive},
		&model.DramaProduction{ProjectID: "sdk-drama", UserID: "sdk-user", Status: "paused", Stage: "script", SettingsJSON: agentSDKDramaSettings},
		&model.DramaEpisode{ID: "sdk-episode", ProjectID: "sdk-drama", Synopsis: "SDK_DRAMA_SOURCE"},
		&model.CanvasProject{ID: "sdk-canvas", UserID: "sdk-user", Title: "SDK canvas", PayloadJSON: `{"referenceFieldsVersion":1,"nodes":[{"id":"cat","type":"image","title":"SDK image","position":{"x":200,"y":200},"width":240,"height":240,"metadata":{"status":"success","storageKey":"resource:sdk-image"}},{"id":"sdk-board","type":"script","title":"SDK storyboard","position":{"x":450,"y":140},"width":650,"height":480,"metadata":{"storyboardShotCount":"3","storyboardShotDuration":"10","storyboard":{"rows":[{"id":"sdk-locked","shotNumber":1,"durationSeconds":10,"plotDescription":"SDK_LOCKED_SHOT","locked":true},{"id":"sdk-media-row","shotNumber":2,"durationSeconds":5,"plotDescription":"SDK_OLD_SHOT","imageNodeId":"cat"}],"referenceNodeIds":[]}}}],"connections":[],"chatSessions":[],"activeChatId":null,"previsScenes":[],"viewport":{"x":0,"y":0,"k":1}}`},
		&model.User{ID: "sdk-user-two", Username: "sdk-user-two", Role: model.UserRoleUser, Status: model.UserStatusActive},
		&model.AuthSession{ID: "sdk-second", UserID: "sdk-user-two", TokenHash: auth.HashToken("sdk-second-token"), ExpiresAt: time.Now().Add(time.Hour)},
		&model.CreditAccount{UserID: "sdk-user-two", AvailableMicrocredits: 500_000_000},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	dataDir := t.TempDir()
	agentSDKSeedImage(t, db, dataDir)
	svc := service.New(repository.New(db), dataDir)
	agentSDKSeedIsolation(t, db, svc)
	previous := runtimeService
	ConfigureRuntime(svc)
	t.Cleanup(func() { runtimeService = previous })
	svc.StartWorker()
	t.Cleanup(func() {
		stopContext, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if err := svc.StopWorker(stopContext); err != nil {
			t.Errorf("isolated backend did not stop: %v", err)
		}
		_ = svc.Close()
	})
	router := gin.New()
	var splitRequests atomic.Int32
	router.Use(func(c *gin.Context) {
		if c.Request.Method == http.MethodPost && strings.HasSuffix(c.Request.URL.Path, "/storyboard/split") {
			splitRequests.Add(1)
		}
		c.Next()
	})
	RegisterCanvasAPI(router.Group("/api"), svc)
	RegisterInternalAgentRoutes(router, svc)
	if browserScript := os.Getenv("AGENT_SDK_BROWSER_SCRIPT"); browserScript != "" {
		runAgentSDKBrowserSmoke(t, bun, node, agentDir, browserScript, router)
		if pixels.Load() != 1 || leaks.Load() != 0 {
			t.Fatalf("browser model requests: images=%d, leaks=%d", pixels.Load(), leaks.Load())
		}
		var splitTasks int64
		if err := db.Model(&model.Task{}).Where("operation = ?", "storyboard").Count(&splitTasks).Error; err != nil || splitTasks != 0 || splitRequests.Load() != 0 {
			t.Fatalf("storyboard used legacy split: requests=%d tasks=%d err=%v", splitRequests.Load(), splitTasks, err)
		}
		return
	}
	server := httptest.NewServer(router)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 480*time.Second)
	defer cancel()
	worker, lines := agentSDKStartWorker(t, ctx, node, agentDir, server.URL, "old", true)
	agentSDKAwaitLine(t, lines, "READY")
	webDir, _ := filepath.Abs(filepath.Join(agentDir, "..", "web"))
	client := exec.CommandContext(ctx, bun, "test", "test/agent-sdk.e2e.test.ts")
	client.Dir = webDir
	client.Env = append(os.Environ(), "AGENT_SDK_E2E_URL="+server.URL+"/api",
		"AGENT_SDK_E2E_COOKIE="+service.SessionCookieName+"=sdk-session.sdk-token",
		"AGENT_SDK_E2E_SECOND_COOKIE="+service.SessionCookieName+"=sdk-second.sdk-second-token")
	var output bytes.Buffer
	client.Stdout, client.Stderr = &output, &output
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Process.Kill() })
	agentSDKAwaitLine(t, lines, "READ_COMMITTED")
	var leased model.CloudAgentExecution
	if err := db.Where("lease_owner = ?", "sdk-worker:old").First(&leased).Error; err != nil {
		t.Fatal(err)
	}
	var session model.CloudAgentPiSession
	db.Where("user_id = ? AND conversation_id = ?", leased.UserID, leased.ConversationID).First(&session)
	oldOwner := fmt.Sprintf("%s@%d", leased.LeaseOwner, session.LeaseEpoch)
	_ = worker.Process.Kill()
	_ = worker.Wait()
	past := time.Now().UTC().Add(-time.Minute)
	db.Model(&model.CloudAgentExecution{}).Where("id = ?", leased.ID).Update("lease_expires_at", past)
	db.Model(&model.CloudAgentPiSession{}).Where("id = ?", session.ID).Update("lease_expires_at", past)
	_, replacement := agentSDKStartWorker(t, ctx, node, agentDir, server.URL, "new", false)
	agentSDKAwaitLine(t, replacement, "READY")
	_, peer := agentSDKStartWorker(t, ctx, node, agentDir, server.URL, "peer", false)
	agentSDKAwaitLine(t, peer, "READY")
	if err := client.Wait(); err != nil {
		t.Fatalf("SDK cross-process client: %v\n%s", err, output.String())
	}
	if err := svc.RenewPiAgentLease(leased.UserID, leased.ID, oldOwner); err == nil {
		t.Fatal("old epoch renewed after worker takeover")
	}
	var settled, canvases int64
	db.Model(&model.BillingOrder{}).Where("status = ?", model.BillingStatusSettled).Count(&settled)
	db.Model(&model.CanvasProject{}).Count(&canvases)
	if requests.Load() != 38 || settled != 38 || pixels.Load() != 1 || canvases != 5 || isolated.Load() != 32 || leaks.Load() != 0 {
		t.Fatalf("wrong request or isolation counts: calls=%d settled=%d images=%d canvases=%d scoped=%d leaks=%d",
			requests.Load(), settled, pixels.Load(), canvases, isolated.Load(), leaks.Load())
	}
	t.Logf("SDK smoke: 2 businesses, 2 users, 8 objects, 16 isolated conversations, 38 model calls/orders, pixels, worker takeover and question answer\n%s", tail(output.String(), 10))
}

const agentSDKDramaSettings = `{"models":{"text":{"channelId":"sdk-text","modelKey":"text-e2e"}}}`

func runAgentSDKBrowserSmoke(t *testing.T, bun, node, agentDir, script string, router *gin.Engine) {
	t.Helper()
	webDist := filepath.Join(agentDir, "..", "web", "dist")
	if _, err := os.Stat(filepath.Join(webDist, "index.html")); err != nil {
		t.Fatal("build the web app before browser smoke")
	}
	router.Static("/assets", filepath.Join(webDist, "assets"))
	router.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.Status(http.StatusNotFound)
			return
		}
		c.File(filepath.Join(webDist, "index.html"))
	})
	server := httptest.NewServer(router)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 270*time.Second)
	defer cancel()
	_, worker := agentSDKStartWorker(t, ctx, node, agentDir, server.URL, "browser", false)
	agentSDKAwaitLine(t, worker, "READY")
	cmd := exec.CommandContext(ctx, bun, script)
	cmd.Env = append(os.Environ(), "AGENT_SDK_BROWSER_URL="+server.URL, "AGENT_SDK_BROWSER_COOKIE_NAME="+service.SessionCookieName)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
}

func agentSDKOpenDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_AGENT_POSTGRES_DSN")
	if dsn == "" {
		db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "sdk.db")+"?_journal_mode=WAL&_busy_timeout=5000"), &gorm.Config{})
		if err != nil {
			t.Fatal(err)
		}
		conn, _ := db.DB()
		t.Cleanup(func() { _ = conn.Close() })
		return db
	}
	base, err := database.Open(database.Config{Driver: "postgres", DSN: dsn})
	if err != nil {
		t.Fatal("could not open isolated Agent test database")
	}
	baseConn, _ := base.DB()
	t.Cleanup(func() { _ = baseConn.Close() })
	var dbName string
	if err := base.Raw("SELECT current_database()").Scan(&dbName).Error; err != nil || dbName != "canvas_event_integration" {
		t.Fatal("Agent HTTP test requires the isolated canvas_event_integration database")
	}
	schema := fmt.Sprintf("agent_sdk_http_%d", time.Now().UnixNano())
	if err := base.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Exec("DROP SCHEMA " + schema + " CASCADE").Error })
	target, err := url.Parse(dsn)
	if err != nil {
		t.Fatal("could not parse isolated Agent test connection")
	}
	query := target.Query()
	query.Set("search_path", schema)
	target.RawQuery = query.Encode()
	db, err := database.Open(database.Config{Driver: "postgres", DSN: target.String()})
	if err != nil {
		t.Fatal("could not open isolated Agent test schema")
	}
	conn, _ := db.DB()
	t.Cleanup(func() { _ = conn.Close() })
	return db
}

func agentSDKStartWorker(t *testing.T, ctx context.Context, node, dir, base, slot string, crash bool) (*exec.Cmd, <-chan string) {
	t.Helper()
	cmd := exec.CommandContext(ctx, node, "test/agent-sdk-driver.mjs")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "AGENT_SDK_INTERNAL_URL="+base, "AGENT_SDK_WORKER_TOKEN="+strings.Repeat("w", 48), "AGENT_SDK_SLOT="+slot)
	if crash {
		cmd.Env = append(cmd.Env, "AGENT_SDK_CRASH_AFTER_READ=1")
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	lines := make(chan string, 64)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if stderr.Len() > 0 {
			t.Logf("worker %s: %s", slot, stderr.String())
		}
	})
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()
	return cmd, lines
}

func agentSDKAwaitLine(t *testing.T, lines <-chan string, expected string) {
	t.Helper()
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("worker exited before %s", expected)
			}
			if line == expected {
				return
			}
		case <-timer.C:
			t.Fatalf("worker did not reach %s", expected)
		}
	}
}

func agentSDKSeedImage(t *testing.T, db *gorm.DB, dir string) {
	t.Helper()
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 3, 2))); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "resources"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "resources", "sdk.png"), data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Resource{ID: "sdk-image", UserID: "sdk-user", Kind: "image", Provider: "local",
		Status: model.ResourceStatusReady, MimeType: "image/png", ObjectKey: "sdk.png", Size: int64(data.Len()), Width: 3, Height: 2}).Error; err != nil {
		t.Fatal(err)
	}
}

func agentSDKSeedIsolation(t *testing.T, db *gorm.DB, svc *service.Service) {
	t.Helper()
	for userIndex, userID := range []string{"sdk-user", "sdk-user-two"} {
		preference := []string{"ja-JP", "en-US"}[userIndex]
		if _, err := svc.UpdateAgentSharedPreferences(userID, service.AgentSharedPreferences{Language: preference, Tone: "professional"}); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.UpdateUserAgentSystemPrompt(userID, service.AgentSystemPromptRequest{SystemPrompt: "SDK_PRIVATE_LEGACY"}); err != nil {
			t.Fatal(err)
		}
		for objectIndex := range 2 {
			canvas := fmt.Sprintf("isolation-%d-canvas-%d", userIndex, objectIndex)
			drama := fmt.Sprintf("isolation-%d-drama-%d", userIndex, objectIndex)
			doc := fmt.Sprintf(`{"nodes":[{"id":"source","type":"text","metadata":{"content":"SDK_SOURCE_%d_canvas_%d"}}]}`, userIndex, objectIndex)
			for _, row := range []any{
				&model.CanvasProject{ID: canvas, UserID: userID, PayloadJSON: doc},
				&model.Project{ID: drama, UserID: userID, Name: drama, Status: model.ProjectStatusActive},
				&model.DramaProduction{ProjectID: drama, UserID: userID, Status: "paused", Stage: "script", SettingsJSON: agentSDKDramaSettings},
				&model.DramaEpisode{ID: drama + "-ep", ProjectID: drama, Synopsis: fmt.Sprintf("SDK_SOURCE_%d_drama_%d", userIndex, objectIndex)},
			} {
				if err := db.Create(row).Error; err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

var agentSDKIsolationMarker = regexp.MustCompile(`SDK_ISOLATION_([01])_(canvas|drama)_([01])_([01])`)
var agentSDKSourceMarker = regexp.MustCompile(`SDK_SOURCE_[01]_(canvas|drama)_[01]`)
var agentSDKStoryboardSnapshot = regexp.MustCompile(`"snapshotHash"\s*:\s*"([a-f0-9]{64})"`)

func agentSDKToolDelta(name, arguments string) map[string]any {
	return map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "sdk-" + name,
		"type": "function", "function": map[string]any{"name": name, "arguments": arguments}}}}
}

func agentSDKUpstream(requests, pixels, isolated, leaks *atomic.Int32) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		encoded, _ := json.Marshal(body["messages"])
		text := string(encoded)
		delta, finish := map[string]any{"content": "SDK_RESULT: 待审阅分析建议"}, "stop"
		identity := agentSDKIsolationMarker.FindStringSubmatch(text)
		switch {
		case strings.Contains(text, "SDK_STORYBOARD_STORY"):
			delta, finish = agentSDKStoryboardDelta(body["messages"], text)
		case len(identity) > 0:
			isolated.Add(1)
			source := "SDK_SOURCE_" + strings.Join(identity[1:4], "_")
			preference := map[string]string{"0": "ja-JP", "1": "en-US"}[identity[1]]
			if !strings.Contains(text, preference) || strings.Contains(text, "SDK_PRIVATE_LEGACY") ||
				strings.Contains(text, "data:image") || agentSDKModelHasForeignMarker(text, identity[0], source) {
				leaks.Add(1)
			}
			if strings.Contains(text, source) && strings.Contains(text, `"role":"tool"`) {
				delta = map[string]any{"content": "SDK_ISOLATED_RESULT " + identity[0]}
			} else {
				path := "canvas:text/source"
				if identity[2] == "drama" {
					path = fmt.Sprintf("drama:episode/isolation-%s-drama-%s-ep", identity[1], identity[3])
				}
				args, _ := json.Marshal(map[string]string{"path": path})
				delta, finish = agentSDKToolDelta("read", string(args)), "tool_calls"
			}
		case strings.Contains(text, "SDK_ANSWER"):
			delta = map[string]any{"content": "SDK_ANSWERED_RESULT"}
		case strings.Contains(text, "SDK_QUESTION"):
			delta, finish = agentSDKToolDelta("ask_user", `{"question":"选择分析重点","options":[{"label":"剧情"},{"label":"镜头"}]}`), "tool_calls"
		case strings.Contains(text, "SDK_CANCEL_WAIT"):
			select {
			case <-r.Context().Done():
				return
			case <-time.After(20 * time.Second):
			}
		case strings.Contains(text, "data:image/png;base64,"):
			pixels.Add(1)
		case strings.Contains(text, "SDK_DRAMA_SOURCE"):
		default:
			path := "drama:episode/sdk-episode"
			if strings.Contains(text, "SDK_CANVAS") {
				path = "canvas:image/cat"
			}
			args, _ := json.Marshal(map[string]string{"path": path})
			delta, finish = agentSDKToolDelta("read", string(args)), "tool_calls"
		}
		w.Header().Set("Content-Type", "text/event-stream")
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta}}})
		fmt.Fprintf(w, "data: %s\n\n", chunk)
		end, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": finish}},
			"usage": map[string]int{"prompt_tokens": 30, "completion_tokens": 10, "total_tokens": 40}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", end)
	})
}

func agentSDKStoryboardDelta(messages any, text string) (map[string]any, string) {
	if !strings.Contains(text, "3 镜") || !strings.Contains(text, "每镜 10 秒") || !strings.Contains(text, "sdk-board") {
		return map[string]any{"content": "SDK_STORYBOARD_INVALID_CONSTRAINTS"}, "stop"
	}
	confirmed := false
	snapshot := ""
	applied := false
	items, _ := messages.([]any)
	for _, item := range items {
		message, _ := item.(map[string]any)
		raw, _ := json.Marshal(message["content"])
		content := strings.ReplaceAll(string(raw), `\"`, `"`)
		if message["role"] == "user" && strings.Contains(content, "SDK_CONFIRM_STORYBOARD") {
			confirmed = true
		}
		if message["role"] == "tool" && strings.Contains(content, `"applied":true`) && strings.Contains(content, "sdk-board") {
			applied = true
		}
		if message["role"] == "tool" && strings.Contains(content, `"nodeId":"sdk-board"`) {
			if match := agentSDKStoryboardSnapshot.FindStringSubmatch(content); len(match) != 0 {
				snapshot = match[1]
			}
		}
	}
	switch {
	case applied:
		return map[string]any{"content": "SDK_STORYBOARD_WRITTEN"}, "stop"
	case snapshot == "":
		return agentSDKToolDelta("storyboard", `{"action":"read","nodeId":"sdk-board"}`), "tool_calls"
	case !confirmed:
		return agentSDKToolDelta("ask_user", `{"question":"SDK_STORYBOARD_PLAN：保留锁定镜头、更新第二镜、追加第三镜，共三镜，每镜十秒","options":[{"label":"SDK_CONFIRM_STORYBOARD"},{"label":"调一下方案"}]}`), "tool_calls"
	default:
		arguments, _ := json.Marshal(map[string]any{
			"action": "write_rows", "nodeId": "sdk-board", "snapshotHash": snapshot, "operationId": "sdk-storyboard-confirmed-01",
			"updates": []any{map[string]any{"rowId": "sdk-media-row", "patch": map[string]any{"plotDescription": "SDK_UPDATED_SHOT", "durationSeconds": 10}}},
			"newRows": []any{map[string]any{"plotDescription": "SDK_NEW_SHOT", "durationSeconds": 10}},
		})
		return agentSDKToolDelta("storyboard", string(arguments)), "tool_calls"
	}
}

func agentSDKModelHasForeignMarker(text, prompt, source string) bool {
	for _, marker := range agentSDKIsolationMarker.FindAllString(text, -1) {
		if marker != prompt {
			return true
		}
	}
	for _, marker := range agentSDKSourceMarker.FindAllString(text, -1) {
		if marker != source {
			return true
		}
	}
	return false
}
