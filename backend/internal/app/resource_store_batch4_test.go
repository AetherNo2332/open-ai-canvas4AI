package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/gorm"
	"yingce/backend/internal/database"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func newBatch4ResourceStoreService(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	db := newSQLiteTestDB(t)
	if err := database.MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	return New(repository.New(db), t.TempDir()), db
}

func batch4DailyUploadBytes(t *testing.T, db *gorm.DB, userID string) int64 {
	t.Helper()
	var total int64
	if err := db.Model(&model.UserDailyUploadUsage{}).
		Where("user_id = ?", userID).
		Select("COALESCE(SUM(bytes), 0)").
		Scan(&total).Error; err != nil {
		t.Fatal(err)
	}
	return total
}

func TestBatch4ResourceStoreSuccessCommitsObjectRecordAndQuota(t *testing.T) {
	svc, db := newBatch4ResourceStoreService(t)
	const userID = "batch4-success"
	data := mediaTestPNG(t)

	resource, err := svc.UploadResourceFile(userID, "picture.png", int64(len(data)), "image", 0, 0, 0, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if resource == nil || resource.Status != model.ResourceStatusReady || resource.Provider != "local" {
		t.Fatalf("resource = %#v", resource)
	}
	stored, err := svc.repo.ResourceForUser(userID, resource.ID)
	if err != nil {
		t.Fatal(err)
	}
	objectPath := filepath.Join(svc.dataDir, "resources", filepath.FromSlash(stored.ObjectKey))
	payload, err := os.ReadFile(objectPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(payload, data) {
		t.Fatal("stored object bytes changed")
	}
	opened, body, err := svc.OpenResource(userID, stored.ID)
	if err != nil {
		t.Fatal(err)
	}
	if opened.ID != stored.ID {
		t.Fatalf("opened resource = %q, want %q", opened.ID, stored.ID)
	}
	read, err := io.ReadAll(body)
	closeErr := body.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("read=%v close=%v", err, closeErr)
	}
	if !bytes.Equal(read, data) {
		t.Fatal("opened object bytes changed")
	}
	if got := svc.pendingStorage[userID]; got != 0 {
		t.Fatalf("pending storage = %d, want 0", got)
	}
	if got := batch4DailyUploadBytes(t, db, userID); got != int64(len(data)) {
		t.Fatalf("daily upload bytes = %d, want %d", got, len(data))
	}
}

type batch4CancelAfterReader struct {
	data       []byte
	offset     int
	seekStarts int
	partial    int
}

func (r *batch4CancelAfterReader) Read(buffer []byte) (int, error) {
	if r.seekStarts >= 2 && r.offset >= r.partial {
		return 0, context.Canceled
	}
	if r.offset >= len(r.data) {
		return 0, io.EOF
	}
	end := len(r.data)
	if r.seekStarts >= 2 && end > r.partial {
		end = r.partial
	}
	count := copy(buffer, r.data[r.offset:end])
	r.offset += count
	return count, nil
}

func (r *batch4CancelAfterReader) Seek(offset int64, whence int) (int64, error) {
	if whence != io.SeekStart || offset != 0 {
		return 0, errors.New("unexpected seek")
	}
	r.offset = 0
	r.seekStarts++
	return 0, nil
}

func TestBatch4ResourceStoreCancellationLeavesFailedRowPartialObjectAndReleasedQuota(t *testing.T) {
	svc, db := newBatch4ResourceStoreService(t)
	const userID = "batch4-cancel"
	data := mediaTestPNG(t)
	const partial = 8
	reader := &batch4CancelAfterReader{data: data, partial: partial}

	resource, err := svc.UploadResourceFile(userID, "picture.png", int64(len(data)), "image", 0, 0, 0, reader)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("upload error = %v, want context.Canceled", err)
	}
	if resource != nil {
		t.Fatalf("resource = %#v, want nil", resource)
	}
	var stored model.Resource
	if err := db.First(&stored, "user_id = ?", userID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != model.ResourceStatusFailed || stored.Error == "" {
		t.Fatalf("stored resource = %#v", stored)
	}
	payload, err := os.ReadFile(filepath.Join(svc.dataDir, "resources", filepath.FromSlash(stored.ObjectKey)))
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) != partial {
		t.Fatalf("partial object bytes = %d, want %d", len(payload), partial)
	}
	if got := svc.pendingStorage[userID]; got != 0 {
		t.Fatalf("pending storage = %d, want 0", got)
	}
	if got := batch4DailyUploadBytes(t, db, userID); got != 0 {
		t.Fatalf("daily upload bytes = %d, want 0", got)
	}
}

