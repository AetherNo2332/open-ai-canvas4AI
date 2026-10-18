package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

// Task 8：模型可见合同不再包含画布连线操作。连线改由服务端按引用字段派生，
// Agent 只能写 generationSpec.referenceBindings、metadata.inputBindings、
// 行 assetBindings 或 canvas_edit_storyboard 的语义动作。
var agentReferenceContractLegacyOps = []string{"connect_nodes", "update_connection", "delete_connection"}

func agentCanvasWriteRequest() CloudAgentRequest {
	req := agentTestRequest()
	req.PermissionMode = "auto"
	req.ContextScope = []string{"canvas"}
	req.Budget.MaxGenerationTasks = 1
	return req
}

func agentToolParameters(t *testing.T, req CloudAgentRequest) map[string]map[string]any {
	t.Helper()
	result := map[string]map[string]any{}
	for _, tool := range cloudAgentTools(req) {
		function, ok := tool["function"].(map[string]any)
		if !ok {
			continue
		}
		name := stringField(function, "name")
		parameters, _ := function["parameters"].(map[string]any)
		if name != "" && parameters != nil {
			result[name] = parameters
		}
	}
	return result
}

func agentCanvasApplyOpsItem(t *testing.T, parameters map[string]any) map[string]any {
	t.Helper()
	properties, ok := parameters["properties"].(map[string]any)
	if !ok {
		t.Fatal("canvas_apply_ops 缺少 properties")
	}
	ops, ok := properties["ops"].(map[string]any)
	if !ok {
		t.Fatal("canvas_apply_ops 缺少 ops")
	}
	item, ok := ops["items"].(map[string]any)
	if !ok {
		t.Fatal("canvas_apply_ops.ops 缺少 items")
	}
	return item
}

func agentAssertNoConnectionContract(t *testing.T, label string, item map[string]any) {
	t.Helper()
	properties, _ := item["properties"].(map[string]any)
	if properties == nil {
		t.Fatalf("%s 缺少 ops.items.properties", label)
	}
	for _, field := range []string{"fromNodeId", "toNodeId", "fromHandleId", "toHandleId"} {
		if _, exists := properties[field]; exists {
			t.Fatalf("%s 仍向模型暴露只服务连线的字段 %s", label, field)
		}
	}
	opTypes, _ := properties["type"].(map[string]any)
	enum, _ := opTypes["enum"].([]string)
	if len(enum) == 0 {
		if raw, ok := opTypes["enum"].([]any); ok {
			for _, value := range raw {
				enum = append(enum, stringValue(value))
			}
		}
	}
	for _, op := range agentReferenceContractLegacyOps {
		for _, value := range enum {
			if value == op {
				t.Fatalf("%s 仍向模型暴露连线操作 %s", label, op)
			}
		}
		if strings.Contains(stringValue(opTypes["description"]), op) {
			t.Fatalf("%s 的操作说明仍提示 %s", label, op)
		}
	}
	for _, raw := range agentOneOfBranches(t, label, item) {
		branchProperties, _ := raw["properties"].(map[string]any)
		constType, _ := branchProperties["type"].(map[string]any)
		for _, op := range agentReferenceContractLegacyOps {
			if stringValue(constType["const"]) == op {
				t.Fatalf("%s 的条件必填分支仍保留 %s", label, op)
			}
		}
	}
}

func agentOneOfBranches(t *testing.T, label string, item map[string]any) []map[string]any {
	t.Helper()
	switch branches := item["oneOf"].(type) {
	case []map[string]any:
		return branches
	case []any:
		result := make([]map[string]any, 0, len(branches))
		for _, branch := range branches {
			typed, ok := branch.(map[string]any)
			if !ok {
				t.Fatalf("%s 的 oneOf 分支结构无效", label)
			}
			result = append(result, typed)
		}
		return result
	default:
		t.Fatalf("%s 缺少 oneOf 条件必填", label)
		return nil
	}
}

func agentActionEnum(t *testing.T, parameters map[string]any) []string {
	t.Helper()
	properties, _ := parameters["properties"].(map[string]any)
	action, _ := properties["action"].(map[string]any)
	values := []string{}
	switch enum := action["enum"].(type) {
	case []string:
		values = append(values, enum...)
	case []any:
		for _, value := range enum {
			values = append(values, stringValue(value))
		}
	}
	if len(values) == 0 {
		t.Fatal("canvas_edit_storyboard 缺少 action 枚举")
	}
	return values
}

