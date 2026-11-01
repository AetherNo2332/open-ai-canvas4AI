package app

import (
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"

	"yingce/backend/internal/assets"
)

// cloudAgentDrawingEdit carries one drawing edit through its steps. node and
// meta alias the cloned document, so every step writes into the same clone.
type cloudAgentDrawingEdit struct {
	next                  map[string]any
	nodes                 []map[string]any
	meta                  map[string]any
	engine                string
	wrapper               map[string]any
	records               map[string]map[string]any
	ids                   []string
	shapeCount, pageCount int
}

// Each operation supplies a complete native record. Work on a clone so a late
// invalid image, binding or size limit cannot leave earlier edits in a preview.
func editCloudAgentDrawingDocument(doc map[string]any, args cloudAgentDrawingArgs) error {
	if len(args.Operations) == 0 || len(args.Operations) > 100 {
		return BadAuthRequest("绘图修改需包含 1 至 100 项")
	}
	edit, err := resolveCloudAgentDrawingEditTarget(doc, args)
	if err != nil {
		return err
	}
	if err := applyCloudAgentDrawingOps(edit.records, args.Operations); err != nil {
		return err
	}
	if len(edit.records) > 10000 {
		return BadAuthRequest("绘图记录数量超限")
	}
	snapshot := edit.wrapper["snapshot"].(map[string]any)
	edit.shapeCount, edit.pageCount = 0, 1
	edit.ids = make([]string, 0, len(edit.records))
	for id := range edit.records {
		edit.ids = append(edit.ids, id)
	}
	sort.Strings(edit.ids)
	if edit.engine == "excalidraw" {
		err = edit.rebuildExcalidrawSnapshot(snapshot)
	} else {
		err = edit.rebuildTldrawSnapshot(snapshot)
	}
	if err != nil {
		return err
	}
	if err := validateCloudAgentDrawingValue(snapshot, 0); err != nil {
		return err
	}
	return edit.commit(doc, args.NodeID)
}

// resolveCloudAgentDrawingEditTarget clones the document, finds the drawing
// node and returns its native document wrapper and records.
func resolveCloudAgentDrawingEditTarget(doc map[string]any, args cloudAgentDrawingArgs) (*cloudAgentDrawingEdit, error) {
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	var next map[string]any
	if err = json.Unmarshal(raw, &next); err != nil {
		return nil, err
	}
	nodes := creationMaps(next["nodes"])
	index := cloudAgentNodeIndex(nodes, args.NodeID)
	if index < 0 || stringValue(nodes[index]["type"]) != "drawing" {
		return nil, BadAuthRequest("目标不是当前画布绘图节点")
	}
	node := nodes[index]
	meta := cloudAgentNodeMetadata(node)
	engine := stringValue(meta["drawingEngine"])
	if engine == "" {
		engine = "excalidraw"
	}
	wrapper, _ := meta["drawingDocument"].(map[string]any)
	if wrapper == nil {
		if stringValue(meta["drawingId"]) != "" {
			return nil, BadAuthRequest("本地绘图尚未同步，请先打开并保存绘图")
		}
		if engine != "excalidraw" {
			return nil, BadAuthRequest("请先在 tldraw 编辑器同步原生空白文档")
		}
		wrapper = map[string]any{"version": 2, "engine": engine, "snapshot": map[string]any{"type": "excalidraw", "version": 2, "source": "canvas", "elements": []any{}, "appState": map[string]any{}, "files": map[string]any{}}, "revision": 0}
	}
	if args.Engine != engine || stringValue(wrapper["engine"]) != engine {
		return nil, BadAuthRequest("绘图引擎与当前文档不一致")
	}
	records, err := cloudAgentDrawingRecords(wrapper)
	if err != nil {
		return nil, err
	}
	return &cloudAgentDrawingEdit{next: next, nodes: nodes, meta: meta, engine: engine, wrapper: wrapper, records: records}, nil
}

