package app

import (
	"strings"
	"testing"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// 分镜表工具开关：运行请求冻结 StoryboardDisabled，关闭时工具不披露、
// 提示缓存键区分、执行前强制拒绝；读取类工具不受影响。

func TestStoryboardDisabledRemovesToolFromCatalog(t *testing.T) {
	enabled := CloudAgentRequest{PermissionMode: "auto", ContextScope: []string{"canvas"}, SubagentEnabled: true, SubagentWriteEnabled: true}
	if !cloudAgentToolAllowed(enabled, "storyboard") {
		t.Fatal("storyboard tool missing for enabled run")
	}
	disabled := enabled
	disabled.StoryboardDisabled = true
	if cloudAgentToolAllowed(disabled, "storyboard") {
		t.Fatal("disabled run must not disclose storyboard")
	}
	for _, tool := range []string{"canvas_read_content", "canvas_read_batch_table", "canvas_get_state", "canvas_apply_ops"} {
		if !cloudAgentToolAllowed(disabled, tool) {
			t.Fatalf("disabled run must keep %s", tool)
		}
	}
}

func TestStoryboardDisabledHidesScriptNodeType(t *testing.T) {
	req := CloudAgentRequest{PermissionMode: "auto", ContextScope: []string{"canvas"}, SubagentEnabled: true, SubagentWriteEnabled: true}
	addNodeType := ""
	for _, tool := range cloudAgentToolsListOf(req) {
		nt := canvasApplyOpsNodeTypeEnum(tool)
		if nt == nil {
			continue
		}
		for _, v := range nt {
			if v == "script" {
				addNodeType = "found"
			}
		}
	}
	if addNodeType == "found" {
		t.Fatal("enabled run must keep script nodeType")
	}
	off := req
	off.StoryboardDisabled = true
	found := ""
	for _, tool := range cloudAgentToolsListOf(off) {
		nt := canvasApplyOpsNodeTypeEnum(tool)
		if nt == nil {
			continue
		}
		for _, v := range nt {
			if v == "script" {
				found = "script"
			}
		}
	}
	if found == "script" {
		t.Fatal("disabled run must hide script nodeType")
	}
}

// canvasApplyOpsNodeTypeEnum 取 canvas_apply_ops 的 nodeType 枚举；非该工具返回 nil。
func cloudAgentToolsListOf(req CloudAgentRequest) []map[string]any {
	return compileCloudAgentTools(req)
}

func canvasApplyOpsNodeTypeEnum(tool map[string]any) []any {
	function, ok := tool["function"].(map[string]any)
	if !ok || function["name"] != "canvas_apply_ops" {
		return nil
	}
	parameters, ok := function["parameters"].(map[string]any)
	if !ok {
		return nil
	}
	properties, ok := parameters["properties"].(map[string]any)
	if !ok {
		return nil
	}
	nodeType, ok := properties["nodeType"].(map[string]any)
	if !ok {
		return nil
	}
	enum, _ := nodeType["enum"].([]any)
	return enum
}

func docForGate(t *testing.T, s *Service, canvasID string) map[string]any {
	t.Helper()
	canvas, err := s.repo.CanvasProjectForUser("user", canvasID)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestStoryboardDisabledChangesPromptCacheKey(t *testing.T) {
	base := CloudAgentRequest{PermissionMode: "auto", ContextScope: []string{"canvas"}, CanvasID: "c"}
	off := base
	off.StoryboardDisabled = true
	if cloudAgentPromptCacheKey(base) == cloudAgentPromptCacheKey(off) {
		t.Fatal("storyboard toggle must split the prompt cache key")
	}
}

func TestStoryboardDisabledRejectsExecution(t *testing.T) {
	s, db := storyboardSplitTestService(t)
	req := agentTestRequest()
	req.CanvasID = "split-canvas"
	req.PermissionMode = "auto"
	req.StoryboardDisabled = true
	root, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	call := piAgentTestCall("board-call", "storyboard", `{"action":"read","nodeId":"board"}`)
	run, state := agentStagePiOutputForTest(t, s, db, root.ID, "", []cloudAgentCall{call})
	if err := agentPiExecuteCallForTest(t, s, run.ID, call.ID); err != nil {
		t.Fatal(err)
	}
	_, state = agentInterjectionState(t, s, run.ID)
	receipt := cloudAgentToolMessageForTest(t, state, call.ID)
	// 第一道闸是工具披露预检（permission_violation）。
	if !strings.Contains(receipt, "未获本轮权限或能力授权") {
		t.Fatalf("disabled storyboard disclosure gate missing: %s", receipt)
	}
	// 创建闸：闸禁止 add_node script / duplicate script；text/md 与已有 script 正文可写。
	gateOps := []agentCanvasOp{
		{Type: "add_node", ID: "st1", NodeType: "script"},
		{Type: "duplicate_node", ID: "dup1", SourceNodeID: "board"},
	}
	if err := validateCloudAgentStoryboardCreateGate(gateOps, true, docForGate(t, s, "split-canvas")); err == nil {
		t.Fatal("gate must reject script creation/duplication")
	}
	textOps := []agentCanvasOp{{Type: "add_node", ID: "note1", NodeType: "text"}, {Type: "add_node", ID: "md1", NodeType: "markdown"}}
	if err := validateCloudAgentStoryboardCreateGate(textOps, true, docForGate(t, s, "split-canvas")); err != nil {
		t.Fatalf("text/markdown creation must pass gate: %v", err)
	}
	if err := validateCloudAgentStoryboardCreateGate(gateOps, false, docForGate(t, s, "split-canvas")); err != nil {
		t.Fatalf("enabled run must allow script creation: %v", err)
	}

	// 第二道闸：直接调用执行入口（绕过披露预检的内部路径）必须被强制闸拒绝。
	// 先刷新 run/state——第一道闸的拒绝回执已推进 revision；把调用游标归零，
	// 让强制闸像"本批仍待执行"一样走完整记录路径。
	run, state = agentInterjectionState(t, s, run.ID)
	state.CallIndex = 0
	if err := s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	run, state = agentInterjectionState(t, s, run.ID)
	if err := s.executeCloudAgentStoryboard(run, &state, call); err != nil {
		t.Fatal(err)
	}
	_, state = agentInterjectionState(t, s, run.ID)
	// 同 callId 有两条工具消息（预检拒绝 + 强制闸拒绝）；取最后一条核对强制闸。
	var forced string
	for _, message := range state.Canonical.Messages {
		if stringField(message, "role") == "tool" && stringField(message, "tool_call_id") == call.ID {
			forced = stringField(message, "content")
		}
	}
	if !strings.Contains(forced, "分镜表工具已在 Agent 设置中关闭") {
		t.Fatalf("forced gate missing: %s", forced)
	}
	if strings.Contains(forced, `"status":"submitted"`) || strings.Contains(forced, "markdown") {
		t.Fatalf("disabled storyboard must not execute: %s", forced)
	}
}
