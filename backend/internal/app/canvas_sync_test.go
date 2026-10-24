package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/canvas"
	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

func TestCanvasLeaseLegacyRequestAndRealAgentWriterShareGuard(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	if err := database.MigrateCanvasCommands(db); err != nil {
		t.Fatal(err)
	}
	row := &model.CanvasProject{ID: "leased", UserID: "user", PayloadJSON: `{"id":"leased","title":"Canvas","referenceFieldsVersion":1,"nodes":[{"id":"text","type":"text","title":"Text","metadata":{"content":"original"}}],"connections":[]}`}
	if err := s.repo.UpsertCanvasProject(row); err != nil {
		t.Fatal(err)
	}
	proof, err := s.AcquireCanvasEditLease(context.Background(), "user", row.ID, CanvasEditProof{NodeID: "text", SessionID: "tab", Token: strings.Repeat("a", 48)})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := s.UserCanvasProject("user", row.ID)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	_ = json.Unmarshal(raw, &doc)
	creationMaps(doc["nodes"])[0]["metadata"].(map[string]any)["content"] = "legacy draft"
	draft, _ := json.Marshal(doc)
	if _, err = s.UpsertUserCanvasProject("user", draft); !errors.Is(err, repository.ErrCanvasNodeLocked) {
		t.Fatalf("legacy bypass: %v", err)
	}
	header, _ := json.Marshal([]CanvasEditProof{proof.CanvasEditProof})
	ctx, err := canvas.ParseCanvasEditProofHeader(context.Background(), string(header))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CanvasRequest(ctx).UpsertUserCanvasProject("user", draft); err != nil {
		t.Fatal(err)
	}
	// A second plain request must not inherit the preceding request's proof.
	stored, _ := s.repo.CanvasProjectForUser("user", row.ID)
	current, _ := creationDocument(stored.PayloadJSON)
	call := cloudAgentCall{}
	call.Function.Arguments = mustCanvasSyncJSON(t, map[string]any{"snapshotHash": cloudAgentCanvasHash(current), "ops": []map[string]any{{"type": "update_node", "id": "text", "patch": map[string]any{"content": "Agent draft"}}}})
	policy, err := s.RuntimePolicy()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = applyCloudAgentCanvas(s.repo, "user", row.ID, call, policy); !errors.Is(err, repository.ErrCanvasNodeLocked) {
		t.Fatalf("actual Agent bypass: %v", err)
	}
	after, _ := s.repo.CanvasProjectForUser("user", row.ID)
	if after.PayloadJSON != stored.PayloadJSON || after.Revision != stored.Revision {
		t.Fatal("blocked Agent changed document")
	}
	if _, err = applyCloudAgentCanvas(s.repo.WithContext(ctx), "user", row.ID, call, policy); err != nil {
		t.Fatalf("proved Agent persist: %v", err)
	}
}

func mustCanvasSyncJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestCanvasSyncStatusKeepsMultipleAgentsAndNeverLoadsStateOrToken(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	if err := database.MigrateCanvasCommands(db); err != nil {
		t.Fatal(err)
	}
	row := &model.CanvasProject{ID: "sync", UserID: "user", PayloadJSON: `{"nodes":[{"id":"text","type":"text","metadata":{"content":"draft"}}],"connections":[]}`}
	if err := s.repo.UpsertCanvasProject(row); err != nil {
		t.Fatal(err)
	}
	proof, err := s.AcquireCanvasEditLease(context.Background(), "user", row.ID, CanvasEditProof{NodeID: "text", SessionID: "tab", Token: strings.Repeat("a", 48)})
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range []model.CloudAgentExecution{
		{ID: "one", UserID: "user", CanvasID: row.ID, ConversationID: "conversation-one", Status: "running", StateJSON: "not valid JSON"},
		{ID: "two", UserID: "user", CanvasID: row.ID, ConversationID: "conversation-two", Status: "waiting_approval", StateJSON: "secret-state"},
		{ID: "done", UserID: "user", CanvasID: row.ID, Status: "completed"},
		{ID: "other", UserID: "other", CanvasID: row.ID, Status: "running"},
	} {
		if err := db.Create(&run).Error; err != nil {
			t.Fatal(err)
		}
	}
	out, err := s.CanvasSyncStatus(context.Background(), "user", row.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw := mustCanvasSyncJSON(t, out)
	if out.Revision != row.Revision || out.DocumentEpoch == "" || out.EventSeq != 1 || len(out.Leases) != 1 || len(out.ActiveAgents) != 2 {
		t.Fatalf("bad summary: %s", raw)
	}
	if strings.Contains(raw, "token") || strings.Contains(raw, proof.Token) || strings.Contains(raw, "secret-state") || strings.Contains(raw, "not valid JSON") {
		t.Fatalf("state leaked: %s", raw)
	}
	if _, err = s.CanvasSyncStatus(context.Background(), "other", row.ID); err == nil {
		t.Fatal("other account read sync state")
	}
}

func TestCanvasSyncStatusIdentifiesLeasedNodesWithoutExposingProofs(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	if err := database.MigrateCanvasCommands(db); err != nil {
		t.Fatal(err)
	}
	row := &model.CanvasProject{ID: "lease-names", UserID: "user", PayloadJSON: `{"nodes":[{"id":"picture","type":"image","title":"镜06 · 关键帧（已归档）"},{"id":"hero","type":"text","title":"顾行","metadata":{"workflowKind":"character"}},{"id":"untitled","type":"image"},{"id":"one","type":"video","title":"同名"},{"id":"two","type":"video","title":"同名"},{"id":"removed","type":"text","title":"old"}],"connections":[]}`}
	if err := s.repo.UpsertCanvasProject(row); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"picture", "hero", "untitled", "one", "two", "removed"} {
		if _, err := s.AcquireCanvasEditLease(context.Background(), "user", row.ID, CanvasEditProof{NodeID: id, SessionID: "tab", Token: strings.Repeat("private-proof", 4)}); err != nil {
			t.Fatal(err)
		}
	}
	// Characterize a stale lease after its node disappears, without mutating a real canvas.
	doc, _ := creationDocument(row.PayloadJSON)
	doc["nodes"] = creationMaps(doc["nodes"])[:5]
	if err := db.Model(&model.CanvasProject{}).Where("id = ?", row.ID).Update("payload_json", mustCanvasSyncJSON(t, doc)).Error; err != nil {
		t.Fatal(err)
	}
	out, err := s.CanvasSyncStatus(context.Background(), "user", row.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw := mustCanvasSyncJSON(t, out)
	var result struct {
		Leases []struct {
			NodeID, NodeTitle, NodeType, NodeTypeLabel string
			NodeExists                                 bool
		}
	}
	json.Unmarshal([]byte(raw), &result)
	byID := map[string]struct {
		title, kind, label string
		exists             bool
	}{}
	for _, lease := range result.Leases {
		byID[lease.NodeID] = struct {
			title, kind, label string
			exists             bool
		}{lease.NodeTitle, lease.NodeType, lease.NodeTypeLabel, lease.NodeExists}
	}
	for id, want := range map[string]string{"picture": "镜06 · 关键帧（已归档）", "hero": "顾行", "untitled": "未命名图片（untitled）", "one": "同名（one）", "two": "同名（two）", "removed": "节点已删除（removed）"} {
		if byID[id].title != want {
			t.Errorf("%s title = %q, want %q", id, byID[id].title, want)
		}
	}
	if byID["hero"].kind != "character" || byID["hero"].label != "角色卡" || !byID["picture"].exists || byID["removed"].exists {
		t.Fatalf("wrong business types or deleted-node status: %s", raw)
	}
	if strings.Contains(raw, "private-proof") || strings.Contains(raw, "token") {
		t.Fatalf("private lease proof leaked: %s", raw)
	}
	if _, err := s.CanvasSyncStatus(context.Background(), "other", row.ID); err == nil {
		t.Fatal("other account read node names")
	}
}
