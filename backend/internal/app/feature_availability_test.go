package app

import (
	"errors"
	"testing"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/platform"
	"infinite-canvas/backend/internal/protocol"
	"infinite-canvas/backend/internal/repository"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestFeatureAvailabilityDefaultsToDisableFrontendModels(t *testing.T) {
	svc, _ := newFeatureAvailabilityTestService(t)

	setting, err := svc.FeatureAvailability()
	if err != nil {
		t.Fatal(err)
	}
	if !setting.WelcomeEnabled {
		t.Fatal("welcome should be enabled by default")
	}
	if setting.Configured || !setting.ShortDramaEnabled || !setting.TaskCenterEnabled || !setting.CreditsEnabled || !setting.CustomChannelsEnabled || setting.FrontendModelsEnabled || !setting.PluginCenterEnabled || !setting.SystemPluginsVisibleToUsers {
		t.Fatalf("FeatureAvailability() = %#v", setting)
	}
}

func TestFeatureAvailabilityLegacySettingKeepsCustomChannelsEnabled(t *testing.T) {
	svc, db := newFeatureAvailabilityTestService(t)
	legacy := &model.SystemSetting{Key: featureAvailabilitySettingKey, ValueJSON: `{"shortDramaEnabled":false,"taskCenterEnabled":true,"creditsEnabled":true}`}
	if err := db.Create(legacy).Error; err != nil {
		t.Fatal(err)
	}

	setting, err := svc.FeatureAvailability()
	if err != nil {
		t.Fatal(err)
	}
	if setting.ShortDramaEnabled || !setting.CustomChannelsEnabled || setting.FrontendModelsEnabled || !setting.PluginCenterEnabled || !setting.SystemPluginsVisibleToUsers {
		t.Fatalf("FeatureAvailability() = %#v", setting)
	}
	if !setting.WelcomeEnabled {
		t.Fatal("existing settings without welcomeEnabled should keep welcome enabled")
	}
}

func TestFeatureAvailabilityDefaultsDirectorDeskToOffAndRejectsUnknownMode(t *testing.T) {
	svc, db := newFeatureAvailabilityTestService(t)

	defaultValue, err := svc.FeatureAvailability()
	if err != nil {
		t.Fatal(err)
	}
	if defaultValue.DirectorDeskPrevisMode != platform.DirectorDeskPrevisOff {
		t.Fatalf("default directorDeskPrevisMode = %q, want %q", defaultValue.DirectorDeskPrevisMode, platform.DirectorDeskPrevisOff)
	}

	if err := db.Create(&model.SystemSetting{Key: featureAvailabilitySettingKey, ValueJSON: `{"directorDeskPrevisMode":"unsafe"}`}).Error; err != nil {
		t.Fatal(err)
	}
	value, err := svc.FeatureAvailability()
	if err != nil {
		t.Fatal(err)
	}
	if value.DirectorDeskPrevisMode != platform.DirectorDeskPrevisOff {
		t.Fatalf("unknown directorDeskPrevisMode = %q, want %q", value.DirectorDeskPrevisMode, platform.DirectorDeskPrevisOff)
	}
}

func TestWelcomeAvailabilityCanBeDisabledAndReenabled(t *testing.T) {
	svc, _ := newFeatureAvailabilityTestService(t)
	actor := &model.User{ID: "admin-welcome", Role: model.UserRoleAdmin}
	for _, enabled := range []bool{false, true} {
		value := platform.DefaultFeatureAvailability()
		value.WelcomeEnabled = enabled
		if _, err := svc.UpdateFeatureAvailability(actor, value); err != nil {
			t.Fatal(err)
		}
		setting, err := svc.FeatureAvailability()
		if err != nil {
			t.Fatal(err)
		}
		if setting.WelcomeEnabled != enabled {
			t.Fatalf("welcomeEnabled = %v, want %v", setting.WelcomeEnabled, enabled)
		}
	}
}

func TestUpdateFeatureAvailabilityPersistsAndAudits(t *testing.T) {
	svc, db := newFeatureAvailabilityTestService(t)
	actor := &model.User{ID: "admin-1", Role: model.UserRoleAdmin}
	want := FeatureAvailability{ShortDramaEnabled: false, TaskCenterEnabled: true, CreditsEnabled: false, CustomChannelsEnabled: true, PluginCenterEnabled: false, SystemPluginsVisibleToUsers: false}

	setting, err := svc.UpdateFeatureAvailability(actor, want)
	if err != nil {
		t.Fatal(err)
	}
	if !setting.Configured || setting.ShortDramaEnabled || !setting.TaskCenterEnabled || setting.CreditsEnabled || !setting.CustomChannelsEnabled || setting.PluginCenterEnabled || setting.SystemPluginsVisibleToUsers {
		t.Fatalf("UpdateFeatureAvailability() = %#v", setting)
	}
	if enabled, err := svc.FeatureEnabled(FeaturePluginCenter); err != nil || enabled {
		t.Fatalf("FeatureEnabled(pluginCenter) = %v, %v", enabled, err)
	}
	if err := svc.RequireFeature(FeatureShortDrama); err == nil {
		t.Fatal("RequireFeature(shortDrama) error = nil")
	} else {
		var authErr *AuthError
		if !errors.As(err, &authErr) || authErr.Status != 403 {
			t.Fatalf("RequireFeature(shortDrama) error = %#v", err)
		}
	}
	var auditCount int64
	if err := db.Model(&model.AdminAuditEvent{}).Where("action = ?", "feature_availability.update").Count(&auditCount).Error; err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("audit count = %d, want 1", auditCount)
	}
}

