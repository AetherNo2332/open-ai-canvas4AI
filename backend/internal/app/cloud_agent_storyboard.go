package app

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

const maxCloudAgentStoryboardRows = 100

// maxCloudAgentStoryboardBindings 是单行允许的资产绑定数量上限（与前端关联资产列一致）。
const maxCloudAgentStoryboardBindings = 8

// cloudAgentStoryboardBindingRoles 是前端 StoryboardAssetRole 的同一份枚举。
var cloudAgentStoryboardBindingRoles = []string{"character", "environment", "wardrobe", "prop", "weapon", "style", "motion", "audio"}

func cloudAgentStoryboardBindingRoleList() []string {
	return append([]string(nil), cloudAgentStoryboardBindingRoles...)
}

var cloudAgentStoryboardTextFields = []string{
	"plotDescription", "dialogue", "videoMotionPrompt", "imageGenerationPrompt", "camera", "motion", "shotSize",
	"emotion", "lightingAndAtmosphere", "audioEffects", "narrativeIntent", "viewerPOV", "performanceBlocking",
	"timeBeats", "continuityOut", "negativePrompt",
}

type cloudAgentStoryboardCreateArgs struct {
	SnapshotHash string           `json:"snapshotHash"`
	NodeID       string           `json:"nodeId"`
	Title        string           `json:"title"`
	Rows         []map[string]any `json:"rows"`
	X            float64          `json:"x"`
	Y            float64          `json:"y"`
}

type cloudAgentStoryboardEditArgs struct {
	SnapshotHash string         `json:"snapshotHash"`
	NodeID       string         `json:"nodeId"`
	Action       string         `json:"action"`
	RowID        string         `json:"rowId"`
	Patch        map[string]any `json:"patch"`
}

type cloudAgentStoryboardMutationPlan struct {
	Canvas             *model.CanvasProject
	Document           map[string]any
	BeforeJSON         string
	BeforeSnapshotHash string
	Preview            cloudAgentApprovalPreview
}

func storyboardArgumentError(action string) error {
	return &cloudAgentArgumentError{BadAuthRequest(fmt.Sprintf("%s分镜工具参数无效：仅允许工具 schema 中声明的字段，请重新读取分镜并按结构化参数重试", action))}
}

// One shared row-field schema serves both create (rows.items) and edit (patch):
// every field is a plain string except durationSeconds. The per-field maxLength
// block used to be serialized twice per request for identical information, and
// the same 20000-rune limit is enforced by validateCloudAgentStoryboardRow, so
// the limit is now stated once in the two tool descriptions instead.
func cloudAgentStoryboardRowSchema() map[string]any {
	properties := map[string]any{"durationSeconds": map[string]any{"type": "number", "exclusiveMinimum": 0}}
	for _, field := range cloudAgentStoryboardTextFields {
		properties[field] = map[string]any{"type": "string"}
	}
	// 资产绑定是分镜行与画布素材的真实关联（前端"关联资产"列），只允许指向当前画布上
	// 已就绪的图片/视频/音频节点；角色/场景/服装等语义由 role 表达。
	properties["assetBindings"] = map[string]any{
		"type": "array", "maxItems": maxCloudAgentStoryboardBindings,
		"description": "该镜头引用的画布素材：nodeId 必须是当前画布上已就绪的图片/视频/音频节点；role 表达用途；priority 越大越靠前",
		"items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"nodeId":   map[string]any{"type": "string", "description": "当前画布上已就绪的图片/视频/音频节点ID"},
				"role":     map[string]any{"type": "string", "enum": cloudAgentStoryboardBindingRoleList()},
				"priority": map[string]any{"type": "integer", "minimum": 0},
			},
			"required": []string{"nodeId", "role"}, "additionalProperties": false,
		},
	}
	return map[string]any{"type": "object", "properties": properties, "required": []string{"durationSeconds"}, "additionalProperties": false}
}

func cloudAgentStoryboardPatchSchema() map[string]any {
	schema := cloudAgentStoryboardRowSchema()
	delete(schema, "required")
	schema["minProperties"] = 1
	return schema
}

