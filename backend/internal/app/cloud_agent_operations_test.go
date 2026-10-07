package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func parityDocument(t *testing.T, raw string) map[string]any {
	t.Helper()
	doc, err := creationDocument(raw)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}
func parityOps(t *testing.T, doc map[string]any, ops string) error {
	t.Helper()
	args, err := decodeCloudAgentCanvasArgs(`{"snapshotHash":"test","ops":` + ops + `}`)
	if err != nil {
		return err
	}
	_, err = applyCloudAgentCanvasPlan(doc, args.Ops)
	return err
}
func TestAgentParityDeleteCleansGraphAndReferences(t *testing.T) {
	doc := parityDocument(t, `{"nodes":[{"id":"picture","type":"image","metadata":{"content":"resource:r"}},{"id":"group","type":"frame"},{"id":"child","type":"text","parentId":"group","metadata":{"content":"keep"}},{"id":"script","type":"script","metadata":{"storyboard":{"referenceNodeIds":["picture"],"rows":[{"id":"shot","assetBindings":[{"nodeId":"picture"}],"imageNodeId":"picture"}]} }},{"id":"table","type":"batch-table","metadata":{"batchTable":{"rows":[{"id":"row","inputNodeIds":["picture"],"outputNodeId":"picture"}]}}}],"connections":[{"id":"edge","fromNodeId":"picture","toNodeId":"script"}]}`)
	if err := parityOps(t, doc, `[{"type":"delete_node","id":"picture"},{"type":"delete_node","id":"group"}]`); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(doc)
	if strings.Contains(string(encoded), `"picture"`) || strings.Contains(string(encoded), `"group"`) || len(creationMaps(doc["connections"])) != 0 {
		t.Fatalf("dangling references: %s", encoded)
	}
	if len(creationMaps(doc["nodes"])) != 3 {
		t.Fatal("container children should survive")
	}
}
func TestAgentParityDuplicateClearsTaskIdentity(t *testing.T) {
	doc := parityDocument(t, `{"nodes":[{"id":"source","type":"image","title":"Original","position":{"x":10,"y":20},"metadata":{"content":"resource:ready","composerContent":"draft","taskId":"paid-task","taskStatus":"running","status":"loading","generationAppliedKeys":["effect"]}}],"connections":[]}`)
	if err := parityOps(t, doc, `[{"type":"duplicate_node","id":"copy","sourceNodeId":"source","x":50,"y":60}]`); err != nil {
		t.Fatal(err)
	}
	nodes := creationMaps(doc["nodes"])
	if len(nodes) != 2 {
		t.Fatal("missing copy")
	}
	meta := nodes[1]["metadata"].(map[string]any)
	if meta["taskId"] != nil || meta["taskStatus"] != nil || meta["generationAppliedKeys"] != nil || meta["content"] != "resource:ready" {
		t.Fatalf("invalid copied metadata: %+v", meta)
	}
}
func TestAgentParityUniqueTextReplacementAndAtomicFailure(t *testing.T) {
	text := strings.Repeat("正文", 10000) + "目标片段" + strings.Repeat("保留", 10000)
	doc := parityDocument(t, `{"nodes":[{"id":"note","type":"text","title":"Before","metadata":{"content":""}}],"connections":[]}`)
	creationMaps(doc["nodes"])[0]["metadata"].(map[string]any)["content"] = text
	if err := parityOps(t, doc, `[{"type":"replace_text","id":"note","match":"目标片段","replacement":"已修改"}]`); err != nil {
		t.Fatal(err)
	}
	if creationMaps(doc["nodes"])[0]["metadata"].(map[string]any)["content"] != strings.Replace(text, "目标片段", "已修改", 1) {
		t.Fatal("unrelated content changed")
	}
	before, _ := json.Marshal(doc)
	if err := parityOps(t, doc, `[{"type":"update_node","id":"note","patch":{"title":"After"}},{"type":"replace_text","id":"note","match":"保留","replacement":"wrong"}]`); err == nil {
		t.Fatal("ambiguous replacement accepted")
	}
	after, _ := json.Marshal(doc)
	if string(before) != string(after) {
		t.Fatal("failed batch changed original document")
	}
}
func TestAgentParityParentRejectsCycleAndLockedContentRemainsEditable(t *testing.T) {
	doc := parityDocument(t, `{"nodes":[{"id":"frame","type":"frame"},{"id":"note","type":"text","metadata":{"content":"old","locked":true}}],"connections":[]}`)
	if err := parityOps(t, doc, `[{"type":"set_parent","id":"note","parentId":"frame"},{"type":"update_node","id":"note","patch":{"content":"new"}}]`); err != nil {
		t.Fatal(err)
	}
	if err := parityOps(t, doc, `[{"type":"update_node","id":"note","patch":{"x":10}}]`); err == nil {
		t.Fatal("locked geometry mutated")
	}
	if err := parityOps(t, doc, `[{"type":"set_parent","id":"frame","parentId":"frame"}]`); err == nil {
		t.Fatal("cycle accepted")
	}
}
func TestAgentParityDisconnectAndReorderRows(t *testing.T) {
	doc := parityDocument(t, `{"nodes":[{"id":"text","type":"text","metadata":{"content":"draft"}},{"id":"image","type":"image"},{"id":"script","type":"script","metadata":{"storyboard":{"rows":[{"id":"r1"},{"id":"r2"}]}}}],"connections":[{"id":"edge","fromNodeId":"text","toNodeId":"image"}]}`)
	if err := parityOps(t, doc, `[{"type":"delete_connection","id":"edge"},{"type":"reorder_rows","id":"script","rowIds":["r2","r1"]},{"type":"reorder_nodes","id":"order","nodeIds":["script","image","text"]}]`); err != nil {
		t.Fatal(err)
	}
	nodes := creationMaps(doc["nodes"])
	if nodes[0]["id"] != "script" || len(creationMaps(doc["connections"])) != 0 {
		t.Fatal("graph order/disconnect failed")
	}
	board := nodes[0]["metadata"].(map[string]any)["storyboard"].(map[string]any)
	if creationMaps(board["rows"])[0]["id"] != "r2" {
		t.Fatal("row order failed")
	}
}

func TestAgentParityDeletionProducesBeforeAfterDelta(t *testing.T) {
 before:=[]map[string]any{{"id":"gone","type":"text"},{"id":"keep","type":"text"}}
 changes:=cloudAgentObjectChanges(before,[]map[string]any{{"id":"keep","type":"text"}})
 if len(changes)!=1 || changes[0]["after"]!=nil || changes[0]["before"].(map[string]any)["id"]!="gone" {t.Fatalf("missing deletion delta: %+v",changes)}
}
