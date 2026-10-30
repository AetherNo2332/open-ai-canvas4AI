package commands

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"

	"yingce/backend/internal/canvas/references"
	"yingce/backend/internal/identity"
	"yingce/backend/internal/model"
)

// 历史画布：显式字段与旧连线在同一个分镜作用域上互相矛盾（行素材与输出各一处），
// 并且没有任何 referenceFieldsVersion 标记。
const commandConflictDocument = `{
  "id":"canvas",
  "title":"冲突画布",
  "nodes":[
    {"id":"hero","type":"text","metadata":{"workflowKind":"character","characterAssetId":"hero-asset"}},
    {"id":"scene","type":"image","metadata":{}},
    {"id":"board","type":"script","metadata":{"storyboard":{"referenceNodeIds":[],"rows":[{"id":"shot","plotDescription":"第一镜","assetBindings":[{"nodeId":"hero","role":"character","priority":100}],"imageNodeId":"old-product"}]}}},
    {"id":"old-product","type":"image","metadata":{}},
    {"id":"new-product","type":"image","metadata":{}}
  ],
  "connections":[
    {"id":"row-scene","fromNodeId":"scene","toNodeId":"board","toHandleId":"row:shot"},
    {"id":"output-new","fromNodeId":"board","toNodeId":"new-product","fromHandleId":"row:shot"}
  ]
}`

// 只有旧连线的历史画布：字段缺失，规范化不需要任何来源选择。
const commandEdgeOnlyDocument = `{
  "id":"canvas",
  "title":"历史画布",
  "nodes":[
    {"id":"scene","type":"image","metadata":{}},
    {"id":"board","type":"script","metadata":{"storyboard":{"referenceNodeIds":[],"rows":[{"id":"shot","plotDescription":"第一镜"}]}}},
    {"id":"product","type":"image","metadata":{"prompt":"镜头一"}}
  ],
  "connections":[
    {"id":"row-scene","fromNodeId":"scene","toNodeId":"board","toHandleId":"row:shot"},
    {"id":"output-product","fromNodeId":"board","toNodeId":"product","fromHandleId":"row:shot"}
  ]
}`

func migrationOperation(patch map[string]any) Operation {
	return Operation{Type: OperationMigrateReferences, ID: "canvas", Patch: patch}
}

// migrationOperationFor 让真实画布 ID 同时出现在 id 与文档上：迁移只接受画布自身。
func migrationOperationFor(canvasID string, patch map[string]any) Operation {
	return Operation{Type: OperationMigrateReferences, ID: canvasID, Patch: patch}
}

func migrationScopeChoices(key, choice string) map[string]any {
	return map[string]any{"scopeChoices": map[string]any{key: choice}}
}

func migrationErrorReason(t *testing.T, err error) string {
	t.Helper()
	var typed *Error
	if !errors.As(err, &typed) {
		t.Fatalf("不是画布业务错误：%v", err)
	}
	return typed.Reason
}

