package app

import (
	"context"
	"testing"

	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/model"
)

// Models 复用用户分组可见的系统渠道与逻辑模型目录，返回可直接用于提交的模型选择。
func TestPlatformModelsListsSubmittableSelections(t *testing.T) {
	f := newPlatformImageFixture(t)
	p := f.svc.CreationPlatform()
	caller := cp.Caller{UserID: "user-a"}
	catalog, err := p.Models(context.Background(), caller, cp.CapabilityImage)
	if err != nil {
		t.Fatal(err)
	}
	var system *cp.ModelOption
	for index := range catalog.Models {
		if catalog.Models[index].Selection.ChannelID == f.channelID {
			system = &catalog.Models[index]
		}
		if catalog.Models[index].Selection.LogicalModelID != "" {
			t.Fatalf("logical models must stay hidden while frontend models are disabled: %+v", catalog.Models[index])
		}
	}
	if system == nil || system.Selection.ModelKey != f.modelKey || system.Capability != cp.CapabilityImage || !system.Available || system.Label == "" {
		t.Fatalf("system channel model missing: %+v", catalog.Models)
	}
	// 提交时直接使用目录给出的选择。
	req := f.systemRequest("models-1")
	req.Model = system.Selection
	if _, err := p.Submit(context.Background(), caller, req); err != nil {
		t.Fatalf("catalog selection must be submittable: %v", err)
	}
	if videos, err := p.Models(context.Background(), caller, cp.CapabilityVideo); err != nil || len(videos.Models) != 0 {
		t.Fatalf("capability filter: %+v %v", videos, err)
	}
	if _, err := f.svc.UpdateFeatureAvailability(&model.User{ID: "admin", Role: model.UserRoleAdmin}, FeatureAvailability{TaskCenterEnabled: true, CreditsEnabled: true, FrontendModelsEnabled: true}); err != nil {
		t.Fatal(err)
	}
	withLogical, err := p.Models(context.Background(), caller, cp.CapabilityImage)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, option := range withLogical.Models {
		found = found || option.Selection.LogicalModelID == f.logicalID
	}
	if !found {
		t.Fatalf("logical model missing when frontend models are enabled: %+v", withLogical.Models)
	}
	if _, err := p.Models(context.Background(), caller, "previs"); platformErrorCode(err) != cp.CodeInvalidRequest {
		t.Fatalf("unknown capability: %v", err)
	}
	if _, err := p.Models(context.Background(), cp.Caller{}, cp.CapabilityImage); platformErrorCode(err) != cp.CodeUnauthorized {
		t.Fatalf("anonymous models: %v", err)
	}
	// 分组无权的渠道不出现在目录里。
	if err := f.svc.EnsureDefaultUserGroup(); err != nil {
		t.Fatal(err)
	}
	f.db.Model(&model.UserGroup{}).Where("code = ?", model.DefaultUserGroupCode).Update("all_channels", false)
	f.svc.invalidateUserGroups()
	restricted, err := p.Models(context.Background(), caller, cp.CapabilityImage)
	if err != nil {
		t.Fatal(err)
	}
	for _, option := range restricted.Models {
		if option.Selection.ChannelID == f.channelID {
			t.Fatalf("restricted group still sees the channel: %+v", restricted.Models)
		}
	}
}

// 未指定 operation 时不能把能力名当作操作名：视频模型只声明 text_to_video / image_to_video。
func TestPlatformEmptyOperationUsesModelDefault(t *testing.T) {
	f := newPlatformImageFixture(t)
	req := f.systemRequest("operation-default")
	req.Operation = ""
	taskReq, err := f.svc.CreationPlatform().(*creationPlatform).platformTaskRequest("user-a", req)
	if err != nil {
		t.Fatal(err)
	}
	if taskReq.Operation != "" {
		t.Fatalf("operation = %q, want empty so the model's default operation applies", taskReq.Operation)
	}
	if _, err := f.svc.CreationPlatform().Submit(context.Background(), cp.Caller{UserID: "user-a"}, req); err != nil {
		t.Fatalf("submit without operation: %v", err)
	}
}
