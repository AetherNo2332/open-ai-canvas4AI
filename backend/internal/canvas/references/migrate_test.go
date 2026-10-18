package references

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"infinite-canvas/backend/internal/canvas/capability"
	"infinite-canvas/backend/internal/canvas/contract"
)

// 历史画布：没有任何声明字段，引用只存在于旧连线里。
const migrationLegacyEdgeDocument = `{
  "title": "历史画布",
  "nodes": [
    {"id":"hero","type":"text","metadata":{"workflowKind":"character","characterAssetId":"hero-asset"}},
    {"id":"scene","type":"image","metadata":{}},
    {"id":"sketch","type":"drawing","metadata":{}},
    {"id":"compare","type":"compare","metadata":{}},
    {"id":"note","type":"markdown","metadata":{"content":"备注"}},
    {"id":"board","type":"script","metadata":{"storyboard":{"referenceNodeIds":["hero"],"rows":[{"id":"shot","plotDescription":"第一镜"}]}}},
    {"id":"product","type":"image","metadata":{"prompt":"镜头一已提交","composerContent":"镜头一草稿","logicalModelId":"model-1","count":2,"size":"1024x1024","transparentBackground":true}},
    {"id":"frame","type":"image","metadata":{}}
  ],
  "connections": [
    {"id":"row-scene","fromNodeId":"scene","toNodeId":"board","toHandleId":"row:shot"},
    {"id":"row-sketch","fromNodeId":"sketch","toNodeId":"board","toHandleId":"row:shot"},
    {"id":"context-hero","fromNodeId":"hero","toNodeId":"board","toHandleId":"storyboard:context"},
    {"id":"output-product","fromNodeId":"board","toNodeId":"product","fromHandleId":"row:shot"},
    {"id":"input-compare","fromNodeId":"scene","toNodeId":"compare"},
    {"id":"media-product","fromNodeId":"frame","toNodeId":"product"},
    {"id":"visual-note","fromNodeId":"scene","toNodeId":"note"}
  ]
}`

// 显式字段与旧连线互相矛盾：空/不同字段不能静默并集。
const migrationConflictDocument = `{
  "title": "冲突画布",
  "nodes": [
    {"id":"hero","type":"text","metadata":{"workflowKind":"character","characterAssetId":"hero-asset"}},
    {"id":"scene","type":"image","metadata":{}},
    {"id":"board","type":"script","metadata":{"storyboard":{"referenceNodeIds":[],"rows":[{"id":"shot","assetBindings":[{"nodeId":"hero","role":"character","priority":100}],"imageNodeId":"old-product","videoNodeId":"old-clip"}]}}},
    {"id":"old-product","type":"image","metadata":{}},
    {"id":"old-clip","type":"video","metadata":{}},
    {"id":"new-product","type":"image","metadata":{}}
  ],
  "connections": [
    {"id":"row-scene","fromNodeId":"scene","toNodeId":"board","toHandleId":"row:shot"},
    {"id":"output-new","fromNodeId":"board","toNodeId":"new-product","fromHandleId":"row:shot"}
  ]
}`

// 模式不明的首尾帧：普通边不能猜出 first-frame。
const migrationFrameDocument = `{
  "nodes": [
    {"id":"frame","type":"image","metadata":{}},
    {"id":"board","type":"script","metadata":{"storyboard":{"referenceNodeIds":[],"rows":[{"id":"shot","imageNodeId":"frame","videoNodeId":"clip"}]}}},
    {"id":"clip","type":"video","metadata":{"prompt":"动作"}}
  ],
  "connections": [{"id":"frame-input","fromNodeId":"frame","toNodeId":"clip"}]
}`

