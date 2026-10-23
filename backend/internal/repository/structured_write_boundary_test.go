package repository

import (
	"errors"
	"path/filepath"
	"testing"

	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func structuredWriteFixture(t *testing.T) (*Repository, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "structured.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB, _ := db.DB(); _ = sqlDB.Close() })
	if err = db.AutoMigrate(&model.User{}, &model.CanvasProject{}, &model.Asset{}); err != nil {
		t.Fatal(err)
	}
	if err = db.Create(&model.User{ID: "owner", Status: model.UserStatusActive}).Error; err != nil {
		t.Fatal(err)
	}
	return New(db), db
}

func TestStructuredQuotaSurvivesNewTransactionRepository(t *testing.T) {
	repo, _ := structuredWriteFixture(t)
	quota := errors.New("structured quota")
	repo.SetStructuredWriteQuota(func(*Repository, string, string, int64, int64) error { return quota })
	err := repo.db.Transaction(func(tx *gorm.DB) error {
		return New(tx).UpsertCanvasProject(&model.CanvasProject{ID: "canvas", UserID: "owner", PayloadJSON: `{"nodes":[]}`})
	})
	if !errors.Is(err, quota) {
		t.Fatalf("New(tx) lost quota callback: %v", err)
	}
}

func TestAssetReplacementAndCharacterCreateHonorTransactionalQuota(t *testing.T) {
	repo, db := structuredWriteFixture(t)
	original := model.Asset{ID: "original", UserID: "owner", PayloadJSON: "old"}
	if err := repo.UpsertAsset(&original); err != nil {
		t.Fatal(err)
	}
	quota := errors.New("asset quota")
	repo.SetStructuredWriteQuota(func(tx *Repository, owner, kind string, countDelta, byteDelta int64) error {
		usage, err := tx.CanvasStructuredStorageUsage(owner)
		if err != nil {
			return err
		}
		if kind != "asset" || usage.AssetCount != 1 {
			t.Fatalf("unexpected quota inputs: %s %+v", kind, usage)
		}
		return quota
	})
	if err := repo.ReplaceAssets("owner", []model.Asset{{ID: "new", UserID: "owner", PayloadJSON: "larger"}}); !errors.Is(err, quota) {
		t.Fatalf("replacement guard: %v", err)
	}
	if err := repo.CreateUserCharacter(&model.Asset{ID: "character", UserID: "owner", PayloadJSON: "{}"}, &model.AssetVersion{}, nil, nil); !errors.Is(err, quota) {
		t.Fatalf("character guard: %v", err)
	}
	var count int64
	if err := db.Model(&model.Asset{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("partial asset commit: %d %v", count, err)
	}
	stored, err := repo.AssetForUser("owner", "original")
	if err != nil || stored.PayloadJSON != "old" {
		t.Fatalf("replacement modified original: %+v %v", stored, err)
	}
}

func TestLegacyWriteDoesNotIgnorePartialCommandSchema(t *testing.T) {
	repo, db := structuredWriteFixture(t)
	if err := database.MigrateCanvasCommands(db); err != nil {
		t.Fatal(err)
	}
	row := model.CanvasProject{ID: "canvas", UserID: "owner", PayloadJSON: `{"nodes":[]}`}
	if err := repo.UpsertCanvasProject(&row); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrator().DropTable(&model.CanvasCommandEvent{}); err != nil {
		t.Fatal(err)
	}
	row.PayloadJSON = `{"nodes":[],"title":"failed"}`
	if err := repo.UpsertCanvasProject(&row); err == nil {
		t.Fatal("ignored missing command event table")
	}
	stored, err := repo.CanvasProjectForUser("owner", "canvas")
	if err != nil || stored.Revision != 1 || stored.PayloadJSON != `{"nodes":[]}` {
		t.Fatalf("partial legacy save: %+v %v", stored, err)
	}
}
