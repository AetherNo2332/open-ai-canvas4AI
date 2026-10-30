package app

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"yingce/backend/internal/model"
)

// 结构化纠错与熔断（handoff 工作项 B 第 3 步）：
// 错误指纹 → 第二次同类错误收紧工具集 → 第三次停止自动重试。

func repairCall(id, raw string) cloudAgentCall {
	call := cloudAgentCall{ID: id}
	call.Function.Name = "canvas_apply_ops"
	call.Function.Arguments = raw
	return call
}

// 换了错法不算同一个错误：新字段错误从第一次重新给机会，而不是继承上一次的额度。
func TestCloudAgentRepairFingerprintSeparatesDistinctErrors(t *testing.T) {
	state := &cloudAgentRuntime{}
	first := cloudAgentFieldError("ops[0].type", "required", "缺少操作类型")
	second := cloudAgentFieldError("snapshotHash", "required", "缺少快照")

	call := repairCall("call-1", `{"ops":[{"type":"add_node"}]}`)
	cloudAgentToolResult("run", state, call, nil, first)
	if repair := state.ToolRepairs["canvas_apply_ops"]; repair.Attempt != 1 {
		t.Fatalf("第一次错误应记 1 次：%+v", repair)
	}
	firstFingerprint := state.ToolRepairs["canvas_apply_ops"].Fingerprint

	// 同一工具、同一类问题（字段与 issue 都相同）→ 继续累加
	cloudAgentToolResult("run", state, call, nil, first)
	if repair := state.ToolRepairs["canvas_apply_ops"]; repair.Attempt != 2 || repair.Fingerprint != firstFingerprint {
		t.Fatalf("同类错误应累加：%+v", repair)
	}

	// 换成另一种错误 → 指纹变了，额度重新开始
	other := repairCall("call-2", `{"snapshotHash":"h"}`)
	cloudAgentToolResult("run", state, other, nil, second)
	repair := state.ToolRepairs["canvas_apply_ops"]
	if repair.Attempt != 1 || repair.Fingerprint == firstFingerprint {
		t.Fatalf("换了错法应从第一次重新计数：%+v", repair)
	}
}

// 同一个错误第二次出现：下一步只开放修复所需的工具。
func TestCloudAgentRepairScopesToolsOnSecondFailure(t *testing.T) {
	state := &cloudAgentRuntime{}
	call := repairCall("call-1", `{"ops":[{"type":"add_node"}]}`)
	err := cloudAgentFieldError("ops[0].type", "required", "缺少操作类型")
	cloudAgentToolResult("run", state, call, nil, err)
	if len(state.ToolScope) != 0 {
		t.Fatalf("第一次错误不该收紧工具集：%+v", state.ToolScope)
	}
	cloudAgentToolResult("run", state, call, nil, err)
	retry, _ := state.Events[len(state.Events)-1].Payload["retry"].(map[string]any)
	if retry["status"] != "retrying" || retry["ladder"] != "scoped" {
		t.Fatalf("第二次同类错误应进入收紧档且仍是 retrying：%+v", retry)
	}
	if len(state.ToolScope) == 0 {
		t.Fatal("第二次同类错误应收紧工具集")
	}

	// 收紧后的工具表只留修复所需的工具，并且预检同样按收紧后的集合判定。
	req := agentTestRequest()
	req.PermissionMode = "auto"
	tools := cloudAgentScopedTools(cloudAgentTools(req), state.ToolScope)
	if len(tools) == 0 || len(tools) >= len(cloudAgentTools(req)) {
		t.Fatalf("收紧后的工具表应是非空真子集：%d", len(tools))
	}
	scoped := &cloudAgentRuntime{Request: req, Canonical: canonicalAgentRequest{Tools: tools}}
	for _, tool := range tools {
		function, _ := tool["function"].(map[string]any)
		if !cloudAgentToolInScope(state, stringField(function, "name")) {
			t.Fatalf("收紧档里的工具应被判为在范围内：%v", function["name"])
		}
	}
	blocked := repairCall("call-2", `{"mode":"video","prompt":"x","nodeId":"n1","title":"t","referenceNodeIds":[]}`)
	blocked.Function.Name = "generate_media"
	admissions := cloudAgentPreflightBatch(scoped, []cloudAgentCall{blocked})
	if admissions[0].Allowed || admissions[0].Issue != cloudAgentAdmissionInvalidOutput {
		t.Fatalf("收紧档期间不在范围内的工具应被拒：%+v", admissions[0])
	}
	allowed := cloudAgentPreflightBatch(scoped, []cloudAgentCall{repairCall("call-3", `{"snapshotHash":"h","ops":[{"type":"add_node","id":"n1","nodeType":"text"}]}`)})
	if !allowed[0].Allowed {
		t.Fatalf("修复所需的工具应被放行：%+v", allowed[0])
	}
}