func migrationDocument(t *testing.T, raw string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func migrationNode(t *testing.T, doc map[string]any, id string) map[string]any {
	t.Helper()
	node := index(doc)[id]
	if node == nil {
		t.Fatalf("节点缺失：%s", id)
	}
	return node
}

func migrationRow(t *testing.T, doc map[string]any, boardID, rowID string) map[string]any {
	t.Helper()
	row := rowFor(migrationNode(t, doc, boardID), "storyboard", rowID)
	if row == nil {
		t.Fatalf("分镜行缺失：%s", rowID)
	}
	return row
}

func migrationEdge(doc map[string]any, id string) map[string]any {
	for _, edge := range maps(doc["connections"]) {
		if str(edge["id"]) == id {
			return edge
		}
	}
	return nil
}

func migrationManagedEdge(doc map[string]any, from, to, handle string) map[string]any {
	for _, edge := range maps(doc["connections"]) {
		if str(edge["managedBy"]) != ManagedBy {
			continue
		}
		if str(edge["fromNodeId"]) != from || str(edge["toNodeId"]) != to {
			continue
		}
		if str(edge["toHandleId"]) == handle || str(edge["fromHandleId"]) == handle {
			return edge
		}
	}
	return nil
}

func migrationCounter() (func() string, *int) {
	allocated := 0
	return func() string { allocated++; return fmt.Sprintf("migration-edge-%d", allocated) }, &allocated
}

func migrationDecodeBindings(t *testing.T, raw any) []contract.ReferenceBinding {
	t.Helper()
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	bindings := []contract.ReferenceBinding{}
	if err := json.Unmarshal(encoded, &bindings); err != nil {
		t.Fatal(err)
	}
	return bindings
}

func migrationSnapshot(t *testing.T, doc map[string]any) string {
	t.Helper()
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestReferenceMigrationEdgeOnly(t *testing.T) {
	doc := migrationDocument(t, migrationLegacyEdgeDocument)
	preimage := migrationSnapshot(t, doc)
	nextID, _ := migrationCounter()
	got, err := NormalizeLegacy(doc, nil, capability.BuiltinRegistry(), nextID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Conflicts) != 0 {
		t.Fatalf("只有旧连线的历史画布不应要求选择来源：%+v", got.Conflicts)
	}
	if number(got.Document["referenceFieldsVersion"]) != Version {
		t.Fatal("规范化后的文档没有写入引用字段版本标记")
	}
	row := migrationRow(t, got.Document, "board", "shot")
	want := []any{
		map[string]any{"nodeId": "scene", "role": "style", "priority": float64(60)},
		map[string]any{"nodeId": "sketch", "role": "style", "priority": float64(60)},
	}
	if !reflect.DeepEqual(row["assetBindings"], want) {
		t.Fatalf("行引用没有从旧连线补全：%#v", row["assetBindings"])
	}
	if row["imageNodeId"] != "product" {
		t.Fatalf("分镜输出没有从旧连线补全：%v", row["imageNodeId"])
	}
	board := obj(meta(migrationNode(t, got.Document, "board"))["storyboard"])
	if !reflect.DeepEqual(board["referenceNodeIds"], []any{"hero"}) {
		t.Fatalf("表级共享引用被改写：%#v", board["referenceNodeIds"])
	}
	compare := migrationDecodeBindings(t, meta(migrationNode(t, got.Document, "compare"))["inputBindings"])
	if len(compare) != 1 || compare[0].NodeID != "scene" || compare[0].Role != "reference" || compare[0].MediaType != "image" || compare[0].Order != 0 {
		t.Fatalf("普通输入节点没有补全引用：%+v", compare)
	}
	spec := obj(meta(migrationNode(t, got.Document, "product"))["generationSpec"])
	if number(spec["version"]) != contract.GenerationVersion || str(spec["mode"]) != "image" {
		t.Fatalf("历史媒体节点没有从正式字段补全生成合同：%#v", spec)
	}
	// 合成必须保留历史扁平镜像的语义（prompt/modelSelection/options），否则接管即丢配置。
	decoded, err := contract.Decode(mustJSON(t, spec))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Prompt != "镜头一草稿" || decoded.TextInputMode != "append-sources" {
		t.Fatalf("历史提示词或文本输入模式没有被保留：%+v", decoded)
	}
	if decoded.ModelSelection == nil || decoded.ModelSelection.Kind != "logical" || decoded.ModelSelection.LogicalModelID != "model-1" {
		t.Fatalf("历史模型选择没有被保留：%+v", decoded.ModelSelection)
	}
	if decoded.Options.Count == nil || *decoded.Options.Count != 2 || decoded.Options.Size == nil || *decoded.Options.Size != "1024x1024" ||
		decoded.Options.TransparentBackground == nil || !*decoded.Options.TransparentBackground {
		t.Fatalf("历史生成参数没有被保留：%+v", decoded.Options)
	}
	if prompt := str(meta(migrationNode(t, got.Document, "product"))["prompt"]); prompt != "镜头一已提交" {
		t.Fatalf("迁移改写了已提交提示词镜像：%q", prompt)
	}
	mirrored := migrationDecodeBindings(t, spec["referenceBindings"])
	if len(mirrored) != 1 || mirrored[0].NodeID != "frame" || mirrored[0].Role != "reference" {
		t.Fatalf("历史媒体节点没有补全连线引用：%+v", mirrored)
	}
	for _, legacy := range []string{"row-scene", "row-sketch", "context-hero", "output-product", "input-compare", "media-product"} {
		if migrationEdge(got.Document, legacy) != nil {
			t.Fatalf("旧连线 %s 在接管后仍然存在", legacy)
		}
	}
	if edge := migrationEdge(got.Document, "visual-note"); edge == nil || str(edge["managedBy"]) == ManagedBy {
		t.Fatal("不相关普通连线被删除或接管")
	}
	if migrationManagedEdge(got.Document, "scene", "board", "row:shot") == nil || migrationManagedEdge(got.Document, "sketch", "board", "row:shot") == nil {
		t.Fatal("行引用没有生成托管连线")
	}
	if migrationManagedEdge(got.Document, "hero", "board", "storyboard:context") == nil {
		t.Fatal("表级共享引用没有生成托管连线")
	}
	if migrationManagedEdge(got.Document, "board", "product", "row:shot") == nil {
		t.Fatal("分镜输出没有生成托管连线")
	}
	if migrationManagedEdge(got.Document, "scene", "compare", "") == nil {
		t.Fatal("普通输入节点没有生成托管连线")
	}
	if migrationManagedEdge(got.Document, "frame", "product", "") == nil {
		t.Fatal("历史媒体节点没有生成托管连线")
	}
	for _, consumed := range []string{"row-scene", "media-product"} {
		found := false
		for _, id := range got.ReadEdgeIDs {
			if id == consumed {
				found = true
			}
		}
		if !found {
			t.Fatalf("被接管的旧连线 %s 没有进入读集：%v", consumed, got.ReadEdgeIDs)
		}
	}
	if migrationSnapshot(t, doc) != preimage {
		t.Fatal("规范化修改了输入文档")
	}
}

func TestReferenceMigrationConflictRequiresChoice(t *testing.T) {
	doc := migrationDocument(t, migrationConflictDocument)
	preimage := migrationSnapshot(t, doc)
	nextID, allocated := migrationCounter()
	preview, err := NormalizeLegacy(doc, nil, capability.BuiltinRegistry(), nextID)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Conflicts) != 1 {
		t.Fatalf("矛盾作用域没有列为冲突：%+v", preview.Conflicts)
	}
	conflict := preview.Conflicts[0]
	if conflict.ScopeKey != "storyboard-row:board:shot" {
		t.Fatalf("冲突作用域标识不稳定：%q", conflict.ScopeKey)
	}
	if !reflect.DeepEqual(conflict.FieldNodeIDs, []string{"hero", "old-product"}) || !reflect.DeepEqual(conflict.EdgeNodeIDs, []string{"new-product", "scene"}) {
		t.Fatalf("冲突没有列出两侧节点：%+v", conflict)
	}
	if *allocated != 0 {
		t.Fatal("预览分配了新的标识")
	}
	if migrationSnapshot(t, preview.Document) != preimage {
		t.Fatal("存在未决冲突时文档仍被改写")
	}
	if _, exists := preview.Document["referenceFieldsVersion"]; exists {
		t.Fatal("存在未决冲突时仍写入了版本标记")
	}

	connections := migrationDocument(t, migrationConflictDocument)
	nextID, _ = migrationCounter()
	applied, err := NormalizeLegacy(connections, map[string]string{conflict.ScopeKey: MigrationChoiceConnections}, capability.BuiltinRegistry(), nextID)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied.Conflicts) != 0 {
		t.Fatalf("选择连线后仍有冲突：%+v", applied.Conflicts)
	}
	row := migrationRow(t, applied.Document, "board", "shot")
	if !reflect.DeepEqual(row["assetBindings"], []any{map[string]any{"nodeId": "scene", "role": "style", "priority": float64(60)}}) {
		t.Fatalf("选择连线没有以连线为来源：%#v", row["assetBindings"])
	}
	if row["imageNodeId"] != "new-product" {
		t.Fatalf("选择连线没有采用连线声明的输出：%v", row["imageNodeId"])
	}
	if row["videoNodeId"] != "old-clip" {
		t.Fatalf("无连线冲突的输出被改写：%v", row["videoNodeId"])
	}
	if number(applied.Document["referenceFieldsVersion"]) != Version {
		t.Fatal("解决冲突后没有写入版本标记")
	}
	if migrationEdge(applied.Document, "row-scene") != nil || migrationEdge(applied.Document, "output-new") != nil {
		t.Fatal("解决冲突后旧连线仍然存在")
	}
	if migrationManagedEdge(applied.Document, "board", "new-product", "row:shot") == nil {
		t.Fatal("选择连线后没有生成新的输出托管连线")
	}

	fields := migrationDocument(t, migrationConflictDocument)
	nextID, _ = migrationCounter()
	kept, err := NormalizeLegacy(fields, map[string]string{conflict.ScopeKey: MigrationChoiceFields}, capability.BuiltinRegistry(), nextID)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept.Conflicts) != 0 {
		t.Fatalf("选择字段后仍有冲突：%+v", kept.Conflicts)
	}
	row = migrationRow(t, kept.Document, "board", "shot")
	if !reflect.DeepEqual(row["assetBindings"], []any{map[string]any{"nodeId": "hero", "role": "character", "priority": float64(100)}}) {
		t.Fatalf("选择字段没有保留字段：%#v", row["assetBindings"])
	}
	if row["imageNodeId"] != "old-product" {
		t.Fatalf("选择字段没有保留输出字段：%v", row["imageNodeId"])
	}
	if migrationManagedEdge(kept.Document, "scene", "board", "row:shot") != nil || migrationManagedEdge(kept.Document, "board", "new-product", "row:shot") != nil {
		t.Fatal("选择字段后仍按连线建立了托管关系")
	}
	if migrationManagedEdge(kept.Document, "board", "old-product", "row:shot") == nil {
		t.Fatal("选择字段后没有为字段输出建立托管连线")
	}

	unknown := migrationDocument(t, migrationConflictDocument)
	nextID, _ = migrationCounter()
	pending, err := NormalizeLegacy(unknown, map[string]string{conflict.ScopeKey: "merge"}, capability.BuiltinRegistry(), nextID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending.Conflicts) != 1 {
		t.Fatal("未知来源选择被当成有效选择")
	}
	if migrationSnapshot(t, pending.Document) != preimage {
		t.Fatal("未知来源选择改写了文档")
	}
}

