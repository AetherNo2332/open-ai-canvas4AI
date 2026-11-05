package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
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

// TestPlatformSDKFourCapabilitiesAgainstRealBackend 启动真实路由与后台 worker，上游是本地确定性假服务，
// 再用 Bun 运行 web/test/creation-platform-sdk.e2e.test.ts：SDK 通过真实 HTTP 完成文本、图片、视频、音频各一次生成。
func TestPlatformSDKFourCapabilitiesAgainstRealBackend(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Skip("bun is not on PATH; SDK end-to-end test not run")
	}
	gin.SetMode(gin.TestMode)
	t.Setenv("CANVAS_ALLOW_PRIVATE_UPSTREAMS", "true")
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	upstream := httptest.NewServer(fakeGenerationUpstream(t))
	defer upstream.Close()

	db, err := gorm.Open(sqlite.Open(t.TempDir()+"/sdk-e2e.db?_journal_mode=WAL&_busy_timeout=5000"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := database.MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	channels := seedFourCapabilityChannels(t, db, upstream.URL)
	for _, row := range []any{
		&model.User{ID: "sdk-user", Username: "sdk-user", Role: model.UserRoleUser, Status: model.UserStatusActive},
		&model.AuthSession{ID: "sdk-session", UserID: "sdk-user", TokenHash: auth.HashToken("sdk-token"), ExpiresAt: time.Now().Add(time.Hour)},
		&model.CreditAccount{UserID: "sdk-user", AvailableMicrocredits: 100_000_000},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	svc := service.New(repository.New(db), t.TempDir())
	previous := runtimeService
	ConfigureRuntime(svc)
	t.Cleanup(func() { runtimeService = previous })
	svc.StartWorker()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = svc.StopWorker(ctx)
	})
	router := gin.New()
	RegisterCanvasAPI(router.Group("/api"), svc)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: router, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })

	channelsJSON, _ := json.Marshal(channels)
	webDir, err := filepath.Abs(filepath.Join("..", "..", "..", "web"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bun, "test", "test/creation-platform-sdk.e2e.test.ts")
	cmd.Dir = webDir
	cmd.Env = append(os.Environ(),
		"CANVAS_PLATFORM_E2E_URL=http://"+listener.Addr().String()+"/api",
		"CANVAS_PLATFORM_E2E_COOKIE="+service.SessionCookieName+"=sdk-session.sdk-token",
		"CANVAS_PLATFORM_E2E_CHANNELS="+string(channelsJSON),
	)
	output, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(output), " 5 pass") || strings.Contains(string(output), " skip") {
		t.Fatalf("bun SDK e2e failed: %v\n%s", err, output)
	}
	t.Logf("bun SDK e2e:\n%s", tail(string(output), 6))
}

func tail(text string, lines int) string {
	parts := strings.Split(strings.TrimSpace(text), "\n")
	if len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	return strings.Join(parts, "\n")
}

// fakeGenerationUpstream 模拟四种协议：chat/completions（流式与非流式）、图片、视频（创建、轮询、下载）与语音合成。
func fakeGenerationUpstream(t *testing.T) http.Handler {
	t.Helper()
	var picture bytes.Buffer
	if err := png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 2, 3))); err != nil {
		t.Fatal(err)
	}
	mp3 := append([]byte("ID3\x04\x00\x00\x00\x00\x00\x00"), bytes.Repeat([]byte{0xFF, 0xFB, 0x90, 0x64}, 64)...)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/chat/completions"):
			var body struct {
				Stream bool `json:"stream"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Stream {
				w.Header().Set("Content-Type", "text/event-stream")
				for _, piece := range []string{"e2e ", "text ", "result"} {
					fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", piece)
				}
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":3}}\n\ndata: [DONE]\n\n")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"e2e text result"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":3}}`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/images/"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"b64_json":"` + base64.StdEncoding.EncodeToString(picture.Bytes()) + `"}]}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/videos"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"video-e2e","status":"queued"}`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/videos/video-e2e"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"video-e2e","status":"completed"}`))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/videos/video-e2e/content"):
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write(minimalMP4())
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/audio/speech"):
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = w.Write(mp3)
		default:
			http.NotFound(w, r)
		}
	})
}

// minimalMP4 是只含 ftyp 与空 moov 的最小 MP4 文件头，满足视频类型识别。
func minimalMP4() []byte {
	box := func(kind string, payload []byte) []byte {
		size := make([]byte, 4)
		n := uint32(8 + len(payload))
		size[0], size[1], size[2], size[3] = byte(n>>24), byte(n>>16), byte(n>>8), byte(n)
		return append(append(size, []byte(kind)...), payload...)
	}
	return append(box("ftyp", []byte("isom\x00\x00\x02\x00isomiso2mp41")), box("moov", nil)...)
}

func seedFourCapabilityChannels(t *testing.T, db *gorm.DB, upstreamURL string) map[string]string {
	t.Helper()
	specs := []struct {
		capability string
		protocol   model.ChannelInterfaceType
		mode       string
	}{
		{"text", model.ChannelInterfaceChatCompletion, "token"},
		{"image", model.ChannelInterfaceOpenAIImage, "fixed_request"},
		{"video", model.ChannelInterfaceNewAPIVideo, "fixed_request"},
		{"audio", model.ChannelInterfaceOpenAIAudio, "fixed_request"},
	}
	channels := map[string]string{}
	for _, spec := range specs {
		modelKey := spec.capability + "-e2e"
		profile := service.DefaultModelCapabilityConfigForModel(string(spec.protocol), modelKey)
		if spec.capability == "image" {
			profile.Image.Size = service.ImageSizeConfig{Parameter: "size", Values: []string{"1024x1024"}, Default: "1024x1024"}
			profile.Image.Quality = service.ImageQualityConfig{Supported: false}
			profile.Image.Resolution.Supported = false
		}
		capabilityJSON, _ := json.Marshal(profile)
		channelID := "sdk-" + spec.capability
		tier := model.ChannelModelPriceTier{ID: "sdk-tier-" + spec.capability, ChannelModelID: "sdk-cm-" + spec.capability, SelectorKey: "{}", SelectorJSON: "{}", BillingMode: spec.mode, UnitPriceMicrocredits: 10_000, PriceConfigured: true, Enabled: true}
		if spec.mode == "token" {
			tier.InputTokenPriceMicrocredits, tier.OutputTokenPriceMicrocredits = 1_000, 1_000
		}
		for _, row := range []any{
			&model.ModelChannel{ID: channelID, Scope: model.ChannelScopeSystem, Enabled: true, Name: "SDK " + spec.capability, BaseURL: upstreamURL + "/v1", APIKey: "sdk-upstream-key", APIFormat: "openai"},
			&model.ChannelModel{ID: "sdk-cm-" + spec.capability, ChannelID: channelID, ModelKey: modelKey, ProviderModelKey: modelKey, Capability: spec.capability, Protocol: spec.protocol, CapabilityConfigJSON: string(capabilityJSON), BillingMode: spec.mode, UnitPriceMicrocredits: 10_000, PriceConfigured: true, Enabled: true},
			&tier,
		} {
			if err := db.Create(row).Error; err != nil {
				t.Fatal(err)
			}
		}
		channels[spec.capability] = channelID
	}
	return channels
}
