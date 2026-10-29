package app

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

type agentUndoFixture struct {
	s      *Service
	db     *gorm.DB
	runID  string
	policy RuntimePolicySetting
}

func newAgentUndoFixture(t *testing.T, payload string) agentUndoFixture {
	t.Helper()
	s, db, _, _ := creationTestService(t)
	if err := db.Create(&model.CanvasProject{ID: "agent-canvas", UserID: "user", PayloadJSON: payload}).Error; err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateCloudAgentRun("user", agentTestRequest(), "")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := s.RuntimePolicy()
	if err != nil {
		t.Fatal(err)
	}
	return agentUndoFixture{s: s, db: db, runID: run.ID, policy: policy}
}

func (f agentUndoFixture) apply(t *testing.T, callID string, ops ...map[string]any) {
	t.Helper()
	canvas, err := f.s.repo.CanvasProjectForUser("user", "agent-canvas")
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := creationDocument(canvas.PayloadJSON)
	args, _ := json.Marshal(map[string]any{"snapshotHash": cloudAgentCanvasHash(doc), "ops": ops})
	call := cloudAgentCall{ID: callID}
	call.Function.Name = "canvas_apply_ops"
	call.Function.Arguments = string(args)
	execution, err := f.s.repo.CloudAgent("user", f.runID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.repo.MutateCloudAgent("user", f.runID, execution.Revision, func(_ *model.CloudAgentExecution, repo *repository.Repository) error {
		_, err := applyCloudAgentCanvas(repo, "user", "agent-canvas", call, f.policy, cloudAgentMutationRecorderForRun(f.runID))
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func (f agentUndoFixture) finish(t *testing.T) {
	t.Helper()
	if err := f.db.Model(&model.CloudAgentExecution{}).Where("id = ?", f.runID).Update("status", "completed").Error; err != nil {
		t.Fatal(err)
	}
}

func (f agentUndoFixture) canvas(t *testing.T) *model.CanvasProject {
	t.Helper()
	canvas, err := f.s.repo.CanvasProjectForUser("user", "agent-canvas")
	if err != nil {
		t.Fatal(err)
	}
	return canvas
}

// userEdit stands in for a browser save: it changes one node through the
// browser write path, which bumps that node's entity version.
func (f agentUndoFixture) userEdit(t *testing.T, nodeID, content string) {
	t.Helper()
	canvas := f.canvas(t)
	doc, _ := creationDocument(canvas.PayloadJSON)
	for _, node := range creationMaps(doc["nodes"]) {
		if stringValue(node["id"]) == nodeID {
			meta, _ := node["metadata"].(map[string]any)
			meta["content"] = content
		}
	}
	raw, _ := json.Marshal(doc)
	canvas.PayloadJSON = string(raw)
	if err := f.s.repo.UpsertCanvasProject(canvas); err != nil {
		t.Fatal(err)
	}
}

func nodeContent(t *testing.T, canvas *model.CanvasProject, nodeID string) (string, bool) {
	t.Helper()
	doc, _ := creationDocument(canvas.PayloadJSON)
	for _, node := range creationMaps(doc["nodes"]) {
		if stringValue(node["id"]) == nodeID {
			meta, _ := node["metadata"].(map[string]any)
			content, _ := meta["content"].(string)
			return content, true
		}
	}
	return "", false
}

const agentUndoSeed = `{"nodes":[{"id":"keep","type":"text","title":"原节点","position":{"x":0,"y":0},"width":320,"height":200,"metadata":{"content":"原文"}},{"id":"other","type":"text","title":"用户节点","position":{"x":400,"y":0},"width":320,"height":200,"metadata":{"content":"用户原文"}}],"connections":[]}`

func TestAgentUndoRevertsWholeRunAndKeepsUnrelatedUserEdits(t *testing.T) {
	f := newAgentUndoFixture(t, agentUndoSeed)
	f.apply(t, "call-add", map[string]any{"type": "add_node", "id": "added", "nodeType": "text", "title": "Agent 新增", "content": "新内容", "x": 10, "y": 300})
	f.apply(t, "call-edit", map[string]any{"type": "update_node", "id": "keep", "patch": map[string]any{"content": "Agent 改写"}})
	f.finish(t)
	// A user edit on a node the Agent never touched must survive the undo.
	f.userEdit(t, "other", "用户后来修改")
	before := f.canvas(t)

	result, err := f.s.UndoCloudAgentRun("user", f.runID, "用户撤销")
	if err != nil {
		t.Fatal(err)
	}
	after := f.canvas(t)
	if result.Revision != before.Revision+1 || after.Revision != result.Revision {
		t.Fatalf("undo must persist one new revision through the shared write path: before=%d result=%d stored=%d", before.Revision, result.Revision, after.Revision)
	}
	if len(result.RestoredNodeIDs) != 2 || result.RestoredNodeIDs[0] != "added" || result.RestoredNodeIDs[1] != "keep" {
		t.Fatalf("unexpected restored nodes: %+v", result.RestoredNodeIDs)
	}
	if _, ok := nodeContent(t, after, "added"); ok {
		t.Fatal("node added by the Agent was not removed")
	}
	if content, _ := nodeContent(t, after, "keep"); content != "原文" {
		t.Fatalf("field changed by the Agent was not restored: %q", content)
	}
	if content, _ := nodeContent(t, after, "other"); content != "用户后来修改" {
		t.Fatalf("unrelated user edit was lost: %q", content)
	}
	chain, err := f.s.repo.CloudAgentCanvasMutationChain("user", f.runID, "agent-canvas")
	if err != nil || len(chain) != 0 {
		t.Fatalf("run mutations must be marked undone: %+v %v", chain, err)
	}
	if _, err := f.s.UndoCloudAgentRun("user", f.runID, "重复"); err == nil {
		t.Fatal("second undo of the same run must not write again")
	}
}

func TestAgentUndoConflictsWhenUserChangedAgentNode(t *testing.T) {
	f := newAgentUndoFixture(t, agentUndoSeed)
	f.apply(t, "call-edit", map[string]any{"type": "update_node", "id": "keep", "patch": map[string]any{"content": "Agent 改写"}})
	f.finish(t)
	f.userEdit(t, "keep", "用户在 Agent 之后又改")
	before := f.canvas(t)

	_, err := f.s.UndoCloudAgentRun("user", f.runID, "冲突")
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Status != 409 || appErr.Reason != kernel.ReasonAgentUndoConflict {
		t.Fatalf("expected 409 agent_undo_conflict, got %v", err)
	}
	after := f.canvas(t)
	if after.Revision != before.Revision || after.PayloadJSON != before.PayloadJSON {
		t.Fatal("conflicting undo must leave the canvas unchanged")
	}
	chain, _ := f.s.repo.CloudAgentCanvasMutationChain("user", f.runID, "agent-canvas")
	if len(chain) != 1 {
		t.Fatalf("conflicting undo must keep the mutation applied: %+v", chain)
	}
}

func TestAgentUndoRejectsOtherUsersAndRunningRuns(t *testing.T) {
	f := newAgentUndoFixture(t, agentUndoSeed)
	f.apply(t, "call-edit", map[string]any{"type": "update_node", "id": "keep", "patch": map[string]any{"content": "Agent 改写"}})
	var appErr *AppError
	if _, err := f.s.UndoCloudAgentRun("user", f.runID, ""); !errors.As(err, &appErr) || appErr.Status != 409 {
		t.Fatalf("running run must not be undone, got %v", err)
	}
	f.finish(t)
	if _, err := f.s.UndoCloudAgentRun("intruder", f.runID, ""); !errors.As(err, &appErr) || appErr.Status != 404 {
		t.Fatalf("another user's run must be not found, got %v", err)
	}
	if _, err := f.s.UndoCloudAgentRun("", f.runID, ""); !errors.As(err, &appErr) || appErr.Status != 401 {
		t.Fatalf("missing user must be unauthorized, got %v", err)
	}
}

func (f agentUndoFixture) userEditTitle(t *testing.T, nodeID, title string) {
	t.Helper()
	canvas := f.canvas(t)
	doc, _ := creationDocument(canvas.PayloadJSON)
	for _, node := range creationMaps(doc["nodes"]) {
		if stringValue(node["id"]) == nodeID {
			node["title"] = title
		}
	}
	raw, _ := json.Marshal(doc)
	canvas.PayloadJSON = string(raw)
	if err := f.s.repo.UpsertCanvasProject(canvas); err != nil {
		t.Fatal(err)
	}
}

// Agent edits A, the user edits A, the Agent edits A again: live A equals the
// run's final value, but undoing to the pre-run value would erase the user's
// mid-run edit, so it must conflict.
func TestAgentUndoConflictsOnUserEditBetweenAgentMutations(t *testing.T) {
	f := newAgentUndoFixture(t, agentUndoSeed)
	f.apply(t, "call-1", map[string]any{"type": "update_node", "id": "keep", "patch": map[string]any{"content": "第一次"}})
	f.userEditTitle(t, "keep", "用户中途改标题")
	f.apply(t, "call-2", map[string]any{"type": "update_node", "id": "keep", "patch": map[string]any{"content": "第二次"}})
	f.finish(t)
	before := f.canvas(t)

	_, err := f.s.UndoCloudAgentRun("user", f.runID, "")
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Status != 409 || appErr.Reason != kernel.ReasonAgentUndoConflict {
		t.Fatalf("expected 409 agent_undo_conflict, got %v", err)
	}
	if after := f.canvas(t); after.Revision != before.Revision || after.PayloadJSON != before.PayloadJSON {
		t.Fatal("conflicting undo must leave the canvas unchanged")
	}
}

func TestAgentUndoReportsLeaseHeldNodeAsConflict(t *testing.T) {
	f := newAgentUndoFixture(t, agentUndoSeed)
	f.apply(t, "call-edit", map[string]any{"type": "update_node", "id": "keep", "patch": map[string]any{"content": "Agent 改写"}})
	f.finish(t)
	if _, err := f.s.repo.ChangeCanvasEditLease("user", "agent-canvas", model.CanvasEditProof{NodeID: "keep", SessionID: "tab"}, "acquire", strings.Repeat("t", 48), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	before := f.canvas(t)

	_, err := f.s.UndoCloudAgentRun("user", f.runID, "")
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Status != 409 || appErr.Reason != kernel.ReasonAgentUndoConflict {
		t.Fatalf("lease-held node must be a 409 agent_undo_conflict, got %v", err)
	}
	if ids, _ := appErr.Details["ids"].([]string); len(ids) != 1 || ids[0] != "keep" {
		t.Fatalf("conflict must name the leased node: %+v", appErr.Details)
	}
	if after := f.canvas(t); after.Revision != before.Revision || after.PayloadJSON != before.PayloadJSON {
		t.Fatal("blocked undo must leave the canvas unchanged")
	}
}
