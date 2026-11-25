package canvas

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"yingce/backend/internal/canvas/submappreset"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

const userSubmapPresetTestSnapshot = `{
  "version": 1,
  "id": "client-forged-id",
  "title": "我的镜头模板",
  "nodes": [
    { "localId": "text-1", "type": "text", "title": "提示词",
      "metadata": { "generationSpec": { "prompt": "自由正文，不是 @文本N 槽位" } } },
    { "localId": "audio-1", "type": "audio", "title": "旁白" },
    { "localId": "image-1", "type": "image", "title": "首帧",
      "metadata": { "generationSpec": { "prompt": "一张图" }, "referenceTextNodeIds": ["text-1"] } },
    { "localId": "image-2", "type": "image", "title": "第二张图" },
    { "localId": "video-1", "type": "video", "title": "视频",
      "metadata": { "videoStartFrameNodeId": "image-1", "referenceTextNodeIds": ["text-1"] } }
  ]
}`

func TestUserSubmapPresetCreateAndUpdateOwnsIdentity(t *testing.T) {
	svc := newUserSubmapPresetTestService(t)
	created, err := svc.CreateUserSubmapPreset("user-1", UserSubmapPresetRequest{ManifestJSON: userSubmapPresetTestSnapshot})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "client-forged-id" || created.Name != "我的镜头模板" || created.Revision != 1 || created.UserID != "user-1" {
		t.Fatalf("create did not own identity: %+v", created)
	}
	var snapshot struct {
		Version int    `json:"version"`
		ID      string `json:"id"`
		Title   string `json:"title"`
		Nodes   []any  `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(created.ManifestJSON), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.ID != created.ID || snapshot.Version != 1 || snapshot.Title != created.Name || len(snapshot.Nodes) != 5 {
		t.Fatalf("stored snapshot was not normalized: %+v", snapshot)
	}

	updated, err := svc.UpdateUserSubmapPreset("user-1", created.ID, UserSubmapPresetRequest{ManifestJSON: userSubmapPresetTestSnapshot})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != created.ID || updated.Revision != 2 || !strings.Contains(updated.ManifestJSON, `"id":"`+created.ID+`"`) {
		t.Fatalf("update must preserve id and bump revision: created=%+v updated=%+v", created, updated)
	}
}

func TestUserSubmapPresetRejectsInvalidSnapshot(t *testing.T) {
	svc := newUserSubmapPresetTestService(t)
	for name, value := range map[string]string{
		"dangling":                `{"version":1,"title":"坏快照","nodes":[{"localId":"image-1","type":"image","title":"首帧","metadata":{"referenceTextNodeIds":["text-9"]}}]}`,
		"unknown":                 `{"version":1,"title":"多字段","bogus":1,"nodes":[{"localId":"image-1","type":"image","title":"首帧"}]}`,
		"empty":                   `{"version":1,"title":"空快照","nodes":[]}`,
		"wrong-version":           `{"version":2,"title":"未来版本","nodes":[{"localId":"image-1","type":"image","title":"首帧"}]}`,
		"non-string-ref":          `{"version":1,"title":"坏引用","nodes":[{"localId":"image-1","type":"image","title":"首帧","metadata":{"referenceTextNodeIds":[42]}}]}`,
		"dangling-binding":        `{"version":1,"title":"坏绑定","nodes":[{"localId":"image-1","type":"image","title":"首帧","metadata":{"generationSpec":{"referenceBindings":[{"nodeId":"text-9","mediaType":"text","role":"source-text"}]}}}]}`,
		"malformed-input-binding": `{"version":1,"title":"坏输入绑定","nodes":[{"localId":"image-1","type":"image","title":"首帧","metadata":{"inputBindings":["bad"]}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := svc.CreateUserSubmapPreset("user-1", UserSubmapPresetRequest{ManifestJSON: value}); err == nil {
				t.Fatal("expected invalid snapshot to be rejected")
			}
		})
	}
}

func TestUserSubmapPresetBoundaryLimits(t *testing.T) {
	svc := newUserSubmapPresetTestService(t)

	title := strings.Repeat("名", submappreset.MaxNameRunes)
	if _, err := svc.CreateUserSubmapPreset("user-1", UserSubmapPresetRequest{ManifestJSON: userSubmapPresetJSON(t, title, 1)}); err != nil {
		t.Fatalf("80 字名称应通过: %v", err)
	}
	if _, err := svc.CreateUserSubmapPreset("user-1", UserSubmapPresetRequest{ManifestJSON: userSubmapPresetJSON(t, title+"名", 1)}); err == nil {
		t.Fatal("81 字名称应被拒绝")
	}

	if _, err := svc.CreateUserSubmapPreset("user-2", UserSubmapPresetRequest{ManifestJSON: userSubmapPresetJSON(t, "200 节点", submappreset.MaxNodes)}); err != nil {
		t.Fatalf("200 节点应通过: %v", err)
	}
	if _, err := svc.CreateUserSubmapPreset("user-3", UserSubmapPresetRequest{ManifestJSON: userSubmapPresetJSON(t, "201 节点", submappreset.MaxNodes+1)}); err == nil {
		t.Fatal("201 节点应被拒绝")
	}
}

func TestUserSubmapPresetSnapshotByteLimit(t *testing.T) {
	svc := newUserSubmapPresetTestService(t)
	one := userSubmapPresetJSONWithContent(t, "大小边界", "a")
	padding := submappreset.MaxSnapshotLen - len(one) + 1
	if padding < 1 {
		t.Fatalf("base snapshot already exceeds limit: %d", len(one))
	}
	atLimit := userSubmapPresetJSONWithContent(t, "大小边界", strings.Repeat("a", padding))
	if len(atLimit) != submappreset.MaxSnapshotLen {
		t.Fatalf("test fixture size = %d, want %d", len(atLimit), submappreset.MaxSnapshotLen)
	}
	if _, err := svc.CreateUserSubmapPreset("user-1", UserSubmapPresetRequest{ManifestJSON: atLimit}); err != nil {
		t.Fatalf("512 KiB 应通过: %v", err)
	}
	overLimit := userSubmapPresetJSONWithContent(t, "大小边界", strings.Repeat("a", padding+1))
	if _, err := svc.CreateUserSubmapPreset("user-2", UserSubmapPresetRequest{ManifestJSON: overLimit}); err == nil || !strings.Contains(err.Error(), "512KB") {
		t.Fatalf("超出 512 KiB 应被拒绝: %v", err)
	}
}

func TestUserSubmapPresetCountLimit(t *testing.T) {
	svc, db := newUserSubmapPresetTestServiceWithDB(t)
	for index := 0; index < maxUserSubmapPresets; index++ {
		row := model.UserSubmapPreset{
			ID:           fmt.Sprintf("preset-%03d", index),
			UserID:       "user-1",
			Name:         fmt.Sprintf("模板 %d", index),
			ManifestJSON: "{}",
			Revision:     1,
		}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.CreateUserSubmapPreset("user-1", UserSubmapPresetRequest{ManifestJSON: userSubmapPresetTestSnapshot}); err == nil || !strings.Contains(err.Error(), "200") {
		t.Fatalf("第 201 个预制应被拒绝: %v", err)
	}
	if _, err := svc.CreateUserSubmapPreset("user-2", UserSubmapPresetRequest{ManifestJSON: userSubmapPresetTestSnapshot}); err != nil {
		t.Fatalf("其他用户不应受数量影响: %v", err)
	}
}

func TestUserSubmapPresetMutationsAreScopedToOwner(t *testing.T) {
	svc := newUserSubmapPresetTestService(t)
	created, err := svc.CreateUserSubmapPreset("user-1", UserSubmapPresetRequest{ManifestJSON: userSubmapPresetTestSnapshot})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateUserSubmapPreset("user-2", created.ID, UserSubmapPresetRequest{ManifestJSON: userSubmapPresetTestSnapshot}); err == nil {
		t.Fatal("expected cross-user update to fail")
	}
	if err := svc.DeleteUserSubmapPreset("user-2", created.ID); err == nil {
		t.Fatal("expected cross-user delete to fail")
	}
	presets, err := svc.ListUserSubmapPresets("user-1")
	if err != nil || len(presets) != 1 || presets[0].ID != created.ID {
		t.Fatalf("owner preset changed by rejected mutations: %+v, %v", presets, err)
	}
	others, err := svc.ListUserSubmapPresets("user-2")
	if err != nil || len(others) != 0 {
		t.Fatalf("cross-user list must be empty: %+v, %v", others, err)
	}
}

func newUserSubmapPresetTestService(t *testing.T) *Service {
	t.Helper()
	svc, _ := newUserSubmapPresetTestServiceWithDB(t)
	return svc
}

func newUserSubmapPresetTestServiceWithDB(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&model.UserSubmapPreset{}); err != nil {
		t.Fatal(err)
	}
	return New(repository.New(db), nil), db
}

func userSubmapPresetJSON(t *testing.T, title string, nodes int) string {
	t.Helper()
	return userSubmapPresetJSONWithContentAndCount(t, title, nodes, "")
}

func userSubmapPresetJSONWithContent(t *testing.T, title, content string) string {
	t.Helper()
	return userSubmapPresetJSONWithContentAndCount(t, title, 1, content)
}

func userSubmapPresetJSONWithContentAndCount(t *testing.T, title string, nodes int, content string) string {
	t.Helper()
	snapshot := submappreset.Snapshot{
		Version: submappreset.SupportedVersion,
		ID:      strings.Repeat("a", 32),
		Title:   title,
		Nodes:   make([]submappreset.Node, 0, nodes),
	}
	for index := 0; index < nodes; index++ {
		node := submappreset.Node{
			LocalID: fmt.Sprintf("text-%d", index+1),
			Type:    "text",
			Title:   "文本",
		}
		if index == 0 && content != "" {
			node.Metadata = map[string]any{"content": content}
		}
		snapshot.Nodes = append(snapshot.Nodes, node)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
