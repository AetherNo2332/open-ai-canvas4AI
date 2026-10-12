package app

import (
	"encoding/json"
	"fmt"
	"infinite-canvas/backend/internal/assets"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
	"math"
	"net/url"
	"sort"
	"strings"
	"time"
)

type cloudAgentDrawingOp struct {
	Type   string         `json:"type"`
	ID     string         `json:"id"`
	Record map[string]any `json:"record,omitempty"`
}
type cloudAgentDrawingArgs struct {
	SnapshotHash string                `json:"snapshotHash"`
	NodeID       string                `json:"nodeId"`
	Engine       string                `json:"engine"`
	Operations   []cloudAgentDrawingOp `json:"operations"`
}

func validateCloudAgentDrawingValue(value any, depth int) error {
	if depth > 32 {
		return BadAuthRequest("绘图记录嵌套过深")
	}
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			if key == "__proto__" || key == "constructor" || key == "prototype" {
				return BadAuthRequest("绘图记录不接受原型字段")
			}
			clean := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "_", ""), "-", ""))
			for _, part := range []string{"secret", "token", "credential", "apikey", "accesskey", "authorization", "cookie", "password", "authheaders"} {
				if strings.Contains(clean, part) {
					return BadAuthRequest("绘图记录不接受凭证字段")
				}
			}
			switch clean {
			case "__proto__", "constructor", "prototype", "apikey", "authorization", "cookie", "password", "secret", "token", "accesstoken", "refreshtoken", "headers":
				return BadAuthRequest("绘图记录不接受凭证或原型字段")
			}
			if err := validateCloudAgentDrawingValue(item, depth+1); err != nil {
				return err
			}
		}
	case []any:
		for _, item := range v {
			if err := validateCloudAgentDrawingValue(item, depth+1); err != nil {
				return err
			}
		}
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return BadAuthRequest("绘图数值无效")
		}
	case string:
		v = strings.TrimSpace(v)
		lower := strings.ToLower(v)
		if strings.HasPrefix(lower, "data:") || strings.HasPrefix(lower, "blob:") || strings.HasPrefix(lower, "javascript:") {
			return BadAuthRequest("绘图媒体必须引用已上传的平台资源")
		}
		if strings.HasPrefix(lower, "http:") || strings.HasPrefix(lower, "https:") {
			u, err := url.Parse(v)
			if err != nil || u.User != nil {
				return BadAuthRequest("绘图链接无效")
			}
			for key := range u.Query() {
				key = strings.ToLower(key)
				for _, part := range []string{"token", "signature", "credential", "secret", "password", "api_key", "apikey", "authorization"} {
					if strings.Contains(key, part) {
						return BadAuthRequest("绘图记录不接受授权链接")
					}
				}
				if key == "key" {
					return BadAuthRequest("绘图记录不接受授权链接")
				}
			}
		}
	}
	return nil
}

func cloudAgentDrawingRecords(wrapper map[string]any) (map[string]map[string]any, error) {
	result := map[string]map[string]any{}
	snapshot, _ := wrapper["snapshot"].(map[string]any)
	if snapshot == nil {
		return nil, BadAuthRequest("绘图原生文档无效")
	}
	if stringValue(wrapper["engine"]) == "excalidraw" {
		for _, item := range creationMaps(snapshot["elements"]) {
			id := stringValue(item["id"])
			if id == "" || result[id] != nil {
				return nil, BadAuthRequest("绘图元素 ID 无效或重复")
			}
			result[id] = item
		}
		files, _ := snapshot["files"].(map[string]any)
		for id, value := range files {
			item, ok := value.(map[string]any)
			if !ok || result[id] != nil || stringValue(item["id"]) != id {
				return nil, BadAuthRequest("绘图文件 ID 无效")
			}
			result[id] = item
		}
	} else if stringValue(wrapper["engine"]) == "tldraw" {
		content := snapshot
		if document, ok := snapshot["document"].(map[string]any); ok {
			content = document
		}
		store, ok := content["store"].(map[string]any)
		if !ok || content["schema"] == nil {
			return nil, BadAuthRequest("tldraw 需要先在编辑器同步原生文档及 schema")
		}
		for id, value := range store {
			item, ok := value.(map[string]any)
			if !ok || stringValue(item["id"]) != id {
				return nil, BadAuthRequest("tldraw 记录 ID 无效")
			}
			result[id] = item
		}
	} else {
		return nil, BadAuthRequest("不支持的绘图引擎")
	}
	return result, nil
}

