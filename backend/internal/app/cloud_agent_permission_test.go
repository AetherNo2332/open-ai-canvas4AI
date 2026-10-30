package app

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"gorm.io/gorm"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
)

// permissionTestRun 建一个运行（权限 mode），并把画布放在一个已知状态。
func permissionTestRun(t *testing.T, mode string) (*Service, *gorm.DB, *CloudAgentRun) {
	t.Helper()
	s, db, _, _ := creationTestService(t)
	if err := db.Create(&model.CanvasProject{ID: "agent-canvas", UserID: "user", PayloadJSON: `{"nodes":[]}`}).Error; err != nil {
		t.Fatal(err)
	}
	req := agentTestRequest()
	req.PermissionMode = mode
	root, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	return s, db, root
}

func permissionAddNodeArgs(t *testing.T, db *gorm.DB, id string) string {
	t.Helper()
	var canvas model.CanvasProject
	if err := db.First(&canvas, "id = ?", "agent-canvas").Error; err != nil {
		t.Fatal(err)
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{
		"snapshotHash": cloudAgentCanvasHash(doc),
		"ops":          []map[string]any{{"type": "add_node", "id": id, "nodeType": "text", "title": id, "content": "权限测试"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func permissionCanvasHas(t *testing.T, db *gorm.DB, id string) bool {
	t.Helper()
	var canvas model.CanvasProject
	if err := db.First(&canvas, "id = ?", "agent-canvas").Error; err != nil {
		t.Fatal(err)
	}
	return strings.Contains(canvas.PayloadJSON, `"`+id+`"`)
}

func permissionSnapshotAllowed(t *testing.T, s *Service, runID, tool string) bool {
	t.Helper()
	run, _ := agentInterjectionState(t, s, runID)
	snapshot, err := s.PiAgentSnapshot("user", runID, run.LeaseOwner)
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range snapshot.Tools {
		if spec.Name == tool {
			return spec.Allowed
		}
	}
	return false
}

func permissionLastToolEvent(state cloudAgentRuntime, callID string) *CloudAgentEvent {
	for i := len(state.Events) - 1; i >= 0; i-- {
		if state.Events[i].Payload["callId"] == callID && strings.HasPrefix(state.Events[i].Type, "tool_") {
			return &state.Events[i]
		}
	}
	return nil
}

// 只读开局，中途放宽为自动执行：下一次写入直接执行，不弹审批；工具出现在下一份快照的 allowed 里。
func TestCloudAgentPermissionLoosenReadOnlyToAutoAppliesNextWrite(t *testing.T) {
	s, db, root := permissionTestRun(t, "read_only")
	agentSettleToolStep(t, s, db, root.ID, "read-1", "canvas_get_state", `{}`)
	if permissionSnapshotAllowed(t, s, root.ID, "canvas_apply_ops") {
		t.Fatal("read_only run must not allow canvas_apply_ops")
	}
	result, err := s.ChangeCloudAgentPermission("user", root.ID, "auto", "perm-1")
	if err != nil || !result.Changed || result.PermissionMode != "auto" {
		t.Fatalf("change permission: %+v %v", result, err)
	}
	if !permissionSnapshotAllowed(t, s, root.ID, "canvas_apply_ops") {
		t.Fatal("canvas_apply_ops must be allowed in the next snapshot after loosening")
	}
	run, state := agentSettleToolStep(t, s, db, root.ID, "write-1", "canvas_apply_ops", permissionAddNodeArgs(t, db, "loosen-node"))
	if run.Status == "waiting_approval" || state.Approval != nil {
		t.Fatalf("auto write must not request approval: status=%s", run.Status)
	}
	if !permissionCanvasHas(t, db, "loosen-node") {
		t.Fatal("write after loosening to auto did not reach the canvas")
	}
	if !agentHasEvent(state, "permission_changed") {
		t.Fatal("permission_changed event missing")
	}
}

// 自动执行中途收紧为只读：下一次写入被拒，工具不再出现在 allowed。
func TestCloudAgentPermissionTightenAutoToReadOnlyRejectsNextWrite(t *testing.T) {
	s, db, root := permissionTestRun(t, "auto")
	agentSettleToolStep(t, s, db, root.ID, "read-1", "canvas_get_state", `{}`)
	if !permissionSnapshotAllowed(t, s, root.ID, "canvas_apply_ops") {
		t.Fatal("auto run must allow canvas_apply_ops")
	}
	if _, err := s.ChangeCloudAgentPermission("user", root.ID, "read_only", "perm-1"); err != nil {
		t.Fatal(err)
	}
	if permissionSnapshotAllowed(t, s, root.ID, "canvas_apply_ops") {
		t.Fatal("canvas_apply_ops must not be allowed after tightening")
	}
	// 模型拿着旧工具表仍然发出写入：服务端必须拒绝，画布不变。
	args := permissionAddNodeArgs(t, db, "tight-node")
	run, state := agentStagePiOutputForTest(t, s, db, root.ID, "", []cloudAgentCall{piAgentTestCall("write-1", "canvas_apply_ops", args)})
	_ = agentPiExecuteCallForTest(t, s, root.ID, "write-1")
	run, state = agentInterjectionState(t, s, root.ID)
	if permissionCanvasHas(t, db, "tight-node") {
		t.Fatal("write after tightening to read_only reached the canvas")
	}
	if run.Status == "waiting_approval" {
		t.Fatal("read_only write must not request approval")
	}
	if event := permissionLastToolEvent(state, "write-1"); event == nil || event.Type != "tool_failed" {
		t.Fatalf("write after tightening must fail as a tool result: %+v", event)
	}
}

// 自动执行改为请求审批：下一次写入生成审批卡。
func TestCloudAgentPermissionAutoToRequestApprovalCreatesApproval(t *testing.T) {
	s, db, root := permissionTestRun(t, "auto")
	agentSettleToolStep(t, s, db, root.ID, "read-1", "canvas_get_state", `{}`)
	if _, err := s.ChangeCloudAgentPermission("user", root.ID, "request_approval", "perm-1"); err != nil {
		t.Fatal(err)
	}
	run, state := agentSettleToolStep(t, s, db, root.ID, "write-1", "canvas_apply_ops", permissionAddNodeArgs(t, db, "approval-node"))
	if run.Status != "waiting_approval" || state.Approval == nil {
		t.Fatalf("request_approval write must create an approval: status=%s", run.Status)
	}
	if permissionCanvasHas(t, db, "approval-node") {
		t.Fatal("write was applied before approval")
	}
}

// 等待审批时切到自动执行：审批卡保留、仍需用户决定；批准后执行，之后的写入不再审批。
func TestCloudAgentPermissionPendingApprovalSurvivesSwitchToAuto(t *testing.T) {
	s, db, root := permissionTestRun(t, "request_approval")
	agentSettleToolStep(t, s, db, root.ID, "read-1", "canvas_get_state", `{}`)
	run, state := agentSettleToolStep(t, s, db, root.ID, "write-1", "canvas_apply_ops", permissionAddNodeArgs(t, db, "pending-node"))
	if run.Status != "waiting_approval" || state.Approval == nil {
		t.Fatalf("expected waiting approval: %s", run.Status)
	}
	approvalID := state.Approval.ID
	if _, err := s.ChangeCloudAgentPermission("user", root.ID, "auto", "perm-1"); err != nil {
		t.Fatal(err)
	}
	run, state = agentInterjectionState(t, s, root.ID)
	if run.Status != "waiting_approval" || state.Approval == nil || state.Approval.ID != approvalID || state.Approval.Decision != "" {
		t.Fatalf("pending approval must stay untouched: status=%s approval=%+v", run.Status, state.Approval)
	}
	// 未决审批期间推进也不能绕过：工具仍停在审批上。
	if err := advancePiAgentForTest(t, s, root.ID); err != nil {
		t.Fatal(err)
	}
	if permissionCanvasHas(t, db, "pending-node") {
		t.Fatal("switching to auto executed the pending write without approval")
	}
	if err := s.DecideCloudAgentApproval("user", root.ID, approvalID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	if err := agentPiExecuteCallForTest(t, s, root.ID, "write-1"); err != nil {
		t.Fatal(err)
	}
	if !permissionCanvasHas(t, db, "pending-node") {
		t.Fatal("approved write did not execute")
	}
	run, state = agentSettleToolStep(t, s, db, root.ID, "write-2", "canvas_apply_ops", permissionAddNodeArgs(t, db, "after-node"))
	if run.Status == "waiting_approval" || !permissionCanvasHas(t, db, "after-node") {
		t.Fatalf("write after approval must follow auto mode: status=%s", run.Status)
	}
}

func permissionReason(err error) kernel.ErrorReason {
	var appErr *kernel.AppError
	if errors.As(err, &appErr) {
		return appErr.Reason
	}
	return ""
}

func TestCloudAgentPermissionChangeGuards(t *testing.T) {
	s, db, root := permissionTestRun(t, "read_only")
	if _, err := s.ChangeCloudAgentPermission("user", root.ID, "everything", "perm-bad"); err == nil {
		t.Fatal("invalid mode accepted")
	}
	if _, err := s.ChangeCloudAgentPermission("user", root.ID, "auto", ""); err == nil {
		t.Fatal("empty requestId accepted")
	}
	if _, err := s.ChangeCloudAgentPermission("someone-else", root.ID, "auto", "perm-other"); permissionReason(err) != kernel.ReasonNotFound {
		t.Fatalf("other user must get not found: %v", err)
	}
	// 同一 requestId 重放只记一次。
	for i := 0; i < 2; i++ {
		if result, err := s.ChangeCloudAgentPermission("user", root.ID, "auto", "perm-same"); err != nil || !result.Changed {
			t.Fatalf("idempotent replay %d: %+v %v", i, result, err)
		}
	}
	_, state := agentInterjectionState(t, s, root.ID)
	if len(state.PermissionChanges) != 1 || state.StartPermissionMode != "read_only" || state.Request.PermissionMode != "auto" {
		t.Fatalf("replay must record one change: %+v start=%s", state.PermissionChanges, state.StartPermissionMode)
	}
	// 同权限是无操作成功。
	if result, err := s.ChangeCloudAgentPermission("user", root.ID, "auto", "perm-noop"); err != nil || result.Changed {
		t.Fatalf("same mode must be a no-op: %+v %v", result, err)
	}
	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", root.ID).Update("status", "completed").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.ChangeCloudAgentPermission("user", root.ID, "read_only", "perm-done"); permissionReason(err) != kernel.ReasonAgentRunFinished {
		t.Fatalf("finished run must be refused with agent_run_finished: %v", err)
	}
}

func TestCloudAgentPermissionRefusesSubagentRun(t *testing.T) {
	s, db, root := permissionTestRun(t, "read_only")
	run, state := agentInterjectionState(t, s, root.ID)
	state.Subagent = &SubagentRuntime{LinkID: "link", ParentRunID: "parent", ChildRunID: root.ID, DisplayName: "子", RoleLabel: "r", Objective: "o", Depth: 1}
	if err := cloudAgentSave(run, &state); err != nil {
		t.Fatal(err)
	}
	if err := db.Save(run).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.ChangeCloudAgentPermission("user", root.ID, "auto", "perm-child"); permissionReason(err) != kernel.ReasonAgentSubagentPermissionFixed {
		t.Fatalf("child run must be refused: %v", err)
	}
}

// 下一步前多次切换只送一条通知：from 为模型最后知道的权限，to 为最终值；
// 系统提示与缓存键始终按开局权限。
func TestCloudAgentPermissionNoticeCoalescesAndKeepsPromptIdentity(t *testing.T) {
	s, db, root := permissionTestRun(t, "read_only")
	_, before := agentSettleToolStep(t, s, db, root.ID, "read-1", "canvas_get_state", `{}`)
	if _, err := s.InterjectCloudAgent("user", root.ID, "msg-1", "继续"); err != nil {
		t.Fatal(err)
	}
	for i, mode := range []string{"auto", "request_approval"} {
		if _, err := s.ChangeCloudAgentPermission("user", root.ID, mode, "perm-"+string(rune('a'+i))); err != nil {
			t.Fatal(err)
		}
	}
	_, state := agentInterjectionState(t, s, root.ID)
	notices := 0
	var notice cloudAgentInterjection
	for _, item := range state.PendingInterjections {
		if item.Source == cloudAgentPermissionSource {
			notices++
			notice = item
		}
	}
	if notices != 1 || !strings.Contains(notice.Text, "从「只读」改为「请求审批」") || !strings.HasPrefix(notice.Text, "[系统通知]") {
		t.Fatalf("expected one coalesced notice read_only→request_approval: %+v", state.PendingInterjections)
	}
	if state.PendingInterjections[0].ID != "msg-1" {
		t.Fatalf("notice must queue after earlier interjections: %+v", state.PendingInterjections)
	}
	if state.Canonical.SystemPrompt != before.Canonical.SystemPrompt {
		t.Fatal("system prompt changed after permission switch")
	}
	if cloudAgentPromptCacheKey(cloudAgentCacheRequest(&state)) != cloudAgentPromptCacheKey(cloudAgentCacheRequest(&before)) ||
		cloudAgentPromptCacheIdentity(cloudAgentCacheRequest(&state), state.Policy) != cloudAgentPromptCacheIdentity(cloudAgentCacheRequest(&before), before.Policy) {
		t.Fatal("prompt cache identity changed after permission switch")
	}
	// 切回模型已知的权限：通知撤掉。
	if _, err := s.ChangeCloudAgentPermission("user", root.ID, "read_only", "perm-back"); err != nil {
		t.Fatal(err)
	}
	_, state = agentInterjectionState(t, s, root.ID)
	for _, item := range state.PendingInterjections {
		if item.Source == cloudAgentPermissionSource {
			t.Fatalf("switching back to the known mode must drop the notice: %+v", item)
		}
	}
}

// 通知按插话通路送达一次：Pi 用户检查点确认后出队，并发 permission_notice_delivered。
func TestCloudAgentPermissionNoticeDeliveredOnceAtNextStep(t *testing.T) {
	s, db, root := permissionTestRun(t, "read_only")
	agentSettleToolStep(t, s, db, root.ID, "read-1", "canvas_get_state", `{}`)
	if _, err := s.InterjectCloudAgent("user", root.ID, "msg-1", "先读完"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ChangeCloudAgentPermission("user", root.ID, "auto", "perm-1"); err != nil {
		t.Fatal(err)
	}
	run, state := agentInterjectionState(t, s, root.ID)
	snapshot, err := s.PiAgentSnapshot("user", root.ID, run.LeaseOwner)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.PendingInterjections) != 2 || snapshot.PendingInterjections[1].Source != cloudAgentPermissionSource {
		t.Fatalf("snapshot must carry interjection then notice: %+v", snapshot.PendingInterjections)
	}
	var ids, texts []string
	for _, item := range state.PendingInterjections {
		ids = append(ids, item.ID)
		texts = append(texts, cloudAgentInterjectionContent(item))
	}
	agentPiCheckpointForTest(t, s, root.ID, "", map[string]any{"role": "user", "content": []map[string]any{{"type": "text", "text": strings.Join(texts, "\n\n")}}}, ids)
	_, state = agentInterjectionState(t, s, root.ID)
	if len(state.PendingInterjections) != 0 || !agentHasEvent(state, "permission_notice_delivered") || !agentHasEvent(state, "user_interjection_delivered") {
		t.Fatalf("notice must be delivered with the interjection: pending=%+v", state.PendingInterjections)
	}
	// 再次重放同一检查点不会重复送达。
	if _, err := s.ChangeCloudAgentPermission("user", root.ID, "auto", "perm-1"); err != nil {
		t.Fatal(err)
	}
	_, state = agentInterjectionState(t, s, root.ID)
	if len(state.PendingInterjections) != 0 {
		t.Fatal("replayed permission request re-queued the notice")
	}
}

// 工具目录在放宽后补齐新合格工具，已有工具定义不变（schema 不漂移）。
func TestCloudAgentPermissionCatalogExtendsWithoutSchemaDrift(t *testing.T) {
	s, _, root := permissionTestRun(t, "read_only")
	_, before := agentInterjectionState(t, s, root.ID)
	if _, err := s.ChangeCloudAgentPermission("user", root.ID, "auto", "perm-1"); err != nil {
		t.Fatal(err)
	}
	_, after := agentInterjectionState(t, s, root.ID)
	if err := cloudAgentToolSchemaDrift(before.Canonical.Tools, after.Canonical.Tools); err != nil {
		t.Fatal(err)
	}
	names := cloudAgentToolNames(after.Canonical.Tools)
	for _, name := range []string{"canvas_apply_ops", "canvas_arrange_nodes", "canvas_undo"} {
		if !cloudAgentContainsString(names, name) {
			t.Fatalf("catalog missing %s after loosening: %v", name, names)
		}
	}
	run, _ := agentStartPiModelStep(t, s, root.ID)
	snapshot, err := s.PiAgentSnapshot("user", root.ID, run.LeaseOwner)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"canvas_apply_ops", "generate_media", "spawn_subagent", "send_parent_message"} {
		if !cloudAgentContainsString(snapshot.ToolCatalog, name) {
			t.Fatalf("worker lifetime catalog missing %s", name)
		}
	}
}

// 收紧到只读后，卡片上的写入已不被允许：批准被明确拒绝且卡片保留，改回权限后仍可批准执行。
func TestCloudAgentPermissionApproveAfterTighteningIsRefused(t *testing.T) {
	s, db, root := permissionTestRun(t, "request_approval")
	agentSettleToolStep(t, s, db, root.ID, "read-1", "canvas_get_state", `{}`)
	run, state := agentSettleToolStep(t, s, db, root.ID, "write-1", "canvas_apply_ops", permissionAddNodeArgs(t, db, "pending-node"))
	if run.Status != "waiting_approval" || state.Approval == nil {
		t.Fatalf("expected waiting approval: %s", run.Status)
	}
	approvalID := state.Approval.ID
	if _, err := s.ChangeCloudAgentPermission("user", root.ID, "read_only", "perm-ro"); err != nil {
		t.Fatal(err)
	}
	if err := s.DecideCloudAgentApproval("user", root.ID, approvalID, "approve", ""); permissionReason(err) != kernel.ReasonAgentApprovalNotPermitted {
		t.Fatalf("approve under read_only error = %v", err)
	}
	run, state = agentInterjectionState(t, s, root.ID)
	if run.Status != "waiting_approval" || state.Approval == nil || state.Approval.ID != approvalID || state.Approval.Decision != "" {
		t.Fatalf("refused approval must leave the card pending: status=%s approval=%+v", run.Status, state.Approval)
	}
	if _, err := s.ChangeCloudAgentPermission("user", root.ID, "request_approval", "perm-back"); err != nil {
		t.Fatal(err)
	}
	if err := s.DecideCloudAgentApproval("user", root.ID, approvalID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	if err := agentPiExecuteCallForTest(t, s, root.ID, "write-1"); err != nil {
		t.Fatal(err)
	}
	if !permissionCanvasHas(t, db, "pending-node") {
		t.Fatal("approved write did not execute after restoring permission")
	}
}
