package app

import (
	"testing"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMissingOSSSettingsReturnNormalizedDefaults(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.SystemSetting{}, &model.UserOSSSetting{}, &model.StorageLocation{}); err != nil {
		t.Fatal(err)
	}
	svc := New(repository.New(db), t.TempDir())
	admin := &model.User{ID: "admin", Role: model.UserRoleAdmin}

	platformSetting, err := svc.AdminOSSSetting(admin)
	if err != nil {
		t.Fatal(err)
	}
	assertDefaultOSSSetting(t, platformSetting)

	userSetting, err := svc.UserOSSSetting(&model.User{ID: "user-1"})
	if err != nil {
		t.Fatal(err)
	}
	assertDefaultOSSSetting(t, userSetting)
}

func assertDefaultOSSSetting(t *testing.T, value *PublicOSSSetting) {
	t.Helper()
	if value.Provider != aliyunOSSProvider || value.PathPrefix != defaultOSSPathPrefix || value.S3Preset != "custom" {
		t.Fatalf("default OSS setting = %+v", value)
	}
	if value.CDNAttachmentQueryForwarding {
		t.Fatal("CDN attachment contract must default to unconfirmed")
	}
}

func TestOSSAttachmentContractSurvivesPlatformAndUserSettings(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.SystemSetting{}, &model.UserOSSSetting{}, &model.StorageLocation{}, &model.Resource{}); err != nil {
		t.Fatal(err)
	}
	svc := New(repository.New(db), t.TempDir())
	admin := &model.User{ID: "admin", Role: model.UserRoleAdmin}
	request := OSSSettingRequest{PublicBaseURL: "https://canvas.example.com", CDNAttachmentQueryForwarding: true, CDNAuthMode: "public", CDNBaseURL: "https://cdn.example.com"}
	platform, err := svc.UpdateOSSSetting(admin, request)
	if err != nil {
		t.Fatal(err)
	}
	if !platform.CDNAttachmentQueryForwarding {
		t.Fatal("platform response dropped contract")
	}
	loaded, err := svc.AdminOSSSetting(admin)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.CDNAttachmentQueryForwarding {
		t.Fatal("persisted platform contract lost")
	}
	user := &model.User{ID: "user"}
	personal, err := svc.UpdateUserOSSSetting(user, request)
	if err != nil {
		t.Fatal(err)
	}
	if !personal.CDNAttachmentQueryForwarding {
		t.Fatal("user response dropped contract")
	}
	request.CDNAttachmentQueryForwarding = false
	personal, err = svc.UpdateUserOSSSetting(user, request)
	if err != nil {
		t.Fatal(err)
	}
	if personal.CDNAttachmentQueryForwarding {
		t.Fatal("user could not revoke attachment contract")
	}
}
