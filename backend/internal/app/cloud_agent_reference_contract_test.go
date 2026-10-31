package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"yingce/backend/internal/canvas/commands"
	"yingce/backend/internal/canvas/references"
	"yingce/backend/internal/database"
	"yingce/backend/internal/identity"
	"yingce/backend/internal/model"
)

// Task 8：模型可见合同不再包含画布连线操作。连线改由服务端按引用字段派生，
// Agent 只能写 generationSpec.referenceBindings、metadata.inputBindings、
// 行 assetBindings 或 storyboard 的语义动作。
var agentReferenceContractLegacyOps = []string{"connect_nodes", "update_connection", "delete_connection"}

func TestAgentStoryboardCharacterReferencesRequireMigration(t *testing.T) {
	for _, field := range []string{"characterImageNodeId", "characterAssetId"} {
		t.Run(field, func(t *testing.T) {
			patch := map[string]any{"characters": []any{map[string]any{"characterName": "主角", field: "asset"}}}
			if !cloudAgentStoryboardEditWritesReferences(cloudAgentStoryboardEditArgs{Action: "update", Patch: patch}) {
				t.Fatal("角色资产 ID 绕过迁移门禁，无法派生引用连线")
			}
			if !cloudAgentStoryboardRowsWriteReferences([]map[string]any{patch}) {
				t.Fatal("创建分镜时的角色资产 ID 绕过迁移门禁")
			}
		})
	}
	if cloudAgentStoryboardRowsWriteReferences([]map[string]any{{"characters": []any{map[string]any{"characterName": "主角"}}}}) {
		t.Fatal("纯角色文字不应被当作资产引用")
	}
}