func migrationDocumentJSON(t *testing.T, doc Document) string {
	t.Helper()
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func migrationRowOf(t *testing.T, doc Document, rowID string) map[string]any {
	t.Helper()
	for _, node := range maps(doc["nodes"]) {
		storyboard, ok := entityMetadata(node)["storyboard"].(map[string]any)
		if !ok {
			continue
		}
		for _, row := range maps(storyboard["rows"]) {
			if text(row["id"]) == rowID {
				return row
			}
		}
	}
	t.Fatalf("分镜行缺失：%s", rowID)
	return nil
}

func entityMetadata(node map[string]any) map[string]any {
	metadata, _ := node["metadata"].(map[string]any)
	if metadata == nil {
		metadata = map[string]any{}
	}
	return metadata
}

func migrationEdgeOf(doc Document, id string) map[string]any {
	for _, edge := range maps(doc["connections"]) {
		if text(edge["id"]) == id {
			return edge
		}
	}
	return nil
}

// migrationCanonical 把托管连线的随机标识换成由端点与绑定推导的稳定键，用于比较
// 两次迁移的引用语义；普通连线保持原样，因此顺序和标识差异仍会被发现。
func migrationCanonical(t *testing.T, doc Document) string {
	t.Helper()
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	clone := map[string]any{}
	if err := json.Unmarshal(encoded, &clone); err != nil {
		t.Fatal(err)
	}
	edges := maps(clone["connections"])
	for _, edge := range edges {
		if text(edge["managedBy"]) != references.ManagedBy {
			continue
		}
		edge["id"] = strings.Join([]string{text(edge["fromNodeId"]), text(edge["toNodeId"]), text(edge["fromHandleId"]), text(edge["toHandleId"]), text(edge["referenceOwnerNodeId"]), text(edge["referenceBindingId"]), text(edge["relation"])}, "|")
	}
	sort.Slice(edges, func(i, j int) bool { return text(edges[i]["id"]) < text(edges[j]["id"]) })
	clone["connections"] = edges
	canonical, err := json.Marshal(clone)
	if err != nil {
		t.Fatal(err)
	}
	return string(canonical)
}

func migrationManagedEdgeOf(doc Document, from, to string) map[string]any {
	for _, edge := range maps(doc["connections"]) {
		if text(edge["managedBy"]) == references.ManagedBy && text(edge["fromNodeId"]) == from && text(edge["toNodeId"]) == to {
			return edge
		}
	}
	return nil
}

func TestReferenceMigrationCommandRequiresChoice(t *testing.T) {
	doc := fixture(t, commandConflictDocument)
	before := migrationDocumentJSON(t, doc)
	result, err := Plan(doc, command(migrationOperation(nil)), ownerContext())
	if err == nil || migrationErrorReason(t, err) != "reference_migration_conflict" {
		t.Fatalf("未决冲突没有返回 reference_migration_conflict：%v", err)
	}
	if migrationDocumentJSON(t, doc) != before || !reflect.DeepEqual(result.Document, doc) {
		t.Fatal("未决冲突的迁移改写了文档")
	}

	connections := fixture(t, commandConflictDocument)
	result, err = Plan(connections, command(migrationOperation(migrationScopeChoices("storyboard-row:board:shot", references.MigrationChoiceConnections))), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	row := migrationRowOf(t, result.Document, "shot")
	if !reflect.DeepEqual(row["assetBindings"], []any{map[string]any{"nodeId": "scene", "role": "style", "priority": float64(60)}}) {
		t.Fatalf("选择连线没有以连线为来源：%#v", row["assetBindings"])
	}
	if row["imageNodeId"] != "new-product" {
		t.Fatalf("选择连线没有采用连线声明的输出：%v", row["imageNodeId"])
	}
	if result.Document["referenceFieldsVersion"] != float64(1) {
		t.Fatalf("迁移没有写入版本标记：%#v", result.Document["referenceFieldsVersion"])
	}
	if !result.WriteSet["project:referenceFieldsVersion"] {
		t.Fatal("版本标记没有进入写集")
	}
	if migrationEdgeOf(result.Document, "row-scene") != nil || migrationEdgeOf(result.Document, "output-new") != nil {
		t.Fatal("被接管的旧连线仍然存在")
	}
	if migrationManagedEdgeOf(result.Document, "scene", "board") == nil || migrationManagedEdgeOf(result.Document, "board", "new-product") == nil {
		t.Fatal("迁移没有生成托管连线")
	}
	if len(result.Summary) != 1 || result.Summary[0].Operation != OperationMigrateReferences || len(result.Summary[0].Details) != 1 {
		t.Fatalf("迁移回执不完整：%+v", result.Summary)
	}
	restored, err := ApplyChanges(result.Document, result.Inverse)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restored, doc) {
		t.Fatal("逆变更没有恢复规范化前像")
	}

	fields := fixture(t, commandConflictDocument)
	result, err = Plan(fields, command(migrationOperation(migrationScopeChoices("storyboard-row:board:shot", references.MigrationChoiceFields))), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	row = migrationRowOf(t, result.Document, "shot")
	if !reflect.DeepEqual(row["assetBindings"], []any{map[string]any{"nodeId": "hero", "role": "character", "priority": float64(100)}}) || row["imageNodeId"] != "old-product" {
		t.Fatalf("选择字段没有保留字段：%#v", row)
	}
	if migrationManagedEdgeOf(result.Document, "scene", "board") != nil || migrationManagedEdgeOf(result.Document, "board", "new-product") != nil {
		t.Fatal("选择字段后仍按连线建立了托管关系")
	}
	if migrationManagedEdgeOf(result.Document, "board", "old-product") == nil {
		t.Fatal("选择字段后没有为字段输出建立托管连线")
	}
}

func TestReferenceMigrationCommandEdgeOnly(t *testing.T) {
	doc := fixture(t, commandEdgeOnlyDocument)
	before := migrationDocumentJSON(t, doc)
	result, err := Plan(doc, command(migrationOperation(nil)), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	if migrationDocumentJSON(t, doc) != before {
		t.Fatal("规划器改写了输入文档")
	}
	row := migrationRowOf(t, result.Document, "shot")
	if !reflect.DeepEqual(row["assetBindings"], []any{map[string]any{"nodeId": "scene", "role": "style", "priority": float64(60)}}) {
		t.Fatalf("行引用没有从旧连线补全：%#v", row["assetBindings"])
	}
	if row["imageNodeId"] != "product" || result.Document["referenceFieldsVersion"] != float64(1) {
		t.Fatalf("输出或版本标记没有补全：%#v", result.Document["referenceFieldsVersion"])
	}
	if migrationEdgeOf(result.Document, "row-scene") != nil || migrationEdgeOf(result.Document, "output-product") != nil {
		t.Fatal("被接管的旧连线仍然存在")
	}
	// 已规范化文档重复规范化只推进修订、不改文档，因此明确拒绝。
	_, err = Plan(result.Document, command(migrationOperation(nil)), ownerContext())
	if err == nil || migrationErrorReason(t, err) != "reference_migration_not_required" {
		t.Fatalf("已规范化文档没有拒绝重复迁移：%v", err)
	}
	// 非法来源选择必须被拒绝，不能默默忽略。
	for _, patch := range []map[string]any{
		{"scopeChoices": map[string]any{"storyboard-row:board:shot": "merge"}},
		{"scopeChoices": "connections"},
		{"scopeChoices": map[string]any{"storyboard-row:board:shot": "connections"}, "extra": true},
	} {
		legacy := fixture(t, commandConflictDocument)
		_, err := Plan(legacy, command(migrationOperation(patch)), ownerContext())
		if err == nil {
			t.Fatalf("非法迁移参数被接受：%#v", patch)
		}
	}
}

// TestReferenceMigrationGate 覆盖 service 写入口的门禁规则（纯逻辑）。
func TestReferenceMigrationGate(t *testing.T) {
	legacy := fixture(t, commandEdgeOnlyDocument)
	for name, ops := range map[string][]Operation{
		"编辑节点": {{Type: "update_node", ID: "scene", Patch: map[string]any{"content": "x"}}},
		"编辑分镜": {{Type: "edit_storyboard", ID: "board", RowID: "shot", Action: "update", Patch: map[string]any{"plotDescription": "x"}}},
		"连线":   {{Type: "connect_nodes", ID: "edge", FromNodeID: "scene", ToNodeID: "product"}},
		"混合批次": {migrationOperation(nil), {Type: "update_node", ID: "scene", Patch: map[string]any{"content": "x"}}},
	} {
		err := referenceMigrationGate(legacy, ops)
		if err == nil || migrationErrorReason(t, err) != "reference_migration_required" {
			t.Fatalf("%s没有被门禁拦住：%v", name, err)
		}
	}
	title := "新名字"
	for name, ops := range map[string][]Operation{
		"迁移自身": {migrationOperation(nil)},
		"改名":   {{Type: "rename_canvas", ID: "canvas", Title: &title}},
		"删除画布": {{Type: "delete_canvas", ID: "canvas"}},
		"画布外观": {{Type: "edit_canvas_settings", ID: "canvas", Patch: map[string]any{"backgroundMode": "dots"}}},
	} {
		if err := referenceMigrationGate(legacy, ops); err != nil {
			t.Fatalf("%s被门禁拦住：%v", name, err)
		}
	}
	migrated, err := Plan(fixture(t, commandEdgeOnlyDocument), command(migrationOperation(nil)), ownerContext())
	if err != nil {
		t.Fatalf("迁移命令被自己的门禁拦住：%v", err)
	}
	if err = referenceMigrationGate(migrated.Document, []Operation{{Type: "update_node", ID: "scene"}}); err != nil {
		t.Fatalf("已规范化文档仍被门禁拦住：%v", err)
	}
}

// TestReferenceMigrationCommandSQLite 用真实 SQLite 覆盖读取预览、应用、undo 前像、
// 重复执行与权限/版本失败。
func TestReferenceMigrationCommandSQLite(t *testing.T) {
	svc, db, _, snap := commandFixture(t)
	ctx := context.Background()
	p := ownerContext().Principal
	legacy := strings.Replace(commandConflictDocument, `"id":"canvas"`, `"id":"`+snap.CanvasID+`"`, 1)
	if err := db.Model(&model.CanvasProject{}).Where("id = ?", snap.CanvasID).Update("payload_json", legacy).Error; err != nil {
		t.Fatal(err)
	}
	current, err := svc.ReadSnapshot(ctx, p, snap.CanvasID)
	if err != nil {
		t.Fatal(err)
	}
	if current.ReferenceMigration == nil || !current.ReferenceMigration.Required || len(current.ReferenceMigration.Conflicts) != 1 {
		t.Fatalf("读取投影没有给出迁移预览：%+v", current.ReferenceMigration)
	}
	if current.ReferenceMigration.Conflicts[0].ScopeKey != "storyboard-row:board:shot" {
		t.Fatalf("预览冲突作用域不正确：%+v", current.ReferenceMigration.Conflicts[0])
	}
	// service 写入口的门禁：历史画布不能直接编辑，但不改变引用内容的改名放行。
	blocked := command(Operation{Type: "update_node", ID: "scene", Patch: map[string]any{"content": "x"}})
	blocked.CanvasID, blocked.DocumentEpoch, blocked.BaseRevision = snap.CanvasID, current.DocumentEpoch, current.Revision
	blocked.OperationID = "migrate-blocked-edit"
	if _, err = svc.Execute(ctx, p, blocked); err == nil || migrationErrorReason(t, err) != "reference_migration_required" {
		t.Fatalf("历史画布没有被 service 门禁拦住：%v", err)
	}
	title := "迁移前改名"
	renamed := command(Operation{Type: "rename_canvas", ID: snap.CanvasID, Title: &title})
	renamed.CanvasID, renamed.DocumentEpoch, renamed.BaseRevision = snap.CanvasID, current.DocumentEpoch, current.Revision
	renamed.OperationID = "migrate-rename"
	if _, err = svc.Execute(ctx, p, renamed); err != nil {
		t.Fatalf("历史画布改名被门禁拦住：%v", err)
	}
	if current, err = svc.ReadSnapshot(ctx, p, snap.CanvasID); err != nil {
		t.Fatal(err)
	}
	if current.ReferenceMigration == nil || current.Document["title"] != title {
		t.Fatalf("改名后读取投影异常：%+v", current.ReferenceMigration)
	}
	previewJSON := migrationDocumentJSON(t, current.Document)

	req := command(migrationOperationFor(snap.CanvasID, nil))
	req.CanvasID, req.DocumentEpoch, req.BaseRevision = snap.CanvasID, current.DocumentEpoch, current.Revision
	req.OperationID = "migrate-preview"
	if _, err = svc.Execute(ctx, p, req); err == nil || migrationErrorReason(t, err) != "reference_migration_conflict" {
		t.Fatalf("未决冲突没有在服务层被拒绝：%v", err)
	}
	if current, err = svc.ReadSnapshot(ctx, p, snap.CanvasID); err != nil {
		t.Fatal(err)
	}
	if migrationDocumentJSON(t, current.Document) != previewJSON {
		t.Fatal("被拒绝的迁移改写了文档")
	}

	req = command(migrationOperationFor(snap.CanvasID, migrationScopeChoices("storyboard-row:board:shot", references.MigrationChoiceConnections)))
	req.CanvasID, req.DocumentEpoch, req.BaseRevision = snap.CanvasID, current.DocumentEpoch, current.Revision
	req.OperationID = "migrate-apply"
	receipt, err := svc.Execute(ctx, p, req)
	if err != nil {
		t.Fatal(err)
	}
	migrated, err := svc.ReadSnapshot(ctx, p, snap.CanvasID)
	if err != nil {
		t.Fatal(err)
	}
	if migrated.ReferenceMigration != nil {
		t.Fatalf("已规范化文档仍然报告迁移预览：%+v", migrated.ReferenceMigration)
	}
	if migrated.Document["referenceFieldsVersion"] != float64(1) || migrationRowOf(t, migrated.Document, "shot")["imageNodeId"] != "new-product" {
		t.Fatal("迁移结果没有落库")
	}
	migratedJSON := migrationDocumentJSON(t, migrated.Document)

	// 同一 operationId 重复提交返回同一回执，不再推进修订。
	again, err := svc.Execute(ctx, p, req)
	if err != nil || again.Revision != receipt.Revision || !reflect.DeepEqual(again.Changes, receipt.Changes) {
		t.Fatalf("重复提交没有返回同一回执：%v %+v", err, again)
	}

	// undo 恢复规范化前像：字段、旧连线与版本标记都回到历史状态，并重新进入迁移入口。
	if _, err = svc.Undo(ctx, p, snap.CanvasID, migrated.DocumentEpoch, "client", "undo-migrate"); err != nil {
		t.Fatal(err)
	}
	restored, err := svc.ReadSnapshot(ctx, p, snap.CanvasID)
	if err != nil {
		t.Fatal(err)
	}
	if migrationDocumentJSON(t, restored.Document) != previewJSON {
		t.Fatal("undo 没有恢复规范化前像")
	}
	if restored.ReferenceMigration == nil || !restored.ReferenceMigration.Required {
		t.Fatal("恢复历史文档后没有重新进入迁移入口")
	}
	if _, err = svc.Redo(ctx, p, snap.CanvasID, migrated.DocumentEpoch, "client", "redo-migrate"); err != nil {
		t.Fatal(err)
	}
	redone, err := svc.ReadSnapshot(ctx, p, snap.CanvasID)
	if err != nil {
		t.Fatal(err)
	}
	if migrationDocumentJSON(t, redone.Document) != migratedJSON {
		t.Fatal("redo 没有恢复规范化结果")
	}

	// 恢复前像后用新的 operationId 再迁移一次：引用语义必须与首次完全一致。
	// 托管连线标识来自主机的随机分配（与其它连线创建一致），因此比较时归一化标识，
	// 逐字节相等只对同一次提交成立（上面的同一 operationId 回执断言）。
	if _, err = svc.Undo(ctx, p, snap.CanvasID, migrated.DocumentEpoch, "client", "undo-again"); err != nil {
		t.Fatal(err)
	}
	replay, err := svc.ReadSnapshot(ctx, p, snap.CanvasID)
	if err != nil {
		t.Fatal(err)
	}
	// 旧版本提交必须先失败：客户端拿着迁移前的修订号，要先读取最新版本。
	stale := command(migrationOperationFor(snap.CanvasID, migrationScopeChoices("storyboard-row:board:shot", references.MigrationChoiceConnections)))
	stale.CanvasID, stale.DocumentEpoch, stale.BaseRevision = snap.CanvasID, replay.DocumentEpoch, snap.Revision
	stale.OperationID = "migrate-stale"
	if _, err = svc.Execute(ctx, p, stale); err == nil || migrationErrorReason(t, err) != "revision_conflict" {
		t.Fatalf("旧版本提交没有失败：%v", err)
	}
	req = command(migrationOperationFor(snap.CanvasID, migrationScopeChoices("storyboard-row:board:shot", references.MigrationChoiceConnections)))
	req.CanvasID, req.DocumentEpoch, req.BaseRevision = snap.CanvasID, replay.DocumentEpoch, replay.Revision
	req.OperationID = "migrate-replay"
	if _, err = svc.Execute(ctx, p, req); err != nil {
		t.Fatal(err)
	}
	replayed, err := svc.ReadSnapshot(ctx, p, snap.CanvasID)
	if err != nil {
		t.Fatal(err)
	}
	if migrationCanonical(t, replayed.Document) != migrationCanonical(t, migrated.Document) {
		t.Fatal("同一 choices 再次迁移的引用语义与首次不同")
	}

	// 无画布写权限的调用方被拒绝。
	stranger := identity.Principal{ID: "stranger", OwnerID: "stranger", Role: "owner", Scopes: identity.OwnerScopes()}
	denied := command(migrationOperationFor(snap.CanvasID, nil))
	denied.CanvasID, denied.DocumentEpoch, denied.BaseRevision = snap.CanvasID, replayed.DocumentEpoch, replayed.Revision
	denied.OperationID = "migrate-denied"
	if _, err = svc.Execute(ctx, stranger, denied); err == nil || migrationErrorReason(t, err) != "permission_denied" {
		t.Fatalf("无权调用方没有被拒绝：%v", err)
	}
}