// TestAgentReferenceOnlyContract：模型可见合同里没有连线操作与只服务连线的字段，
// 同时必须发布替代它们的引用字段与语义动作。
func TestAgentReferenceOnlyContract(t *testing.T) {
	parameters := agentToolParameters(t, agentCanvasWriteRequest())
	apply, ok := parameters["canvas_apply_ops"]
	if !ok {
		t.Fatal("canvas_apply_ops 未暴露")
	}
	agentAssertNoConnectionContract(t, "canvas_apply_ops", agentCanvasApplyOpsItem(t, apply))

	text := cloudAgentToolText("canvas_apply_ops")
	for _, op := range agentReferenceContractLegacyOps {
		if strings.Contains(text, op) {
			t.Fatalf("canvas_apply_ops 说明仍指导模型调用 %s", op)
		}
	}
	if !strings.Contains(text, "引用") {
		t.Fatalf("canvas_apply_ops 说明没有说明引用字段替代：%s", text)
	}

	// 语义替代动作必须在模型可见合同里，否则关闭连线等于把分镜输出归属砍掉。
	edit, ok := parameters["canvas_edit_storyboard"]
	if !ok {
		t.Fatal("canvas_edit_storyboard 未暴露")
	}
	actions := map[string]bool{}
	for _, action := range agentActionEnum(t, edit) {
		actions[action] = true
	}
	for _, action := range []string{"set_references", "bind_output", "unbind_output"} {
		if !actions[action] {
			t.Fatalf("canvas_edit_storyboard 未发布语义动作 %s：%v", action, actions)
		}
	}
	editProperties, _ := edit["properties"].(map[string]any)
	for _, field := range []string{"referenceNodeIds", "outputMode", "outputNodeId"} {
		if _, exists := editProperties[field]; !exists {
			t.Fatalf("canvas_edit_storyboard 未发布 %s", field)
		}
	}
	create, ok := parameters["canvas_create_storyboard"]
	if !ok {
		t.Fatal("canvas_create_storyboard 未暴露")
	}
	createProperties, _ := create["properties"].(map[string]any)
	if _, exists := createProperties["referenceNodeIds"]; !exists {
		t.Fatal("canvas_create_storyboard 未发布共享引用字段 referenceNodeIds")
	}

	// 共用 schema 制品与工具说明是同一次发布的镜像：漏同步时这里必须失败。
	// 制品在仓库根，不在 backend 挂载内（只读后端的容器里不可见），此时跳过发布检查。
	artifact, err := os.ReadFile(agentToolSchemaArtifactPath(t))
	if err != nil {
		t.Skipf("共用工具制品不在当前挂载内，跳过发布快照检查：%v", err)
	}
	var published agentToolSchemaArtifact
	if err = json.Unmarshal(artifact, &published); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tool := range published.Tools {
		function, _ := tool["function"].(map[string]any)
		if stringField(function, "name") != "canvas_apply_ops" {
			continue
		}
		found = true
		publishedParameters, _ := function["parameters"].(map[string]any)
		agentAssertNoConnectionContract(t, "agent/harness/TOOL_SCHEMA.json", agentCanvasApplyOpsItem(t, publishedParameters))
	}
	if !found {
		t.Fatal("共用 schema 制品缺少 canvas_apply_ops")
	}
	descriptions, err := os.ReadFile(agentToolDescriptionsArtifactPath(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range agentReferenceContractLegacyOps {
		if strings.Contains(string(descriptions), op) {
			t.Fatalf("agent/harness/TOOL_DESCRIPTIONS.md 仍指导模型调用 %s，请重新同步 harness", op)
		}
	}
}

func agentToolDescriptionsArtifactPath(t *testing.T) string {
	t.Helper()
	return strings.TrimSuffix(agentToolSchemaArtifactPath(t), "TOOL_SCHEMA.json") + "TOOL_DESCRIPTIONS.md"
}

func agentAssertConnectionOpsDisabled(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("旧连线操作未被拒绝")
	}
	var fieldErr *cloudAgentFieldArgumentError
	if !errors.As(err, &fieldErr) || fieldErr.Issue != "connection_ops_disabled" {
		t.Fatalf("旧连线操作必须统一返回 connection_ops_disabled：%v", err)
	}
	// 可自纠的参数错误，不是终止整轮的准入失败：模型应当按新 schema 改用引用字段。
	var argumentErr *cloudAgentArgumentError
	if !errors.As(err, &argumentErr) {
		t.Fatalf("旧连线操作应按可修复参数错误返回：%v", err)
	}
}

// TestAgentRejectsLegacyConnectionOperations：绕过 schema 直接构造旧请求也必须失败，
// 预览与真实执行两个入口都要拒绝，画布不能发生任何写入；通用 patch 不能写
// connections 或托管归属标记。
func TestAgentRejectsLegacyConnectionOperations(t *testing.T) {
	for _, legacy := range agentReferenceContractLegacyOps {
		t.Run(legacy, func(t *testing.T) {
			arguments := fmt.Sprintf(`{"snapshotHash":"hash","ops":[{"type":%q,"id":"edge","fromNodeId":"a","toNodeId":"b","fromHandleId":"row:r1"}]}`, legacy)
			call := cloudAgentCall{ID: "legacy-call"}
			call.Function.Name, call.Function.Arguments = "canvas_apply_ops", arguments
			// 审批预览入口：拒绝发生在读取画布之前（repo 为 nil 也能得到同一结论）。
			if _, err := prepareCloudAgentCanvasMutation(nil, "user", "canvas", call); err != nil {
				agentAssertConnectionOpsDisabled(t, err)
			} else {
				t.Fatal("预览入口接受了旧连线操作")
			}
		})
	}

	s, _, args := agentMediaFixture(t)
	policy, err := s.RuntimePolicy()
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.repo.CanvasProjectForUser("user", "agent-canvas")
	if err != nil {
		t.Fatal(err)
	}
	for _, legacy := range agentReferenceContractLegacyOps {
		call := cloudAgentCall{ID: "legacy-call"}
		call.Function.Name = "canvas_apply_ops"
		call.Function.Arguments = fmt.Sprintf(`{"snapshotHash":%q,"ops":[{"type":%q,"id":"edge","fromNodeId":"cat","toNodeId":"blank-video"}]}`, args.SnapshotHash, legacy)
		// 真实执行入口。
		if _, err := applyCloudAgentCanvas(s.repo, "user", "agent-canvas", call, policy); err != nil {
			agentAssertConnectionOpsDisabled(t, err)
		} else {
			t.Fatalf("执行入口接受了旧连线操作 %s", legacy)
		}
	}
	after, err := s.repo.CanvasProjectForUser("user", "agent-canvas")
	if err != nil {
		t.Fatal(err)
	}
	if after.PayloadJSON != before.PayloadJSON {
		t.Fatal("被拒绝的旧连线操作改写了画布")
	}

	for _, patch := range []map[string]any{
		{"connections": []any{}},
		{"metadata": map[string]any{"managedBy": "reference-fields-v1"}},
		{"managedBy": "reference-fields-v1"},
		{"referenceOwnerNodeId": "cat"},
		{"referenceBindingId": "binding-1"},
	} {
		doc, err := creationDocument(before.PayloadJSON)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(map[string]any{"snapshotHash": cloudAgentCanvasHash(doc), "ops": []map[string]any{{"type": "update_node", "id": "cat", "patch": patch}}})
		call := cloudAgentCall{ID: "bypass-call"}
		call.Function.Name, call.Function.Arguments = "canvas_apply_ops", string(raw)
		if _, err := applyCloudAgentCanvas(s.repo, "user", "agent-canvas", call, policy); err == nil {
			t.Fatalf("通用 patch 绕过字段未被拒绝：%v", patch)
		}
	}
	stored, err := s.repo.CanvasProjectForUser("user", "agent-canvas")
	if err != nil {
		t.Fatal(err)
	}
	if stored.PayloadJSON != before.PayloadJSON {
		t.Fatal("被拒绝的绕过 patch 改写了画布")
	}
}

// TestAgentReferenceContractSnapshotDrift：旧检查点携带的旧工具快照必须触发既有漂移
// 检查（停止或重建上下文），不能在新 schema 下暗中执行旧连线请求；同一批里合法改名
// 加非法连线时整批回滚。
func TestAgentReferenceContractSnapshotDrift(t *testing.T) {
	legacy := agentLegacyCanvasApplyOpsSnapshot(t, cloudAgentTools(agentCanvasWriteRequest()))
	if err := cloudAgentToolSchemaDrift(cloudAgentTools(agentCanvasWriteRequest()), legacy); err == nil {
		t.Fatal("旧工具快照未触发漂移检测，旧检查点可能继续执行连线请求")
	}

	// 纯规划：合法改名与非法连线同批时整个批次不落地。
	nodes := []map[string]any{
		{"id": "image-1", "type": "image", "title": "参考图"},
		{"id": "video-1", "type": "video", "title": "视频镜头", "metadata": map[string]any{"generationSpec": map[string]any{"mode": "video"}}},
	}
	doc, err := creationDocument(`{"nodes":[{"id":"image-1","type":"image","title":"参考图"},{"id":"video-1","type":"video","title":"视频镜头","metadata":{"generationSpec":{"mode":"video"}}}],"connections":[]}`)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(doc)
	_, err = applyCloudAgentCanvasPlan(doc, []agentCanvasOp{
		{Type: "update_node", ID: "video-1", Patch: map[string]any{"title": "视频镜头新版"}},
		{Type: "connect_nodes", ID: "edge-1", FromNodeID: "image-1", ToNodeID: "video-1"},
	})
	agentAssertConnectionOpsDisabled(t, err)
	after, _ := json.Marshal(doc)
	if string(before) != string(after) {
		t.Fatal("同批合法改名被非法连线带进了画布")
	}
	_ = nodes

	// 真实执行入口：同一批不得写入任何字段。
	s, _, args := agentMediaFixture(t)
	policy, err := s.RuntimePolicy()
	if err != nil {
		t.Fatal(err)
	}
	stored, err := s.repo.CanvasProjectForUser("user", "agent-canvas")
	if err != nil {
		t.Fatal(err)
	}
	call := cloudAgentCall{ID: "mixed-call"}
	call.Function.Name = "canvas_apply_ops"
	call.Function.Arguments = fmt.Sprintf(`{"snapshotHash":%q,"ops":[{"type":"update_node","id":"cat","patch":{"title":"改名未生效"}},{"type":"connect_nodes","id":"edge","fromNodeId":"cat","toNodeId":"hero"}]}`, args.SnapshotHash)
	if _, err := applyCloudAgentCanvas(s.repo, "user", "agent-canvas", call, policy); err != nil {
		agentAssertConnectionOpsDisabled(t, err)
	} else {
		t.Fatal("执行入口接受了混有旧连线操作的批次")
	}
	persisted, err := s.repo.CanvasProjectForUser("user", "agent-canvas")
	if err != nil {
		t.Fatal(err)
	}
	if persisted.PayloadJSON != stored.PayloadJSON {
		t.Fatal("同批合法改名被非法连线带进了已保存画布")
	}
}

// agentLegacyCanvasApplyOpsSnapshot 复原改造前的 canvas_apply_ops 结构，用来模拟
// 关闭连线前生成的检查点/worker 快照。
func agentLegacyCanvasApplyOpsSnapshot(t *testing.T, tools []map[string]any) []map[string]any {
	t.Helper()
	encoded, err := json.Marshal(tools)
	if err != nil {
		t.Fatal(err)
	}
	var cloned []map[string]any
	if err = json.Unmarshal(encoded, &cloned); err != nil {
		t.Fatal(err)
	}
	for _, tool := range cloned {
		function, _ := tool["function"].(map[string]any)
		if stringField(function, "name") != "canvas_apply_ops" {
			continue
		}
		parameters, _ := function["parameters"].(map[string]any)
		properties, _ := parameters["properties"].(map[string]any)
		ops, _ := properties["ops"].(map[string]any)
		item, _ := ops["items"].(map[string]any)
		fields, _ := item["properties"].(map[string]any)
		for _, field := range []string{"fromNodeId", "toNodeId", "fromHandleId", "toHandleId"} {
			fields[field] = map[string]any{"type": "string"}
		}
		opType, _ := fields["type"].(map[string]any)
		enum, _ := opType["enum"].([]any)
		opType["enum"] = append(enum, "connect_nodes", "delete_connection", "update_connection")
		branches, _ := item["oneOf"].([]any)
		var connectionBranches []any
		for _, branch := range branches {
			typed, _ := branch.(map[string]any)
			if typed == nil {
				continue
			}
			branchProperties, _ := typed["properties"].(map[string]any)
			constType, _ := branchProperties["type"].(map[string]any)
			if stringValue(constType["const"]) != "add_node" {
				continue
			}
			connectionBranches = append(connectionBranches, map[string]any{"properties": map[string]any{"type": map[string]any{"const": "connect_nodes"}}, "required": []any{"fromNodeId", "toNodeId"}})
		}
		item["oneOf"] = append(branches, connectionBranches...)
		return cloned
	}
	t.Fatal("未找到 canvas_apply_ops")
	return nil
}