func TestReferenceMigrationIsIdempotent(t *testing.T) {
	doc := migrationDocument(t, migrationLegacyEdgeDocument)
	nextID, allocated := migrationCounter()
	first, err := NormalizeLegacy(doc, nil, capability.BuiltinRegistry(), nextID)
	if err != nil {
		t.Fatal(err)
	}
	firstSnapshot := migrationSnapshot(t, first.Document)
	used := *allocated
	if used == 0 {
		t.Fatal("首次规范化没有分配托管连线标识")
	}
	second, err := NormalizeLegacy(first.Document, nil, capability.BuiltinRegistry(), nextID)
	if err != nil {
		t.Fatal(err)
	}
	if migrationSnapshot(t, second.Document) != firstSnapshot || *allocated != used {
		t.Fatal("重复规范化改写了文档或重新分配了标识")
	}
	if len(second.Conflicts) != 0 {
		t.Fatalf("重复规范化产生了冲突：%+v", second.Conflicts)
	}

	conflict := migrationDocument(t, migrationConflictDocument)
	preview, err := NormalizeLegacy(conflict, map[string]string{"storyboard-row:board:shot": MigrationChoiceFields}, capability.BuiltinRegistry(), nextID)
	if err != nil {
		t.Fatal(err)
	}
	again, err := NormalizeLegacy(preview.Document, map[string]string{"storyboard-row:board:shot": MigrationChoiceFields}, capability.BuiltinRegistry(), nextID)
	if err != nil {
		t.Fatal(err)
	}
	if migrationSnapshot(t, again.Document) != migrationSnapshot(t, preview.Document) || !reflect.DeepEqual(again.Conflicts, preview.Conflicts) {
		t.Fatal("同一选择重复执行结果不一致")
	}
}

