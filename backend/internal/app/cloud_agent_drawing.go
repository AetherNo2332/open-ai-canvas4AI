package app

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strings"
	"yingce/backend/internal/assets"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
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
			if err := validateCloudAgentDrawingKey(key); err != nil {
				return err
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
		return validateCloudAgentDrawingString(v)
	}
	return nil
}

// validateCloudAgentDrawingKey rejects prototype keys and any key that looks
// like a credential (case, underscores and hyphens ignored).
func validateCloudAgentDrawingKey(key string) error {
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
	return nil
}

// validateCloudAgentDrawingString rejects inline media (data:, blob:,
// javascript:) and http(s) links carrying user info or auth query parameters.
func validateCloudAgentDrawingString(v string) error {
	v = strings.TrimSpace(v)
	lower := strings.ToLower(v)
	if strings.HasPrefix(lower, "data:") || strings.HasPrefix(lower, "blob:") || strings.HasPrefix(lower, "javascript:") {
		return BadAuthRequest("绘图媒体必须引用已上传的平台资源")
	}
	if !strings.HasPrefix(lower, "http:") && !strings.HasPrefix(lower, "https:") {
		return nil
	}
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
		cloudAgentTldrawReadGuide(result, records)
	}
	return result, nil
}

// cloudAgentTldrawReadGuide adds the supported shape types, the editing hint
// and, when the drawing has a page, a ready-to-copy rectangle record.
func cloudAgentTldrawReadGuide(result map[string]any, records map[string]map[string]any) {
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
		result["exampleRecord"] = cloudAgentTldrawExampleRecord(records, stringValue(pages[0]["id"]))
	}
}

// cloudAgentTldrawExampleRecord builds a rectangle on the page with an unused
// shape ID and an index after the page's existing shapes.
func cloudAgentTldrawExampleRecord(records map[string]map[string]any, pageID string) map[string]any {
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
	return map[string]any{
		"id": shapeID, "typeName": "shape", "type": "geo", "x": 0.0, "y": 0.0, "rotation": 0.0, "index": shapeIndex, "parentId": pageID, "isLocked": false, "opacity": 1.0, "meta": map[string]any{},
		"props": map[string]any{"geo": "rectangle", "w": 100.0, "h": 100.0, "growY": 0.0, "scale": 1.0, "color": "black", "labelColor": "black", "fill": "none", "dash": "draw", "size": "m", "font": "draw", "align": "middle", "verticalAlign": "middle", "url": "", "richText": map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph"}}}},
	}
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
