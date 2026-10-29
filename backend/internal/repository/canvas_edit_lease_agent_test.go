package repository

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"yingce/backend/internal/model"
)

// 浏览器与 Agent 共用同一份作者字段判定：Agent 没有编辑凭证，撞上任何有效租约的
// 作者字段改动或删除都返回 node_locked；结果/进度字段放行。
func leaseTestCanvas(t *testing.T) (*Repository, *model.CanvasProject) {
	t.Helper()
	db, repo := projectProjectionTestDB(t)
	if err := db.AutoMigrate(&model.User{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.User{ID: "owner", Username: "owner"}).Error; err != nil {
		t.Fatal(err)
	}
	payload := `{"id":"canvas","nodes":[
		{"id":"leased","type":"image","title":"主角","metadata":{"prompt":"雨夜","progress":0,"status":"idle","inputBindings":[],"generationSpec":{"version":1,"mode":"image","prompt":"雨夜","referenceBindings":[]},"storyboard":{"referenceNodeIds":[]}}},
		{"id":"free","type":"image","title":"配角","metadata":{"prompt":"晴天"}}
	]}`
	canvas := &model.CanvasProject{ID: "canvas", UserID: "owner", Title: "分镜", PayloadJSON: payload}
	if err := repo.UpsertCanvasProject(canvas); err != nil {
		t.Fatal(err)
	}
	lease := model.CanvasEditLease{UserID: "owner", CanvasID: "canvas", NodeID: "leased", SessionID: "browser-a", Token: "token-a", ExpiresAt: time.Now().UTC().Add(30 * time.Second)}
	if err := db.Create(&lease).Error; err != nil {
		t.Fatal(err)
	}
	return repo, canvas
}

func leaseTestEdit(t *testing.T, row *model.CanvasProject, nodeID string, edit func(node map[string]any)) model.CanvasProject {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(row.PayloadJSON), &doc); err != nil {
		t.Fatal(err)
	}
	nodes := []any{}
	for _, raw := range doc["nodes"].([]any) {
		node := raw.(map[string]any)
		if node["id"] == nodeID {
			if edit == nil {
				continue
			}
			edit(node)
		}
		nodes = append(nodes, node)
	}
	doc["nodes"] = nodes
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	after := *row
	after.PayloadJSON = string(encoded)
	return after
}

func leaseTestMeta(node map[string]any) map[string]any { return node["metadata"].(map[string]any) }

func agentLeaseWrite(t *testing.T, repo *Repository, nodeID string, edit func(node map[string]any)) error {
	t.Helper()
	current, err := repo.CanvasProjectForUser("owner", "canvas")
	if err != nil {
		t.Fatal(err)
	}
	after := leaseTestEdit(t, current, nodeID, edit)
	return repo.SaveAgentCanvasWrite(current, &after, AgentActor{RunID: "run", UserID: "owner"}, nil)
}

func assertNodeLocked(t *testing.T, err error, nodeID string) {
	t.Helper()
	var lease *CanvasEditLeaseError
	if !errors.As(err, &lease) || lease.Reason != "node_locked" || lease.NodeID != nodeID || lease.CanvasID != "canvas" {
		t.Fatalf("want node_locked on %s, got %v", nodeID, err)
	}
	if !errors.Is(err, ErrCanvasNodeLocked) || !errors.Is(err, ErrCanvasEditLease) {
		t.Fatalf("node_locked must classify as both lease sentinels: %v", err)
	}
}

func TestAgentWriteRespectsBrowserLease(t *testing.T) {
	authored := map[string]func(node map[string]any){
		"prompt":        func(n map[string]any) { leaseTestMeta(n)["prompt"] = "晴天" },
		"title":         func(n map[string]any) { n["title"] = "反派" },
		"inputBindings": func(n map[string]any) { leaseTestMeta(n)["inputBindings"] = []any{map[string]any{"nodeId": "free"}} },
		"generationSpec.references": func(n map[string]any) {
			leaseTestMeta(n)["generationSpec"].(map[string]any)["referenceBindings"] = []any{"free"}
		},
		"storyboard.referenceNodeIds": func(n map[string]any) {
			leaseTestMeta(n)["storyboard"].(map[string]any)["referenceNodeIds"] = []any{"free"}
		},
	}
	for name, edit := range authored {
		t.Run(name, func(t *testing.T) {
			repo, _ := leaseTestCanvas(t)
			assertNodeLocked(t, agentLeaseWrite(t, repo, "leased", edit), "leased")
			stored, _ := repo.CanvasProjectForUser("owner", "canvas")
			if stored.Revision != 1 {
				t.Fatalf("rejected agent write advanced revision to %d", stored.Revision)
			}
		})
	}
	t.Run("progress and status pass", func(t *testing.T) {
		repo, _ := leaseTestCanvas(t)
		if err := agentLeaseWrite(t, repo, "leased", func(n map[string]any) {
			leaseTestMeta(n)["progress"] = 50
			leaseTestMeta(n)["status"] = "running"
		}); err != nil {
			t.Fatalf("progress write on leased node rejected: %v", err)
		}
	})
	t.Run("unleased node passes", func(t *testing.T) {
		repo, _ := leaseTestCanvas(t)
		if err := agentLeaseWrite(t, repo, "free", func(n map[string]any) { leaseTestMeta(n)["prompt"] = "阴天" }); err != nil {
			t.Fatalf("write on unleased node rejected: %v", err)
		}
	})
}

func TestAgentDeleteLeasedNodeRejected(t *testing.T) {
	repo, _ := leaseTestCanvas(t)
	assertNodeLocked(t, agentLeaseWrite(t, repo, "leased", nil), "leased")
	if err := agentLeaseWrite(t, repo, "free", nil); err != nil {
		t.Fatalf("delete of unleased node rejected: %v", err)
	}
}

func TestBrowserOwnLeaseAllowed(t *testing.T) {
	repo, canvas := leaseTestCanvas(t)
	editPrompt := func(n map[string]any) { leaseTestMeta(n)["prompt"] = "黄昏" }
	stranger := leaseTestEdit(t, canvas, "leased", editPrompt)
	if err := repo.WithCanvasEditProofs([]model.CanvasEditProof{{NodeID: "leased", SessionID: "browser-b", Token: "token-b"}}).UpsertCanvasProject(&stranger); !errors.Is(err, ErrCanvasEditLease) {
		t.Fatalf("other session's proof accepted: %v", err)
	}
	unproven := leaseTestEdit(t, canvas, "leased", func(n map[string]any) { leaseTestMeta(n)["inputBindings"] = []any{map[string]any{"nodeId": "free"}} })
	if err := repo.UpsertCanvasProject(&unproven); !errors.Is(err, ErrCanvasEditLease) {
		t.Fatalf("browser reference change without proof accepted: %v", err)
	}
	var lease *CanvasEditLeaseError
	owner := leaseTestEdit(t, canvas, "leased", editPrompt)
	if err := repo.WithCanvasEditProofs([]model.CanvasEditProof{{NodeID: "leased", SessionID: "browser-a", Token: "token-a"}}).UpsertCanvasProject(&owner); err != nil || errors.As(err, &lease) {
		t.Fatalf("lease holder's own write rejected: %v", err)
	}
}