func validateCloudAgentStoryboardRow(row map[string]any, requireDescription bool) error {
	if row == nil {
		return BadAuthRequest("分镜行不能为空")
	}
	if duration, ok := row["durationSeconds"]; !ok {
		return BadAuthRequest("每个分镜行必须有 durationSeconds")
	} else if value, ok := duration.(float64); !ok || value <= 0 {
		return BadAuthRequest("分镜时长必须是大于零的数字")
	}
	for key := range row {
		if key != "durationSeconds" && key != "assetBindings" && !cloudAgentStoryboardTextField(key) {
			return BadAuthRequest(fmt.Sprintf("不能通过分镜工具写入字段 %s", key))
		}
	}
	if value, exists := row["assetBindings"]; exists {
		if err := validateCloudAgentStoryboardBindings(value); err != nil {
			return err
		}
	}
	for _, key := range cloudAgentStoryboardTextFields {
		if value, exists := row[key]; exists {
			text, ok := value.(string)
			if !ok || utf8.RuneCountInString(text) > 20000 {
				return BadAuthRequest(fmt.Sprintf("分镜字段 %s 必须是不超过20000字的文本", key))
			}
		}
	}
	if requireDescription {
		plot := strings.TrimSpace(stringValue(row["plotDescription"]))
		motion := strings.TrimSpace(stringValue(row["videoMotionPrompt"]))
		if plot == "" && motion == "" {
			return BadAuthRequest("新增镜头需要画面描述或视频提示词")
		}
	}
	return nil
}

func normalizeCloudAgentStoryboardRows(rows []map[string]any, userID, nodeID, seed string) ([]any, error) {
	if len(rows) < 1 || len(rows) > maxCloudAgentStoryboardRows {
		return nil, BadAuthRequest("分镜脚本必须包含 1 到 100 个镜头")
	}
	out := make([]any, 0, len(rows))
	for index, input := range rows {
		if err := validateCloudAgentStoryboardRow(input, true); err != nil {
			return nil, err
		}
		row := cloudAgentStoryboardRowDefaults()
		for key, value := range input {
			row[key] = value
		}
		row["id"] = cloudAgentID(userID, fmt.Sprintf("storyboard:%s:%s:%d", nodeID, seed, index+1))
		row["shotNumber"] = float64(index + 1)
		out = append(out, row)
	}
	return out, nil
}

func storyboardNodeFromDocument(doc map[string]any, nodeID string) (map[string]any, map[string]any, []map[string]any, error) {
	for _, node := range creationMaps(doc["nodes"]) {
		if stringValue(node["id"]) != nodeID {
			continue
		}
		if stringValue(node["type"]) != "script" {
			return nil, nil, nil, BadAuthRequest("目标节点不是分镜脚本节点")
		}
		metadata, ok := node["metadata"].(map[string]any)
		if !ok {
			return nil, nil, nil, BadAuthRequest("分镜节点数据格式无效")
		}
		storyboard, ok := metadata["storyboard"].(map[string]any)
		if !ok {
			return nil, nil, nil, BadAuthRequest("分镜节点缺少结构化表格")
		}
		rows := creationMaps(storyboard["rows"])
		if len(rows) > maxCloudAgentStoryboardRows {
			return nil, nil, nil, BadAuthRequest("分镜脚本超过100个镜头限制")
		}
		ids := map[string]bool{}
		for _, row := range rows {
			rowID := stringValue(row["id"])
			if err := validateCloudAgentID(rowID, "分镜行ID", 120); err != nil || ids[rowID] {
				return nil, nil, nil, BadAuthRequest("分镜节点包含无效或重复的镜头行ID")
			}
			ids[rowID] = true
		}
		return node, storyboard, rows, nil
	}
	return nil, nil, nil, BadAuthRequest("未找到当前画布的分镜脚本节点")
}

