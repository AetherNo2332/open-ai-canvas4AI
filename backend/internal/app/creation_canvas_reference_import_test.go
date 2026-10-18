package app

import (
	"encoding/json"
	"testing"

	"infinite-canvas/backend/internal/canvas/references"
)

// 智能创作提交的整份文档把引用表达成旧连线。导入时必须一次翻译成声明式字段并写入
// 版本标记，否则新建画布会被当作历史画布：首次打开弹迁移面板，引用编辑与画布内
// 生成都会被 reference_migration_required 拦下。
func TestCreationCanvasImportNormalizesReferences(t *testing.T) {
	s, _, id, guard := creationTestService(t)
	ops := []CreationCanvasOp{
		{Type: "add_node", ID: "asset", NodeType: "image", Title: "素材", Position: map[string]any{"x": float64(0), "y": float64(0)}},
		{Type: "add_node", ID: "target", NodeType: "image", Title: "成片", Position: map[string]any{"x": float64(400), "y": float64(0)}},
		{Type: "connect_nodes", ID: "import-edge", FromNodeID: "asset", ToNodeID: "target"},
	}
	if _, err := s.ChangeCreationRun("user", id, "proposal-approve", CreationRequest{CreationGuard: guard, Revision: 2, ProposalVersion: 1, Proposal: json.RawMessage(`{"title":"计划"}`), Ops: ops}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateRunCanvas("user", id, CreationRequest{CreationGuard: guard}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.CreationCanvasSnapshot("user", id)
	if err != nil {
		t.Fatal(err)
	}
	doc := snapshot["document"].(map[string]any)
	doc["nodes"] = []any{creationAddedNode(ops[0]), creationAddedNode(ops[1])}
	doc["connections"] = []any{map[string]any{"id": "import-edge", "fromNodeId": "asset", "toNodeId": "target"}}
	raw, _ := json.Marshal(doc)
	if _, err = s.CommitCreationCanvas("user", id, CreationRequest{CreationGuard: guard, ExpectedSnapshotHash: snapshot["snapshotHash"].(string), Document: raw}); err != nil {
		t.Fatal(err)
	}

	committed, err := s.CreationCanvasSnapshot("user", id)
	if err != nil {
		t.Fatal(err)
	}
	saved := committed["document"].(map[string]any)
	if !references.Enabled(saved) {
		t.Fatal("导入后的画布没有写入引用版本标记")
	}
	if references.PreviewLegacy(saved, nil).Required {
		t.Fatal("导入后的画布仍被判定为需要迁移")
	}
	var target map[string]any
	for _, node := range creationMaps(saved["nodes"]) {
		if stringValue(node["id"]) == "target" {
			target = node
		}
	}
	if target == nil {
		t.Fatal("导入后的画布缺少目标节点")
	}
	scope := references.Scope{Kind: "node", NodeID: "target"}
	if cloudAgentNodeMetadata(target)["generationSpec"] != nil {
		scope.Kind = "generation"
	}
	resolved, err := references.Resolve(saved, scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.Bindings) != 1 || resolved.Bindings[0].NodeID != "asset" {
		t.Fatalf("导入的引用没有翻译成声明式字段：%+v", resolved.Bindings)
	}
}
