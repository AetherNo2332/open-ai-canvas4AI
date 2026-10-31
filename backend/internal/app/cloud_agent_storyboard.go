package app

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"yingce/backend/internal/canvas/capability"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

const maxCloudAgentStoryboardRows = 100

var cloudAgentStoryboardTextFields = []string{
	"plotDescription", "dialogue", "videoMotionPrompt", "imageGenerationPrompt", "camera", "motion", "shotSize",
	"emotion", "lightingAndAtmosphere", "audioEffects", "narrativeIntent", "viewerPOV", "performanceBlocking",
	"timeBeats", "continuityOut", "negativePrompt",
}

type cloudAgentStoryboardCreateArgs struct {
	References       []string         `json:"references,omitempty"`
	SnapshotHash     string           `json:"snapshotHash"`
	NodeID           string           `json:"nodeId"`
	Title            string           `json:"title"`
	Rows             []map[string]any `json:"rows"`
	ReferenceNodeIDs []string         `json:"referenceNodeIds,omitempty"`
	X                *float64         `json:"x"`
	Y                *float64         `json:"y"`
}

type cloudAgentStoryboardEditArgs struct {
	OperationID        string                          `json:"operationId,omitempty"`
	StoryboardRevision string                          `json:"storyboardRevision,omitempty"`
	Updates            []cloudAgentStoryboardRowUpdate `json:"updates,omitempty"`
	Rows               []map[string]any                `json:"rows,omitempty"`
	RowIDs             []string                        `json:"rowIds,omitempty"`
	References         []string                        `json:"references,omitempty"`
	Assets             []cloudAgentStoryboardAsset     `json:"assets,omitempty"`
	SnapshotHash       string                          `json:"snapshotHash"`
	NodeID             string                          `json:"nodeId"`
	Action             string                          `json:"action"`
	RowID              string                          `json:"rowId"`
	Patch              map[string]any                  `json:"patch"`
	ReferenceNodeIDs   []string                        `json:"referenceNodeIds,omitempty"`
	OutputMode         string                          `json:"outputMode,omitempty"`
	OutputNodeID       string                          `json:"outputNodeId,omitempty"`
}