func prepareCloudAgentStoryboardCreate(repo *repository.Repository, userID, canvasID string, call cloudAgentCall) (*cloudAgentStoryboardMutationPlan, error) {
	var args cloudAgentStoryboardCreateArgs
	if err := decodeCloudAgentJSONObject(call.Function.Arguments, &args); err != nil {
		return nil, storyboardArgumentError("创建")
	}
	if args.SnapshotHash == "" || args.NodeID == "" || strings.TrimSpace(args.Title) == "" {
		return nil, BadAuthRequest("创建分镜脚本需要快照、节点ID和标题")
	}
	if err := validateCloudAgentID(args.NodeID, "分镜节点ID", 80); err != nil {
		return nil, err
	}
	if utf8.RuneCountInString(args.Title) > 240 {
		return nil, BadAuthRequest("分镜标题超出限制")
	}
	canvas, err := repo.CanvasProjectForUser(userID, canvasID)
	if err != nil {
		return nil, err
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		return nil, err
	}
	beforeHash := cloudAgentCanvasHash(doc)
	if beforeHash != args.SnapshotHash {
		return nil, cloudAgentSnapshotConflictError("画布已变化，本次未写入；请重新读取并重新申请审批")
	}
	for _, node := range creationMaps(doc["nodes"]) {
		if stringValue(node["id"]) == args.NodeID {
			return nil, BadAuthRequest("分镜节点ID已存在")
		}
	}
	rows, err := normalizeCloudAgentStoryboardRows(args.Rows, userID, args.NodeID, call.ID)
	if err != nil {
		return nil, err
	}
	node := creationAddedNode(CreationCanvasOp{
		Type: "add_node", ID: args.NodeID, NodeType: "script", Title: args.Title, X: &args.X, Y: &args.Y,
		Metadata: map[string]any{"storyboard": map[string]any{
			"rows": rows, "visibleColumns": []any{"shotNumber", "durationSeconds", "videoMotionPrompt", "dialogue", "assets"}, "referenceNodeIds": []any{},
		}},
	})
	if err := cloudAgentStoryboardBindingsInDocAny(doc, rows); err != nil {
		return nil, err
	}
	doc["nodes"] = append(creationMaps(doc["nodes"]), node)
	return &cloudAgentStoryboardMutationPlan{
		Canvas: canvas, Document: doc, BeforeJSON: canvas.PayloadJSON, BeforeSnapshotHash: beforeHash,
		Preview: cloudAgentApprovalPreview{Kind: "canvas_mutation", Title: "确认创建分镜脚本", Description: fmt.Sprintf("Agent 准备创建分镜脚本《%s》，包含 %d 个镜头。批准后才会写入画布。", args.Title, len(rows)), Items: []cloudAgentApprovalPreviewItem{{Operation: "create_storyboard", NodeID: args.NodeID, NodeTitle: args.Title, NodeType: "script", NodeTypeLabel: "分镜脚本", Summary: fmt.Sprintf("创建分镜脚本《%s》（%d个镜头）", args.Title, len(rows))}}},
	}, nil
}

func prepareCloudAgentStoryboardEdit(repo *repository.Repository, userID, canvasID string, call cloudAgentCall) (*cloudAgentStoryboardMutationPlan, error) {
	var args cloudAgentStoryboardEditArgs
	if err := decodeCloudAgentJSONObject(call.Function.Arguments, &args); err != nil {
		return nil, storyboardArgumentError("编辑")
	}
	if args.SnapshotHash == "" || args.NodeID == "" {
		return nil, BadAuthRequest("编辑分镜需要快照和节点ID")
	}
	if args.Action != "append" && args.Action != "update" && args.Action != "remove" {
		return nil, BadAuthRequest("分镜操作必须是 append、update 或 remove")
	}
	if err := validateCloudAgentID(args.NodeID, "分镜节点ID", 80); err != nil {
		return nil, err
	}
	if args.Action == "append" && args.RowID != "" {
		return nil, BadAuthRequest("追加镜头不能指定已有 rowId")
	}
	if args.Action != "append" {
		if err := validateCloudAgentID(args.RowID, "分镜行ID", 120); err != nil {
			return nil, BadAuthRequest("修改或删除镜头必须使用最新读取结果中的真实 rowId")
		}
	}
	if args.Action == "remove" && len(args.Patch) != 0 {
		return nil, BadAuthRequest("删除镜头不接受 patch")
	}
	canvas, err := repo.CanvasProjectForUser(userID, canvasID)
	if err != nil {
		return nil, err
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		return nil, err
	}
	beforeHash := cloudAgentCanvasHash(doc)
	if beforeHash != args.SnapshotHash {
		return nil, cloudAgentSnapshotConflictError("画布已变化，本次未写入；请重新读取并重新申请审批")
	}
	node, storyboard, rows, err := storyboardNodeFromDocument(doc, args.NodeID)
	if err != nil {
		return nil, err
	}
	index := -1
	for i, row := range rows {
		if stringValue(row["id"]) == args.RowID {
			index = i
			break
		}
	}
	if args.Action != "append" && index < 0 {
		return nil, BadAuthRequest("分镜行不存在，请先读取真实行ID")
	}
	next := make([]map[string]any, len(rows))
	for i, row := range rows {
		next[i] = row
	}
	fields := []string{}
	switch args.Action {
	case "remove":
		next = append(next[:index], next[index+1:]...)
		fields = []string{"镜头行"}
	case "append", "update":
		if len(args.Patch) == 0 {
			return nil, BadAuthRequest("分镜操作需要 patch")
		}
		patch := map[string]any{}
		for key, value := range args.Patch {
			if key == "durationSeconds" {
				if duration, ok := value.(float64); !ok || duration <= 0 {
					return nil, BadAuthRequest("分镜时长必须是大于零的数字")
				}
			} else if cloudAgentStoryboardTextField(key) {
				text, ok := value.(string)
				if !ok || utf8.RuneCountInString(text) > 20000 {
					return nil, BadAuthRequest(fmt.Sprintf("分镜字段 %s 必须是不超过20000字的文本", key))
				}
			} else if key == "assetBindings" {
				if err := validateCloudAgentStoryboardBindings(value); err != nil {
					return nil, err
				}
			} else {
				return nil, BadAuthRequest(fmt.Sprintf("不能通过分镜编辑修改字段 %s", key))
			}
			patch[key] = value
			fields = append(fields, key)
		}
		sort.Strings(fields)
		if args.Action == "append" {
			if len(rows) >= maxCloudAgentStoryboardRows {
				return nil, BadAuthRequest("单个分镜表最多100个镜头")
			}
			if err := validateCloudAgentStoryboardRow(patch, true); err != nil {
				return nil, err
			}
			row := cloudAgentStoryboardRowDefaults()
			for key, value := range patch {
				row[key] = value
			}
			row["id"] = cloudAgentID(userID, fmt.Sprintf("storyboard:%s:%s:%d", args.NodeID, call.ID, len(rows)+1))
			row["shotNumber"] = float64(len(rows) + 1)
			next = append(next, row)
		} else {
			for key, value := range patch {
				next[index][key] = value
			}
		}
	}
	for i, row := range next {
		row["shotNumber"] = float64(i + 1)
	}
	if err := cloudAgentStoryboardBindingsInDoc(doc, next); err != nil {
		return nil, err
	}
	storyboard["rows"] = mapsAsAny(next)
	node["metadata"].(map[string]any)["storyboard"] = storyboard
	return &cloudAgentStoryboardMutationPlan{
		Canvas: canvas, Document: doc, BeforeJSON: canvas.PayloadJSON, BeforeSnapshotHash: beforeHash,
		Preview: cloudAgentApprovalPreview{Kind: "canvas_mutation", Title: "确认修改分镜脚本", Description: "Agent 准备修改分镜脚本。批准后才会写入画布。", Items: []cloudAgentApprovalPreviewItem{{Operation: "edit_storyboard", NodeID: args.NodeID, NodeTitle: stringValue(node["title"]), NodeType: "script", NodeTypeLabel: "分镜脚本", Fields: fields, Summary: fmt.Sprintf("%s分镜脚本《%s》", map[string]string{"append": "追加镜头到", "update": "修改", "remove": "删除"}[args.Action], stringValue(node["title"]))}}},
	}, nil
}

