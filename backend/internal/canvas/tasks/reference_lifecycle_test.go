package tasks

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/canvas/commands"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

// referenceLifecycleFixture extends the real-SQLite task fixture with a
// storyboard board whose single row owns "output" as its generated image, so a
// task submitted through Service.Submit persists the same durable Consumer and
// task binding the Agent storyboard path persists.
type referenceLifecycleFixture struct {
	service   *Service
	db        *gorm.DB
	repo      *repository.Repository
	commands  *commands.Service
	principal identity.Principal
	request   SubmitRequest
	submitter *stubSubmitter
}

func newReferenceLifecycleFixture(t *testing.T) *referenceLifecycleFixture {
	t.Helper()
	service, db, repo, cs, principal, request, submitter := taskFixture(t)
	if err := db.AutoMigrate(&model.BillingOrder{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	snapshot, err := cs.ReadSnapshot(ctx, principal, request.Command.CanvasID)
	if err != nil {
		t.Fatal(err)
	}
	x, y := 0.0, 0.0
	if _, err = cs.Execute(ctx, principal, commands.Command{
		Protocol: 1, CanvasID: snapshot.CanvasID, DocumentEpoch: snapshot.DocumentEpoch,
		ClientID: "client", OperationID: "reference-lifecycle-setup", BaseRevision: snapshot.Revision,
		Operations: []commands.Operation{
			{Type: "add_node", ID: "output", NodeType: "image", X: &x, Y: &y},
			{Type: "add_node", ID: "replacement", NodeType: "image", X: &x, Y: &y},
			{Type: "add_node", ID: "board", NodeType: "script", X: &x, Y: &y},
			{Type: "edit_storyboard", ID: "board", Action: "append", RowID: "row", Patch: map[string]any{"durationSeconds": float64(5), "plotDescription": "hero shot"}},
			{Type: "bind_storyboard_output", ID: "board", RowID: "row", OutputMode: "image", SourceNodeID: "output"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err = cs.ReadSnapshot(ctx, principal, request.Command.CanvasID)
	if err != nil {
		t.Fatal(err)
	}
	request.TargetNodeID = "board"
	request.Consumer = Consumer{Kind: "storyboard", RowID: "row", Field: "imageNodeId", OutputNodeID: "output"}
	request.Task = TaskRequest{Mode: "image", Prompt: "hero shot"}
	request.Command.OperationID = "reference-task"
	request.Command.BaseRevision = snapshot.Revision
	return &referenceLifecycleFixture{service: service, db: db, repo: repo, commands: cs, principal: principal, request: request, submitter: submitter}
}

func (f *referenceLifecycleFixture) canvas() string { return f.request.Command.CanvasID }

func (f *referenceLifecycleFixture) snapshot(t *testing.T) commands.Snapshot {
	t.Helper()
	snapshot, err := f.commands.ReadSnapshot(context.Background(), f.principal, f.canvas())
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func (f *referenceLifecycleFixture) submit(t *testing.T) TaskHandle {
	t.Helper()
	handle, err := f.service.Submit(context.Background(), f.principal, f.request)
	if err != nil {
		t.Fatal(err)
	}
	return handle
}

// resubmit prepares the next accepted task for the same row, as the editor does
// after the previous task reached a terminal state.
func (f *referenceLifecycleFixture) resubmit(t *testing.T, operationID string) TaskHandle {
	t.Helper()
	current := f.snapshot(t)
	f.request.Command.OperationID = operationID
	f.request.Command.BaseRevision = current.Revision
	f.request.Intent = "new_version"
	return f.submit(t)
}

// finish reproduces a terminal writer: it stores the terminal status/result and
// enqueues the durable materialization job in the same durable step.
func (f *referenceLifecycleFixture) finish(t *testing.T, taskID string, status model.TaskStatus, result string) {
	t.Helper()
	if err := f.db.Model(&model.Task{}).Where("id = ?", taskID).Updates(map[string]any{"status": status, "result_json": result}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.repo.EnqueueCanvasMaterialization(taskID); err != nil {
		t.Fatal(err)
	}
}

// requeue reproduces a duplicate delivery of an already delivered terminal result.
func (f *referenceLifecycleFixture) requeue(t *testing.T, taskID string) {
	t.Helper()
	if err := f.db.Model(&model.CanvasMaterializationJob{}).Where("task_id = ?", taskID).Updates(map[string]any{"state": "pending", "outcome": "", "next_attempt_at": nil}).Error; err != nil {
		t.Fatal(err)
	}
}

func (f *referenceLifecycleFixture) materialize(t *testing.T, taskID string) string {
	t.Helper()
	if err := f.service.Materialize(context.Background(), taskID); err != nil {
		t.Fatal(err)
	}
	job, err := f.repo.CanvasMaterialization(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if job.State != "done" {
		t.Fatalf("materialization job not done: %+v", job)
	}
	return job.Outcome
}

// readyResource registers the provider artifact so a successful task can be
// materialized without any provider or storage side effect.
func (f *referenceLifecycleFixture) readyResource(t *testing.T, id string) {
	t.Helper()
	if err := f.db.Create(&model.Resource{ID: id, UserID: f.principal.OwnerID, Kind: "image", Status: model.ResourceStatusReady, MimeType: "image/png", Width: 1024, Height: 768}).Error; err != nil {
		t.Fatal(err)
	}
}

// freezeInput stores the accepted request the way the submitter does, so a late
// result cannot rewrite the frozen reference version.
func (f *referenceLifecycleFixture) freezeInput(t *testing.T, taskID, input string) {
	t.Helper()
	if err := f.db.Model(&model.Task{}).Where("id = ?", taskID).Update("input_json", input).Error; err != nil {
		t.Fatal(err)
	}
}

func (f *referenceLifecycleFixture) taskInput(t *testing.T, taskID string) string {
	t.Helper()
	task, err := f.repo.Task(taskID)
	if err != nil {
		t.Fatal(err)
	}
	return task.InputJSON
}

func (f *referenceLifecycleFixture) bindingConsumer(t *testing.T, taskID string) string {
	t.Helper()
	binding, err := f.repo.CanvasTaskBinding(taskID)
	if err != nil {
		t.Fatal(err)
	}
	return binding.ConsumerJSON
}

func (f *referenceLifecycleFixture) counts(t *testing.T) (tasks, assets, outputs, orders int64) {
	t.Helper()
	for _, item := range []struct {
		value any
		into  *int64
	}{{&model.Task{}, &tasks}, {&model.Asset{}, &assets}, {&model.CanvasTaskOutput{}, &outputs}, {&model.BillingOrder{}, &orders}} {
		if err := f.db.Model(item.value).Count(item.into).Error; err != nil {
			t.Fatal(err)
		}
	}
	return tasks, assets, outputs, orders
}

func (f *referenceLifecycleFixture) execute(t *testing.T, operationID string, operations ...commands.Operation) {
	t.Helper()
	current := f.snapshot(t)
	if _, err := f.commands.Execute(context.Background(), f.principal, commands.Command{
		Protocol: 1, CanvasID: current.CanvasID, DocumentEpoch: current.DocumentEpoch,
		ClientID: "client", OperationID: operationID, BaseRevision: current.Revision,
		Operations: operations,
	}); err != nil {
		t.Fatal(err)
	}
}

func (f *referenceLifecycleFixture) undo(t *testing.T, operationID string) {
	t.Helper()
	current := f.snapshot(t)
	if _, err := f.commands.Undo(context.Background(), f.principal, current.CanvasID, current.DocumentEpoch, "client", operationID); err != nil {
		t.Fatal(err)
	}
}

// overwriteRowOutput mutates durable document state the way a historical or
// older writer can: the row keeps its task identity while its declared output
// slot points at another node.
func (f *referenceLifecycleFixture) overwriteRowOutput(t *testing.T, outputNodeID string) {
	t.Helper()
	document := f.snapshot(t).Document
	boardRow(t, document)["imageNodeId"] = outputNodeID
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.CanvasProject{}).Where("id = ?", f.canvas()).Update("payload_json", string(raw)).Error; err != nil {
		t.Fatal(err)
	}
}

// bindOutput runs the semantic output command and returns its failure reason, so
// a test can prove which replacements the live command surface still refuses.
func (f *referenceLifecycleFixture) bindOutput(t *testing.T, operationID, rowID, outputNodeID string) string {
	t.Helper()
	current := f.snapshot(t)
	if _, err := f.commands.Execute(context.Background(), f.principal, commands.Command{
		Protocol: 1, CanvasID: current.CanvasID, DocumentEpoch: current.DocumentEpoch,
		ClientID: "client", OperationID: operationID, BaseRevision: current.Revision,
		Operations: []commands.Operation{{Type: "bind_storyboard_output", ID: "board", RowID: rowID, OutputMode: "image", SourceNodeID: outputNodeID}},
	}); err == nil {
		return ""
	} else if failure, ok := err.(*commands.Error); ok {
		return failure.Reason
	} else {
		t.Fatalf("unexpected bind failure: %v", err)
		return ""
	}
}

func boardRows(t *testing.T, document commands.Document) []map[string]any {
	t.Helper()
	board := findNode(document, "board")
	if board == nil {
		t.Fatal("board node missing")
	}
	storyboard, _ := metadata(board)["storyboard"].(map[string]any)
	return objectList(storyboard["rows"])
}

func boardRow(t *testing.T, document commands.Document) map[string]any {
	t.Helper()
	rows := boardRows(t, document)
	if len(rows) != 1 {
		t.Fatalf("want one storyboard row got %d", len(rows))
	}
	return rows[0]
}

func nodeMeta(t *testing.T, document commands.Document, id string) map[string]any {
	t.Helper()
	node := findNode(document, id)
	if node == nil {
		t.Fatalf("node %s missing", id)
	}
	return metadata(node)
}

const frozenReferenceInput = `{"mode":"image","metadata":{"referenceNodeIds":["asset"]}}`

// A late successful result must retain the artifact without restoring the row or
// the output relationship the user removed while the task was running.
func TestReferenceLateResultDoesNotRebind(t *testing.T) {
	f := newReferenceLifecycleFixture(t)
	handle := f.submit(t)
	f.freezeInput(t, handle.TaskID, frozenReferenceInput)
	f.readyResource(t, "result-image")

	// Removing the row is an ordinary edit: it drops the row's business
	// relationship, not the artifact the provider already produced.
	f.execute(t, "remove-row", commands.Operation{Type: "edit_storyboard", ID: "board", Action: "remove", RowID: "row"})
	before := f.snapshot(t)
	if len(boardRows(t, before.Document)) != 0 {
		t.Fatal("row still present after removal")
	}
	outputBefore := nodeMeta(t, before.Document, "output")

	f.finish(t, handle.TaskID, model.TaskStatusSucceeded, `{"images":[{"resourceId":"result-image"}]}`)
	if outcome := f.materialize(t, handle.TaskID); outcome != "row_deleted" {
		t.Fatalf("late result outcome = %q, want row_deleted", outcome)
	}

	after := f.snapshot(t)
	if len(boardRows(t, after.Document)) != 0 {
		t.Fatal("late result recreated the deleted row")
	}
	// The detached output releases only its pending version; the row stays deleted.
	delete(outputBefore, "mediaVersion")
	delete(nodeMeta(t, after.Document, "output"), "mediaVersion")
	delete(outputBefore, "errorDetails")
	delete(nodeMeta(t, after.Document, "output"), "errorDetails")
	delete(outputBefore, "taskStatus")
	delete(nodeMeta(t, after.Document, "output"), "taskStatus")
	delete(outputBefore, "status")
	delete(nodeMeta(t, after.Document, "output"), "status")
	if !reflect.DeepEqual(outputBefore, nodeMeta(t, after.Document, "output")) {
		t.Fatal("late result re-owned the detached output")
	}
	tasks, assets, outputs, orders := f.counts(t)
	if tasks != 1 || assets != 1 || outputs != 1 || orders != 0 {
		t.Fatalf("late result duplicated work: tasks=%d assets=%d outputs=%d orders=%d", tasks, assets, outputs, orders)
	}
	if f.submitter.calls != 1 || f.submitter.wakes != 1 {
		t.Fatalf("late result resubmitted: prepare=%d wake=%d", f.submitter.calls, f.submitter.wakes)
	}
	if got := f.taskInput(t, handle.TaskID); got != frozenReferenceInput {
		t.Fatalf("frozen input changed: %s", got)
	}
}

// Undo during a running task applies only its own historical difference: it must
// not regenerate, resubmit or consume the pending result twice, and the late
// result must still follow the durable row, output and task identity.
func TestReferenceUndoDuringTask(t *testing.T) {
	f := newReferenceLifecycleFixture(t)
	handle := f.submit(t)
	f.readyResource(t, "result-image")
	afterSubmit := f.snapshot(t)

	// The user renames the shot while the task runs, then undoes that edit.
	f.execute(t, "edit-row", commands.Operation{Type: "edit_storyboard", ID: "board", RowID: "row", Action: "update", Patch: map[string]any{"plotDescription": "second look"}})
	f.undo(t, "undo-edit")
	undone := f.snapshot(t)
	if !reflect.DeepEqual(afterSubmit.Document, undone.Document) {
		t.Fatal("undo applied more than its own historical difference")
	}
	row := boardRow(t, undone.Document)
	if str(row["taskId"]) != handle.TaskID || str(row["imageNodeId"]) != "output" || str(row["status"]) != "loading" {
		t.Fatalf("undo changed the running task binding: %+v", row)
	}
	if tasks, assets, outputs, orders := f.counts(t); tasks != 1 || assets != 0 || outputs != 0 || orders != 0 {
		t.Fatalf("undo consumed results: tasks=%d assets=%d outputs=%d orders=%d", tasks, assets, outputs, orders)
	}
	if f.submitter.calls != 1 || f.submitter.wakes != 1 {
		t.Fatalf("undo resubmitted: prepare=%d wake=%d", f.submitter.calls, f.submitter.wakes)
	}
	if jobs, err := f.repo.PendingCanvasMaterializations(10); err != nil || len(jobs) != 0 {
		t.Fatalf("undo queued a second consumption: %v %+v", err, jobs)
	}

	// The late result still reaches the row's declared output exactly once.
	f.finish(t, handle.TaskID, model.TaskStatusSucceeded, `{"images":[{"resourceId":"result-image"}]}`)
	if outcome := f.materialize(t, handle.TaskID); outcome != "applied" {
		t.Fatalf("late result outcome = %q, want applied", outcome)
	}
	consumed := f.snapshot(t)
	if row = boardRow(t, consumed.Document); str(row["status"]) != "success" || str(row["taskStatus"]) != "succeeded" {
		t.Fatalf("late result not consumed by the restored row: %+v", row)
	}
	meta := nodeMeta(t, consumed.Document, "output")
	if str(meta["taskId"]) != handle.TaskID || str(meta["status"]) != "success" || str(meta["assetId"]) == "" {
		t.Fatalf("declared output not materialized: %+v", meta)
	}

	// A second task reuses the row; the user removes the row and immediately
	// undoes the removal, so the durable binding, not the historical edit, owns
	// the late terminal state.
	second := f.resubmit(t, "reference-task-2")
	afterSecondSubmit := f.snapshot(t)
	f.execute(t, "remove-row-again", commands.Operation{Type: "edit_storyboard", ID: "board", Action: "remove", RowID: "row"})
	f.undo(t, "undo-remove-row")
	restored := f.snapshot(t)
	if !reflect.DeepEqual(afterSecondSubmit.Document, restored.Document) {
		t.Fatal("undo of the removal did not restore exactly that difference")
	}
	f.finish(t, second.TaskID, model.TaskStatusCancelled, "")
	if outcome := f.materialize(t, second.TaskID); outcome != "applied" {
		t.Fatalf("cancelled result outcome = %q, want applied", outcome)
	}
	restored = f.snapshot(t)
	if row = boardRow(t, restored.Document); str(row["taskId"]) != second.TaskID || str(row["status"]) != "error" || str(row["errorDetails"]) == "" {
		t.Fatalf("cancelled result not delivered to the owning row: %+v", row)
	}
	if tasks, assets, outputs, orders := f.counts(t); tasks != 2 || assets != 1 || outputs != 1 || orders != 0 {
		t.Fatalf("history work was regenerated: tasks=%d assets=%d outputs=%d orders=%d", tasks, assets, outputs, orders)
	}
	if f.submitter.calls != 2 {
		t.Fatalf("undo regenerated the task: prepare=%d", f.submitter.calls)
	}
}

// The same accepted result may be delivered repeatedly: the durable job, the
// stored artifact and the frozen task input must all stay single-valued.
func TestReferenceResultDeliveryIsIdempotent(t *testing.T) {
	f := newReferenceLifecycleFixture(t)
	handle := f.submit(t)
	f.freezeInput(t, handle.TaskID, frozenReferenceInput)
	f.readyResource(t, "result-image")
	consumer := f.bindingConsumer(t, handle.TaskID)
	f.finish(t, handle.TaskID, model.TaskStatusSucceeded, `{"images":[{"resourceId":"result-image"}]}`)

	if outcome := f.materialize(t, handle.TaskID); outcome != "applied" {
		t.Fatalf("first delivery outcome = %q, want applied", outcome)
	}
	delivered := f.snapshot(t)
	tasks, assets, outputs, orders := f.counts(t)
	if tasks != 1 || assets != 1 || outputs != 1 || orders != 0 {
		t.Fatalf("first delivery counts: tasks=%d assets=%d outputs=%d orders=%d", tasks, assets, outputs, orders)
	}

	if outcome := f.materialize(t, handle.TaskID); outcome != "applied" {
		t.Fatalf("repeated delivery outcome = %q, want applied", outcome)
	}
	if err := f.service.DrainMaterializations(context.Background()); err != nil {
		t.Fatal(err)
	}
	if jobs, err := f.repo.PendingCanvasMaterializations(10); err != nil || len(jobs) != 0 {
		t.Fatalf("repeated delivery queued work: %v %+v", err, jobs)
	}

	// A duplicate delivery after the job was re-queued converges instead of
	// writing a second artifact or a second document change.
	f.requeue(t, handle.TaskID)
	if outcome := f.materialize(t, handle.TaskID); outcome != "applied" {
		t.Fatalf("requeued delivery outcome = %q, want applied", outcome)
	}

	repeated := f.snapshot(t)
	if !reflect.DeepEqual(delivered.Document, repeated.Document) {
		t.Fatalf("repeated delivery changed the document: %+v", repeated.Document)
	}
	tasks, assets, outputs, orders = f.counts(t)
	if tasks != 1 || assets != 1 || outputs != 1 || orders != 0 {
		t.Fatalf("repeated delivery duplicated work: tasks=%d assets=%d outputs=%d orders=%d", tasks, assets, outputs, orders)
	}
	if f.submitter.calls != 1 || f.submitter.wakes != 1 {
		t.Fatalf("repeated delivery resubmitted: prepare=%d wake=%d", f.submitter.calls, f.submitter.wakes)
	}
	if got := f.taskInput(t, handle.TaskID); got != frozenReferenceInput {
		t.Fatalf("repeated delivery changed the frozen input: %s", got)
	}
	if got := f.bindingConsumer(t, handle.TaskID); got != consumer {
		t.Fatalf("repeated delivery changed the frozen consumer: %s", got)
	}
}

// The terminal callback must verify the row's declared output slot for failures
// and cancellations too. Durable documents can be rewritten by history, an older
// writer or a manual repair without the durable task identity, and then the old
// output and row state must not be written back.
func TestReferenceFailedResultDoesNotWriteReplacedOutput(t *testing.T) {
	for _, status := range []model.TaskStatus{model.TaskStatusFailed, model.TaskStatusCancelled} {
		t.Run(string(status), func(t *testing.T) {
			f := newReferenceLifecycleFixture(t)
			handle := f.submit(t)
			f.freezeInput(t, handle.TaskID, frozenReferenceInput)

			// The live command surface still refuses to rebind a row that owns a
			// pending task, so this durable state can only come from history, an
			// older writer or a manual repair. The terminal callback must verify the
			// declared output binding anyway.
			if reason := f.bindOutput(t, "rebind-busy", "row", "replacement"); reason != "reference_busy" {
				t.Fatalf("busy row accepted a replaced output: %q", reason)
			}
			f.overwriteRowOutput(t, "replacement")

			before := f.snapshot(t)
			row := boardRow(t, before.Document)
			if str(row["taskId"]) != handle.TaskID || str(row["imageNodeId"]) != "replacement" {
				t.Fatalf("fixture did not keep the task identity with a replaced output: %+v", row)
			}
			outputBefore := nodeMeta(t, before.Document, "output")
			replacementBefore := nodeMeta(t, before.Document, "replacement")

			f.finish(t, handle.TaskID, status, "")
			if outcome := f.materialize(t, handle.TaskID); outcome != "output_binding_changed" {
				t.Fatalf("terminal outcome = %q, want output_binding_changed", outcome)
			}

			after := f.snapshot(t)
			// Clearing the original version's pending projection does not rebind or
			// replace media, even after a legacy writer detached the output.
			for _, m := range []map[string]any{outputBefore, nodeMeta(t, after.Document, "output")} {
				for _, key := range []string{"mediaVersion", "status", "taskStatus", "errorDetails"} {
					delete(m, key)
				}
			}
			// The host pipeline reconciles derived managed edges from the declared
			// fields, but no node may be rewritten by the terminal callback.
			if !reflect.DeepEqual(before.Document["nodes"], after.Document["nodes"]) {
				t.Fatalf("terminal state wrote back a replaced binding: %+v", after.Document["nodes"])
			}
			row = boardRow(t, after.Document)
			if row["errorDetails"] != nil || str(row["taskId"]) != handle.TaskID || str(row["status"]) != "loading" || str(row["taskStatus"]) != "queued" {
				t.Fatalf("terminal state rewrote the replaced row: %+v", row)
			}
			if !reflect.DeepEqual(outputBefore, nodeMeta(t, after.Document, "output")) {
				t.Fatalf("terminal state wrote the detached old output: %+v", nodeMeta(t, after.Document, "output"))
			}
			if !reflect.DeepEqual(replacementBefore, nodeMeta(t, after.Document, "replacement")) {
				t.Fatalf("terminal state wrote the replacement output: %+v", nodeMeta(t, after.Document, "replacement"))
			}
			tasks, assets, outputs, orders := f.counts(t)
			if tasks != 1 || assets != 0 || outputs != 0 || orders != 0 {
				t.Fatalf("terminal state changed durable work: tasks=%d assets=%d outputs=%d orders=%d", tasks, assets, outputs, orders)
			}
			if f.submitter.calls != 1 || f.submitter.wakes != 1 {
				t.Fatalf("terminal state resubmitted: prepare=%d wake=%d", f.submitter.calls, f.submitter.wakes)
			}
			if got := f.taskInput(t, handle.TaskID); got != frozenReferenceInput {
				t.Fatalf("terminal state changed the frozen input: %s", got)
			}
		})
	}
}

// A failed task that still owns its row always reaches that row, and a stale
// result may never clobber a newer accepted task on the same row or output.
func TestReferenceFailedResultStopsAtNewerOwner(t *testing.T) {
	f := newReferenceLifecycleFixture(t)
	stale := f.submit(t)

	// The provider fails the first task; its writeback has not run yet.
	f.finish(t, stale.TaskID, model.TaskStatusFailed, "")
	firstDelivered := f.snapshot(t)
	if row := boardRow(t, firstDelivered.Document); str(row["taskId"]) != stale.TaskID || str(row["status"]) != "loading" {
		t.Fatalf("failed task lost its pending row: %+v", row)
	}

	// Versioned nodes wait for the original writeback before another paid task.
	if _, err := f.service.Submit(context.Background(), f.principal, func() SubmitRequest {
		r := f.request
		r.Command.OperationID = "too-early"
		r.Command.BaseRevision = firstDelivered.Revision
		return r
	}()); err == nil {
		t.Fatal("unresolved result allowed a second task")
	}
	f.materialize(t, stale.TaskID)
	next := f.resubmit(t, "reference-task-2")
	pending := f.snapshot(t)
	row := boardRow(t, pending.Document)
	if str(row["taskId"]) != next.TaskID || str(row["status"]) != "loading" || str(row["taskStatus"]) != "queued" {
		t.Fatalf("new task did not take the row: %+v", row)
	}
	output := nodeMeta(t, pending.Document, "output")
	if str(output["taskId"]) != next.TaskID || str(output["status"]) != "loading" {
		t.Fatalf("new task did not take the output: %+v", output)
	}

	// The stale failure arrives after the newer task took over: it must be
	// recorded as changed binding only, without touching row, output or billing.
	if outcome := f.materialize(t, stale.TaskID); outcome != "applied" {
		t.Fatalf("stale result replay outcome = %q, want applied", outcome)
	}
	after := f.snapshot(t)
	if !reflect.DeepEqual(pending.Document, after.Document) {
		t.Fatalf("stale result changed the newer task state: %+v", after.Document)
	}
	output = nodeMeta(t, after.Document, "output")
	if str(output["taskId"]) != next.TaskID || str(output["status"]) != "loading" || output["errorDetails"] != nil {
		t.Fatalf("stale result rewrote the output: %+v", output)
	}
	tasks, assets, outputs, orders := f.counts(t)
	if tasks != 2 || assets != 0 || outputs != 0 || orders != 0 {
		t.Fatalf("stale result duplicated work: tasks=%d assets=%d outputs=%d orders=%d", tasks, assets, outputs, orders)
	}
	if f.submitter.calls != 2 || f.submitter.wakes != 2 {
		t.Fatalf("stale result resubmitted: prepare=%d wake=%d", f.submitter.calls, f.submitter.wakes)
	}

	// The newer task still materializes normally afterwards.
	f.readyResource(t, "result-image")
	f.finish(t, next.TaskID, model.TaskStatusSucceeded, `{"images":[{"resourceId":"result-image"}]}`)
	if outcome := f.materialize(t, next.TaskID); outcome != "applied" {
		t.Fatalf("newer task outcome = %q, want applied", outcome)
	}
	delivered := f.snapshot(t)
	if row = boardRow(t, delivered.Document); str(row["taskId"]) != next.TaskID || str(row["status"]) != "success" {
		t.Fatalf("newer task not consumed: %+v", row)
	}
	if meta := nodeMeta(t, delivered.Document, "output"); str(meta["taskId"]) != next.TaskID || str(meta["status"]) != "success" || str(meta["assetId"]) == "" {
		t.Fatalf("newer task output not materialized: %+v", meta)
	}
	if tasks, assets, outputs, orders = f.counts(t); tasks != 2 || assets != 1 || outputs != 1 || orders != 0 {
		t.Fatalf("newer task counts: tasks=%d assets=%d outputs=%d orders=%d", tasks, assets, outputs, orders)
	}
}

// A failure that still owns its row and output delivers the terminal state to
// both, without producing an artifact or a second provider submission.
func TestReferenceFailedResultMarksOwningRow(t *testing.T) {
	f := newReferenceLifecycleFixture(t)
	handle := f.submit(t)

	f.finish(t, handle.TaskID, model.TaskStatusFailed, "")
	if outcome := f.materialize(t, handle.TaskID); outcome != "applied" {
		t.Fatalf("failed result outcome = %q, want applied", outcome)
	}
	delivered := f.snapshot(t)
	row := boardRow(t, delivered.Document)
	if str(row["taskId"]) != handle.TaskID || str(row["status"]) != "error" || str(row["taskStatus"]) != "failed" || str(row["errorDetails"]) == "" {
		t.Fatalf("owning row did not receive the terminal state: %+v", row)
	}
	meta := nodeMeta(t, delivered.Document, "output")
	if str(meta["taskId"]) != handle.TaskID || str(meta["status"]) != "error" || str(meta["taskStatus"]) != "failed" {
		t.Fatalf("owning output did not receive the terminal state: %+v", meta)
	}
	tasks, assets, outputs, orders := f.counts(t)
	if tasks != 1 || assets != 0 || outputs != 0 || orders != 0 {
		t.Fatalf("failed result produced an artifact: tasks=%d assets=%d outputs=%d orders=%d", tasks, assets, outputs, orders)
	}
	if f.submitter.calls != 1 || f.submitter.wakes != 1 {
		t.Fatalf("failed result resubmitted: prepare=%d wake=%d", f.submitter.calls, f.submitter.wakes)
	}
}