func TestAgentCanvasStateReportsReferenceMigration(t *testing.T) {
	doc := map[string]any{"nodes": []any{}, "connections": []any{}}
	view, err := cloudAgentCanvasState(nil, "user", "canvas", doc, 0, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	state := view.(map[string]any)
	migration, ok := state["referenceMigration"].(map[string]any)
	if !ok || migration["required"] != true || migration["reason"] != "reference_migration_required" {
		t.Fatalf("Agent 读取画布前不知道需要迁移：%#v", state["referenceMigration"])
	}
	doc["referenceFieldsVersion"] = 1
	view, err = cloudAgentCanvasState(nil, "user", "canvas", doc, 0, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if view.(map[string]any)["referenceMigration"].(map[string]any)["required"] != false {
		t.Fatal("已规范化画布仍被报告为需要迁移")
	}
}

func TestAgentReferencesApplyAfterWebMigration(t *testing.T) {
	s, db, canvas := cloudAgentStoryboardFixture(t)
	rows := createCloudAgentStoryboardForTest(t, s, canvas)
	if err := database.MigrateCanvasCommands(db); err != nil {
		t.Fatal(err)
	}
	if err := db.FirstOrCreate(&model.User{}, model.User{ID: "user", Status: model.UserStatusActive}).Error; err != nil {
		t.Fatal(err)
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		t.Fatal(err)
	}
	doc["id"] = canvas.ID
	doc["nodes"] = append(creationMaps(doc["nodes"]), map[string]any{"id": "hero", "type": "image", "metadata": map[string]any{}})
	raw, _ := json.Marshal(doc)
	if err := db.Model(canvas).Update("payload_json", string(raw)).Error; err != nil {
		t.Fatal(err)
	}
	principal := identity.Principal{ID: "user", OwnerID: "user", Role: "owner", Scopes: identity.OwnerScopes()}
	host := commands.NewService(s.repo, commands.ServiceOptions{})
	snapshot, err := host.ReadSnapshot(context.Background(), principal, canvas.ID)
	if err != nil || snapshot.ReferenceMigration == nil || !snapshot.ReferenceMigration.Required {
		t.Fatalf("网页未读到旧画布迁移预览：%+v %v", snapshot, err)
	}
	if _, err := host.Execute(context.Background(), principal, commands.Command{
		Protocol: 1, CanvasID: canvas.ID, DocumentEpoch: snapshot.DocumentEpoch, BaseRevision: snapshot.Revision,
		ClientID: "web-reference-migration", OperationID: "confirm-migration",
		Operations: []commands.Operation{{Type: commands.OperationMigrateReferences, ID: canvas.ID}},
	}); err != nil {
		t.Fatal(err)
	}
	doc, err = creationDocument(agentCanvasPayload(t, s, canvas.ID))
	if err != nil {
		t.Fatal(err)
	}
	policy, err := s.RuntimePolicy()
	if err != nil {
		t.Fatal(err)
	}
	call := cloudAgentStoryboardCall(t, "storyboard", "associate-hero", map[string]any{
		"snapshotHash": cloudAgentCanvasHash(doc), "nodeId": "storyboard-1", "action": "update", "rowId": rows[0]["id"],
		"patch": map[string]any{
			"assetBindings": []any{map[string]any{"nodeId": "hero", "role": "character", "priority": 100}},
			"characters":    []any{map[string]any{"characterName": "主角", "characterImageNodeId": "hero"}},
		},
	})
	if _, err := applyCloudAgentStoryboardMutation(s.repo, "user", canvas.ID, call, policy); err != nil {
		t.Fatal(err)
	}
	saved, err := creationDocument(agentCanvasPayload(t, s, canvas.ID))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := references.Resolve(saved, references.Scope{Kind: "storyboard-row", NodeID: "storyboard-1", RowID: stringValue(rows[0]["id"])}, nil)
	if err != nil || len(resolved.Bindings) != 1 || resolved.Bindings[0].NodeID != "hero" {
		t.Fatalf("成功回执没有对应的真实资产引用：%+v %v", resolved, err)
	}
	for _, edge := range creationMaps(saved["connections"]) {
		if edge["fromNodeId"] == "hero" && edge["toNodeId"] == "storyboard-1" && edge["managedBy"] == references.ManagedBy {
			return
		}
	}
	t.Fatal("Agent 写入字段后，画布没有派生资产到分镜表的托管连接")
}

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
		t.Fatal("storyboard 缺少 action 枚举")
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
	edit, ok := parameters["storyboard"]
	if !ok {
		t.Fatal("storyboard 未暴露")
	}
	actions := map[string]bool{}
	for _, action := range agentActionEnum(t, edit) {
		actions[action] = true
	}
	for _, action := range []string{"set_references", "bind_output", "unbind_output"} {
		if !actions[action] {
			t.Fatalf("storyboard 未发布语义动作 %s：%v", action, actions)
		}
	}
	editProperties, _ := edit["properties"].(map[string]any)
	for _, field := range []string{"referenceNodeIds", "outputMode", "outputNodeId"} {
		if _, exists := editProperties[field]; !exists {
			t.Fatalf("storyboard 未发布 %s", field)
		}
	}
	for _, name := range []string{"canvas_create_storyboard", "canvas_read_storyboard", "canvas_edit_storyboard"} {
		if _, exists := parameters[name]; exists {
			t.Fatalf("retired tool %s is still exposed", name)
		}
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

// ---- Task 11：历史画布（无 referenceFieldsVersion）必须先完成规范化，字段编辑才生效 ----

// agentReferenceFieldOps 是一批"声明引用字段"的 canvas_apply_ops：新增一个输入节点并在
// 同一批里用 inputBindings 声明对既有图片节点的引用。legacy 画布整批拒绝，v1 画布正常派生。
func agentReferenceFieldOps() []map[string]any {
	return []map[string]any{
		{"type": "add_node", "id": "ref-target", "nodeType": "compare", "title": "对比"},
		{"type": "update_node", "id": "ref-target", "patch": map[string]any{"inputBindings": []map[string]any{
			{"id": "node:cat", "nodeId": "cat", "mediaType": "image", "role": "reference", "order": float64(0), "resolution": "latest"},
		}}},
	}
}

func agentCanvasOpsCall(t *testing.T, callID, snapshotHash string, ops []map[string]any) cloudAgentCall {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"snapshotHash": snapshotHash, "ops": ops})
	if err != nil {
		t.Fatal(err)
	}
	call := cloudAgentCall{ID: callID}
	call.Function.Name, call.Function.Arguments = "canvas_apply_ops", string(raw)
	return call
}

func agentAssertMigrationRequired(t *testing.T, err error) *cloudAgentFieldArgumentError {
	t.Helper()
	if err == nil {
		t.Fatal("历史画布上的引用字段编辑未被拒绝")
	}
	var fieldErr *cloudAgentFieldArgumentError
	if !errors.As(err, &fieldErr) || fieldErr.Issue != "reference_migration_required" {
		t.Fatalf("历史画布必须先规范化，实际错误：%v", err)
	}
	if fieldErr.Field != "snapshotHash" {
		t.Fatalf("迁移门禁应指向画布快照字段，实际：%s", fieldErr.Field)
	}
	return fieldErr
}

func agentCanvasPayload(t *testing.T, s *Service, canvasID string) string {
	t.Helper()
	stored, err := s.repo.CanvasProjectForUser("user", canvasID)
	if err != nil {
		t.Fatal(err)
	}
	return stored.PayloadJSON
}

// TestAgentLegacyCanvasRequiresMigration：审批预览与真实执行都拒绝历史画布上的引用字段写入，
// 文档逐字节不变；同一操作在 v1 画布上正常通过，纯画布编辑在历史画布上仍可用。
func TestAgentLegacyCanvasRequiresMigration(t *testing.T) {
	s, db, args := agentMediaFixtureWithReferenceVersion(t, 0)
	policy, err := s.RuntimePolicy()
	if err != nil {
		t.Fatal(err)
	}
	legacy := agentCanvasPayload(t, s, "agent-canvas")

	ops := agentReferenceFieldOps()
	call := agentCanvasOpsCall(t, "legacy-field", args.SnapshotHash, ops)
	if _, err := prepareCloudAgentCanvasMutation(s.repo, "user", "agent-canvas", call); err != nil {
		agentAssertMigrationRequired(t, err)
	} else {
		t.Fatal("审批预览接受了历史画布上的引用字段写入")
	}
	if _, err := applyCloudAgentCanvas(s.repo, "user", "agent-canvas", call, policy); err != nil {
		agentAssertMigrationRequired(t, err)
	} else {
		t.Fatal("真实执行接受了历史画布上的引用字段写入")
	}
	if got := agentCanvasPayload(t, s, "agent-canvas"); got != legacy {
		t.Fatal("被拒绝的历史画布引用写入改写了文档")
	}

	// 不依赖 referenceFieldsVersion 的编辑继续可用：历史画布不该被整体锁死。
	plain := agentCanvasOpsCall(t, "legacy-plain", args.SnapshotHash, []map[string]any{
		{"type": "update_node", "id": "cat", "patch": map[string]any{"title": "历史画布改名"}},
	})
	if _, err := applyCloudAgentCanvas(s.repo, "user", "agent-canvas", plain, policy); err != nil {
		t.Fatalf("历史画布上的普通编辑被过度拦截：%v", err)
	}

	// v1 反向用例：同一批引用字段操作正常通过，并由服务端派生托管连接。
	canvas, err := s.repo.CanvasProjectForUser("user", "agent-canvas")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		t.Fatal(err)
	}
	doc["referenceFieldsVersion"] = 1
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Model(canvas).Update("payload_json", string(encoded)).Error; err != nil {
		t.Fatal(err)
	}
	args.SnapshotHash = cloudAgentCanvasHash(doc)
	migrated := agentCanvasOpsCall(t, "v1-field", args.SnapshotHash, agentReferenceFieldOps())
	if _, err := applyCloudAgentCanvas(s.repo, "user", "agent-canvas", migrated, policy); err != nil {
		t.Fatalf("v1 画布拒绝了同一引用字段操作：%v", err)
	}
	after, err := creationDocument(agentCanvasPayload(t, s, "agent-canvas"))
	if err != nil {
		t.Fatal(err)
	}
	derived := false
	for _, edge := range creationMaps(after["connections"]) {
		if stringValue(edge["fromNodeId"]) == "cat" && stringValue(edge["toNodeId"]) == "ref-target" && stringValue(edge["managedBy"]) == references.ManagedBy {
			derived = true
		}
	}
	if !derived {
		t.Fatalf("v1 画布没有派生托管连接：%+v", creationMaps(after["connections"]))
	}
}