func applyCloudAgentStoryboardMutation(repo *repository.Repository, userID, canvasID string, call cloudAgentCall, policy RuntimePolicySetting, recorder ...cloudAgentMutationRecorder) (any, error) {
	var plan *cloudAgentStoryboardMutationPlan
	var err error
	if call.Function.Name == "canvas_create_storyboard" {
		plan, err = prepareCloudAgentStoryboardCreate(repo, userID, canvasID, call)
	} else {
		plan, err = prepareCloudAgentStoryboardEdit(repo, userID, canvasID, call)
	}
	if err != nil {
		return nil, err
	}
	if err := saveCloudAgentDocument(repo, plan.Canvas, plan.Document, policy); err != nil {
		return nil, err
	}
	if len(recorder) > 0 && recorder[0] != nil {
		if err := recorder[0](repo, cloudAgentMutationInput{UserID: userID, CanvasID: canvasID, StepID: call.ID, Operation: call.Function.Name, BeforeSnapshotHash: plan.BeforeSnapshotHash, AfterSnapshotHash: cloudAgentCanvasHash(plan.Document), BeforeJSON: plan.BeforeJSON, Preview: &plan.Preview}); err != nil {
			return nil, err
		}
	}
	nodeID := ""
	if len(plan.Preview.Items) > 0 {
		nodeID = plan.Preview.Items[0].NodeID
	}
	return map[string]any{"canvasId": canvasID, "nodeId": nodeID, "snapshotHash": cloudAgentCanvasHash(plan.Document), "beforeSnapshotHash": plan.BeforeSnapshotHash, "summary": plan.Preview.Description, "preview": plan.Preview}, nil
}

func mapsAsAny(values []map[string]any) []any {
	out := make([]any, len(values))
	for i, value := range values {
		out[i] = value
	}
	return out
}