// 修好之后工具集恢复原状（收紧只服务于正在修的那一步）。
func TestCloudAgentRepairScopeClearsAfterSuccess(t *testing.T) {
	state := &cloudAgentRuntime{}
	call := repairCall("call-1", `{"ops":[{"type":"add_node"}]}`)
	err := cloudAgentFieldError("ops[0].type", "required", "缺少操作类型")
	cloudAgentToolResult("run", state, call, nil, err)
	cloudAgentToolResult("run", state, call, nil, err)
	if len(state.ToolScope) == 0 {
		t.Fatal("前置条件：应已进入收紧档")
	}
	cloudAgentToolResult("run", state, call, map[string]any{}, nil)
	if len(state.ToolScope) != 0 {
		t.Fatalf("修好后应清空收紧档：%+v", state.ToolScope)
	}
	if len(state.ToolRepairs) != 0 {
		t.Fatalf("修好后应清空纠错记录：%+v", state.ToolRepairs)
	}
}

// 第三次同类错误：停止自动重试，失败事件带上错误指纹。
func TestCloudAgentRepairExhaustsWithFingerprint(t *testing.T) {
	state := &cloudAgentRuntime{}
	run := &model.CloudAgentExecution{ID: "run", Status: "running"}
	call := repairCall("call-1", `{"ops":[{"type":"add_node"}]}`)
	err := cloudAgentFieldError("ops[0].type", "required", "缺少操作类型")
	for attempt := 1; attempt <= cloudAgentToolAttemptLimit; attempt++ {
		cloudAgentRecordToolResult(run, state, call, nil, err)
	}
	last := state.Events[len(state.Events)-1]
	if run.Status != "failed" || last.Type != "run_failed" || last.Payload["reason"] != "tool_retry_exhausted" {
		t.Fatalf("第三次应停止自动重试：%+v", last)
	}
	if stringValue(last.Payload["fingerprint"]) == "" {
		t.Fatalf("失败事件应带上错误指纹：%+v", last.Payload)
	}
	if runsJSON, err := json.Marshal(state.ToolRepairs); err != nil || string(runsJSON) == "" {
		t.Fatalf("纠错记录应可序列化：%v", err)
	}
}

// 现场 run ag9e85648：同一批两次看图都把 summary 写成了坏掉的 JSON 字符串，
// 下一步修好第一张图、第二张又错一次，就以"已尝试 3 次"熔断了。
// 同一批里重复同一个错误只算一次；看图成功也要结束这个工具的纠错组。
func TestCloudAgentRepairCountsOneBatchOnceAndInspectionSuccessCloses(t *testing.T) {
	state := &cloudAgentRuntime{PiToolBatchTaskID: "step-1"}
	run := &model.CloudAgentExecution{ID: "run", Status: "running"}
	inspect := func(id, summary string) cloudAgentCall {
		call := cloudAgentCall{ID: id}
		call.Function.Name = "canvas_inspect_image"
		call.Function.Arguments = `{"nodeId":"n1","sha256":"` + strings.Repeat("a", 64) + `","summary":` + summary + `}`
		return call
	}
	broken := strconv.Quote(`{"short":"海报","detailed":{"subjects":["僧人"]，"composition":"竖幅"}}`)
	schema := map[string]any{"type": "object", "properties": map[string]any{"nodeId": map[string]any{"type": "string"}}, "required": []string{"nodeId"}}
	cloudAgentToolParameterContracts("canvas_inspect_image", schema)
	err := validateCloudAgentToolArguments(schema, inspect("x", broken).Function.Arguments)
	if err == nil || !strings.Contains(err.Error(), "传成了字符串") || !strings.Contains(err.Error(), "不是合法 JSON") {
		t.Fatalf("summary 传成坏 JSON 字符串时应点明原因：%v", err)
	}
	example := cloudAgentArgumentExample(inspect("x", broken), "summary")
	body, _ := json.Marshal(example)
	if example == nil || validateCloudAgentToolArguments(schema, string(body)) != nil {
		t.Fatalf("summary 出错时应给出可直接照抄结构的合法示例：%s", body)
	}
	for _, id := range []string{"first", "second"} {
		cloudAgentRecordToolResult(run, state, inspect(id, broken), nil, err)
	}
	if repair := state.ToolRepairs["canvas_inspect_image"]; repair.Attempt != 1 || run.Status != "running" {
		t.Fatalf("同一批的同一个错误只能算一次：%+v status=%s", repair, run.Status)
	}
	state.PiToolBatchTaskID = "step-2"
	ok := inspect("fixed", `{"short":"海报","detailed":{}}`)
	cloudAgentRecordToolResult(run, state, ok, cloudAgentImageInspection{Receipt: map[string]any{"nodeId": "n1", "summarySaved": true}}, nil)
	if len(state.ToolRepairs) != 0 || len(state.ToolScope) != 0 {
		t.Fatalf("看图成功应结束纠错组并恢复工具集：%+v %+v", state.ToolRepairs, state.ToolScope)
	}
	cloudAgentRecordToolResult(run, state, inspect("other", broken), nil, err)
	if repair := state.ToolRepairs["canvas_inspect_image"]; repair.Attempt != 1 || run.Status != "running" {
		t.Fatalf("修好之后的新错误应从第一次开始：%+v status=%s", repair, run.Status)
	}
	for step := 3; step <= 4; step++ {
		state.PiToolBatchTaskID = "step-" + strconv.Itoa(step)
		cloudAgentRecordToolResult(run, state, inspect("again-"+strconv.Itoa(step), broken), nil, err)
	}
	if run.Status != "failed" {
		t.Fatal("跨三步重复同一个错误仍应熔断")
	}
}