// TestAgentLegacyStoryboardReferencesRequireMigration：分镜共享引用与行素材声明同样受门禁保护；
// 不含引用声明的分镜创建与改名在历史画布上继续可用。
func TestAgentLegacyStoryboardReferencesRequireMigration(t *testing.T) {
	s, db, canvas := cloudAgentStoryboardFixture(t)
	// 历史画布上创建不含引用的分镜仍然允许（门禁只拦引用字段）。
	rows := createCloudAgentStoryboardForTest(t, s, canvas)
	policy, err := s.RuntimePolicy()
	if err != nil {
		t.Fatal(err)
	}
	doc, err := creationDocument(agentCanvasPayload(t, s, canvas.ID))
	if err != nil {
		t.Fatal(err)
	}
	legacy := agentCanvasPayload(t, s, canvas.ID)

	setReferences := cloudAgentStoryboardCall(t, "storyboard", "legacy-refs", map[string]any{
		"snapshotHash": cloudAgentCanvasHash(doc), "nodeId": "storyboard-1", "action": "set_references", "referenceNodeIds": []any{"asset"},
	})
	if _, err := prepareCloudAgentStoryboardEdit(s.repo, "user", canvas.ID, setReferences); err != nil {
		agentAssertMigrationRequired(t, err)
	} else {
		t.Fatal("审批预览接受了历史画布上的共享引用声明")
	}
	if _, err := applyCloudAgentStoryboardMutation(s.repo, "user", canvas.ID, setReferences, policy); err != nil {
		agentAssertMigrationRequired(t, err)
	} else {
		t.Fatal("真实执行接受了历史画布上的共享引用声明")
	}

	rowAssets := cloudAgentStoryboardCall(t, "storyboard", "legacy-rows", map[string]any{
		"snapshotHash": cloudAgentCanvasHash(doc), "nodeId": "storyboard-1", "action": "update", "rowId": rows[0]["id"],
		"patch": map[string]any{"assetBindings": []any{map[string]any{"nodeId": "asset", "role": "style", "priority": 80}}},
	})
	if _, err := prepareCloudAgentStoryboardEdit(s.repo, "user", canvas.ID, rowAssets); err != nil {
		agentAssertMigrationRequired(t, err)
	} else {
		t.Fatal("审批预览接受了历史画布上的行素材声明")
	}
	if got := agentCanvasPayload(t, s, canvas.ID); got != legacy {
		t.Fatal("被拒绝的历史画布分镜引用写入改写了文档")
	}

	// v1 反向用例：同一 set_references 正常通过。
	doc["referenceFieldsVersion"] = 1
	doc["nodes"] = append(creationMaps(doc["nodes"]), map[string]any{"id": "asset", "type": "image", "metadata": map[string]any{}})
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Model(canvas).Update("payload_json", string(encoded)).Error; err != nil {
		t.Fatal(err)
	}
	migrated := cloudAgentStoryboardCall(t, "storyboard", "v1-refs", map[string]any{
		"snapshotHash": cloudAgentCanvasHash(doc), "nodeId": "storyboard-1", "action": "set_references", "referenceNodeIds": []any{"asset"},
	})
	plan, err := prepareCloudAgentStoryboardEdit(s.repo, "user", canvas.ID, migrated)
	if err != nil {
		t.Fatalf("v1 画布拒绝了同一共享引用声明：%v", err)
	}
	if plan == nil || plan.Preview.Kind != "canvas_mutation" {
		t.Fatalf("v1 预览缺少审批信息：%+v", plan)
	}
	if _, err := applyCloudAgentStoryboardMutation(s.repo, "user", canvas.ID, migrated, policy); err != nil {
		t.Fatalf("v1 画布执行共享引用声明失败：%v", err)
	}
	after, err := creationDocument(agentCanvasPayload(t, s, canvas.ID))
	if err != nil {
		t.Fatal(err)
	}
	_, board, _, err := storyboardNodeFromDocument(after, "storyboard-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(cloudAgentStrings(board["referenceNodeIds"])) != 1 {
		t.Fatalf("v1 画布没有写入共享引用：%+v", board["referenceNodeIds"])
	}
}