func applyCloudAgentDrawingOps(records map[string]map[string]any, operations []cloudAgentDrawingOp) error {
	for _, op := range operations {
		if err := validateCloudAgentID(op.ID, "绘图记录 ID", 160); err != nil {
			return err
		}
		switch op.Type {
		case "remove":
			if op.Record != nil {
				return BadAuthRequest("删除绘图记录不接受 record")
			}
			if records[op.ID] == nil {
				return BadAuthRequest("绘图记录不存在")
			}
			delete(records, op.ID)
		case "upsert":
			if op.Record == nil || stringValue(op.Record["id"]) != op.ID {
				return BadAuthRequest("绘图 record.id 必须与操作 ID 一致")
			}
			if err := validateCloudAgentDrawingValue(op.Record, 0); err != nil {
				return err
			}
			records[op.ID] = op.Record
		default:
			return BadAuthRequest("绘图仅支持 upsert/remove")
		}
	}
	return nil
}

func (edit *cloudAgentDrawingEdit) rebuildExcalidrawSnapshot(snapshot map[string]any) error {
	elements := []any{}
	files := map[string]any{}
	for _, id := range cloudAgentExcalidrawOrder(snapshot, edit.records, edit.ids) {
		item := edit.records[id]
		kind := stringValue(item["type"])
		if kind == "" {
			if assets.ResourceID(stringValue(item["dataURL"])) == "" || !strings.HasPrefix(stringValue(item["mimeType"]), "image/") {
				return BadAuthRequest("绘图文件必须是已上传图片资源")
			}
			files[id] = item
			continue
		}
		if err := validateCloudAgentExcalidrawElement(item, kind, edit.records); err != nil {
			return err
		}
		pruneCloudAgentExcalidrawLinks(item, edit.records)
		elements = append(elements, item)
		if item["isDeleted"] != true {
			edit.shapeCount++
		}
	}
	snapshot["elements"], snapshot["files"] = elements, files
	return nil
}

// Preserve native z-order of retained elements; append new records in ID order.
func cloudAgentExcalidrawOrder(snapshot map[string]any, records map[string]map[string]any, ids []string) []string {
	ordered := []string{}
	seen := map[string]bool{}
	for _, item := range creationMaps(snapshot["elements"]) {
		id := stringValue(item["id"])
		if records[id] != nil {
			ordered = append(ordered, id)
			seen[id] = true
		}
	}
	for _, id := range ids {
		if !seen[id] {
			ordered = append(ordered, id)
		}
	}
	return ordered
}

func validateCloudAgentExcalidrawElement(item map[string]any, kind string, records map[string]map[string]any) error {
	switch kind {
	case "rectangle", "diamond", "ellipse", "arrow", "line", "freedraw", "text", "image", "frame", "magicframe", "embeddable", "iframe":
	default:
		return BadAuthRequest("不支持的 Excalidraw 元素类型")
	}
	if kind == "image" {
		fileID := stringValue(item["fileId"])
		file := records[fileID]
		if file == nil || assets.ResourceID(stringValue(file["dataURL"])) == "" {
			return BadAuthRequest("图片元素缺少平台文件")
		}
	}
	for _, field := range []string{"x", "y", "width", "height"} {
		if value, ok := item[field]; ok {
			n, valid := value.(float64)
			if !valid || math.Abs(n) > 1e6 || ((field == "width" || field == "height") && n < 0) {
				return BadAuthRequest("绘图位置或尺寸无效")
			}
		}
	}
	return nil
}

// pruneCloudAgentExcalidrawLinks clears container, frame, binding and bound
// element links that point at records removed in this edit.
func pruneCloudAgentExcalidrawLinks(item map[string]any, records map[string]map[string]any) {
	for _, field := range []string{"containerId", "frameId"} {
		if ref := stringValue(item[field]); ref != "" && records[ref] == nil {
			item[field] = nil
		}
	}
	for _, field := range []string{"startBinding", "endBinding"} {
		if binding, ok := item[field].(map[string]any); ok && records[stringValue(binding["elementId"])] == nil {
			item[field] = nil
		}
	}
	if bound, ok := item["boundElements"].([]any); ok {
		kept := []any{}
		for _, value := range bound {
			if b, ok := value.(map[string]any); ok && records[stringValue(b["id"])] != nil {
				kept = append(kept, b)
			}
		}
		item["boundElements"] = kept
	}
}