func TestReferenceMigrationUndoRestoresPreimage(t *testing.T) {
	doc := migrationDocument(t, migrationLegacyEdgeDocument)
	preimage := migrationSnapshot(t, doc)
	firstID, _ := migrationCounter()
	first, err := NormalizeLegacy(doc, nil, capability.BuiltinRegistry(), firstID)
	if err != nil {
		t.Fatal(err)
	}
	if migrationSnapshot(t, doc) != preimage {
		t.Fatal("规范化修改了调用方持有的前像")
	}
	// 前像必须仍然是可重新进入迁移入口的历史文档：无版本标记、保留旧连线与旧字段。
	restored := migrationDocument(t, preimage)
	if Enabled(restored) {
		t.Fatal("前像已被标记为规范化文档")
	}
	if migrationEdge(restored, "row-scene") == nil || migrationEdge(restored, "output-product") == nil {
		t.Fatal("前像丢失了旧连线")
	}
	if _, exists := migrationRow(t, restored, "board", "shot")["assetBindings"]; exists {
		t.Fatal("前像被写入了规范化字段")
	}
	// 恢复前像后重新进入同一入口，得到同一份规范化文档（命令层的历史撤销断言见 Task 7 命令测试）。
	replayID, _ := migrationCounter()
	replay, err := NormalizeLegacy(restored, nil, capability.BuiltinRegistry(), replayID)
	if err != nil {
		t.Fatal(err)
	}
	if migrationSnapshot(t, replay.Document) != migrationSnapshot(t, first.Document) {
		t.Fatal("恢复前像后无法重建同一份规范化文档")
	}
	for _, id := range []string{"hero", "scene", "sketch", "compare", "note", "board", "product", "frame"} {
		if index(first.Document)[id] == nil {
			t.Fatalf("规范化删除了节点：%s", id)
		}
	}
	if migrationEdge(first.Document, "visual-note") == nil {
		t.Fatal("规范化删除了不相关连线")
	}
}

