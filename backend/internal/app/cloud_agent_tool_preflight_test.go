package app

import (
	"encoding/json"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

// 整批预检 + 单步最多一个写入/生成调用（handoff 工作项 B 第 2 步）。
// 这些用例走真实运行期：预检结论随检查点持久化，执行侧按结论放行或拒绝。

// canvasPayloadJSON 读当前画布正文，用于断言"被跳过的写入真的没落库"。
func canvasPayloadJSON(t *testing.T, s *Service) string {
	t.Helper()
	canvas, err := s.repo.CanvasProjectForUser("user", "agent-canvas")
	if err != nil {
		t.Fatal(err)
	}
	return canvas.PayloadJSON
}

func reloadAgentRun(t *testing.T, s *Service, runID string) (*model.CloudAgentExecution, cloudAgentRuntime) {
	t.Helper()
	execution, err := s.repo.CloudAgent("user", runID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(execution)
	if err != nil {
		t.Fatal(err)
	}
	return execution, state
}

// saveBatchState 写入批次与预检结论；写完必须重新读取（revision 已变）。
func saveBatchState(t *testing.T, s *Service, run *model.CloudAgentExecution, state *cloudAgentRuntime) (*model.CloudAgentExecution, cloudAgentRuntime) {
	t.Helper()
	if err := s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		return cloudAgentSave(current, state)
	}); err != nil {
		t.Fatal(err)
	}
	return reloadAgentRun(t, s, run.ID)
}

// lastAgentToolEvent 取最近一条工具事件：顶层载荷 + 结果明细（字段级错误在明细里）。
func lastAgentToolEvent(t *testing.T, s *Service, runID string) (map[string]any, map[string]any) {
	t.Helper()
	_, runtimeState := reloadAgentRun(t, s, runID)
	for index := len(runtimeState.Events) - 1; index >= 0; index-- {
		if !strings.HasPrefix(runtimeState.Events[index].Type, "tool_") {
			continue
		}
		payload := runtimeState.Events[index].Payload
		detail, _ := payload["result"].(map[string]any)
		if detail == nil {
			detail = map[string]any{}
		}
		return payload, detail
	}
	t.Fatal("缺少工具事件")
	return nil, nil
}