type cloudAgentStoryboardRowUpdate struct {
	RowID string         `json:"rowId"`
	Patch map[string]any `json:"patch"`
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

func cloudAgentStoryboardRowSchema() map[string]any {
	fields := capability.StoryboardRowFields()
	properties := map[string]any{}
	for _, key := range append([]string{"durationSeconds"}, cloudAgentStoryboardTextFields...) {
		properties[key] = fields[key].JSONSchema()
	}
	properties["plotDescription"].(map[string]any)["description"] = "镜头创作说明。可在同一文本中组织构图、动作、表演、节拍、光影和连续性；不必拆成多列。"
	properties["imageGenerationPrompt"].(map[string]any)["description"] = "可选静态关键帧提示词覆盖；省略时使用镜头创作说明。"
	properties["videoMotionPrompt"].(map[string]any)["description"] = "可选视频提示词覆盖；省略时使用镜头创作说明。"
	properties["assets"] = cloudAgentStoryboardAssetsSchema()
	return map[string]any{"type": "object", "properties": properties, "required": []string{"durationSeconds"}, "additionalProperties": false,
		"anyOf": []map[string]any{
			{"properties": map[string]any{"plotDescription": map[string]any{"minLength": 1}}, "required": []string{"plotDescription"}},
			{"properties": map[string]any{"videoMotionPrompt": map[string]any{"minLength": 1}}, "required": []string{"videoMotionPrompt"}},
		}}
}
func cloudAgentStoryboardPatchSchema() map[string]any {
	schema := cloudAgentStoryboardRowSchema()
	properties := schema["properties"].(map[string]any)
	delete(properties, "locked")
	delete(properties, "mediaReview")
	delete(schema, "required")
	delete(schema, "anyOf")
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
	if err := capability.ValidateEditableFields(capability.StoryboardRowFields(), row); err != nil {
		return BadAuthRequest(err.Error())
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
	if args.NodeID == "" {
		args.NodeID = cloudAgentID(userID, "storyboard:"+canvasID+":"+call.ID)
	}
	if args.SnapshotHash == "" || strings.TrimSpace(args.Title) == "" {
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
	// 创建时不带引用声明的分镜在历史画布上仍然允许；带 referenceNodeIds 或行 assetBindings
	// 才要求先完成规范化（否则服务端不会派生托管连接）。
	if cloudAgentStoryboardCreateWritesReferences(args) {
		if err := validateCloudAgentReferenceMigration(doc); err != nil {
			return nil, err
		}
	}
	for _, node := range creationMaps(doc["nodes"]) {
		if stringValue(node["id"]) == args.NodeID {
			return nil, BadAuthRequest("分镜节点ID已存在")
		}
	}
	if args.References != nil {
		if args.ReferenceNodeIDs != nil {
			return nil, BadAuthRequest("references 与 referenceNodeIds 不能同时提交")
		}
		args.ReferenceNodeIDs, err = cloudAgentResolveBusinessReferences(doc, args.References)
		if err != nil {
			return nil, err
		}
	}
	for index, row := range args.Rows {
		normalized, err := cloudAgentNormalizeStoryboardPatch(doc, row)
		if err != nil {
			return nil, err
		}
		args.Rows[index] = normalized
	}
	rows, err := normalizeCloudAgentStoryboardRows(args.Rows, userID, args.NodeID, call.ID)
	if err != nil {
		return nil, err
	}
	for _, row := range args.Rows {
		if err := validateCloudAgentStoryboardReferences(doc, row); err != nil {
			return nil, err
		}
	}
	position, _ := cloudAgentArrangeAddNodePosition(doc, nil, agentCanvasOp{ID: args.NodeID, NodeType: "script", X: args.X, Y: args.Y}, nil)
	if (args.X == nil) != (args.Y == nil) {
		return nil, BadAuthRequest("显式位置需要同时提供 x 和 y")
	}
	if args.X != nil {
		position.X, position.Y = *args.X, *args.Y
	}
	node := creationAddedNode(CreationCanvasOp{
		Type: "add_node", ID: args.NodeID, NodeType: "script", Title: args.Title, X: &position.X, Y: &position.Y,
		Metadata: map[string]any{"storyboard": map[string]any{
			"rows": rows, "visibleColumns": capability.StoryboardDefaultColumnValues(), "referenceNodeIds": []any{},
		}},
	})
	doc["nodes"] = append(creationMaps(doc["nodes"]), node)
	if args.ReferenceNodeIDs != nil {
		cloudAgentNodeMetadata(node)["storyboard"].(map[string]any)["referenceNodeIds"] = args.ReferenceNodeIDs
	}
	if err := reconcileCloudAgentReferenceFields(doc, canvas.PayloadJSON); err != nil {
		return nil, err
	}
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
	if args.Action == "update" && len(args.Updates) > 0 {
		args.Action = "batch_update"
	}
	if args.SnapshotHash == "" || args.NodeID == "" {
		return nil, BadAuthRequest("编辑分镜需要快照和节点ID")
	}
	if args.StoryboardRevision != "" && !cloudAgentStoryboardScopedEdit(args) {
		return nil, cloudAgentFieldError("storyboardRevision", "invalid_revision_scope", "表级版本仅用于纯创作字段 update/batch_update")
	}
	if cloudAgentStoryboardSharedAction(args.Action) || args.Action == "bind_output" || args.Action == "unbind_output" {
		if args.Updates != nil || args.Rows != nil {
			return nil, BadAuthRequest("引用或产物归属动作不接受 updates 或 rows")
		}
		return prepareCloudAgentStoryboardReferenceEdit(repo, userID, canvasID, args)
	}
	if args.Action != "append" && args.Action != "append_rows" && args.Action != "batch_update" && args.Action != "update" && args.Action != "remove" && !cloudAgentStoryboardAssetAction(args.Action) {
		return nil, BadAuthRequest("分镜操作无效")
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
	if err := validateCloudAgentStoryboardRevision(repo, userID, canvasID, doc, args); err != nil {
		return nil, err
	}
	if cloudAgentStoryboardEditWritesReferences(args) {
		if err := validateCloudAgentReferenceMigration(doc); err != nil {
			return nil, err
		}
	}
	node, board, rows, err := storyboardNodeFromDocument(doc, args.NodeID)
	if err != nil {
		return nil, err
	}
	mutationID := call.storyboardMutationID
	if mutationID == "" {
		// Internal callers outside the runtime have no run/step context. Scope
		// their deterministic preparation to the exact document version.
		mutationID = call.ID + ":" + args.SnapshotHash
	}
	next, fields, err := cloudAgentPlanStoryboardRows(doc, rows, args, userID, mutationID)
	if err != nil {
		return nil, err
	}
	if args.Action == "remove" {
		ids := append([]string(nil), args.RowIDs...)
		if args.RowID != "" {
			ids = append(ids, args.RowID)
		}
		for _, id := range ids {
			if err := validateCloudAgentStoryboardOccupancy(repo, userID, canvasID, args.NodeID, id, ""); err != nil {
				return nil, err
			}
		}
	}
	details := []string{}
	for _, row := range rows {
		id := stringValue(row["id"])
		selected := id == args.RowID || cloudAgentContainsString(args.RowIDs, id)
		for _, update := range args.Updates {
			selected = selected || id == update.RowID
		}
		if selected {
			details = append(details, fmt.Sprintf("镜头 %v", row["shotNumber"]))
		}
	}
	if cloudAgentStoryboardAssetAction(args.Action) {
		for _, asset := range args.Assets {
			details = append(details, truncateRunes(asset.Ref, 120)+" · "+asset.Role)
		}
		if len(args.Assets) == 0 {
			details = append(details, "清空所选镜头的资产声明与旧角色列表，共享引用保留")
		}
	}
	board["rows"] = mapsAsAny(next)
	if err := reconcileCloudAgentReferenceFields(doc, canvas.PayloadJSON); err != nil {
		return nil, err
	}
	return &cloudAgentStoryboardMutationPlan{Canvas: canvas, Document: doc, BeforeJSON: canvas.PayloadJSON, BeforeSnapshotHash: beforeHash,
		Preview: cloudAgentApprovalPreview{Kind: "canvas_mutation", Title: "确认修改分镜", Description: "整批校验并修改所选镜头的创作内容或资产引用。", Items: []cloudAgentApprovalPreviewItem{{Operation: "edit_storyboard", NodeID: args.NodeID, NodeTitle: stringValue(node["title"]), NodeType: "script", NodeTypeLabel: "分镜脚本", Fields: fields, Details: details, Summary: fmt.Sprintf("%s：%s", args.Action, stringValue(node["title"]))}}}}, nil
}
func applyCloudAgentStoryboardMutation(repo *repository.Repository, userID, canvasID string, call cloudAgentCall, policy RuntimePolicySetting, recorder ...cloudAgentMutationRecorder) (any, error) {
	var result any
	err := repo.CanvasAtomicWrite(context.Background(), userID, canvasID, func(tx *repository.Repository) error {
		var err error
		result, err = applyCloudAgentStoryboardMutationAtomic(tx, userID, canvasID, call, policy, recorder...)
		return err
	})
	return result, err
}

func applyCloudAgentStoryboardMutationAtomic(repo *repository.Repository, userID, canvasID string, call cloudAgentCall, policy RuntimePolicySetting, recorder ...cloudAgentMutationRecorder) (any, error) {
	if result, found, err := cloudAgentStoryboardReplay(repo, userID, canvasID, call); found || err != nil {
		return result, err
	}
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
	receipt, err := cloudAgentStoryboardMutationReceipt(plan, call)
	if err != nil {
		return nil, err
	}
	if err := saveCloudAgentDocument(repo, plan.Canvas, plan.Document, policy); err != nil {
		return nil, err
	}
	revision, err := cloudAgentStoryboardRevision(repo, userID, canvasID, plan.Document, stringValue(receipt["nodeId"]))
	if err != nil {
		return nil, err
	}
	receipt["storyboardRevision"] = revision
	if len(recorder) > 0 && recorder[0] != nil {
		if err := recorder[0](repo, cloudAgentMutationInput{UserID: userID, CanvasID: canvasID, StepID: call.ID, Operation: call.Function.Name, BeforeSnapshotHash: plan.BeforeSnapshotHash, AfterSnapshotHash: cloudAgentCanvasHash(plan.Document), BeforeJSON: plan.BeforeJSON, Preview: &plan.Preview}); err != nil {
			return nil, err
		}
	}
	if err := cloudAgentStoreStoryboardOperation(repo, userID, canvasID, call, receipt); err != nil {
		return nil, err
	}
	return receipt, nil
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
			if id, exists := row["id"]; exists {
				row["rowId"] = id
				delete(row, "id")
			}
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

func validateCloudAgentStoryboardReferences(doc, row map[string]any) error {
	nodes := map[string]map[string]any{}
	for _, node := range creationMaps(doc["nodes"]) {
		nodes[stringValue(node["id"])] = node
	}
	for _, character := range creationMaps(row["characters"]) {
		if strings.TrimSpace(stringValue(character["characterName"])) == "" {
			return BadAuthRequest("分镜角色名称不能为空")
		}
		if id := stringValue(character["characterImageNodeId"]); id != "" {
			node := nodes[id]
			if node == nil || (stringValue(node["type"]) != "image" && stringValue(node["type"]) != "drawing") {
				return BadAuthRequest("分镜角色图片必须引用当前画布内图片或绘图节点")
			}
		}
	}
	seen := map[string]bool{}
	for _, binding := range creationMaps(row["assetBindings"]) {
		id := stringValue(binding["nodeId"])
		node := nodes[id]
		if validateCloudAgentID(id, "资产节点ID", 80) != nil || node == nil || seen[id] {
			return BadAuthRequest("分镜资产引用必须是当前画布内唯一节点")
		}
		seen[id] = true
		kind := stringValue(node["type"])
		if kind == "drawing" {
			kind = "image"
		}
		if cloudAgentCharacterNode(node) {
			kind = "character"
		}
		role := stringValue(binding["role"])
		allowed := false
		switch role {
		case "character":
			allowed = kind == "image" || kind == "character"
		case "environment", "wardrobe", "prop", "weapon", "style":
			allowed = kind == "image"
		case "motion":
			allowed = kind == "image" || kind == "video"
		case "audio":
			allowed = kind == "audio"
		}
		if !allowed {
			return BadAuthRequest("分镜资产类型不符合引用角色")
		}
	}
	return validateCloudAgentStoryboardSourceRange(row)
}

func validateCloudAgentStoryboardSourceRange(row map[string]any) error {
	start, hasStart := row["sourceStartMs"].(float64)
	end, hasEnd := row["sourceEndMs"].(float64)
	if hasStart && hasEnd && end < start {
		return BadAuthRequest("分镜来源结束时间不能早于开始时间")
	}
	if frame, exists := row["keyframeTimeMs"].(float64); exists && ((hasStart && frame < start) || (hasEnd && frame > end)) {
		return BadAuthRequest("分镜关键帧时间必须位于来源时间范围内")
	}
	return nil
}