// 批量表：列连线按单元素广播/多元素对齐解释到行，但不代表整行输入列表。
const migrationBatchDocument = `{
  "nodes": [
    {"id":"person","type":"image","metadata":{}},
    {"id":"garment","type":"image","metadata":{}},
    {"id":"prompt","type":"text","metadata":{"content":"提示词"}},
    {"id":"table","type":"batch-table","metadata":{"batchTable":{"operation":"try_on","referenceColumns":[{"id":"reference-1","label":"参考图 1"},{"id":"reference-2","label":"参考图 2"}],"textColumns":[{"id":"text-1","label":"提示词"}],"rows":[{"id":"batch-row-1","enabled":true,"inputNodeIds":[]},{"id":"batch-row-2","enabled":true,"inputNodeIds":[]}]}}}
  ],
  "connections": [
    {"id":"col-1","fromNodeId":"person","toNodeId":"table","toHandleId":"batch-reference:reference-1"},
    {"id":"col-2","fromNodeId":"garment","toNodeId":"table","toHandleId":"batch-reference:reference-2"},
    {"id":"col-text","fromNodeId":"prompt","toNodeId":"table","toHandleId":"batch-text:text-1"}
  ]
}`

func migrationBatchRow(t *testing.T, doc map[string]any, tableID, rowID string) map[string]any {
	t.Helper()
	node := migrationNode(t, doc, tableID)
	rows := maps(obj(meta(node)["batchTable"])["rows"])
	for _, row := range rows {
		if str(row["id"]) == rowID {
			return row
		}
	}
	t.Fatalf("批量行缺失：%s", rowID)
	return nil
}