func preflightApplyCall(t *testing.T, id, snapshotHash, nodeID, title string) cloudAgentCall {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"snapshotHash": snapshotHash,
		"ops": []map[string]any{
			{"type": "update_node", "id": nodeID, "patch": map[string]any{"title": title}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	call := cloudAgentCall{ID: id}
	call.Function.Name = "canvas_apply_ops"
	call.Function.Arguments = string(raw)
	return call
}

func readOnlyCall(id, name string) cloudAgentCall {
	call := cloudAgentCall{ID: id}
	call.Function.Name = name
	call.Function.Arguments = `{}`
	return call
}

func writeBatch(t *testing.T, s *Service, run *model.CloudAgentExecution, state *cloudAgentRuntime, calls []cloudAgentCall) (*model.CloudAgentExecution, cloudAgentRuntime) {
	t.Helper()
	state.Calls = calls
	state.CallAdmissions = cloudAgentPreflightBatch(state, calls)
	return saveBatchState(t, s, run, state)
}

// 一批两个写工具：只执行第一个，第二个以 single_write_per_step 明确跳过（不是失败），
// 且画布只被改了一次。
func TestCloudAgentBatchExecutesOneWriteAndSkipsTheRest(t *testing.T) {
	s, _, args := agentMediaFixture(t)
	run, state := agentMediaRun(t, s, args, "auto")

	run, state = writeBatch(t, s, run, &state, []cloudAgentCall{
		preflightApplyCall(t, "call-1", args.SnapshotHash, "cat", "第一个写入"),
		preflightApplyCall(t, "call-2", args.SnapshotHash, "hero", "第二个写入"),
	})
	if !state.CallAdmissions[0].Allowed {
		t.Fatalf("第一个写入应被放行：%+v", state.CallAdmissions[0])
	}
	if state.CallAdmissions[1].Allowed || state.CallAdmissions[1].Issue != cloudAgentAdmissionSingleWrite {
		t.Fatalf("同批第二个写入应被跳过：%+v", state.CallAdmissions[1])
	}

	if err := s.advanceCloudAgentTool(run, &state); err != nil {
		t.Fatal(err)
	}
	afterFirst := canvasPayloadJSON(t, s)
	if !strings.Contains(afterFirst, "第一个写入") {
		t.Fatalf("第一个写入没有落到画布：%s", afterFirst)
	}
	run, state = reloadAgentRun(t, s, run.ID)

	if err := s.advanceCloudAgentTool(run, &state); err != nil {
		t.Fatal(err)
	}
	payload, detail := lastAgentToolEvent(t, s, run.ID)
	if payload["errorClass"] != cloudAgentToolErrorCallSkipped || detail["admission"] != cloudAgentAdmissionSingleWrite {
		t.Fatalf("跳过的写入应给出 call_skipped/single_write_per_step：%+v / %+v", payload, detail)
	}
	if detail["errorClassLabel"] != "本步未执行" || detail["requiredAction"] != "resubmit_next_step" {
		t.Fatalf("跳过应给出可重发的口径：%+v", detail)
	}
	if afterSecond := canvasPayloadJSON(t, s); afterSecond != afterFirst {
		t.Fatalf("被跳过的写入不应该改画布\nbefore=%s\nafter=%s", afterFirst, afterSecond)
	}
}

// 一批多个同类错误不会变成多条业务失败：第一个参数不合法之后，同批后续写入整批取消；
// 而且第一条失败是**字段级**的（业务代码根本没执行）。
func TestCloudAgentPreflightCancelsRemainingWritesAfterSchemaFailure(t *testing.T) {
	s, _, args := agentMediaFixture(t)
	run, state := agentMediaRun(t, s, args, "auto")

	broken := preflightApplyCall(t, "call-1", args.SnapshotHash, "cat", "缺 ops")
	broken.Function.Arguments = `{"snapshotHash":"` + args.SnapshotHash + `"}`
	run, state = writeBatch(t, s, run, &state, []cloudAgentCall{
		broken,
		preflightApplyCall(t, "call-2", args.SnapshotHash, "hero", "后续写入 2"),
		preflightApplyCall(t, "call-3", args.SnapshotHash, "hero", "后续写入 3"),
		preflightApplyCall(t, "call-4", args.SnapshotHash, "hero", "后续写入 4"),
	})
	if state.CallAdmissions[0].Allowed || state.CallAdmissions[0].Field != "ops" ||
		state.CallAdmissions[0].Issue != "schema_error" || state.CallAdmissions[0].FieldIssue != "required" {
		t.Fatalf("缺必填字段应被字段级拒绝：%+v", state.CallAdmissions[0])
	}
	for index := 1; index < len(state.CallAdmissions); index++ {
		if state.CallAdmissions[index].Allowed || state.CallAdmissions[index].Issue != cloudAgentAdmissionBatchCancel {
			t.Fatalf("同批后续写入应整批取消：%+v", state.CallAdmissions[index])
		}
	}
	before := canvasPayloadJSON(t, s)

	if err := s.advanceCloudAgentTool(run, &state); err != nil {
		t.Fatal(err)
	}
	payload, detail := lastAgentToolEvent(t, s, run.ID)
	if detail["reason"] != "invalid_tool_arguments" || detail["field"] != "ops" || detail["issue"] != "required" {
		t.Fatalf("缺必填字段应回字段级参数错误：%+v / %+v", payload, detail)
	}
	if payload["errorClass"] != cloudAgentToolErrorSchemaError {
		t.Fatalf("应归类为 schema_error：%+v", payload)
	}
	run, state = reloadAgentRun(t, s, run.ID)

	for index := 1; index < len(state.Calls); index++ {
		if err := s.advanceCloudAgentTool(run, &state); err != nil {
			t.Fatal(err)
		}
		payload, detail = lastAgentToolEvent(t, s, run.ID)
		if detail["admission"] != cloudAgentAdmissionBatchCancel || payload["errorClass"] != cloudAgentToolErrorCallSkipped {
			t.Fatalf("第 %d 个调用应以 batch_cancelled 跳过：%+v / %+v", index, payload, detail)
		}
		run, state = reloadAgentRun(t, s, run.ID)
	}
	// 第一条（真的参数错）消耗一次纠错名额；后面三个"批次跳过"不得再消耗。
	if repair, ok := state.ToolRepairs["canvas_apply_ops"]; !ok || repair.Attempt != 1 {
		t.Fatalf("只有参数错的那条应消耗一次纠错名额：%+v", state.ToolRepairs)
	}
	if after := canvasPayloadJSON(t, s); after != before {
		t.Fatalf("整批都不该改画布\nbefore=%s\nafter=%s", before, after)
	}
}

// 本轮工具表里没有的名字在业务执行前就被拒（invalid_model_output），不是普通工具失败。
func TestCloudAgentPreflightRejectsToolOutsideThisRun(t *testing.T) {
	s, _, args := agentMediaFixture(t)
	run, state := agentMediaRun(t, s, args, "auto")

	unknown := cloudAgentCall{ID: "call-1"}
	unknown.Function.Name = "canvas_delete_everything"
	unknown.Function.Arguments = `{}`
	run, state = writeBatch(t, s, run, &state, []cloudAgentCall{unknown})
	if state.CallAdmissions[0].Allowed || state.CallAdmissions[0].Issue != cloudAgentAdmissionInvalidOutput {
		t.Fatalf("工具表外的调用应被拒：%+v", state.CallAdmissions[0])
	}

	if err := s.advanceCloudAgentTool(run, &state); err != nil {
		t.Fatal(err)
	}
	payload, detail := lastAgentToolEvent(t, s, run.ID)
	if payload["errorClass"] != cloudAgentToolErrorInvalidModelOutput {
		t.Fatalf("应归类为 invalid_model_output：%+v", payload)
	}
	if detail["admission"] != cloudAgentAdmissionInvalidOutput {
		t.Fatalf("回执应带上预检结论：%+v", detail)
	}
	if !strings.Contains(stringValue(payload["text"]), "不存在的工具") {
		t.Fatalf("回执应指向「工具不存在」：%+v", payload)
	}
}

// 只读工具不受"单步一个写"的限制：一批读调用全部放行。
func TestCloudAgentPreflightAllowsConcurrentReads(t *testing.T) {
	s, _, args := agentMediaFixture(t)
	run, state := agentMediaRun(t, s, args, "auto")

	run, state = writeBatch(t, s, run, &state, []cloudAgentCall{
		readOnlyCall("call-1", "canvas_get_state"),
		readOnlyCall("call-2", "model_list"),
	})
	for index, admission := range state.CallAdmissions {
		if !admission.Allowed {
			t.Fatalf("只读调用 %d 应被放行：%+v", index, admission)
		}
	}
	if err := s.advanceCloudAgentTool(run, &state); err != nil {
		t.Fatal(err)
	}
	payload, _ := lastAgentToolEvent(t, s, run.ID)
	if payload["errorClass"] == cloudAgentToolErrorCallSkipped {
		t.Fatalf("只读调用不应被跳过：%+v", payload)
	}
}

// 旧检查点没有预检结论时不能拦住执行（升级前创建的批次继续跑）。
func TestCloudAgentWithoutAdmissionsExecutesNormally(t *testing.T) {
	s, _, args := agentMediaFixture(t)
	run, state := agentMediaRun(t, s, args, "auto")

	state.Calls = []cloudAgentCall{readOnlyCall("call-1", "canvas_get_state")}
	run, state = saveBatchState(t, s, run, &state)
	if len(state.CallAdmissions) != 0 {
		t.Fatalf("前置条件：本用例不应有预检结论")
	}
	if err := s.advanceCloudAgentTool(run, &state); err != nil {
		t.Fatal(err)
	}
	_, runtimeState := reloadAgentRun(t, s, run.ID)
	if len(runtimeState.Events) == 0 || runtimeState.Events[len(runtimeState.Events)-1].Type != "tool_completed" {
		t.Fatalf("没有预检结论时应正常执行：%+v", runtimeState.Events)
	}
}

// 未知操作类型（schema 里没有的 op type）在预检阶段就被拒：不写画布、不申请审批，
// 并按 schema_error 交回模型自纠。这一条与
// TestCloudAgentCanvasApprovalAdmissionFailureTerminatesRun 成对：前者管"模型能自己改的"，
// 后者管"业务上真正不可准入的"（仍然终止整轮）。
func TestCloudAgentUnknownOperationTypeIsRepairable(t *testing.T) {
	s, _, args := agentMediaFixture(t)
	run, state := agentMediaRun(t, s, args, "request_approval")

	raw, err := json.Marshal(map[string]any{
		"snapshotHash": args.SnapshotHash,
		"ops":          []map[string]any{{"type": "unsupported_canvas_op", "id": "bad-op"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	call := cloudAgentCall{ID: "call-1"}
	call.Function.Name = "canvas_apply_ops"
	call.Function.Arguments = string(raw)
	run, state = writeBatch(t, s, run, &state, []cloudAgentCall{call})
	if state.CallAdmissions[0].Allowed || state.CallAdmissions[0].Field != "ops[0].type" {
		t.Fatalf("未知操作类型应在预检被拒且指向具体 op：%+v", state.CallAdmissions[0])
	}
	before := canvasPayloadJSON(t, s)
	if err := s.advanceCloudAgentTool(run, &state); err != nil {
		t.Fatal(err)
	}
	payload, detail := lastAgentToolEvent(t, s, run.ID)
	if payload["errorClass"] != cloudAgentToolErrorSchemaError || detail["reason"] != "invalid_tool_arguments" {
		t.Fatalf("应作为可自纠的 schema_error 交回模型：%+v / %+v", payload, detail)
	}
	if detail["parameters"] == nil {
		t.Fatalf("应把本轮 schema 回给模型：%+v", detail)
	}
	run, state = reloadAgentRun(t, s, run.ID)
	if run.Status == "failed" {
		t.Fatalf("可自纠的 schema 错误不该判死整轮：%s", run.FailureMessage)
	}
	if state.Approval != nil {
		t.Fatal("被预检拒绝的写入不能进入审批")
	}
	if after := canvasPayloadJSON(t, s); after != before {
		t.Fatalf("预检拒绝的写入不得改画布\nbefore=%s\nafter=%s", before, after)
	}
}