func TestPluginsForUserKeepsOfficialApplicationsAndHidesManagedPlugins(t *testing.T) {
	svc, _ := newFeatureAvailabilityTestService(t)
	admin := &model.User{ID: "admin-1", Role: model.UserRoleAdmin}
	if _, err := svc.UpdateFeatureAvailability(admin, FeatureAvailability{
		ShortDramaEnabled: true, TaskCenterEnabled: true, CreditsEnabled: true,
		CustomChannelsEnabled: true, PluginCenterEnabled: true, SystemPluginsVisibleToUsers: false,
	}); err != nil {
		t.Fatal(err)
	}
	svc.pluginRuntime = &pluginRuntime{plugins: map[string]pluginRecord{
		"bundled":  {Source: "bundled", Metadata: protocol.Metadata{ID: "bundled", Name: "系统协议", Version: "1"}},
		"uploaded": {Source: "uploaded", Metadata: protocol.Metadata{ID: "uploaded", Name: "自定义协议", Version: "1"}},
		"app":      {Source: "bundled", Metadata: protocol.Metadata{ID: WorkflowPluginRunningHub, Name: "官方应用", Version: "1"}},
	}}

	visibleToUser, err := svc.PluginsForUser(&model.User{ID: "user-1", Role: model.UserRoleUser})
	if err != nil {
		t.Fatal(err)
	}
	if len(visibleToUser) != 1 || visibleToUser[0].Manifest.ID != WorkflowPluginRunningHub {
		t.Fatalf("PluginsForUser(user) = %#v", visibleToUser)
	}
	visibleToAdmin, err := svc.PluginsForUser(admin)
	if err != nil {
		t.Fatal(err)
	}
	if len(visibleToAdmin) != 3 {
		t.Fatalf("PluginsForUser(admin) = %#v", visibleToAdmin)
	}
}