func TestReferenceMigrationBatchColumns(t *testing.T) {
	doc := migrationDocument(t, migrationBatchDocument)
	preimage := migrationSnapshot(t, doc)
	preview, err := NormalizeLegacy(doc, nil, capability.BuiltinRegistry(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Conflicts) != 2 || preview.Conflicts[0].ScopeKey != "batch-row:table:batch-row-1" || preview.Conflicts[1].ScopeKey != "batch-row:table:batch-row-2" {
		t.Fatalf("批量行空字段与列连线应列为冲突：%+v", preview.Conflicts)
	}
	if len(preview.Conflicts[0].FieldNodeIDs) != 0 || !reflect.DeepEqual(preview.Conflicts[0].EdgeNodeIDs, []string{"garment", "person"}) {
		t.Fatalf("批量冲突没有列出两侧节点：%+v", preview.Conflicts[0])
	}
	if migrationSnapshot(t, preview.Document) != preimage {
		t.Fatal("未决冲突改写了批量文档")
	}

	applied := migrationDocument(t, migrationBatchDocument)
	got, err := NormalizeLegacy(applied, map[string]string{
		"batch-row:table:batch-row-1": MigrationChoiceConnections,
		"batch-row:table:batch-row-2": MigrationChoiceConnections,
	}, capability.BuiltinRegistry(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Conflicts) != 0 {
		t.Fatalf("选择连线后仍有批量冲突：%+v", got.Conflicts)
	}
	for _, rowID := range []string{"batch-row-1", "batch-row-2"} {
		row := migrationBatchRow(t, got.Document, "table", rowID)
		if !reflect.DeepEqual(row["inputNodeIds"], []any{"person", "garment"}) {
			t.Fatalf("批量行 %s 没有按列补全参考图：%#v", rowID, row["inputNodeIds"])
		}
		if !reflect.DeepEqual(row["textNodeIds"], []any{"prompt"}) {
			t.Fatalf("批量行 %s 没有按列补全文本输入：%#v", rowID, row["textNodeIds"])
		}
	}
	for _, legacy := range []string{"col-1", "col-2", "col-text"} {
		if migrationEdge(got.Document, legacy) != nil {
			t.Fatalf("批量列连线 %s 在接管后仍然存在", legacy)
		}
	}

	fill := migrationDocument(t, migrationBatchDocument)
	for _, rowID := range []string{"batch-row-1", "batch-row-2"} {
		row := migrationBatchRow(t, fill, "table", rowID)
		delete(row, "inputNodeIds")
		delete(row, "textNodeIds")
	}
	filled, err := NormalizeLegacy(fill, nil, capability.BuiltinRegistry(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(filled.Conflicts) != 0 {
		t.Fatalf("字段缺失时应直接补全而不是要求选择：%+v", filled.Conflicts)
	}
	row := migrationBatchRow(t, filled.Document, "table", "batch-row-1")
	if !reflect.DeepEqual(row["inputNodeIds"], []any{"person", "garment"}) || !reflect.DeepEqual(row["textNodeIds"], []any{"prompt"}) {
		t.Fatalf("批量行字段缺失时没有补全：%#v", row)
	}
}

func TestReferenceMigrationNeverGuessesFrames(t *testing.T) {
	doc := migrationDocument(t, migrationFrameDocument)
	nextID, _ := migrationCounter()
	direct, err := NormalizeLegacy(doc, nil, capability.BuiltinRegistry(), nextID)
	if err != nil {
		t.Fatal(err)
	}
	bindings := migrationDecodeBindings(t, obj(meta(migrationNode(t, direct.Document, "clip"))["generationSpec"])["referenceBindings"])
	if len(bindings) != 1 || bindings[0].NodeID != "frame" || bindings[0].Role != "reference" {
		t.Fatalf("模式未知时普通边被猜成首尾帧：%+v", bindings)
	}

	keyframe := migrationDocument(t, migrationFrameDocument)
	board := migrationNode(t, keyframe, "board")
	metadata := meta(board)
	metadata["storyboardVideoInputMode"] = "keyframe"
	board["metadata"] = metadata
	nextID, _ = migrationCounter()
	declared, err := NormalizeLegacy(keyframe, nil, capability.BuiltinRegistry(), nextID)
	if err != nil {
		t.Fatal(err)
	}
	bindings = migrationDecodeBindings(t, obj(meta(migrationNode(t, declared.Document, "clip"))["generationSpec"])["referenceBindings"])
	if len(bindings) != 1 || bindings[0].NodeID != "frame" || bindings[0].Role != "first-frame" || bindings[0].MediaType != "image" {
		t.Fatalf("已声明关键帧模式的首帧没有按字段确认：%+v", bindings)
	}
}
