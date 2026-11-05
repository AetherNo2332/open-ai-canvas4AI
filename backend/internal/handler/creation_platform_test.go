package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"yingce/backend/internal/auth"
	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/database"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
	"yingce/backend/internal/service"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type platformEnvelope[T any] struct {
	Code    int            `json:"code"`
	Data    T              `json:"data"`
	Msg     string         `json:"msg"`
	Reason  string         `json:"reason"`
	Details map[string]any `json:"details"`
}

// 真实 service + 真实后台 worker + 本地假图片上游：HTTP 提交的任务必须经 worker 生成并能按资源接口读取。
func TestPlatformImageSubmitViaHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("CANVAS_ALLOW_PRIVATE_UPSTREAMS", "true")
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	var picture bytes.Buffer
	if err := png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 2, 3))); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"` + base64.StdEncoding.EncodeToString(picture.Bytes()) + `"}]}`))
	}))
	defer upstream.Close()

	db, err := gorm.Open(sqlite.Open(t.TempDir()+"/platform.db?_journal_mode=WAL&_busy_timeout=5000"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := database.MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	profile := service.DefaultModelCapabilityConfigForModel(string(model.ChannelInterfaceOpenAIImage), "gpt-image-2")
	profile.Image.Size = service.ImageSizeConfig{Parameter: "size", Values: []string{"1024x1024"}, Default: "1024x1024"}
	profile.Image.Quality = service.ImageQualityConfig{Supported: false}
	profile.Image.Resolution.Supported = false
	capabilityJSON, _ := json.Marshal(profile)
	for _, row := range []any{
		&model.User{ID: "http-a", Username: "http-a", Role: model.UserRoleUser, Status: model.UserStatusActive},
		&model.User{ID: "http-b", Username: "http-b", Role: model.UserRoleUser, Status: model.UserStatusActive},
		&model.AuthSession{ID: "session-a", UserID: "http-a", TokenHash: auth.HashToken("token-a"), ExpiresAt: time.Now().Add(time.Hour)},
		&model.AuthSession{ID: "session-b", UserID: "http-b", TokenHash: auth.HashToken("token-b"), ExpiresAt: time.Now().Add(time.Hour)},
		&model.CreditAccount{UserID: "http-a", AvailableMicrocredits: 10_000_000},
		&model.ModelChannel{ID: "http-channel", Scope: model.ChannelScopeSystem, Enabled: true, Name: "HTTP 平台测试", BaseURL: upstream.URL, APIKey: "upstream-http-key", APIFormat: "openai"},
		&model.ChannelModel{ID: "http-cm", ChannelID: "http-channel", ModelKey: "gpt-image-2", ProviderModelKey: "gpt-image-2", Capability: "image", Protocol: model.ChannelInterfaceOpenAIImage, CapabilityConfigJSON: string(capabilityJSON), BillingMode: "fixed_request", UnitPriceMicrocredits: 100_000, PriceConfigured: true, Enabled: true},
		&model.ChannelModelPriceTier{ID: "http-tier", ChannelModelID: "http-cm", SelectorKey: "{}", SelectorJSON: "{}", BillingMode: "fixed_request", UnitPriceMicrocredits: 100_000, PriceConfigured: true, Enabled: true},
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
	call := func(method, path string, body any, cookie string) *httptest.ResponseRecorder {
		var reader *bytes.Reader
		if body != nil {
			raw, _ := json.Marshal(body)
			reader = bytes.NewReader(raw)
		} else {
			reader = bytes.NewReader(nil)
		}
		r := httptest.NewRequest(method, path, reader)
		r.Header.Set("Content-Type", "application/json")
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: service.SessionCookieName, Value: cookie})
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	request := cp.GenerationRequest{
		ContractVersion: cp.ContractVersion, Capability: cp.CapabilityImage, Operation: "text_to_image",
		Model: cp.ModelSelection{ChannelID: "http-channel", ModelKey: "gpt-image-2"}, Prompt: "白底陶瓷杯",
		Parameters: map[string]any{"size": "1024x1024"}, Business: cp.BusinessRef{Application: "ecommerce", ObjectType: "batch_item", ObjectID: "item-1"},
		IdempotencyKey: "http-item-1",
	}

	if w := call(http.MethodPost, "/api/platform/v1/generations", request, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous submit: %d %s", w.Code, w.Body.String())
	}
	invalid := request
	invalid.Model.LogicalModelID = "also-logical"
	w := call(http.MethodPost, "/api/platform/v1/generations", invalid, "session-a.token-a")
	var invalidResponse platformEnvelope[any]
	_ = json.Unmarshal(w.Body.Bytes(), &invalidResponse)
	if w.Code != http.StatusBadRequest || invalidResponse.Reason != string(cp.CodeInvalidRequest) {
		t.Fatalf("invalid request: %d %s", w.Code, w.Body.String())
	}

	w = call(http.MethodPost, "/api/platform/v1/generations", request, "session-a.token-a")
	var submitted platformEnvelope[cp.SubmitReceipt]
	if err := json.Unmarshal(w.Body.Bytes(), &submitted); err != nil || w.Code != http.StatusOK || submitted.Code != 0 || submitted.Data.Snapshot.TaskID == "" {
		t.Fatalf("submit: %d %s %v", w.Code, w.Body.String(), err)
	}
	taskID := submitted.Data.Snapshot.TaskID

	var done cp.TaskSnapshot
	deadline := time.Now().Add(20 * time.Second)
	for {
		w = call(http.MethodGet, "/api/platform/v1/tasks/"+taskID, nil, "session-a.token-a")
		var read platformEnvelope[cp.TaskSnapshot]
		if err := json.Unmarshal(w.Body.Bytes(), &read); err != nil || w.Code != http.StatusOK {
			t.Fatalf("read task: %d %s", w.Code, w.Body.String())
		}
		done = read.Data
		if done.Execution.Terminal() && done.Resources != cp.ResourcesDelivering {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("task did not finish: %+v", done)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if done.Execution != cp.ExecutionSucceeded || done.Resources != cp.ResourcesReady || done.Result == nil || len(done.Result.Resources) != 1 || calls.Load() != 1 {
		t.Fatalf("completed via HTTP: %+v calls=%d", done, calls.Load())
	}
	resourceID := done.Result.Resources[0].ResourceID
	if w = call(http.MethodGet, "/api/resources/"+resourceID+"/file", nil, "session-a.token-a"); w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), picture.Bytes()) {
		t.Fatalf("owner cannot read generated resource: %d", w.Code)
	}
	if w = call(http.MethodGet, "/api/resources/"+resourceID+"/file", nil, "session-b.token-b"); w.Code == http.StatusOK {
		t.Fatal("user B read user A's generated resource")
	}
	if w = call(http.MethodGet, "/api/platform/v1/tasks/"+taskID, nil, "session-b.token-b"); w.Code != http.StatusNotFound {
		t.Fatalf("user B read user A's task: %d %s", w.Code, w.Body.String())
	}
	if bytes.Contains(w.Body.Bytes(), []byte("upstream-http-key")) {
		t.Fatal("response leaked upstream key")
	}

	// 重投同一请求返回原任务；同键改内容返回 409 idempotency_conflict。
	w = call(http.MethodPost, "/api/platform/v1/generations", request, "session-a.token-a")
	var replayed platformEnvelope[cp.SubmitReceipt]
	if err := json.Unmarshal(w.Body.Bytes(), &replayed); err != nil || w.Code != http.StatusOK || !replayed.Data.Replayed || replayed.Data.Snapshot.TaskID != taskID {
		t.Fatalf("replay: %d %s", w.Code, w.Body.String())
	}
	changed := request
	changed.Prompt = "另一个提示词"
	w = call(http.MethodPost, "/api/platform/v1/generations", changed, "session-a.token-a")
	var conflict platformEnvelope[any]
	_ = json.Unmarshal(w.Body.Bytes(), &conflict)
	if w.Code != http.StatusConflict || conflict.Reason != string(cp.CodeIdempotencyConflict) {
		t.Fatalf("conflict: %d %s", w.Code, w.Body.String())
	}

	w = call(http.MethodPost, "/api/platform/v1/quotes", request, "session-a.token-a")
	var quote platformEnvelope[cp.Quote]
	if err := json.Unmarshal(w.Body.Bytes(), &quote); err != nil || w.Code != http.StatusOK || quote.Data.AmountMicrocredits != 100_000 || quote.Data.QuoteHash == "" {
		t.Fatalf("quote: %d %s", w.Code, w.Body.String())
	}
	w = call(http.MethodGet, "/api/platform/v1/billing/balance", nil, "session-a.token-a")
	var balance platformEnvelope[cp.CreditBalance]
	if err := json.Unmarshal(w.Body.Bytes(), &balance); err != nil || w.Code != http.StatusOK || balance.Data.AvailableMicrocredits != 10_000_000-100_000 {
		t.Fatalf("balance: %d %s", w.Code, w.Body.String())
	}
	w = call(http.MethodGet, "/api/platform/v1/billing/aggregate?application=ecommerce", nil, "session-a.token-a")
	var aggregate platformEnvelope[cp.BillingAggregate]
	if err := json.Unmarshal(w.Body.Bytes(), &aggregate); err != nil || w.Code != http.StatusOK || aggregate.Data.TaskCount != 1 || aggregate.Data.ChargedMicrocredits != 100_000 {
		t.Fatalf("aggregate: %d %s", w.Code, w.Body.String())
	}
	if w = call(http.MethodGet, "/api/platform/v1/billing/aggregate", nil, "session-a.token-a"); w.Code != http.StatusBadRequest {
		t.Fatalf("aggregate without application: %d %s", w.Code, w.Body.String())
	}
	if w = call(http.MethodGet, "/api/platform/v1/billing/balance", nil, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous balance: %d", w.Code)
	}
}