func TestCustomChannelTaskInputRequiresFeature(t *testing.T) {
	svc, _ := newFeatureAvailabilityTestService(t)
	actor := &model.User{ID: "admin-1", Role: model.UserRoleAdmin}
	if _, err := svc.UpdateFeatureAvailability(actor, FeatureAvailability{ShortDramaEnabled: true, TaskCenterEnabled: true, CreditsEnabled: true, CustomChannelsEnabled: false}); err != nil {
		t.Fatal(err)
	}

	customInput, err := normalizeTaskInput(map[string]any{"config": providerConfig{BaseURL: "https://example.com", APIKey: "private-key", Model: "text-model"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.requireCustomChannelsForTaskInput(customInput); err == nil {
		t.Fatal("custom channel task must be rejected when the feature is disabled")
	}
	systemInput, err := normalizeTaskInput(map[string]any{"config": providerConfig{ChannelID: "system-1", BaseURL: "/api/ai/system/system-1", APIKey: "system", Model: "text-model"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.requireCustomChannelsForTaskInput(systemInput); err != nil {
		t.Fatalf("system channel task error = %v", err)
	}
	legacySystemInput, err := normalizeTaskInput(map[string]any{"config": providerConfig{BaseURL: "/api/ai/system/system-1", APIKey: "system", Model: "text-model"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.requireCustomChannelsForTaskInput(legacySystemInput); err != nil {
		t.Fatalf("legacy system proxy task error = %v", err)
	}
}

func TestCreateTaskDoesNotClassifyCustomChannelAsMissingSystemModel(t *testing.T) {
	svc, _ := newFeatureAvailabilityTestService(t)
	actor := &model.User{ID: "admin-1", Role: model.UserRoleAdmin}
	if _, err := svc.UpdateFeatureAvailability(actor, FeatureAvailability{ShortDramaEnabled: true, TaskCenterEnabled: true, CreditsEnabled: true, CustomChannelsEnabled: false, FrontendModelsEnabled: true}); err != nil {
		t.Fatal(err)
	}

	_, err := svc.CreateTask("user-1", CreateTaskRequest{
		Type:   "canvas_text",
		Prompt: "test custom channel",
		Input: map[string]any{
			"mode":   "text",
			"config": providerConfig{BaseURL: "https://example.com/v1", APIKey: "private-key", Model: "text-model"},
		},
	})
	if err == nil {
		t.Fatal("CreateTask() error = nil")
	}
	var authErr *AuthError
	if !errors.As(err, &authErr) || authErr.Message != "自定义渠道暂未开放" {
		t.Fatalf("CreateTask() error = %#v, want custom channel feature error", err)
	}
}

func TestTaskBillingOrderSkipsPricingWhenCreditsDisabled(t *testing.T) {
	svc, _ := newFeatureAvailabilityTestService(t)
	actor := &model.User{ID: "admin-1", Role: model.UserRoleAdmin}
	if _, err := svc.UpdateFeatureAvailability(actor, FeatureAvailability{ShortDramaEnabled: true, TaskCenterEnabled: true, CreditsEnabled: false, CustomChannelsEnabled: true}); err != nil {
		t.Fatal(err)
	}

	order, err := svc.taskBillingOrder("user-1", &model.Task{ID: "task-1"}, map[string]any{"config": map[string]any{"channelId": "missing", "model": "missing"}})
	if err != nil {
		t.Fatal(err)
	}
	if order != nil {
		t.Fatalf("taskBillingOrder() = %#v, want nil", order)
	}
}

// 灰度分群全链路:管理员配置 canary 白名单/比例 → 用户视角按组解析,
// 全局视角(管理端)保留原始分组参数,off/on 覆盖分组。
func TestUserFeatureAvailabilityResolvesCanaryGrouping(t *testing.T) {
	svc, db := newFeatureAvailabilityTestService(t)
	admin := &model.User{ID: "admin-1", Role: model.UserRoleAdmin}
	internalUser := &model.User{ID: "internal-1", Role: model.UserRoleUser}
	otherUser := &model.User{ID: "other-1", Role: model.UserRoleUser}

	value := FeatureAvailability{
		ShortDramaEnabled: true, TaskCenterEnabled: true, CreditsEnabled: true,
		CustomChannelsEnabled: true, PluginCenterEnabled: true, SystemPluginsVisibleToUsers: true,
		DirectorDeskPrevisMode:        platform.DirectorDeskPrevisCanary,
		DirectorDeskPrevisCanaryUsers: []string{"internal-1"},
	}
	if _, err := svc.UpdateFeatureAvailability(admin, value); err != nil {
		t.Fatal(err)
	}

	global, err := svc.AdminFeatureAvailability(admin)
	if err != nil {
		t.Fatal(err)
	}
	if global.DirectorDeskPrevisMode != platform.DirectorDeskPrevisCanary || len(global.DirectorDeskPrevisCanaryUsers) != 1 || global.DirectorDeskPrevisCanaryUsers[0] != "internal-1" {
		t.Fatalf("admin view lost canary grouping: %#v", global)
	}

	internal, err := svc.UserFeatureAvailability(internalUser)
	if err != nil {
		t.Fatal(err)
	}
	if internal.DirectorDeskPrevisMode != platform.DirectorDeskPrevisCanary {
		t.Fatalf("allowlisted user mode = %q, want canary", internal.DirectorDeskPrevisMode)
	}
	other, err := svc.UserFeatureAvailability(otherUser)
	if err != nil {
		t.Fatal(err)
	}
	if other.DirectorDeskPrevisMode != platform.DirectorDeskPrevisOff {
		t.Fatalf("unlisted user with zero ratio mode = %q, want off", other.DirectorDeskPrevisMode)
	}

	// 比例桶生效:10000 → 全体(有账号者)进 canary。
	value.DirectorDeskPrevisCanaryBasisPoints = 10000
	if _, err := svc.UpdateFeatureAvailability(admin, value); err != nil {
		t.Fatal(err)
	}
	otherFull, err := svc.UserFeatureAvailability(otherUser)
	if err != nil {
		t.Fatal(err)
	}
	if otherFull.DirectorDeskPrevisMode != platform.DirectorDeskPrevisCanary {
		t.Fatalf("full-ratio user mode = %q, want canary", otherFull.DirectorDeskPrevisMode)
	}

	// 全局 on 覆盖分组;全局 off 关闭分组。
	value.DirectorDeskPrevisMode = platform.DirectorDeskPrevisOn
	if _, err := svc.UpdateFeatureAvailability(admin, value); err != nil {
		t.Fatal(err)
	}
	onView, err := svc.UserFeatureAvailability(otherUser)
	if err != nil || onView.DirectorDeskPrevisMode != platform.DirectorDeskPrevisOn {
		t.Fatalf("mode=on view = %#v, %v", onView, err)
	}
	value.DirectorDeskPrevisMode = platform.DirectorDeskPrevisOff
	if _, err := svc.UpdateFeatureAvailability(admin, value); err != nil {
		t.Fatal(err)
	}
	offView, err := svc.UserFeatureAvailability(internalUser)
	if err != nil || offView.DirectorDeskPrevisMode != platform.DirectorDeskPrevisOff {
		t.Fatalf("mode=off view = %#v, %v", offView, err)
	}

	// 配置持久化保留分组字段。
	var rows int64
	if err := db.Model(&model.SystemSetting{}).Where("key = ?", featureAvailabilitySettingKey).Count(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("feature setting rows = %d, want 1", rows)
	}
}

func newFeatureAvailabilityTestService(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	if err := db.AutoMigrate(&model.SystemSetting{}, &model.AdminAuditEvent{}); err != nil {
		t.Fatal(err)
	}
	return New(repository.New(db), t.TempDir()), db
}