// Each operation supplies a complete native record. Work on a clone so a late
// invalid image, binding or size limit cannot leave earlier edits in a preview.
func editCloudAgentDrawingDocument(doc map[string]any, args cloudAgentDrawingArgs) error {
	if len(args.Operations) == 0 || len(args.Operations) > 100 {
		return BadAuthRequest("绘图修改需包含 1 至 100 项")
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	var next map[string]any
	if err = json.Unmarshal(raw, &next); err != nil {
		return err
	}
	nodes := creationMaps(next["nodes"])
	index := cloudAgentNodeIndex(nodes, args.NodeID)
	if index < 0 || stringValue(nodes[index]["type"]) != "drawing" {
		return BadAuthRequest("目标不是当前画布绘图节点")
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
			return BadAuthRequest("本地绘图尚未同步，请先打开并保存绘图")
		}
		if engine != "excalidraw" {
			return BadAuthRequest("请先在 tldraw 编辑器同步原生空白文档")
		}
		wrapper = map[string]any{"version": 2, "engine": engine, "snapshot": map[string]any{"type": "excalidraw", "version": 2, "source": "canvas", "elements": []any{}, "appState": map[string]any{}, "files": map[string]any{}}, "revision": 0}
	}
	if args.Engine != engine || stringValue(wrapper["engine"]) != engine {
		return BadAuthRequest("绘图引擎与当前文档不一致")
	}
	records, err := cloudAgentDrawingRecords(wrapper)
	if err != nil {
		return err
	}
	for _, op := range args.Operations {
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
	if len(records) > 10000 {
		return BadAuthRequest("绘图记录数量超限")
	}
	snapshot := wrapper["snapshot"].(map[string]any)
	shapeCount, pageCount := 0, 1
	ids := make([]string, 0, len(records))
	for id := range records {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if engine == "excalidraw" {
		elements := []any{}
		files := map[string]any{}
		// Preserve native z-order of retained elements; append new records in ID order.
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
		for _, id := range ordered {
			item := records[id]
			kind := stringValue(item["type"])
			if kind == "" {
				if assets.ResourceID(stringValue(item["dataURL"])) == "" || !strings.HasPrefix(stringValue(item["mimeType"]), "image/") {
					return BadAuthRequest("绘图文件必须是已上传图片资源")
				}
				files[id] = item
				continue
			}
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
			elements = append(elements, item)
			if item["isDeleted"] != true {
				shapeCount++
			}
		}
		snapshot["elements"], snapshot["files"] = elements, files
	} else {
		if err := validateCloudAgentTldrawRecords(records); err != nil {
			return err
		}
		content := snapshot
		if nested, ok := snapshot["document"].(map[string]any); ok {
			content = nested
		}
		store := map[string]any{}
		pageCount = 0
		for _, id := range ids {
			item := records[id]
			kind := stringValue(item["typeName"])
			switch kind {
			case "shape":
				shapeCount++
				if !strings.HasPrefix(id, "shape:") {
					return BadAuthRequest("tldraw shape ID 无效")
				}
				parent := stringValue(item["parentId"])
				if parent == "" || records[parent] == nil {
					return BadAuthRequest("tldraw 形状缺少有效父页面或形状")
				}
			case "page":
				pageCount++
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
				if records[stringValue(item["fromId"])] == nil || records[stringValue(item["toId"])] == nil {
					return BadAuthRequest("tldraw binding 缺少目标形状")
				}
			case "document":
			default:
				return BadAuthRequest("tldraw 仅接受文档、页面、形状、素材和绑定记录")
			}
			store[id] = item
		}
		if pageCount < 1 {
			return BadAuthRequest("tldraw 至少保留一个页面")
		}
		content["store"] = store
		delete(snapshot, "session")
	}
	if err := validateCloudAgentDrawingValue(snapshot, 0); err != nil {
		return err
	}
	revision := 1
	switch v := wrapper["revision"].(type) {
	case float64:
		revision = int(v) + 1
	case int:
		revision = v + 1
	}
	wrapper["revision"], wrapper["updatedAt"], wrapper["shapeCount"], wrapper["pageCount"] = revision, time.Now().UTC().Format(time.RFC3339Nano), shapeCount, pageCount
	bytes, err := json.Marshal(wrapper)
	if err != nil {
		return err
	}
	if len(bytes) > 4<<20 {
		return BadAuthRequest("绘图原生文档超过 4 MiB")
	}
	meta["drawingDocument"], meta["drawingEngine"], meta["drawingRevision"], meta["drawingShapeCount"], meta["drawingPageCount"] = wrapper, engine, revision, shapeCount, pageCount
	meta["drawingUpdatedAt"] = wrapper["updatedAt"]
	if stringValue(meta["drawingId"]) == "" {
		meta["drawingId"] = args.NodeID + "-document"
	}
	delete(meta, "drawingPreviewUrl")
	delete(meta, "drawingPreviewStorageKey")
	delete(meta, "drawingPreviewRevision")
	next["nodes"] = mapsAsAny(nodes)
	for key := range doc {
		delete(doc, key)
	}
	for key, value := range next {
		doc[key] = value
	}
	return nil
}

func prepareCloudAgentDrawing(repo *repository.Repository, userID, canvasID string, call cloudAgentCall) (*cloudAgentCanvasMutationPlan, error) {
	var args cloudAgentDrawingArgs
	if err := decodeCloudAgentJSONObject(call.Function.Arguments, &args); err != nil {
		return nil, cloudAgentJSONArgumentError(err)
	}
	canvas, err := repo.CanvasProjectForUser(userID, canvasID)
	if err != nil {
		return nil, err
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		return nil, err
	}
	hash := cloudAgentCanvasHash(doc)
	if hash != args.SnapshotHash {
		return nil, cloudAgentSnapshotConflictError("绘图或画布已变化，请重新读取")
	}
	if err := editCloudAgentDrawingDocument(doc, args); err != nil {
		return nil, err
	}
	if err := validateCloudAgentOwnedReferences(repo, userID, doc); err != nil {
		return nil, err
	}
	preview := cloudAgentApprovalPreview{Kind: "canvas_mutation", Title: "修改绘图内容", Description: fmt.Sprintf("修改 %d 项原生绘图记录", len(args.Operations)), Items: []cloudAgentApprovalPreviewItem{{Operation: "edit_drawing", NodeID: args.NodeID, NodeType: "drawing", Summary: "修改绘图中的元素或素材"}}}
	return &cloudAgentCanvasMutationPlan{Canvas: canvas, Document: doc, BeforeJSON: canvas.PayloadJSON, BeforeSnapshotHash: hash, Preview: preview}, nil
}

func applyCloudAgentDrawing(repo *repository.Repository, userID, canvasID string, call cloudAgentCall, policy RuntimePolicySetting, recorder cloudAgentMutationRecorder) (any, error) {
	plan, err := prepareCloudAgentDrawing(repo, userID, canvasID, call)
	if err != nil {
		return nil, err
	}
	if err := saveCloudAgentDocument(repo, plan.Canvas, plan.Document, policy); err != nil {
		return nil, err
	}
	if recorder != nil {
		if err := recorder(repo, cloudAgentMutationInput{UserID: userID, CanvasID: canvasID, StepID: call.ID, Operation: call.Function.Name, BeforeJSON: plan.BeforeJSON, BeforeSnapshotHash: plan.BeforeSnapshotHash, AfterSnapshotHash: cloudAgentCanvasHash(plan.Document), Preview: &plan.Preview}); err != nil {
			return nil, err
		}
	}
	return map[string]any{"accepted": true, "nodeId": plan.Preview.Items[0].NodeID, "snapshotHash": cloudAgentCanvasHash(plan.Document), "preview": plan.Preview}, nil
}

func cloudAgentReadDrawing(repo *repository.Repository, userID, canvasID string, call cloudAgentCall) (any, error) {
	var args struct {
		NodeID string `json:"nodeId"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := decodeCloudAgentJSONObject(call.Function.Arguments, &args); err != nil {
		return nil, cloudAgentJSONArgumentError(err)
	}
	if args.Limit == 0 {
		args.Limit = 40
	}
	if args.Offset < 0 || args.Limit < 1 || args.Limit > 100 {
		return nil, BadAuthRequest("绘图分页参数无效")
	}
	canvas, err := repo.CanvasProjectForUser(userID, canvasID)
	if err != nil {
		return nil, err
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		return nil, err
	}
	nodes := creationMaps(doc["nodes"])
	index := cloudAgentNodeIndex(nodes, args.NodeID)
	if index < 0 || stringValue(nodes[index]["type"]) != "drawing" {
		return nil, BadAuthRequest("目标不是当前画布绘图节点")
	}
	wrapper, _ := cloudAgentNodeMetadata(nodes[index])["drawingDocument"].(map[string]any)
	if wrapper == nil {
		return map[string]any{"nodeId": args.NodeID, "snapshotHash": cloudAgentCanvasHash(doc), "synced": false, "records": []any{}, "totalRecords": 0, "hasMore": false, "editingHint": "本地原生绘图尚未同步，请先在绘图编辑器打开并保存一次；不能将未同步的已有绘图按空白覆盖。"}, nil
	}
	records, err := cloudAgentDrawingRecords(wrapper)
	if err != nil {
		return nil, err
	}
	if err := validateCloudAgentDrawingValue(wrapper["snapshot"], 0); err != nil {
		return nil, BadAuthRequest("绘图文档包含不安全记录，请在编辑器修复后同步")
	}
	ids := make([]string, 0, len(records))
	for id := range records {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if args.Offset > len(ids) {
		return nil, BadAuthRequest("绘图偏移超出范围")
	}
	end := min(args.Offset+args.Limit, len(ids))
	items := []any{}
	for _, id := range ids[args.Offset:end] {
		items = append(items, records[id])
	}
	result := map[string]any{"nodeId": args.NodeID, "engine": wrapper["engine"], "synced": true, "records": items, "totalRecords": len(ids), "offset": args.Offset, "nextOffset": end, "hasMore": end < len(ids), "snapshotHash": cloudAgentCanvasHash(doc)}
	if stringValue(wrapper["engine"]) == "tldraw" {
		shapeTypes := []string{}
		for shapeType := range cloudAgentTLShapeProps {
			shapeTypes = append(shapeTypes, shapeType)
		}
		sort.Strings(shapeTypes)
		result["supportedShapeTypes"] = shapeTypes
		result["editingHint"] = "upsert 必须提交完整原生记录。修改已有形状时复制读取到的完整 record 后修改所需字段；新建矩形可复制 exampleRecord，使用未占用的 shape:ID，保留所有 base 字段和 props。其他类型的 props 不相同，不要只改 type。原生图片素材只接受 image 类型的平台资源。"
		pages := []map[string]any{}
		for _, record := range records {
			if stringValue(record["typeName"]) == "page" {
				pages = append(pages, record)
			}
		}
		sort.Slice(pages, func(i, j int) bool {
			if stringValue(pages[i]["index"]) != stringValue(pages[j]["index"]) {
				return stringValue(pages[i]["index"]) < stringValue(pages[j]["index"])
			}
			return stringValue(pages[i]["id"]) < stringValue(pages[j]["id"])
		})
		if len(pages) > 0 {
			pageID := stringValue(pages[0]["id"])
			shapeID, shapeIndex := "shape:new", "a1"
			for i := 1; records[shapeID] != nil; i++ {
				shapeID = fmt.Sprintf("shape:new%d", i)
			}
			for _, record := range records {
				if stringValue(record["typeName"]) == "shape" && stringValue(record["parentId"]) == pageID && stringValue(record["index"]) >= shapeIndex {
					shapeIndex = stringValue(record["index"])
				}
			}
			shapeIndex += "V"
			result["exampleRecord"] = map[string]any{
				"id": shapeID, "typeName": "shape", "type": "geo", "x": 0.0, "y": 0.0, "rotation": 0.0, "index": shapeIndex, "parentId": pageID, "isLocked": false, "opacity": 1.0, "meta": map[string]any{},
				"props": map[string]any{"geo": "rectangle", "w": 100.0, "h": 100.0, "growY": 0.0, "scale": 1.0, "color": "black", "labelColor": "black", "fill": "none", "dash": "draw", "size": "m", "font": "draw", "align": "middle", "verticalAlign": "middle", "url": "", "richText": map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph"}}}},
			}
		}
	}
	return result, nil
}

func validateCloudAgentOwnedReferences(repo *repository.Repository, userID string, doc map[string]any) error {
	raw, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	refs, err := assets.CollectDocumentResourceReferences(string(raw))
	if err != nil {
		return err
	}
	ids := map[string]struct{}{}
	for _, ref := range refs {
		ids[ref.ResourceID] = struct{}{}
	}
	if len(ids) == 0 {
		return nil
	}
	resources, err := repo.ResourcesForUserIDs(userID, assets.SortedIDs(ids))
	if err != nil {
		return err
	}
	ready := map[string]bool{}
	byID := map[string]model.Resource{}
	for _, resource := range resources {
		ready[resource.ID] = resource.Status == model.ResourceStatusReady
		byID[resource.ID] = resource
	}
	for id := range ids {
		if !ready[id] {
			return BadAuthRequest("画布引用的素材不存在、尚未就绪或不属于当前用户")
		}
	}
	for _, ref := range refs {
		if strings.Contains(ref.Path, ".drawingDocument.") && (ref.ReferenceType == "dataURL" || ref.ReferenceType == "src") {
			resource := byID[ref.ResourceID]
			if !strings.HasPrefix(resource.MimeType, "image/") {
				return BadAuthRequest("绘图图片必须引用已上传的真实图片资源")
			}
		}
	}
	return nil
}
