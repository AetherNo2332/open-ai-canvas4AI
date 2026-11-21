package app

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"yingce/backend/internal/canvas/layout"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// errCloudAgentSnapshotConflict 标记"画布在模型读取之后变了"这一可恢复结果。
//
// 合并说明：这个辅助函数原本定义在我们的 cloud_agent_runtime.go 里，而合并把云端 Agent
// 主链路整体取上游（上游没有布局工具，改用 cloudAgentMediaAdmissionError 表达媒体准入冲突）。
// 现在它同时服务于布局工具与三条画布写入链路的过期快照判定：画布写入不属于媒体准入，
// 用同一个 409 语义能让运行期把"画布已变化"作为工具结果交回模型（可恢复），而不是判死整轮。
var errCloudAgentSnapshotConflict = errors.New("canvas snapshot conflict")

func cloudAgentSnapshotConflictError(message string) error {
	return &AppError{Status: 409, Code: 409, Message: message, Cause: errCloudAgentSnapshotConflict}
}

func cloudAgentSnapshotConflict(err error) bool {
	return errors.Is(err, errCloudAgentSnapshotConflict)
}

// 云端 Agent 的节点整理：模型只决定"整理哪些节点 / 怎么归类"，几何一律由服务端算。
// 一次整理的上限刻意保守：坐标批量改写比内容改写更容易让用户迷失，且审批卡要能一眼看完。
const (
	cloudAgentArrangeMaxNodes  = 50
	cloudAgentArrangeMaxGroups = 12
	cloudAgentArrangeMaxGap    = 400
)

type cloudAgentArrangeGroup struct {
	Label   string   `json:"label,omitempty"`
	NodeIDs []string `json:"nodeIds"`
	Mode    string   `json:"mode,omitempty"`
}

type cloudAgentArrangeArgs struct {
	SnapshotHash string                   `json:"snapshotHash"`
	NodeIDs      []string                 `json:"nodeIds,omitempty"`
	Mode         string                   `json:"mode,omitempty"`
	Groups       []cloudAgentArrangeGroup `json:"groups,omitempty"`
	Align        string                   `json:"align,omitempty"`
	Gap          float64                  `json:"gap,omitempty"`
	DryRun       bool                     `json:"dryRun,omitempty"`
}

type cloudAgentArrangeMove struct {
	ID    string
	Title string
	From  layout.Position
	To    layout.Position
}

type cloudAgentArrangePlan struct {
	Args       cloudAgentArrangeArgs
	Canvas     *model.CanvasProject
	Document   map[string]any
	Positions  map[string]layout.Position
	Moves      []cloudAgentArrangeMove
	Skipped    []string
	BandLabels []string
	Preview    cloudAgentApprovalPreview
	BeforeHash string
	Mode       layout.Mode
}

// cloudAgentLayoutLane 把节点类型映射到整理泳道（与前端 canvasLayoutLane 的语义一致：
// 视频 / 音频 / 图片 / 文本，其余类型归文本泳道）。第二个返回值表示该类型是否参与整理。
func cloudAgentLayoutLane(nodeType string) (string, bool) {
	return layout.Lane(nodeType)
}

// cloudAgentLayoutNodes 把画布文档投影成排布输入。parentID 用于判断顶层节点。
func cloudAgentLayoutNodes(doc map[string]any) []layout.Node {
	return layout.ParseDocument(doc).Nodes()
}

// cloudAgentArrangeNodes 按 args 选出的节点集合。显式 nodeIds 会逐个校验：
// 不存在、被锁定或属于容器/隐藏批次的节点会被跳过并在回执里说明，而不是让整轮失败。
func cloudAgentArrangeScope(doc map[string]any, args cloudAgentArrangeArgs) ([]layout.Node, []string, error) {
	groups := make([][]string, 0, len(args.Groups))
	for _, group := range args.Groups {
		groups = append(groups, group.NodeIDs)
	}
	selected, skipped, err := layout.ParseDocument(doc).Select(args.NodeIDs, groups)
	if err != nil {
		var missing *layout.NodeNotFoundError
		if errors.As(err, &missing) {
			return nil, nil, cloudAgentFieldError("nodeIds", "invalid_value", err.Error())
		}
		return nil, nil, err
	}
	return selected, skipped, nil
}

