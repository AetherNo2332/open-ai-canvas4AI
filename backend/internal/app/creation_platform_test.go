package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/database"
	"yingce/backend/internal/model"
	"yingce/backend/internal/platform"
	"yingce/backend/internal/repository"
)

// platformImageFixture 是一个真实迁移的 SQLite 库：系统渠道指向本地假上游，同一个渠道模型
// 也挂在一个逻辑模型路由下，用于覆盖两条模型选择路线。
type platformImageFixture struct {
	svc       *Service
	db        *gorm.DB
	upstream  *httptest.Server
	calls     *atomic.Int32
	channelID string
	modelKey  string
	logicalID string
}

func newPlatformImageFixture(t *testing.T) *platformImageFixture {
	t.Helper()
	t.Setenv("CANVAS_ALLOW_PRIVATE_UPSTREAMS", "true")
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	db := newSQLiteTestDB(t)
	if err := database.MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	svc := &Service{repo: repository.New(db), dataDir: t.TempDir(), activeCancels: make(map[string]context.CancelFunc), coordinator: platform.NewCoordinatorWithRedis(nil, "platform-test")}
	calls := &atomic.Int32{}
	png := base64.StdEncoding.EncodeToString(mediaTestPNG(t))
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"` + png + `"}]}`))
	}))
	t.Cleanup(upstream.Close)
	profile := DefaultModelCapabilityConfigForModel(string(model.ChannelInterfaceOpenAIImage), "gpt-image-2")
	profile.Image.Size = ImageSizeConfig{Parameter: "size", Values: []string{"1024x1024"}, Default: "1024x1024"}
	profile.Image.Quality = ImageQualityConfig{Supported: false}
	profile.Image.Resolution = ImageResolutionConfig{Supported: false}
	capabilityJSON := mustEncodeModelCapabilityConfig(t, profile)
	spec, err := CapabilitySpecFromModelCapabilityConfig(profile, "image")
	if err != nil {
		t.Fatal(err)
	}
	specJSON, _ := json.Marshal(spec)
	f := &platformImageFixture{svc: svc, db: db, upstream: upstream, calls: calls, channelID: "platform-channel", modelKey: "gpt-image-2", logicalID: "platform-logical-image"}
	rows := []any{
		&model.User{ID: "user-a", Username: "user-a", Role: model.UserRoleUser, Status: model.UserStatusActive},
		&model.User{ID: "user-b", Username: "user-b", Role: model.UserRoleUser, Status: model.UserStatusActive},
		&model.CreditAccount{UserID: "user-a", AvailableMicrocredits: 10_000_000},
		&model.CreditAccount{UserID: "user-b", AvailableMicrocredits: 10_000_000},
		&model.ModelChannel{ID: f.channelID, Scope: model.ChannelScopeSystem, Enabled: true, Name: "平台测试", BaseURL: upstream.URL, APIKey: "upstream-test-key", APIFormat: "openai"},
		&model.ChannelModel{ID: "platform-cm", ChannelID: f.channelID, ModelKey: f.modelKey, ProviderModelKey: f.modelKey, Capability: "image", Protocol: model.ChannelInterfaceOpenAIImage, CapabilityConfigJSON: capabilityJSON, BillingMode: "fixed_request", UnitPriceMicrocredits: 100_000, PriceConfigured: true, Enabled: true},
		&model.ChannelModelPriceTier{ID: "platform-tier", ChannelModelID: "platform-cm", SelectorKey: "{}", SelectorJSON: "{}", BillingMode: "fixed_request", UnitPriceMicrocredits: 100_000, PriceConfigured: true, Enabled: true},
		&model.LogicalModel{ID: f.logicalID, Code: "platform-image", Name: "平台图片", Capability: "image", Enabled: true, ActiveRevisionID: "platform-revision", PricePolicy: "channel"},
		&model.LogicalModelRevision{ID: "platform-revision", LogicalModelID: f.logicalID, Version: 1, CapabilitySpecJSON: string(specJSON), DefaultOptionsJSON: `{"size":"1024x1024"}`},
		&model.LogicalModelRoute{ID: "platform-route", LogicalModelRevisionID: "platform-revision", ChannelModelID: "platform-cm", Enabled: true, Weight: 1},
		&model.Resource{ID: "ref-a", UserID: "user-a", Kind: "image", Status: model.ResourceStatusReady, MimeType: "image/png", Width: 2, Height: 3, Size: 70},
		&model.Resource{ID: "ref-b", UserID: "user-b", Kind: "image", Status: model.ResourceStatusReady, MimeType: "image/png", Width: 2, Height: 3, Size: 70},
	}
	for _, row := range rows {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *platformImageFixture) systemRequest(key string) cp.GenerationRequest {
	return cp.GenerationRequest{
		ContractVersion: cp.ContractVersion, Capability: cp.CapabilityImage, Operation: "text_to_image",
		Model:  cp.ModelSelection{ChannelID: f.channelID, ModelKey: f.modelKey},
		Prompt: "白底商品主图", Parameters: map[string]any{"size": "1024x1024"},
		Business:       cp.BusinessRef{Application: "ecommerce", ObjectType: "batch_item", ObjectID: "item-1", OperationID: key},
		IdempotencyKey: key,
	}
}

// runQueued 用真实 worker 领取并执行队列中的一个任务。
func (f *platformImageFixture) runQueued(t *testing.T) {
	t.Helper()
	claimed, err := f.svc.repo.ClaimNextTask("platform-worker", time.Minute)
	if err != nil || claimed == nil {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	if err := f.svc.taskWorker().processClaimedTask(claimed, nil); err != nil {
		t.Fatalf("process: %v", err)
	}
}

func platformErrorCode(err error) cp.ErrorCode {
	if platformErr, ok := cp.AsError(err); ok {
		return platformErr.Code
	}
	return ""
}

func TestPlatformImageSubmitViaGoReachesWorkerAndStoredResource(t *testing.T) {
	f := newPlatformImageFixture(t)
	platform := f.svc.CreationPlatform()
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		req  cp.GenerationRequest
	}{
		{"system channel model", f.systemRequest("go-system-1")},
		{"logical model", func() cp.GenerationRequest {
			req := f.systemRequest("go-logical-1")
			req.Model = cp.ModelSelection{LogicalModelID: f.logicalID}
			return req
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.req.Model.LogicalModelID != "" {
				if _, err := f.svc.UpdateFeatureAvailability(&model.User{ID: "admin", Role: model.UserRoleAdmin}, FeatureAvailability{TaskCenterEnabled: true, CreditsEnabled: true, FrontendModelsEnabled: true}); err != nil {
					t.Fatal(err)
				}
			}
			before := f.calls.Load()
			receipt, err := platform.Submit(ctx, cp.Caller{UserID: "user-a"}, tc.req)
			if err != nil {
				t.Fatalf("submit: %v", err)
			}
			snapshot := receipt.Snapshot
			if receipt.Replayed || snapshot.TaskID == "" || snapshot.Execution != cp.ExecutionQueued || snapshot.Capability != cp.CapabilityImage || snapshot.Billing.Status != cp.BillingReserved || snapshot.Business.Application != "ecommerce" {
				t.Fatalf("submitted snapshot: %+v", snapshot)
			}
			if f.calls.Load() != before {
				t.Fatal("submit must not call the provider before the worker runs")
			}
			f.runQueued(t)
			if f.calls.Load() != before+1 {
				t.Fatalf("provider calls = %d, want %d", f.calls.Load(), before+1)
			}
			done, err := platform.Task(ctx, cp.Caller{UserID: "user-a"}, snapshot.TaskID)
			if err != nil {
				t.Fatal(err)
			}
			if done.Execution != cp.ExecutionSucceeded || done.Resources != cp.ResourcesReady || done.Result == nil || len(done.Result.Resources) != 1 {
				t.Fatalf("completed snapshot: %+v", done)
			}
			ref := done.Result.Resources[0]
			resource, err := f.svc.repo.ResourceForUser("user-a", ref.ResourceID)
			if err != nil || resource.Status != model.ResourceStatusReady || ref.Kind != "image" || ref.Width != 2 || ref.Height != 3 {
				t.Fatalf("stored resource %+v: %+v %v", ref, resource, err)
			}
			if done.Billing.Status != cp.BillingSettled || done.Billing.ChargedMicrocredits == nil || *done.Billing.ChargedMicrocredits != 100_000 {
				t.Fatalf("billing: %+v", done.Billing)
			}
			raw, _ := json.Marshal(done)
			if strings.Contains(string(raw), "upstream-test-key") || strings.Contains(string(raw), f.upstream.URL) {
				t.Fatalf("snapshot leaked upstream configuration: %s", raw)
			}
		})
	}
}

func TestPlatformRejectsInvalidModelBeforeProviderCall(t *testing.T) {
	ctx := context.Background()
	cases := map[string]struct {
		mutate func(*cp.GenerationRequest)
		setup  func(*platformImageFixture)
		want   cp.ErrorCode
	}{
		"unsupported parameter": {mutate: func(r *cp.GenerationRequest) { r.Parameters["size"] = "333x333" }, want: cp.CodeModelNotSupported},
		"unknown model":         {mutate: func(r *cp.GenerationRequest) { r.Model.ModelKey = "missing-model" }, want: cp.CodeModelUnavailable},
		"capability mismatch":   {mutate: func(r *cp.GenerationRequest) { r.Capability = cp.CapabilityVideo }, want: cp.CodeModelNotSupported},
		"disabled model": {setup: func(f *platformImageFixture) {
			f.db.Model(&model.ChannelModel{}).Where("id = ?", "platform-cm").Update("enabled", false)
		}, want: cp.CodeModelUnavailable},
		"group without channel": {setup: func(f *platformImageFixture) {
			if err := f.svc.EnsureDefaultUserGroup(); err != nil {
				panic(err)
			}
			if updated := f.db.Model(&model.UserGroup{}).Where("code = ?", model.DefaultUserGroupCode).Update("all_channels", false); updated.Error != nil || updated.RowsAffected != 1 {
				panic("default group not restricted")
			}
			f.svc.invalidateUserGroups()
		}, want: cp.CodeModelUnavailable},
		"invalid structure": {mutate: func(r *cp.GenerationRequest) { r.Model.LogicalModelID = "platform-logical-image" }, want: cp.CodeInvalidRequest},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newPlatformImageFixture(t)
			platform := f.svc.CreationPlatform()
			if tc.setup != nil {
				tc.setup(f)
			}
			req := f.systemRequest("reject-" + name)
			if tc.mutate != nil {
				tc.mutate(&req)
			}
			_, err := platform.Submit(ctx, cp.Caller{UserID: "user-a"}, req)
			if got := platformErrorCode(err); got != tc.want {
				t.Fatalf("code = %q (%v), want %q", got, err, tc.want)
			}
			var tasks int64
			f.db.Model(&model.Task{}).Count(&tasks)
			var orders int64
			f.db.Model(&model.BillingOrder{}).Count(&orders)
			if tasks != 0 || orders != 0 || f.calls.Load() != 0 {
				t.Fatalf("rejected request left tasks=%d orders=%d calls=%d", tasks, orders, f.calls.Load())
			}
		})
	}
}

func TestPlatformRejectsForeignReferenceAndForeignReads(t *testing.T) {
	f := newPlatformImageFixture(t)
	platform := f.svc.CreationPlatform()
	ctx := context.Background()
	foreign := f.systemRequest("foreign-ref")
	foreign.References = []cp.ReferenceResource{{ResourceID: "ref-b", Kind: "image"}}
	if _, err := platform.Submit(ctx, cp.Caller{UserID: "user-a"}, foreign); platformErrorCode(err) != cp.CodeReferenceUnavailable {
		t.Fatalf("foreign reference: %v", err)
	}
	owned := f.systemRequest("owned-ref")
	owned.Operation = "image_to_image"
	owned.References = []cp.ReferenceResource{{ResourceID: "ref-a", Kind: "image"}}
	receipt, err := platform.Submit(ctx, cp.Caller{UserID: "user-a"}, owned)
	if err != nil {
		t.Fatalf("owned reference: %v", err)
	}
	if _, err := platform.Task(ctx, cp.Caller{UserID: "user-b"}, receipt.Snapshot.TaskID); platformErrorCode(err) != cp.CodeNotFound {
		t.Fatalf("user B read user A task: %v", err)
	}
	if _, err := platform.Task(ctx, cp.Caller{}, receipt.Snapshot.TaskID); platformErrorCode(err) != cp.CodeUnauthorized {
		t.Fatalf("anonymous read: %v", err)
	}
	// BusinessRef 指向别人的项目也只是归属标签：任务仍属于调用者，不会绑定到他人名下。
	spoofed := f.systemRequest("spoofed-business")
	spoofed.Business.ProjectID = "user-b-project"
	spoofedReceipt, err := platform.Submit(ctx, cp.Caller{UserID: "user-a"}, spoofed)
	if err != nil {
		t.Fatal(err)
	}
	task, err := f.svc.repo.Task(spoofedReceipt.Snapshot.TaskID)
	if err != nil || task.UserID != "user-a" || task.ProjectID != "" {
		t.Fatalf("spoofed business ref bound a task: %+v %v", task, err)
	}
	if _, err := platform.Submit(ctx, cp.Caller{}, f.systemRequest("anonymous")); platformErrorCode(err) != cp.CodeUnauthorized {
		t.Fatalf("anonymous submit: %v", err)
	}
	if f.calls.Load() != 0 {
		t.Fatal("no provider call should happen without the worker")
	}
}

func TestPlatformLegacyTaskEntryStillWorks(t *testing.T) {
	f := newPlatformImageFixture(t)
	task, err := f.svc.CreateTask("user-a", CreateTaskRequest{Type: "canvas_image", Prompt: "旧入口", Input: map[string]any{"mode": "image", "prompt": "旧入口", "config": map[string]any{"channelId": f.channelID, "model": f.modelKey, "size": "1024x1024"}}})
	if err != nil {
		t.Fatal(err)
	}
	f.runQueued(t)
	stored, err := f.svc.repo.Task(task.ID)
	if err != nil || stored.Status != model.TaskStatusSucceeded {
		t.Fatalf("legacy task: %+v %v", stored, err)
	}
	// 平台快照只覆盖经平台提交的任务；旧入口任务仍由 /api/tasks 读取。
	if _, err := f.svc.CreationPlatform().Task(context.Background(), cp.Caller{UserID: "user-a"}, task.ID); platformErrorCode(err) != cp.CodeNotFound {
		t.Fatalf("legacy tasks are not platform tasks: %v", err)
	}
}