func (edit *cloudAgentDrawingEdit) rebuildTldrawSnapshot(snapshot map[string]any) error {
	if err := validateCloudAgentTldrawRecords(edit.records); err != nil {
		return err
	}
	content := snapshot
	if nested, ok := snapshot["document"].(map[string]any); ok {
		content = nested
	}
	store := map[string]any{}
	edit.pageCount = 0
	for _, id := range edit.ids {
		item := edit.records[id]
		if err := edit.checkTldrawRecord(id, item); err != nil {
			return err
		}
		store[id] = item
	}
	if edit.pageCount < 1 {
		return BadAuthRequest("tldraw 至少保留一个页面")
	}
	content["store"] = store
	delete(snapshot, "session")
	return nil
}

// checkTldrawRecord validates one store record and counts shapes and pages.
func (edit *cloudAgentDrawingEdit) checkTldrawRecord(id string, item map[string]any) error {
	switch stringValue(item["typeName"]) {
	case "shape":
		edit.shapeCount++
		if !strings.HasPrefix(id, "shape:") {
			return BadAuthRequest("tldraw shape ID 无效")
		}
		parent := stringValue(item["parentId"])
		if parent == "" || edit.records[parent] == nil {
			return BadAuthRequest("tldraw 形状缺少有效父页面或形状")
		}
	case "page":
		edit.pageCount++
		if !strings.HasPrefix(id, "page:") {
			return BadAuthRequest("tldraw page ID 无效")
		}
	case "asset":
		if stringValue(item["type"]) != "image" {
			return BadAuthRequest("绘图同步当前仅支持图片素材，请移除视频或书签素材")
		}
		props, _ := item["props"].(map[string]any)
		if assets.ResourceID(stringValue(props["src"])) == "" {
			return BadAuthRequest("tldraw 媒体必须是平台资源")
		}
	case "binding":
		if edit.records[stringValue(item["fromId"])] == nil || edit.records[stringValue(item["toId"])] == nil {
			return BadAuthRequest("tldraw binding 缺少目标形状")
		}
	case "document":
	default:
		return BadAuthRequest("tldraw 仅接受文档、页面、形状、素材和绑定记录")
	}
	return nil
}

// commit bumps the revision, enforces the 4 MiB limit and only then replaces
// the caller's document with the edited clone.
func (edit *cloudAgentDrawingEdit) commit(doc map[string]any, nodeID string) error {
	wrapper, meta := edit.wrapper, edit.meta
	revision := 1
	switch v := wrapper["revision"].(type) {
	case float64:
		revision = int(v) + 1
	case int:
		revision = v + 1
	}
	wrapper["revision"], wrapper["updatedAt"], wrapper["shapeCount"], wrapper["pageCount"] = revision, time.Now().UTC().Format(time.RFC3339Nano), edit.shapeCount, edit.pageCount
	bytes, err := json.Marshal(wrapper)
	if err != nil {
		return err
	}
	if len(bytes) > 4<<20 {
		return BadAuthRequest("绘图原生文档超过 4 MiB")
	}
	meta["drawingDocument"], meta["drawingEngine"], meta["drawingRevision"], meta["drawingShapeCount"], meta["drawingPageCount"] = wrapper, edit.engine, revision, edit.shapeCount, edit.pageCount
	meta["drawingUpdatedAt"] = wrapper["updatedAt"]
	if stringValue(meta["drawingId"]) == "" {
		meta["drawingId"] = nodeID + "-document"
	}
	delete(meta, "drawingPreviewUrl")
	delete(meta, "drawingPreviewStorageKey")
	delete(meta, "drawingPreviewRevision")
	edit.next["nodes"] = mapsAsAny(edit.nodes)
	for key := range doc {
		delete(doc, key)
	}
	for key, value := range edit.next {
		doc[key] = value
	}
	return nil
}