func cloudAgentArrangePositions(doc map[string]any, args cloudAgentArrangeArgs, selected []layout.Node) (map[string]layout.Position, []string, layout.Mode, error) {
	mode, err := cloudAgentArrangeMode(args.Mode)
	if err != nil {
		return nil, nil, "", err
	}
	align, err := cloudAgentArrangeAlign(args.Align)
	if err != nil {
		return nil, nil, "", err
	}
	groups := make([]layout.ArrangeGroup, 0, len(args.Groups))
	selectedIDs := make(map[string]bool, len(selected))
	for _, node := range selected {
		selectedIDs[node.ID] = true
	}
	for _, group := range args.Groups {
		hasMember := false
		for _, id := range group.NodeIDs {
			if selectedIDs[strings.TrimSpace(id)] {
				hasMember = true
				break
			}
		}
		if !hasMember {
			continue
		}
		groupMode, err := cloudAgentArrangeMode(group.Mode)
		if err != nil {
			return nil, nil, "", err
		}
		groups = append(groups, layout.ArrangeGroup{Label: group.Label, NodeIDs: group.NodeIDs, Mode: groupMode})
	}
	if len(args.Groups) > 0 && len(groups) == 0 {
		return nil, nil, "", cloudAgentFieldError("groups", "invalid_value", "分组里没有可整理的节点")
	}
	result, err := layout.ParseDocument(doc).Arrange(selected, layout.ArrangeOptions{Groups: groups, Mode: mode, Align: align, Gap: args.Gap})
	if err != nil {
		if errors.Is(err, layout.ErrEmptyGroups) {
			return nil, nil, "", cloudAgentFieldError("groups", "invalid_value", err.Error())
		}
		return nil, nil, "", err
	}
	return result.Positions, result.Labels, mode, nil
}