// TestAgentReferenceContractGatePrecedence：参数合同先于画布状态判定——旧连线操作在任何画布上
// 都是 connection_ops_disabled；只有符合 schema 的引用字段写入才落到 reference_migration_required。
func TestAgentReferenceContractGatePrecedence(t *testing.T) {
	legacy := map[string]any{"nodes": []any{map[string]any{"id": "cat", "type": "image"}, map[string]any{"id": "target", "type": "compare", "metadata": map[string]any{}}}, "connections": []any{}}
	migrated := map[string]any{"referenceFieldsVersion": 1, "nodes": []any{map[string]any{"id": "cat", "type": "image"}, map[string]any{"id": "target", "type": "compare", "metadata": map[string]any{}}}, "connections": []any{}}
	fieldOps := []agentCanvasOp{{Type: "update_node", ID: "target", Patch: map[string]any{"inputBindings": []any{map[string]any{"id": "node:cat", "nodeId": "cat", "mediaType": "image", "role": "reference", "order": float64(0), "resolution": "latest"}}}}}
	connectionOp := []agentCanvasOp{{Type: "connect_nodes", ID: "edge", FromNodeID: "cat", ToNodeID: "target"}}

	for name, tc := range map[string]struct {
		doc    map[string]any
		ops    []agentCanvasOp
		issue  string
		field  string
		accept bool
	}{
		"legacy connection":   {legacy, connectionOp, "connection_ops_disabled", "ops[0].type", false},
		"migrated connection": {migrated, connectionOp, "connection_ops_disabled", "ops[0].type", false},
		"legacy references":   {legacy, fieldOps, "reference_migration_required", "snapshotHash", false},
		"migrated references": {migrated, fieldOps, "", "", true},
	} {
		t.Run(name, func(t *testing.T) {
			doc, err := creationDocument(mustJSON(tc.doc))
			if err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(doc)
			_, err = applyCloudAgentCanvasPlan(doc, tc.ops)
			if tc.accept {
				if err != nil {
					t.Fatalf("v1 画布拒绝了引用字段写入：%v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("操作未被拒绝")
			}
			var fieldErr *cloudAgentFieldArgumentError
			if !errors.As(err, &fieldErr) || fieldErr.Issue != tc.issue || fieldErr.Field != tc.field {
				t.Fatalf("判定顺序或字段不正确：%v", err)
			}
			after, _ := json.Marshal(doc)
			if string(before) != string(after) {
				t.Fatal("被拒绝的操作改写了文档")
			}
		})
	}
}

// TestAgentLegacyCanvasReadsStayAvailable：只读路径不受迁移门禁影响——历史画布仍可读取，
// 且读取不修改文档。
func TestAgentLegacyCanvasReadsStayAvailable(t *testing.T) {
	s, _, canvas := cloudAgentStoryboardFixture(t)
	createCloudAgentStoryboardForTest(t, s, canvas)
	doc, err := creationDocument(agentCanvasPayload(t, s, canvas.ID))
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(doc)
	state, err := cloudAgentCanvasState(s.repo, "user", canvas.ID, doc, 0, nil, 0)
	if err != nil {
		t.Fatalf("历史画布读取被门禁挡住：%v", err)
	}
	stateMap, _ := state.(map[string]any)
	if stateMap == nil || len(creationMaps(stateMap["nodes"])) == 0 {
		t.Fatalf("历史画布读取结果不完整：%+v", state)
	}
	after, _ := json.Marshal(doc)
	if string(before) != string(after) {
		t.Fatal("只读路径修改了历史画布文档")
	}
}
