package app

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"
	"time"
	"yingce/backend/internal/model"
)

func testRecoveredAssetID(taskID string, index int) string {
	return fmt.Sprintf("generation_%x", sha256.Sum256([]byte(fmt.Sprintf("materialize:%s:%d", taskID, index))))
}

func TestTaskReadExposesRegisteredAssetIdentityAndCanvasOrigin(t *testing.T) {
	s, db := newMediaRecoveryTestService(t)
	now := time.Now()
	canvas := model.CanvasProject{ID: "origin-canvas", UserID: "media-user", Title: "自由画布 7", PayloadJSON: `{"nodes":[]}`, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&canvas).Error; err != nil {
		t.Fatal(err)
	}
	task := model.Task{ID: "origin-task", UserID: canvas.UserID, ProjectID: canvas.ID, Type: "canvas_image", Status: model.TaskStatusSucceeded, ResultJSON: `{"mode":"image","images":[{"storageKey":"resource:r1"},{"storageKey":"resource:r2"}]}`, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	for index, id := range []string{"r1", "r2"} {
		asset := model.Asset{ID: testRecoveredAssetID(task.ID, index), UserID: task.UserID, Kind: "image", Category: model.AssetCategoryMaterial, Status: model.AssetVersionStatusConfirmed, Title: "保留标题", PayloadJSON: `{"id":"` + testRecoveredAssetID(task.ID, index) + `","kind":"image","title":"保留标题","metadata":{"source":"generation-task","taskId":"origin-task"},"data":{"storageKey":"resource:` + id + `","width":2,"height":3}}`, CreatedAt: now, UpdatedAt: now}
		if err := db.Create(&asset).Error; err != nil {
			t.Fatal(err)
		}
	}
	output, err := s.Task(task.UserID, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Images []struct {
			AssetID string `json:"assetId"`
		} `json:"images"`
	}
	if err := json.Unmarshal([]byte(output.ResultJSON), &result); err != nil {
		t.Fatal(err)
	}
	for index, image := range result.Images {
		if image.AssetID != testRecoveredAssetID(task.ID, index) {
			t.Fatalf("output %d asset=%s", index, image.AssetID)
		}
	}
	if _, err := s.Task("other-user", task.ID); err == nil {
		t.Fatal("task access crossed user boundary")
	}
	page, err := s.UserAssetsPage(task.UserID, 1, 20, UserAssetPageFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range page.Assets {
		var asset map[string]any
		if err := json.Unmarshal(raw, &asset); err != nil {
			t.Fatal(err)
		}
		metadata, _ := asset["metadata"].(map[string]any)
		if metadata["canvasId"] != canvas.ID || metadata["canvasName"] != canvas.Title {
			t.Fatalf("origin=%v", metadata)
		}
		if metadata["projectIds"] != nil {
			t.Fatal("free canvas was mislabelled as a business project")
		}
	}
	if len(page.Assets) != 2 {
		t.Fatalf("assets=%d", len(page.Assets))
	}
	var count int64
	if err := db.Model(&model.Asset{}).Where("user_id = ?", task.UserID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("read path changed asset count: %d", count)
	}
}

func TestAssetCanvasOriginsRespectOwnerBoundary(t *testing.T) {
	s, db := newMediaRecoveryTestService(t)
	if err := db.Create(&model.CanvasProject{ID: "private-canvas", UserID: "other-user", Title: "private-name", PayloadJSON: `{}`}).Error; err != nil {
		t.Fatal(err)
	}
	asset := model.Asset{ID: "owned-asset", UserID: "user", Kind: "image", PayloadJSON: `{"id":"owned-asset","metadata":{"canvasId":"private-canvas","canvasName":"unverified-name"}}`}
	rows := []model.Asset{asset}
	if err := s.enrichAssetCanvasOrigins("user", rows); err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(rows[0].PayloadJSON), &document); err != nil {
		t.Fatal(err)
	}
	if document["metadata"].(map[string]any)["canvasName"] != nil {
		t.Fatal("unverified canvas name was exposed")
	}
}

func TestTaskOutputAssetsRejectMismatchedResourceAndProviderAssetID(t *testing.T) {
	s, db := newMediaRecoveryTestService(t)
	task := model.Task{ID: "mismatch-task", UserID: "user", Status: model.TaskStatusSucceeded, ResultJSON: `{"images":[{"storageKey":"resource:new","assetId":"provider-supplied-id"}]}`}
	asset := model.Asset{ID: testRecoveredAssetID(task.ID, 0), UserID: task.UserID, Kind: "image", PayloadJSON: `{"data":{"storageKey":"resource:old"}}`}
	if err := db.Create(&asset).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.bindRegisteredTaskAssets(&task); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Images []map[string]any `json:"images"`
	}
	if err := json.Unmarshal([]byte(task.ResultJSON), &result); err != nil {
		t.Fatal(err)
	}
	if result.Images[0]["assetId"] != nil {
		t.Fatal("mismatched or provider-supplied asset identity was accepted")
	}
}