// prepareCloudAgentArrangeNodes 复刻 canvas_apply_ops 的准备流程：校验快照、算出新坐标、生成审批预览。
// 它不写库；真正落库在 applyCloudAgentArrangeNodes（或在审批通过后的同一条执行路径里）。
func prepareCloudAgentArrangeNodes(repo *repository.Repository, userID, canvasID string, call cloudAgentCall) (*cloudAgentArrangePlan, error) {
	var args cloudAgentArrangeArgs
	if err := decodeCloudAgentJSONObject(call.Function.Arguments, &args); err != nil {
		return nil, cloudAgentJSONArgumentError(err)
	}
	if strings.TrimSpace(args.SnapshotHash) == "" {
		return nil, cloudAgentFieldError("snapshotHash", "required", "整理节点需要先读取画布并传入 snapshotHash")
	}
	if len(args.Groups) > cloudAgentArrangeMaxGroups {
		return nil, cloudAgentFieldError("groups", "item_count", fmt.Sprintf("一次最多分成 %d 组", cloudAgentArrangeMaxGroups))
	}
	if args.Gap < 0 || args.Gap > cloudAgentArrangeMaxGap {
		return nil, cloudAgentFieldError("gap", "invalid_value", fmt.Sprintf("组间距必须在 0 到 %d 之间", cloudAgentArrangeMaxGap))
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
	if beforeHash != strings.TrimSpace(args.SnapshotHash) {
		// 与 canvas_apply_ops 同一写法：过期快照既要按上游口径归类成字段错误（模型据此知道
		// 是 snapshotHash 过期），又要保留 errCloudAgentSnapshotConflict 让我方运行期走
		// "重新读取画布再申请审批"的可恢复分支，而不是被当成准入失败判死整轮。
		return nil, &cloudAgentFieldArgumentError{
			error: &cloudAgentArgumentError{cloudAgentSnapshotConflictError("画布已变化，本次未整理；请重新读取并重新申请审批")},
			Field: "snapshotHash", Issue: "stale_snapshot",
		}
	}
	selected, skipped, err := cloudAgentArrangeScope(doc, args)
	if err != nil {
		return nil, err
	}
	if len(selected) < 2 {
		return nil, cloudAgentFieldError("nodeIds", "invalid_value", "可整理的节点少于两个；单个节点请直接用 update_node 指定坐标")
	}
	if len(selected) > cloudAgentArrangeMaxNodes {
		return nil, cloudAgentFieldError("nodeIds", "item_count", fmt.Sprintf("一次最多整理 %d 个节点，请分批或用 nodeIds 指定范围", cloudAgentArrangeMaxNodes))
	}
	positions, labels, mode, err := cloudAgentArrangePositions(doc, args, selected)
	if err != nil {
		return nil, err
	}

	moves := make([]cloudAgentArrangeMove, 0, len(positions))
	for _, node := range selected {
		position, ok := positions[node.ID]
		if !ok {
			continue
		}
		if math.Abs(position.X-node.X) < 0.01 && math.Abs(position.Y-node.Y) < 0.01 {
			continue
		}
		moves = append(moves, cloudAgentArrangeMove{ID: node.ID, From: layout.Position{X: node.X, Y: node.Y}, To: position})
	}
	plan := &cloudAgentArrangePlan{
		Args: args, Canvas: canvas, Document: doc, Positions: positions,
		Skipped: skipped, BandLabels: labels, BeforeHash: beforeHash, Mode: mode,
	}
	if len(moves) == 0 {
		return plan, nil
	}
	titles := cloudAgentLayoutTitles(doc)
	for _, move := range moves {
		move.Title = titles[move.ID]
		plan.Moves = append(plan.Moves, move)
	}
	plan.Preview = cloudAgentArrangePreview(doc, plan)
	return plan, nil
}

func cloudAgentLayoutTitles(doc map[string]any) map[string]string {
	titles := map[string]string{}
	for _, node := range creationMaps(doc["nodes"]) {
		id := stringValue(node["id"])
		if id == "" {
			continue
		}
		title := strings.TrimSpace(stringValue(node["title"]))
		if title == "" {
			if capability, ok := cloudAgentNodeCapabilityForType(stringValue(node["type"])); ok {
				title = capability.Label
			}
		}
		titles[id] = truncateRunes(title, 60)
	}
	return titles
}

func cloudAgentArrangePreview(doc map[string]any, plan *cloudAgentArrangePlan) cloudAgentApprovalPreview {
	modeLabel := cloudAgentArrangeModeLabel(plan.Mode)
	headline := fmt.Sprintf("整理 %d 个节点的位置（%s）", len(plan.Moves), modeLabel)
	if len(plan.BandLabels) > 0 {
		headline = fmt.Sprintf("把 %d 个节点归入 %d 组并整理位置", len(plan.Moves), len(plan.BandLabels))
	}
	if alignLabel := cloudAgentArrangeAlignLabel(layout.AlignMode(strings.ToLower(strings.TrimSpace(plan.Args.Align)))); alignLabel != "" {
		headline += "，" + alignLabel
	}
	details := make([]string, 0, 6)
	if len(plan.BandLabels) > 0 {
		details = append(details, "分组："+strings.Join(plan.BandLabels, " / "))
	}
	details = append(details, fmt.Sprintf("移动 %d 个节点（上限 %d）", len(plan.Moves), cloudAgentArrangeMaxNodes))
	for index, move := range plan.Moves {
		if index == 6 {
			details = append(details, fmt.Sprintf("等共 %d 个节点", len(plan.Moves)))
			break
		}
		details = append(details, fmt.Sprintf("%s → (%g, %g)", move.Title, move.To.X, move.To.Y))
	}
	if len(plan.Skipped) > 0 {
		details = append(details, fmt.Sprintf("%d 个锁定/容器/隐藏节点保持不动", len(plan.Skipped)))
	}
	return cloudAgentApprovalPreview{
		Kind:        "arrange_nodes",
		Title:       "整理节点位置",
		Description: headline + "；只调整坐标，不改内容、不建连线、不改层级。",
		Items: []cloudAgentApprovalPreviewItem{{
			Operation: "arrange_nodes",
			Summary:   headline,
			Details:   details,
		}},
	}
}

// applyCloudAgentArrangeNodes 落库并记录一次可撤销的 mutation。
func applyCloudAgentArrangeNodes(repo *repository.Repository, userID, canvasID string, call cloudAgentCall, policy RuntimePolicySetting, recorder ...cloudAgentMutationRecorder) (any, error) {
	plan, err := prepareCloudAgentArrangeNodes(repo, userID, canvasID, call)
	if err != nil {
		return nil, err
	}
	if len(plan.Moves) == 0 {
		return map[string]any{
			"canvasId": canvasID, "snapshotHash": plan.BeforeHash, "moved": 0,
			"summary": "节点位置已经是整理后的结果，本次没有改动",
		}, nil
	}
	if plan.Args.DryRun {
		ids := make([]string, 0, len(plan.Moves))
		for _, move := range plan.Moves {
			ids = append(ids, move.ID)
		}
		return map[string]any{
			"canvasId": canvasID, "snapshotHash": plan.BeforeHash, "moved": 0, "wouldMove": len(ids),
			"nodeIds": ids, "summary": fmt.Sprintf("预演：将移动 %d 个节点，未写入画布", len(ids)),
		}, nil
	}
	before := plan.Canvas.PayloadJSON
	if err := cloudAgentApplyArrangePositions(plan); err != nil {
		return nil, err
	}
	if err := saveCloudAgentDocument(repo, plan.Canvas, plan.Document, policy); err != nil {
		return nil, err
	}
	if len(recorder) > 0 && recorder[0] != nil {
		if err := recorder[0](repo, cloudAgentMutationInput{
			UserID: userID, CanvasID: canvasID, StepID: call.ID, Operation: "canvas_arrange_nodes",
			BeforeSnapshotHash: plan.BeforeHash, AfterSnapshotHash: cloudAgentCanvasHash(plan.Document),
			BeforeJSON: before, Preview: &plan.Preview,
		}); err != nil {
			return nil, err
		}
	}
	ids := make([]string, 0, len(plan.Moves))
	for _, move := range plan.Moves {
		ids = append(ids, move.ID)
	}
	return map[string]any{
		"canvasId": canvasID, "snapshotHash": cloudAgentCanvasHash(plan.Document),
		"beforeSnapshotHash": plan.BeforeHash, "moved": len(plan.Moves), "nodeIds": ids,
		"groups": plan.BandLabels, "skipped": plan.Skipped,
		"summary": fmt.Sprintf("已整理 %d 个节点的位置（%s）", len(plan.Moves), cloudAgentArrangeModeLabel(plan.Mode)),
	}, nil
}

func cloudAgentApplyArrangePositions(plan *cloudAgentArrangePlan) error {
	layout.ParseDocument(plan.Document).ApplyPositions(plan.Positions)
	return nil
}

// cloudAgentArrangeAddNodePosition 给"没给坐标"的新节点找一个有序的落位：优先采用
// 模型显式给出的单个坐标轴，否则按泳道追加到同类节点之后。连线不再由 Agent 创建，
// 因此这里不再按同批连线推断相对位置。
func cloudAgentArrangeAddNodePosition(doc map[string]any, pending []layout.Node, op agentCanvasOp, hint *layout.Position) (layout.Position, bool) {
	width, height := cloudAgentAddedNodeSize(op.NodeType)
	return layout.ParseDocument(doc).PlaceNode(pending, op.NodeType, width, height, hint)
}

func findLayoutNode(nodes []layout.Node, id string) (layout.Node, bool) {
	return layout.FindNode(nodes, id)
}

func cloudAgentAddedNodeSize(nodeType string) (float64, float64) {
	if capability, ok := cloudAgentNodeCapabilityForType(nodeType); ok {
		return capability.DefaultWidth, capability.DefaultHeight
	}
	return 340, 240
}