func cloudAgentStoryboardReadResult(view any, nodeID string) (map[string]any, error) {
	state, ok := view.(map[string]any)
	if !ok {
		return nil, BadAuthRequest("分镜读取结果无效")
	}
	nodes, _ := state["nodes"].([]any)
	if len(nodes) != 1 {
		return nil, BadAuthRequest("分镜读取结果缺少目标节点")
	}
	node, ok := nodes[0].(map[string]any)
	if !ok {
		return nil, BadAuthRequest("分镜读取结果格式无效")
	}
	storyboard, ok := node["storyboard"].(map[string]any)
	if !ok {
		return nil, BadAuthRequest("分镜读取结果缺少镜头行")
	}
	rows, _ := storyboard["rows"].([]any)
	for _, value := range rows {
		if row, ok := value.(map[string]any); ok {
			// One name per value: the row ID is published as rowId only.
			row["rowId"] = row["id"]
			delete(row, "id")
		}
	}
	return map[string]any{
		"nodeId":       nodeID,
		"title":        node["title"],
		"snapshotHash": state["snapshotHash"],
		"storyboard":   storyboard,
	}, nil
}

func cloudAgentStoryboardTextField(target string) bool {
	for _, field := range cloudAgentStoryboardTextFields {
		if field == target {
			return true
		}
	}
	return false
}

func cloudAgentStoryboardRowDefaults() map[string]any {
	row := map[string]any{
		"characters":      []any{},
		"mustHave":        []any{},
		"optionalDetails": []any{},
		"assetBindings":   []any{},
		"status":          "idle",
	}
	for _, field := range cloudAgentStoryboardTextFields {
		row[field] = ""
	}
	return row
}

// validateCloudAgentStoryboardBindings 校验资产绑定的形状（数量、字段、role 枚举、priority）。
func validateCloudAgentStoryboardBindings(value any) error {
	items, ok := value.([]any)
	if !ok {
		return BadAuthRequest("assetBindings 必须是数组（每项含 nodeId 与 role）")
	}
	if len(items) > maxCloudAgentStoryboardBindings {
		return BadAuthRequest(fmt.Sprintf("单行最多关联 %d 个素材", maxCloudAgentStoryboardBindings))
	}
	seen := map[string]bool{}
	for _, item := range items {
		binding, ok := item.(map[string]any)
		if !ok {
			return BadAuthRequest("assetBindings 每项必须是 {nodeId, role, priority} 对象")
		}
		nodeID := strings.TrimSpace(stringValue(binding["nodeId"]))
		if nodeID == "" {
			return BadAuthRequest("assetBindings 每项都需要 nodeId")
		}
		if seen[nodeID] {
			return BadAuthRequest("同一行不能重复关联同一个素材节点")
		}
		seen[nodeID] = true
		if role := strings.TrimSpace(stringValue(binding["role"])); !cloudAgentContainsString(cloudAgentStoryboardBindingRoles, role) {
			return BadAuthRequest("assetBindings 的 role 只能是 character、environment、wardrobe、prop、weapon、style、motion 或 audio")
		}
		if priority, exists := binding["priority"]; exists {
			if _, ok := cloudAgentSafeNumber(priority); !ok {
				return BadAuthRequest("assetBindings 的 priority 必须是非负整数")
			}
		}
	}
	return nil
}

// cloudAgentStoryboardBindingsInDoc 校验绑定指向的节点确实在当前画布上，且是可用于引用的媒体节点。
// 与 generate_media 的参考素材同口径：只接已就绪的图片/视频/音频节点，不接受任意 URL 或文本节点。
func cloudAgentStoryboardBindingsInDoc(doc map[string]any, rows []map[string]any) error {
	return cloudAgentStoryboardBindingsInDocAny(doc, mapsAsAny(rows))
}

func cloudAgentStoryboardBindingsInDocAny(doc map[string]any, rows []any) error {
	byID := map[string]map[string]any{}
	for _, node := range creationMaps(doc["nodes"]) {
		byID[stringValue(node["id"])] = node
	}
	for _, item := range creationMaps(rows) {
		items, _ := item["assetBindings"].([]any)
		for _, item := range items {
			binding, _ := item.(map[string]any)
			nodeID := strings.TrimSpace(stringValue(binding["nodeId"]))
			if nodeID == "" {
				continue
			}
			node := byID[nodeID]
			if node == nil {
				return BadAuthRequest(fmt.Sprintf("关联素材 %s 不在当前画布上，请先用 canvas_get_state 读取真实节点ID", nodeID))
			}
			nodeType := stringValue(node["type"])
			if nodeType != "image" && nodeType != "video" && nodeType != "audio" {
				return BadAuthRequest(fmt.Sprintf("节点 %s 是 %s，只能关联图片/视频/音频节点", nodeID, nodeType))
			}
		}
	}
	return nil
}
