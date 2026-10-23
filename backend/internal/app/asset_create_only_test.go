package app

import (
	"encoding/json"
	"errors"
	"testing"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

func TestCreateUserAssetRejectsExistingWithoutReplacingBody(t *testing.T) {
	s, db := syncFixture(t)
	if err := db.AutoMigrate(&model.AssetFolder{}, &model.SystemSetting{}, &model.CanvasProject{}, &model.UserDailyActivity{}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"asset", "foreign-asset"} {
		_, err := s.CreateUserAsset("owner", json.RawMessage(`{"id":"`+id+`","kind":"text","title":"stale","coverUrl":"","tags":[],"data":{"content":"stale"}}`))
		var appErr *AppError
		if !errors.As(err, &appErr) || appErr.Status != 412 || appErr.Reason != "asset_already_exists" {
			t.Fatalf("existing %s: %v", id, err)
		}
	}
	asset, err := s.repo.AssetForUser("owner", "asset")
	if err != nil || asset.Title != "remote title" || asset.PayloadJSON != `{"data":{"content":"remote body","source":"canvas"}}` {
		t.Fatalf("existing overwritten: %+v %v", asset, err)
	}
	created, err := s.CreateUserAsset("owner", json.RawMessage(`{"id":"fresh","kind":"text","title":"fresh","coverUrl":"","tags":[],"data":{"content":"new"}}`))
	if err != nil || created.ID != "fresh" {
		t.Fatalf("create: %+v %v", created, err)
	}
}

func TestCreateUserAssetQuotaAndFolderFailureHaveNoSideEffects(t *testing.T) {
	s, db := syncFixture(t)
	if err := db.AutoMigrate(&model.AssetFolder{}); err != nil {
		t.Fatal(err)
	}
	quota := errors.New("quota exceeded")
	s.repo.SetStructuredWriteQuota(func(_ *repository.Repository, owner, kind string, count, bytes int64) error {
		if owner != "owner" || kind != "asset" || count != 1 || bytes <= 0 {
			t.Fatal("invalid quota input")
		}
		return quota
	})
	_, err := s.CreateUserAsset("owner", json.RawMessage(`{"id":"fresh","kind":"text","title":"fresh","coverUrl":"","tags":[],"data":{"content":"new"}}`))
	if !errors.Is(err, quota) {
		t.Fatalf("quota: %v", err)
	}
	s.repo.SetStructuredWriteQuota(nil)
	_, err = s.CreateUserAsset("owner", json.RawMessage(`{"id":"fresh","folderId":"missing","kind":"text","title":"fresh","coverUrl":"","tags":[],"data":{"content":"new"}}`))
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Status != 400 {
		t.Fatalf("folder: %v", err)
	}
	var count int64
	if err := db.Model(&model.Asset{}).Where("id = ?", "fresh").Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("partial create count=%d %v", count, err)
	}
}

func TestPostgresCreateUserAssetOnlyOneConnectionCreates(t *testing.T) {
	a, b, db := postgresSyncFixture(t)
	start, results := make(chan struct{}), make(chan error, 2)
	for _, svc := range []*Service{a, b} {
		go func(svc *Service) {
			<-start
			results <- svc.repo.CreateAssetIfAbsent(&model.Asset{ID: "fresh", UserID: "owner", Title: "created", PayloadJSON: `{"body":"created"}`})
		}(svc)
	}
	close(start)
	success, conflicts := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			success++
		} else if errors.Is(err, repository.ErrAssetAlreadyExists) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatalf("success=%d conflicts=%d", success, conflicts)
	}
	if err := db.Model(&model.Asset{}).Where("id = ?", "fresh").Updates(map[string]any{"title": "edited", "payload_json": `{"body":"edited"}`}).Error; err != nil {
		t.Fatal(err)
	}
	if err := b.repo.CreateAssetIfAbsent(&model.Asset{ID: "fresh", UserID: "owner", Title: "late stale", PayloadJSON: `{"body":"old"}`}); !errors.Is(err, repository.ErrAssetAlreadyExists) {
		t.Fatalf("late create: %v", err)
	}
	asset, err := a.repo.AssetForUser("owner", "fresh")
	if err != nil || asset.Title != "edited" || asset.PayloadJSON != `{"body":"edited"}` {
		t.Fatalf("late overwrite: %+v %v", asset, err)
	}
}
