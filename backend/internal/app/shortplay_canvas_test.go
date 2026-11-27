package app

import (
	"encoding/json"
	"testing"

	"yingce/backend/internal/model"
)

// W06：每集画布按需创建。同一集同时打开两次得到同一张画布，画布带着本集原文，
// 且只有作品所有者能反查归属。

func TestEnsureShortplayEpisodeCanvasIsIdempotent(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "p-canvas", "sp-canvas")

	first, err := svc.EnsureShortplayEpisodeCanvas("owner", "sp-canvas", "sp-canvas-ep1")
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created || first.CanvasID != "sp-canvas-c1" {
		t.Fatalf("first ensure = %#v", first)
	}
	var canvas model.CanvasProject
	if err := db.First(&canvas, "id = ?", first.CanvasID).Error; err != nil {
		t.Fatal(err)
	}
	if canvas.ProjectID != "p-canvas" || canvas.UserID != "owner" || canvas.Title != "第一集" {
		t.Fatalf("canvas row = %#v", canvas)
	}
	var payload struct {
		Nodes []struct {
			Title    string `json:"title"`
			Metadata struct {
				Content      string `json:"content"`
				WorkflowKind string `json:"workflowKind"`
			} `json:"metadata"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(canvas.PayloadJSON), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Nodes) != 1 || payload.Nodes[0].Metadata.WorkflowKind != "story_input" ||
		payload.Nodes[0].Metadata.Content != "沈青禾撑伞站在巷口。" {
		t.Fatalf("payload = %s", canvas.PayloadJSON)
	}

	second, err := svc.EnsureShortplayEpisodeCanvas("owner", "p-canvas", "sp-canvas-ep1")
	if err != nil {
		t.Fatal(err)
	}
	if second.Created || second.CanvasID != first.CanvasID {
		t.Fatalf("second ensure = %#v", second)
	}
	var count int64
	if err := db.Model(&model.CanvasProject{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("canvas rows = %d, err = %v", count, err)
	}
}

func TestEnsureShortplayEpisodeCanvasRejectsForeignEpisode(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "p-a", "sp-a")
	seedShortplay(t, db, "intruder", "p-b", "sp-b")

	if _, err := svc.EnsureShortplayEpisodeCanvas("intruder", "sp-a", "sp-a-ep1"); err == nil {
		t.Fatal("non-owner opened someone else's episode canvas")
	}
	// 同一用户名下的另一部作品也不能借 :id 与 episodeId 混用越权。
	if _, err := svc.EnsureShortplayEpisodeCanvas("owner", "sp-a", "sp-b-ep1"); err == nil {
		t.Fatal("cross-shortplay episode id was accepted")
	}
}

func TestShortplayByCanvasBindsEpisode(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "p-bind", "sp-bind")
	if _, err := svc.EnsureShortplayEpisodeCanvas("owner", "sp-bind", "sp-bind-ep2"); err != nil {
		t.Fatal(err)
	}

	binding, found, err := svc.ShortplayByCanvas("owner", "sp-bind-c2")
	if err != nil || !found {
		t.Fatalf("binding = %#v, %v, %v", binding, found, err)
	}
	if binding.ShortplayID != "sp-bind" || binding.ProjectID != "p-bind" ||
		binding.EpisodeID != "sp-bind-ep2" || binding.EpisodeNumber != 2 || binding.EpisodeTitle != "第二集" {
		t.Fatalf("binding = %#v", binding)
	}

	// 别人的画布、不存在的画布、以及没有任何集绑定的自由画布都不算命中。
	if _, found, err := svc.ShortplayByCanvas("intruder", "sp-bind-c2"); err != nil || found {
		t.Fatalf("foreign binding found=%v err=%v", found, err)
	}
	if _, found, err := svc.ShortplayByCanvas("owner", "missing-canvas"); err != nil || found {
		t.Fatalf("missing canvas found=%v err=%v", found, err)
	}
	free := model.CanvasProject{ID: "free-canvas", UserID: "owner", Title: "自由画布", PayloadJSON: `{"nodes":[]}`}
	if err := db.Create(&free).Error; err != nil {
		t.Fatal(err)
	}
	if _, found, err := svc.ShortplayByCanvas("owner", "free-canvas"); err != nil || found {
		t.Fatalf("free canvas found=%v err=%v", found, err)
	}
}
