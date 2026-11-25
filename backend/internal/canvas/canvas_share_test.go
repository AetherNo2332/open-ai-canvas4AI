package canvas

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"yingce/backend/internal/assets"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestCanvasShareTokenHashDoesNotExposeToken(t *testing.T) {
	token := "example-share-token"
	first := canvasShareTokenHash(token)
	if first == token || first != canvasShareTokenHash(token) || len(first) != 64 {
		t.Fatalf("unexpected token hash: %q", first)
	}
}

func TestPublicCanvasProjectScrubsSecretsAndRewritesResources(t *testing.T) {
	payload := map[string]any{
		"id": "project-1", "title": "公开画布", "chatSessions": []any{map[string]any{"secret": "chat"}},
		"nodes": []any{map[string]any{
			"id": "node-1", "type": "image", "title": "镜头", "position": map[string]any{"x": 1, "y": 2}, "width": 320, "height": 240,
			"metadata": map[string]any{
				"content": "data:image/png;base64,secret", "storageKey": "resource:resource_123", "prompt": "保留提示词",
				"apiKey": "secret", "taskId": "task-1", "skillSnapshot": map[string]any{"apiKey": "nested-secret", "name": "技能"},
			},
		}},
		"connections": []any{},
	}
	raw, _ := json.Marshal(payload)
	project := &model.CanvasProject{ID: "project-1", Title: "公开画布", PayloadJSON: string(raw)}
	public, resources, err := publicCanvasProject(project, "share-token")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(public)
	text := string(encoded)
	for _, forbidden := range []string{"data:image", "secret", "task-1", `"secret":"chat"`} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("public payload leaked %q: %s", forbidden, text)
		}
	}
	if !strings.Contains(text, "保留提示词") || !strings.Contains(text, "/api/public/canvas-shares/share-token/resources/resource_123/file") {
		t.Fatalf("public payload lost expected data: %s", text)
	}
	if !resources["resource_123"] {
		t.Fatal("referenced resource was not authorized")
	}
}

func TestCanvasResourceIDRejectsInvalidValues(t *testing.T) {
	for _, value := range []string{"resource:", "resource:../secret", "/api/resources/a%2Fb/file", "resource:" + strings.Repeat("a", 81)} {
		if got := assets.ResourceID(value); got != "" {
			t.Fatalf("expected invalid resource id for %q, got %q", value, got)
		}
	}
	if got := assets.ResourceID("/api/resources/abc_123/file"); got != "abc_123" {
		t.Fatalf("unexpected resource id: %q", got)
	}
}

func TestPublicCanvasProjectDropsUnmanagedMediaURL(t *testing.T) {
	payload := `{"id":"p","nodes":[{"id":"n","type":"image","title":"image","position":{"x":0,"y":0},"width":10,"height":10,"metadata":{"content":"https://tracker.example/image.png","prompt":"public prompt"}}],"connections":[]}`
	public, _, err := publicCanvasProject(&model.CanvasProject{ID: "p", PayloadJSON: payload}, "share-token")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(public)
	if strings.Contains(string(encoded), "tracker.example") || !strings.Contains(string(encoded), "public prompt") {
		t.Fatalf("unexpected public payload: %s", encoded)
	}
}

func TestPublicCanvasProjectKeepsSubmapRelationsAndScope(t *testing.T) {
	payload := `{
		"id":"p","nodes":[
			{"id":"submap-1","type":"submap","title":"镜头","position":{"x":0,"y":0},"width":320,"height":240,"metadata":{"collapsed":true,"workflowId":"storyboard","workflowVersion":1}},
			{"id":"submap-1__text-1","type":"text","scopeId":"submap-1","title":"台词","position":{"x":20,"y":40},"width":180,"height":80,"metadata":{"content":"第一句"}},
			{"id":"submap-1__image-1","type":"image","scopeId":"submap-1","title":"首帧","position":{"x":20,"y":140},"width":180,"height":120,"metadata":{}},
			{"id":"submap-1__video-1","type":"video","scopeId":"submap-1","title":"视频","position":{"x":240,"y":140},"width":180,"height":120,"metadata":{"referenceNodeIds":["submap-1__image-1"],"referenceTextNodeIds":["submap-1__text-1"],"videoStartFrameNodeId":"submap-1__image-1","derivedFromNodeIds":["submap-1__image-1"],"generationSpec":{"referenceBindings":[{"id":"private-binding","nodeId":"submap-1__image-1","role":"first-frame","mediaType":"image","assetId":"private-asset","resourceId":"private-resource"},{"id":"private-asset-only","role":"reference","mediaType":"image","assetId":"private-asset"}]}}}
		],"connections":[]
	}`
	public, _, err := publicCanvasProject(&model.CanvasProject{ID: "p", PayloadJSON: payload}, "share-token")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(public)
	text := string(encoded)
	for _, expected := range []string{`"scopeId":"submap-1"`, `"referenceNodeIds":["submap-1__image-1"]`, `"referenceTextNodeIds":["submap-1__text-1"]`, `"videoStartFrameNodeId":"submap-1__image-1"`, `"derivedFromNodeIds":["submap-1__image-1"]`, `"collapsed":true`, `"workflowId":"storyboard"`, `"inputBindings":[{"id":"node:submap-1__image-1:first-frame"`} {
		if !strings.Contains(text, expected) {
			t.Fatalf("public payload lost %q: %s", expected, text)
		}
	}
	if strings.Contains(text, "private-") || strings.Contains(text, `"generationSpec"`) {
		t.Fatalf("public payload retained private generation settings: %s", text)
	}
}

func TestPublicCanvasShareResourceOwnershipAndAllowlist(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "share.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.CanvasProject{}, &model.CanvasShare{}, &model.Resource{}); err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	repo := repository.New(db)
	svc := New(repo, nil)
	project := &model.CanvasProject{
		ID: "project-a", UserID: "user-a", Title: "分享画布",
		PayloadJSON: `{"id":"project-a","nodes":[{"id":"image-a","type":"image","title":"A 资源","position":{"x":0,"y":0},"width":10,"height":10,"metadata":{"storageKey":"resource:resource-a"}}],"connections":[]}`,
	}
	if err := repo.Create(project); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(&model.Resource{ID: "resource-a", UserID: "user-a", Kind: "image", Status: model.ResourceStatusReady}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(&model.Resource{ID: "resource-b", UserID: "user-b", Kind: "image", Status: model.ResourceStatusReady}); err != nil {
		t.Fatal(err)
	}
	status, err := svc.CreateCanvasShare("user-a", "project-a", CanvasShareRequest{})
	if err != nil {
		t.Fatal(err)
	}
	share, err := svc.PublicCanvasShare(status.Token)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mustJSON(t, share.Project)), "/resources/resource-a/file") {
		t.Fatalf("public share did not rewrite the owner resource: %+v", share.Project)
	}
	ownerID, resource, _, err := svc.sharedCanvasResource(status.Token, "resource-a")
	if err != nil || ownerID != "user-a" || resource.ID != "resource-a" {
		t.Fatalf("owner resource lookup = %q %+v %v", ownerID, resource, err)
	}
	if _, _, _, err := svc.sharedCanvasResource(status.Token, "resource-b"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("resource outside the shared document error = %v", err)
	}
	if _, err := svc.CanvasShareStatus("user-b", "project-a"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("foreign share status error = %v", err)
	}
	status, err = svc.CreateCanvasShare("user-a", "project-a", CanvasShareRequest{Rotate: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PublicCanvasShare(status.Token); err != nil {
		t.Fatalf("rotated share token failed: %v", err)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