func TestBatch4ResourceStoreWriteFailureLeavesFailedRowNoObjectAndReleasedQuota(t *testing.T) {
	svc, db := newBatch4ResourceStoreService(t)
	const userID = "batch4-write-failure"
	data := mediaTestPNG(t)
	blockedDataDir := filepath.Join(t.TempDir(), "data-dir-is-a-file")
	if err := os.WriteFile(blockedDataDir, []byte("blocked"), 0o640); err != nil {
		t.Fatal(err)
	}
	svc.dataDir = blockedDataDir

	resource, err := svc.UploadResourceFile(userID, "picture.png", int64(len(data)), "image", 0, 0, 0, bytes.NewReader(data))
	if err == nil {
		t.Fatal("upload error = nil, want write failure")
	}
	if resource != nil {
		t.Fatalf("resource = %#v, want nil", resource)
	}
	var stored model.Resource
	if err := db.First(&stored, "user_id = ?", userID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != model.ResourceStatusFailed || stored.Error == "" {
		t.Fatalf("stored resource = %#v", stored)
	}
	if _, err := os.Stat(filepath.Join(blockedDataDir, "resources")); err == nil {
		t.Fatal("resources directory unexpectedly exists under a file")
	}
	if got := svc.pendingStorage[userID]; got != 0 {
		t.Fatalf("pending storage = %d, want 0", got)
	}
	if got := batch4DailyUploadBytes(t, db, userID); got != 0 {
		t.Fatalf("daily upload bytes = %d, want 0", got)
	}
}

func TestBatch4ResourceStoreReadyRegistrationFailureRollsBackObjectRowAndQuota(t *testing.T) {
	svc, db := newBatch4ResourceStoreService(t)
	const userID = "batch4-registration-failure"
	data := mediaTestPNG(t)
	if err := db.Callback().Update().Before("gorm:update").Register("batch4:reject_ready_resource", func(tx *gorm.DB) {
		resource, ok := tx.Statement.Dest.(*model.Resource)
		if tx.Statement.Table == "resources" && ok && resource.Status == model.ResourceStatusReady {
			tx.AddError(errors.New("forced ready registration failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}

	resource, err := svc.UploadResourceFile(userID, "picture.png", int64(len(data)), "image", 0, 0, 0, bytes.NewReader(data))
	if err == nil || !strings.Contains(err.Error(), "forced ready registration failure") {
		t.Fatalf("upload error = %v", err)
	}
	if resource != nil {
		t.Fatalf("resource = %#v, want nil", resource)
	}
	var resourceCount int64
	if err := db.Model(&model.Resource{}).Where("user_id = ?", userID).Count(&resourceCount).Error; err != nil {
		t.Fatal(err)
	}
	if resourceCount != 0 {
		t.Fatalf("resource count = %d, want 0", resourceCount)
	}
	root := filepath.Join(svc.dataDir, "resources")
	objectFiles := 0
	walkErr := filepath.WalkDir(root, func(_ string, entry fs.DirEntry, walkErr error) error {
		if errors.Is(walkErr, os.ErrNotExist) {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			objectFiles++
		}
		return nil
	})
	if walkErr != nil {
		t.Fatal(walkErr)
	}
	if objectFiles != 0 {
		t.Fatalf("object files = %d, want 0", objectFiles)
	}
	if got := svc.pendingStorage[userID]; got != 0 {
		t.Fatalf("pending storage = %d, want 0", got)
	}
	if got := batch4DailyUploadBytes(t, db, userID); got != 0 {
		t.Fatalf("daily upload bytes = %d, want 0", got)
	}
}
