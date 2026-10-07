package app

import (
	"testing"
	"time"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
)

type parityHistoryFixture struct {
	s               *Service
	db              *gorm.DB
	canvasID, runID string
	policy          RuntimePolicySetting
	state           *cloudAgentRuntime
}

func newParityHistoryFixture(t *testing.T) parityHistoryFixture {
	t.Helper()
	s, db, _, _ := creationTestService(t)
	canvas := model.CanvasProject{ID: "agent-canvas", UserID: "user", PayloadJSON: `{"nodes":[],"connections":[]}`}
	if err := db.Create(&canvas).Error; err != nil {
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
	return parityHistoryFixture{s: s, db: db, canvasID: canvas.ID, runID: run.ID, policy: policy, state: &cloudAgentRuntime{Request: agentTestRequest()}}
}

func (f parityHistoryFixture) hash(t *testing.T) string {
	t.Helper()
	canvas, err := f.s.repo.CanvasProjectForUser("user", f.canvasID)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		t.Fatal(err)
	}
	return cloudAgentCanvasHash(doc)
}

func (f parityHistoryFixture) add(t *testing.T, id string) string {
	t.Helper()
	call := parityCall("canvas_apply_ops", map[string]any{"snapshotHash": f.hash(t), "ops": []any{map[string]any{"type": "add_node", "id": id, "nodeType": "text", "content": id}}})
	call.ID = "add-" + id
	if _, err := applyCloudAgentCanvas(f.s.repo, "user", f.canvasID, call, f.policy, cloudAgentMutationRecorderForRun(f.runID)); err != nil {
		t.Fatal(err)
	}
	return f.hash(t)
}

func (f parityHistoryFixture) history(t *testing.T, name, want string) {
	t.Helper()
	result, err := applyCloudAgentHistory(f.s.repo, "user", f.canvasID, f.runID, f.state, parityCall(name, map[string]any{"snapshotHash": f.hash(t)}), f.policy)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if result.(map[string]any)["snapshotHash"] != want || f.hash(t) != want {
		t.Fatalf("%s did not restore the expected snapshot", name)
	}
}

func TestAgentParityDatabaseHistoryMultipleUndoRedo(t *testing.T) {
	f := newParityHistoryFixture(t)
	empty := f.hash(t)
	a := f.add(t, "A")
	b := f.add(t, "B")
	f.history(t, "canvas_undo", a)
	f.history(t, "canvas_undo", empty)
	f.history(t, "canvas_redo", a)
	f.history(t, "canvas_redo", b)
}

func TestAgentParityDatabaseHistoryNewBranchDiscardsRedo(t *testing.T) {
	f := newParityHistoryFixture(t)
	a := f.add(t, "A")
	f.add(t, "B")
	branch, err := f.s.repo.LatestCloudAgentCanvasMutation("user", f.runID)
	if err != nil {
		t.Fatal(err)
	}
	f.history(t, "canvas_undo", a)
	c := f.add(t, "C")
	if err := f.db.First(branch, "id = ?", branch.ID).Error; err != nil {
		t.Fatal(err)
	}
	if branch.Status != "discarded" {
		t.Fatal("new mutation did not discard the abandoned redo branch")
	}
	if _, err := prepareCloudAgentHistory(f.s.repo, "user", f.canvasID, f.runID, parityCall("canvas_redo", map[string]any{"snapshotHash": c})); err == nil {
		t.Fatal("redo accepted after a new mutation branch")
	}
	f.history(t, "canvas_undo", a)
	f.history(t, "canvas_redo", c)
}

func TestAgentParityDatabaseHistoryDoesNotCrossTaskOrUnavailableBarrier(t *testing.T) {
	for _, kind := range []string{"submitted_task", "not_undoable"} {
		t.Run(kind, func(t *testing.T) {
			f := newParityHistoryFixture(t)
			a := f.add(t, "A")
			barrier := &model.CloudAgentCanvasMutation{ID: "barrier", UserID: "user", RunID: f.runID, CanvasID: f.canvasID, Status: "applied", Operation: kind, CreatedAt: time.Now().UTC(), BeforeSnapshotHash: a, AfterSnapshotHash: a, HasSubmittedTask: kind == "submitted_task"}
			if kind == "not_undoable" {
				barrier.Status = "not_undoable"
			}
			if err := f.s.repo.CreateCloudAgentCanvasMutation(barrier); err != nil {
				t.Fatal(err)
			}
			f.add(t, "B")
			f.history(t, "canvas_undo", a)
			if _, err := prepareCloudAgentHistory(f.s.repo, "user", f.canvasID, f.runID, parityCall("canvas_undo", map[string]any{"snapshotHash": a})); err == nil {
				t.Fatal("undo crossed a paid-task or unavailable snapshot barrier")
			}
		})
	}
}

func TestAgentParityDatabaseHistoryDetectsSubsequentManualEdit(t *testing.T) {
	f := newParityHistoryFixture(t)
	f.add(t, "A")
	canvas, err := f.s.repo.CanvasProjectForUser("user", f.canvasID)
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := creationDocument(canvas.PayloadJSON)
	creationMaps(doc["nodes"])[0]["title"] = "manual edit"
	if err := saveCloudAgentDocument(f.s.repo, canvas, doc, f.policy); err != nil {
		t.Fatal(err)
	}
	current := f.hash(t)
	if _, err := prepareCloudAgentHistory(f.s.repo, "user", f.canvasID, f.runID, parityCall("canvas_undo", map[string]any{"snapshotHash": current})); err == nil || !cloudAgentSnapshotConflict(err) {
		t.Fatalf("manual edit did not conflict: %v", err)
	}
	if f.hash(t) != current {
		t.Fatal("history preview changed the manually edited canvas")
	}
}

func TestAgentParityDatabaseHistoryIgnoresOtherCanvasAndRollsBackDiscard(t *testing.T) {
	f := newParityHistoryFixture(t)
	a := f.add(t, "A")
	b := f.add(t, "B")
	latest, err := f.s.repo.LatestCloudAgentCanvasMutation("user", f.runID)
	if err != nil {
		t.Fatal(err)
	}
	other := &model.CloudAgentCanvasMutation{ID: "other-canvas", UserID: "user", RunID: f.runID, CanvasID: "unrelated", Status: "applied", Operation: "other", CreatedAt: time.Now().UTC()}
	if err := f.s.repo.CreateCloudAgentCanvasMutation(other); err != nil {
		t.Fatal(err)
	}
	f.history(t, "canvas_undo", a)
	duplicate := *latest
	duplicate.Status = "applied"
	if err := f.s.repo.CreateCloudAgentCanvasMutation(&duplicate); err == nil {
		t.Fatal("duplicate mutation insert unexpectedly succeeded")
	}
	// A failed insertion must not discard the existing redo branch.
	f.history(t, "canvas_redo", b)
}
