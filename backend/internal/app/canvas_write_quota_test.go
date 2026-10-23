package app

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"infinite-canvas/backend/internal/canvas/commands"
	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestApplicationConstructorInstallsTransactionalStructuredQuota(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "quota.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB, _ := db.DB(); _ = sqlDB.Close() })
	if err = db.AutoMigrate(&model.User{}, &model.CanvasProject{}, &model.Asset{}, &model.SystemSetting{}); err != nil {
		t.Fatal(err)
	}
	if err = db.Create(&model.User{ID: "owner", Status: model.UserStatusActive}).Error; err != nil {
		t.Fatal(err)
	}
	policy := defaultRuntimePolicy()
	policy.Resource.CanvasCount = 1
	policy.Resource.AssetCount = 1
	raw, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	repo := repository.New(db)
	if err = repo.SaveSystemSetting(&model.SystemSetting{Key: "runtime_policy", ValueJSON: string(raw)}); err != nil {
		t.Fatal(err)
	}
	_ = New(repo, t.TempDir())
	if err = repo.UpsertCanvasProject(&model.CanvasProject{ID: "one", UserID: "owner", PayloadJSON: `{"nodes":[]}`}); err != nil {
		t.Fatal(err)
	}
	assertQuota := func(err error) {
		t.Helper()
		var typed *AppError
		if !errors.As(err, &typed) || typed.Code != CodeQuotaExceeded {
			t.Fatalf("expected app quota rejection, got %v", err)
		}
	}
	assertQuota(repo.UpsertCanvasProject(&model.CanvasProject{ID: "two", UserID: "owner", PayloadJSON: `{"nodes":[]}`}))
	if err = repo.UpsertAsset(&model.Asset{ID: "one", UserID: "owner", PayloadJSON: "{}"}); err != nil {
		t.Fatal(err)
	}
	assertQuota(repo.UpsertAsset(&model.Asset{ID: "two", UserID: "owner", PayloadJSON: "{}"}))
}

func TestAgentSaveAdvancesCommandEntityBaseline(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	if err := database.MigrateCanvasCommands(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.User{ID: "user", Status: model.UserStatusActive}).Error; err != nil {
		t.Fatal(err)
	}
	p := identity.Principal{ID: "user", OwnerID: "user", Role: "owner", Scopes: identity.OwnerScopes()}
	host := commands.NewService(s.repo, commands.ServiceOptions{})
	x, y := float64(0), float64(0)
	before, err := host.Create(context.Background(), p, commands.CreateCanvasRequest{ClientID: "web", OperationID: "create-agent-parity", InitialOperations: []commands.Operation{{Type: "add_node", ID: "text", NodeType: "text", X: &x, Y: &y}}})
	if err != nil {
		t.Fatal(err)
	}
	row, err := s.repo.CanvasProjectForUser("user", before.CanvasID)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err = json.Unmarshal([]byte(row.PayloadJSON), &doc); err != nil {
		t.Fatal(err)
	}
	doc["nodes"].([]any)[0].(map[string]any)["metadata"].(map[string]any)["content"] = "agent edit"
	if err = saveCloudAgentDocument(s.repo, row, doc, mustRuntimePolicy(t, s)); err != nil {
		t.Fatal(err)
	}
	_, err = host.Execute(context.Background(), p, commands.Command{Protocol: 1, CanvasID: before.CanvasID, DocumentEpoch: before.DocumentEpoch, ClientID: "web", OperationID: "stale-web-edit", BaseRevision: before.Revision, ExpectedEntityVersions: before.EntityVersions, Operations: []commands.Operation{{Type: "update_node", ID: "text", Patch: map[string]any{"content": "stale"}}}})
	var conflict *commands.Error
	if !errors.As(err, &conflict) || conflict.Reason != "entity_version_conflict" {
		t.Fatalf("Agent save did not invalidate command baseline: %v", err)
	}
	events, err := host.ReadEvents(context.Background(), p, before.CanvasID, before.EventSeq, 10)
	if err != nil || len(events) != 1 || len(events[0].Changes.Entities) != 1 || events[0].Changes.Entities[0].Key != "node:text" {
		t.Fatalf("Agent event is not recoverable: %+v %v", events, err)
	}
}
