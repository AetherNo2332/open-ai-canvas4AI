package canvas

import (
	"encoding/json"
	"testing"
	"time"

	"yingce/backend/internal/model"
)

func TestClientAssetPayloadRepairsWorkflowAssetDocument(t *testing.T) {
	createdAt := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	asset := model.Asset{
		ID:   "workflow-asset-test",
		Kind: "video", Title: "镜头01 · 视频",
		Category: model.AssetCategoryMaterial, Status: model.AssetVersionStatusConfirmed,
		PayloadJSON: `{
			"id":"workflow-asset-test",
			"kind":"video",
			"title":"镜头01 · 视频",
			"category":"material",
			"status":"confirmed",
			"tags":[],
			"data":{"url":"/api/resources/res-1/file","storageKey":"resource:res-1","width":0,"height":0,"bytes":1,"mimeType":"video/mp4"}
		}`,
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}

	raw := ClientAssetPayload(asset)
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["coverUrl"] != "/api/resources/res-1/file" {
		t.Fatalf("coverUrl = %#v", payload["coverUrl"])
	}
	if payload["createdAt"] == nil || payload["updatedAt"] == nil {
		t.Fatalf("timestamps missing: %#v", payload)
	}
	data := payload["data"].(map[string]any)
	if data["width"].(float64) != 1 || data["height"].(float64) != 1 {
		t.Fatalf("dimensions = %#v", data)
	}
}

func TestClientAssetPayloadPreservesCompleteDocument(t *testing.T) {
	rawInput := `{"id":"asset-1","kind":"image","title":"完整","coverUrl":"https://example.com/a.png","tags":["角色"],"createdAt":"2026-08-29T00:00:00.000Z","updatedAt":"2026-08-29T00:00:00.000Z","data":{"dataUrl":"https://example.com/a.png","width":2,"height":3,"bytes":1,"mimeType":"image/png"}}`
	asset := model.Asset{PayloadJSON: rawInput}
	raw := ClientAssetPayload(asset)
	var before map[string]any
	var after map[string]any
	if err := json.Unmarshal([]byte(rawInput), &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &after); err != nil {
		t.Fatal(err)
	}
	beforeJSON, _ := json.Marshal(before)
	afterJSON, _ := json.Marshal(after)
	if string(beforeJSON) != string(afterJSON) {
		t.Fatalf("payload changed: %s", raw)
	}
}

func TestClientAssetPayloadUsesPersistedIdentity(t *testing.T) {
	asset := model.Asset{
		ID: "actual-id", Kind: "video", Title: "当前标题", Category: model.AssetCategoryMaterial,
		Status: model.AssetVersionStatusConfirmed, PrimaryVersionID: "current-version",
		PayloadJSON: `{"id":"stale-id","kind":"image","title":"旧标题","folderId":"removed-folder","primaryVersionId":"old-version","data":{"url":"/api/resources/video/file","width":1920,"height":1080,"bytes":1024,"mimeType":"video/mp4"}}`,
	}
	var payload map[string]any
	if err := json.Unmarshal(ClientAssetPayload(asset), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["id"] != "actual-id" || payload["kind"] != "video" || payload["title"] != "当前标题" || payload["primaryVersionId"] != "current-version" {
		t.Fatalf("response returned stale payload identity: %#v", payload)
	}
	if _, exists := payload["folderId"]; exists {
		t.Fatal("response restored a removed folder")
	}
}

func TestClientAssetPayloadDoesNotInventMissingMedia(t *testing.T) {
	asset := model.Asset{
		ID: "broken-video", Kind: "video", Title: "缺少资源信息",
		PayloadJSON: `{"data":{"width":1920,"height":1080,"mimeType":"video/mp4"}}`,
	}
	raw := ClientAssetPayload(asset)
	if _, err := AssetFromJSON("owner", raw); err == nil {
		t.Fatalf("invalid media unexpectedly became writable: %s", raw)
	}
}

func TestClientAssetListPayloadDropsVerboseMetadata(t *testing.T) {
	asset := model.Asset{PayloadJSON: `{"id":"asset-1","kind":"image","title":"列表","coverUrl":"https://example.com/a.png","tags":[],"data":{"dataUrl":"https://example.com/a.png","width":2,"height":3,"bytes":1,"mimeType":"image/png"},"metadata":{"prompt":"很长的生成提示词","nodeId":"node-1","projectIds":["project-1"]}}`}
	var payload map[string]any
	if err := json.Unmarshal(ClientAssetListPayload(asset), &payload); err != nil {
		t.Fatal(err)
	}
	metadata := payload["metadata"].(map[string]any)
	if _, found := metadata["prompt"]; found {
		t.Fatalf("list payload still contains prompt: %#v", metadata)
	}
	if _, found := metadata["nodeId"]; found {
		t.Fatalf("list payload still contains node metadata: %#v", metadata)
	}
	if metadata["projectIds"].([]any)[0] != "project-1" {
		t.Fatalf("list payload dropped useful metadata: %#v", metadata)
	}
	if payload["coverUrl"] != "" {
		t.Fatalf("duplicate cover URL was not removed: %#v", payload["coverUrl"])
	}
}

// 早期生成结果以 "ready" 入库；下发给浏览器时必须落在素材合同的状态里，否则整张素材列表被拒收。
func TestClientAssetPayloadMapsLegacyReadyStatus(t *testing.T) {
	for stored, want := range map[model.AssetVersionStatus]any{"ready": "confirmed", "confirmed": "confirmed", "bogus": nil} {
		raw := ClientAssetPayload(model.Asset{ID: "task-output-x-0", Kind: "image", Title: "图", Status: stored, PayloadJSON: `{"id":"task-output-x-0","kind":"image","data":{"url":"/a.png"}}`})
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}
		if got := payload["status"]; (want == nil && got != nil) || (want != nil && got != model.AssetVersionStatus(want.(string)) && got != want) {
			t.Fatalf("stored %q -> status %#v, want %#v", stored, got, want)
		}
	}
}

// 早期视频生成结果把缺失的 hasAudio 写成 null，浏览器会拒收整张素材列表（“hasAudio 必须是布尔值”）。
func TestClientAssetPayloadDropsNullHasAudio(t *testing.T) {
	for raw, want := range map[string]any{`null`: nil, `true`: true, `false`: false, `"yes"`: nil} {
		payload := ClientAssetPayload(model.Asset{ID: "task-output-v-0", Kind: "video", Title: "视频", Status: "confirmed", PayloadJSON: `{"id":"task-output-v-0","kind":"video","data":{"url":"/v.mp4","storageKey":"resource:v","width":720,"height":1280,"mimeType":"video/mp4","hasAudio":` + raw + `}}`})
		var decoded map[string]any
		if err := json.Unmarshal(payload, &decoded); err != nil {
			t.Fatal(err)
		}
		data := decoded["data"].(map[string]any)
		got, present := data["hasAudio"]
		if (want == nil && present) || (want != nil && got != want) {
			t.Fatalf("hasAudio %s -> %#v (present=%v), want %#v", raw, got, present, want)
		}
	}
}
