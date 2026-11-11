package app

import (
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// canvas_build_board 把一份分镜脚本一次展开成制作看板。布局是确定性的服务端计算，
// 不让模型 20 个一批地摆坐标：同样的分镜得到同样的看板，重复调用只补缺失的占位。
const (
	cloudAgentBoardMaxAssets   = 30
	cloudAgentBoardMaxActs     = 12
	cloudAgentBoardColumns     = 6
	cloudAgentBoardAssetWidth  = 360.0
	cloudAgentBoardAssetHeight = 203.0
	cloudAgentBoardShotWidth   = 400.0
	cloudAgentBoardShotHeight  = 225.0
	cloudAgentBoardGap         = 32.0
	cloudAgentBoardFramePad    = 24.0
	cloudAgentBoardFrameHeader = 36.0
)

// 背板颜色只存语义名，前端按主题把它映射成具体颜色。
var cloudAgentBoardFrameColors = []string{"blue", "green", "violet", "amber", "rose", "teal"}

type cloudAgentBoardArgs struct {
	SnapshotHash     string `json:"snapshotHash"`
	StoryboardNodeID string `json:"storyboardNodeId"`
	Overview         string `json:"overview"`
	Assets           []struct {
		ID       string   `json:"id"`
		Category string   `json:"category"`
		Title    string   `json:"title"`
		Prompt   string   `json:"prompt"`
		RowIDs   []string `json:"rowIds"`
	} `json:"assets"`
	Keyframes *bool `json:"keyframes"`
	Videos    *bool `json:"videos"`
	Acts      []struct {
		Label  string   `json:"label"`
		RowIDs []string `json:"rowIds"`
	} `json:"acts"`
}

type cloudAgentBoardPlan struct {
	Canvas     *model.CanvasProject
	Document   map[string]any
	BeforeHash string
	Preview    cloudAgentApprovalPreview
	Counts     map[string]int
	Rows       []map[string]any
	Assets     []string
	Missing    []string
}

func cloudAgentBoardRowPrompt(row map[string]any, primary string, extras ...string) string {
	if text := strings.TrimSpace(stringValue(row[primary])); text != "" {
		return text
	}
	parts := []string{}
	for _, key := range append([]string{"plotDescription"}, extras...) {
		if text := strings.TrimSpace(stringValue(row[key])); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func prepareCloudAgentBuildBoard(repo *repository.Repository, userID, canvasID string, call cloudAgentCall) (*cloudAgentBoardPlan, error) {
	var args cloudAgentBoardArgs
	if err := decodeCloudAgentJSONObject(call.Function.Arguments, &args); err != nil {
		return nil, cloudAgentJSONArgumentError(err)
	}
	if strings.TrimSpace(args.SnapshotHash) == "" {
		return nil, cloudAgentFieldError("snapshotHash", "required", "搭建看板需要先读取画布并传入 snapshotHash")
	}
	if len(args.Assets) > cloudAgentBoardMaxAssets {
		return nil, cloudAgentFieldError("assets", "item_count", fmt.Sprintf("资产最多 %d 个", cloudAgentBoardMaxAssets))
	}
	if len(args.Acts) > cloudAgentBoardMaxActs {
		return nil, cloudAgentFieldError("acts", "item_count", fmt.Sprintf("分组最多 %d 个", cloudAgentBoardMaxActs))
	}
	if utf8.RuneCountInString(args.Overview) > 4000 {
		return nil, cloudAgentFieldError("overview", "length_exceeded", "总控说明最多 4000 字")
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
		return nil, &cloudAgentFieldArgumentError{
			error: &cloudAgentArgumentError{creationConflict("画布已变化，本次未搭建看板；请重新读取画布后再调用")},
			Field: "snapshotHash", Issue: "stale_snapshot",
		}
	}
	nodes := creationMaps(doc["nodes"])
	edges := creationMaps(doc["connections"])
	var storyboard map[string]any
	if index := cloudAgentNodeIndex(nodes, args.StoryboardNodeID); index >= 0 {
		storyboard = nodes[index]
	}
	if storyboard == nil || stringValue(storyboard["type"]) != "script" {
		return nil, cloudAgentFieldError("storyboardNodeId", "not_found", "分镜脚本节点不存在；请先用 storyboard action=split 建好分镜")
	}
	storyMeta, _ := storyboard["metadata"].(map[string]any)
	table, _ := storyMeta["storyboard"].(map[string]any)
	rows := creationMaps(table["rows"])
	if len(rows) == 0 {
		return nil, cloudAgentFieldError("storyboardNodeId", "invalid_value", "分镜脚本里还没有镜头行")
	}
	rowIndex := map[string]int{}
	for index, row := range rows {
		rowIndex[stringValue(row["id"])] = index
	}
	existing := map[string]bool{}
	for _, node := range nodes {
		existing[stringValue(node["id"])] = true
	}
	b := &cloudAgentBoardBuilder{args: args, userID: userID, canvasID: canvasID, doc: doc, nodes: nodes, edges: edges, storyboard: storyboard, rows: rows, rowIndex: rowIndex, existing: existing,
		plan: &cloudAgentBoardPlan{Canvas: canvas, Document: doc, BeforeHash: beforeHash, Counts: map[string]int{}}}
	if err := b.validateAssets(); err != nil {
		return nil, err
	}
	if err := b.validateActs(); err != nil {
		return nil, err
	}
	b.preparePlacement()
	b.addAssetColumns()
	b.prepareShotGroups()
	b.addShotGroups()
	b.prepareConnections()
	if err := b.connectRows(); err != nil {
		return nil, err
	}
	return b.finish(), nil
}

type cloudAgentBoardActGroup struct {
	label string
	rows  []int
}
type cloudAgentBoardBuilder struct {
	args                                                                                       cloudAgentBoardArgs
	userID, canvasID                                                                           string
	doc, storyboard                                                                            map[string]any
	nodes, edges, rows                                                                         []map[string]any
	rowIndex, actOfRow                                                                         map[string]int
	existing                                                                                   map[string]bool
	keyframes, videos                                                                          bool
	originX, originY, cursorX, overviewHeight, assetColumnHeight, columnX, cellHeight, cursorY float64
	plan                                                                                       *cloudAgentBoardPlan
	groups                                                                                     []cloudAgentBoardActGroup
	keyframeOfRow, videoOfRow                                                                  map[string]string
	add                                                                                        func(map[string]any)
	newNode                                                                                    func(string, string, string, float64, float64, float64, float64, map[string]any) map[string]any
	mediaMetadata                                                                              func(string, string) map[string]any
	frameNode                                                                                  func(string, string, string, float64, float64, float64, float64) map[string]any
	connect                                                                                    func(string, string, string) error
}

func (b *cloudAgentBoardBuilder) validateAssets() error {
	assetIDs := map[string]bool{}
	for index, asset := range b.args.Assets {
		field := func(name string) string { return fmt.Sprintf("assets[%d].%s", index, name) }
		if err := validateCloudAgentID(asset.ID, "资产节点 ID", 80); err != nil {
			return cloudAgentFieldError(field("id"), "invalid_value", cloudAgentSafeToolError(err))
		}
		if assetIDs[asset.ID] {
			return cloudAgentFieldError(field("id"), "duplicate", "资产节点 ID 重复")
		}
		assetIDs[asset.ID] = true
		if asset.Category != "character" && asset.Category != "environment" && asset.Category != "prop" {
			return cloudAgentFieldError(field("category"), "invalid_value", "资产类别只能是 character、environment 或 prop")
		}
		if strings.TrimSpace(asset.Title) == "" || utf8.RuneCountInString(asset.Title) > 120 {
			return cloudAgentFieldError(field("title"), "invalid_value", "资产名不能为空且不超过 120 字")
		}
		if strings.TrimSpace(asset.Prompt) == "" || utf8.RuneCountInString(asset.Prompt) > 4000 {
			return cloudAgentFieldError(field("prompt"), "invalid_value", "资产提示词不能为空且不超过 4000 字")
		}
		for position, rowID := range asset.RowIDs {
			if _, ok := b.rowIndex[rowID]; !ok {
				return cloudAgentFieldError(fmt.Sprintf("assets[%d].rowIds[%d]", index, position), "not_found", "镜头行不存在；请用 canvas_read_storyboard 返回的 rowId")
			}
		}
	}
	return nil
}

func (b *cloudAgentBoardBuilder) validateActs() error {
	b.actOfRow = map[string]int{}
	for index, act := range b.args.Acts {
		if strings.TrimSpace(act.Label) == "" || utf8.RuneCountInString(act.Label) > 60 {
			return cloudAgentFieldError(fmt.Sprintf("acts[%d].label", index), "invalid_value", "分组名不能为空且不超过 60 字")
		}
		for position, rowID := range act.RowIDs {
			if _, ok := b.rowIndex[rowID]; !ok {
				return cloudAgentFieldError(fmt.Sprintf("acts[%d].rowIds[%d]", index, position), "not_found", "镜头行不存在；请用 canvas_read_storyboard 返回的 rowId")
			}
			if _, taken := b.actOfRow[rowID]; taken {
				return cloudAgentFieldError(fmt.Sprintf("acts[%d].rowIds[%d]", index, position), "duplicate", "一个镜头行只能属于一个分组")
			}
			b.actOfRow[rowID] = index
		}
	}
	return nil
}

func (b *cloudAgentBoardBuilder) preparePlacement() {
	b.keyframes = b.args.Keyframes == nil || *b.args.Keyframes
	b.videos = b.args.Videos == nil || *b.args.Videos

	// 看板放在现有内容右侧，保证不压住用户已经摆好的节点。
	b.originX, b.originY = 80.0, 80.0
	if position, ok := b.storyboard["position"].(map[string]any); ok {
		b.originY, _ = position["y"].(float64)
	}
	for _, node := range cloudAgentLayoutNodes(b.doc) {
		width := node.Width
		if width <= 0 {
			width = 340
		}
		b.originX = math.Max(b.originX, node.X+width+200)
	}
	b.add = func(node map[string]any) {
		b.nodes = append(b.nodes, node)
		b.existing[stringValue(node["id"])] = true
	}
	b.newNode = func(id, nodeType, title string, x, y, width, height float64, metadata map[string]any) map[string]any {
		return creationAddedNode(CreationCanvasOp{Type: "add_node", ID: id, NodeType: nodeType, Title: truncateRunes(title, 200), X: &x, Y: &y, Width: &width, Height: &height, Metadata: metadata})
	}
	b.mediaMetadata = func(nodeType, prompt string) map[string]any {
		descriptor, _ := cloudAgentNodeCapabilityForType(nodeType)
		return descriptor.Metadata(prompt)
	}
	b.frameNode = func(id, title, color string, x, y, width, height float64) map[string]any {
		return b.newNode(id, "frame", title, x, y, width, height, map[string]any{"frame": map[string]any{"collapsed": false, "expandedWidth": width, "expandedHeight": height, "color": color}})
	}

	b.cursorX = b.originX
	b.overviewHeight = 0.0
	if text := strings.TrimSpace(b.args.Overview); text != "" {
		id := cloudAgentID(b.userID, fmt.Sprintf("board:%s:%s:overview", b.canvasID, b.args.StoryboardNodeID))
		if !b.existing[id] {
			descriptor, _ := cloudAgentNodeCapabilityForType("markdown")
			b.add(b.newNode(id, "markdown", "总控 · "+truncateRunes(stringValue(b.storyboard["title"]), 60), b.cursorX, b.originY, 420, 320, descriptor.Metadata(text)))
			b.plan.Counts["overview"]++
		}
		b.cursorX += 420 + 80
		b.overviewHeight = 320
	}
}

func (b *cloudAgentBoardBuilder) addAssetColumns() {
	// 资产按类别分三列，每列一个背板。
	b.assetColumnHeight = 0.0
	b.columnX = b.cursorX
	for _, asset := range b.args.Assets {
		b.plan.Assets = append(b.plan.Assets, asset.ID)
	}
	for _, category := range []struct{ key, label string }{{"character", "人物"}, {"environment", "场景"}, {"prop", "道具"}} {
		members := []int{}
		for index, asset := range b.args.Assets {
			if asset.Category == category.key && !b.existing[asset.ID] {
				members = append(members, index)
			}
		}
		if len(members) == 0 {
			continue
		}
		frameID := cloudAgentID(b.userID, fmt.Sprintf("board:%s:%s:assets:%s", b.canvasID, b.args.StoryboardNodeID, category.key))
		height := cloudAgentBoardFrameHeader + cloudAgentBoardFramePad*2 + float64(len(members))*cloudAgentBoardAssetHeight + float64(len(members)-1)*cloudAgentBoardGap
		width := cloudAgentBoardAssetWidth + cloudAgentBoardFramePad*2
		if !b.existing[frameID] {
			b.add(b.frameNode(frameID, "资产 · "+category.label, "slate", b.columnX, b.originY, width, height))
			b.plan.Counts["frames"]++
		}
		for position, index := range members {
			asset := b.args.Assets[index]
			metadata := b.mediaMetadata("image", asset.Prompt)
			metadata["assetCategory"] = asset.Category
			node := b.newNode(asset.ID, "image", asset.Title, b.columnX+cloudAgentBoardFramePad, b.originY+cloudAgentBoardFrameHeader+cloudAgentBoardFramePad+float64(position)*(cloudAgentBoardAssetHeight+cloudAgentBoardGap), cloudAgentBoardAssetWidth, cloudAgentBoardAssetHeight, metadata)
			node["parentId"] = frameID
			b.add(node)
			b.plan.Counts["assets"]++
		}
		b.assetColumnHeight = math.Max(b.assetColumnHeight, height)
		b.columnX += width + 40
	}

}

func (b *cloudAgentBoardBuilder) prepareShotGroups() {
	// 镜头矩阵：每个分组一条横向背板，背板里每格是"关键帧在上、视频在下"。
	b.groups = []cloudAgentBoardActGroup{}
	for _, act := range b.args.Acts {
		group := cloudAgentBoardActGroup{label: act.Label}
		for _, rowID := range act.RowIDs {
			group.rows = append(group.rows, b.rowIndex[rowID])
		}
		b.groups = append(b.groups, group)
	}
	leftover := cloudAgentBoardActGroup{label: "其他镜头"}
	for index, row := range b.rows {
		if _, grouped := b.actOfRow[stringValue(row["id"])]; !grouped {
			leftover.rows = append(leftover.rows, index)
		}
	}
	if len(leftover.rows) > 0 {
		if len(b.groups) == 0 {
			leftover.label = ""
		}
		b.groups = append(b.groups, leftover)
	}
	b.cellHeight = 0.0
	if b.keyframes {
		b.cellHeight += cloudAgentBoardShotHeight
	}
	if b.videos {
		if b.cellHeight > 0 {
			b.cellHeight += 16
		}
		b.cellHeight += cloudAgentBoardShotHeight
	}
	b.cursorY = b.originY + math.Max(b.overviewHeight, b.assetColumnHeight) + 160
	b.keyframeOfRow = map[string]string{}
	b.videoOfRow = map[string]string{}
	for _, row := range b.rows {
		rowID := stringValue(row["id"])
		if nodeID := stringValue(row["imageNodeId"]); b.existing[nodeID] {
			b.keyframeOfRow[rowID] = nodeID
		}
		if nodeID := stringValue(row["videoNodeId"]); b.existing[nodeID] {
			b.videoOfRow[rowID] = nodeID
		}
	}
}

func (b *cloudAgentBoardBuilder) addShotGroups() {
	if b.cellHeight > 0 {
		for groupIndex, group := range b.groups {
			framed := group.label != ""
			columns := min(cloudAgentBoardColumns, len(group.rows))
			lines := int(math.Ceil(float64(len(group.rows)) / float64(cloudAgentBoardColumns)))
			innerX, innerY := b.originX, b.cursorY
			frameID := ""
			if framed {
				frameID = cloudAgentID(b.userID, fmt.Sprintf("board:%s:%s:act:%d", b.canvasID, b.args.StoryboardNodeID, groupIndex))
				width := float64(columns)*cloudAgentBoardShotWidth + float64(columns-1)*cloudAgentBoardGap + cloudAgentBoardFramePad*2
				height := float64(lines)*b.cellHeight + float64(lines-1)*cloudAgentBoardGap*2 + cloudAgentBoardFramePad*2 + cloudAgentBoardFrameHeader
				if !b.existing[frameID] {
					b.add(b.frameNode(frameID, group.label, cloudAgentBoardFrameColors[groupIndex%len(cloudAgentBoardFrameColors)], b.originX, b.cursorY, width, height))
					b.plan.Counts["frames"]++
				}
				innerX, innerY = b.originX+cloudAgentBoardFramePad, b.cursorY+cloudAgentBoardFrameHeader+cloudAgentBoardFramePad
			}
			for position, rowPosition := range group.rows {
				row := b.rows[rowPosition]
				rowID := stringValue(row["id"])
				shot := fmt.Sprintf("S%02d", rowPosition+1)
				x := innerX + float64(position%cloudAgentBoardColumns)*(cloudAgentBoardShotWidth+cloudAgentBoardGap)
				y := innerY + float64(position/cloudAgentBoardColumns)*(b.cellHeight+cloudAgentBoardGap*2)
				if b.keyframes && stringValue(row["imageNodeId"]) == "" {
					prompt := cloudAgentBoardRowPrompt(row, "imageGenerationPrompt", "shotSize", "camera", "lightingAndAtmosphere")
					id := cloudAgentID(b.userID, fmt.Sprintf("board:%s:%s:keyframe:%s", b.canvasID, b.args.StoryboardNodeID, rowID))
					if prompt == "" {
						b.plan.Missing = append(b.plan.Missing, shot+" 关键帧")
					} else if !b.existing[id] {
						node := b.newNode(id, "image", shot+" · 关键帧", x, y, cloudAgentBoardShotWidth, cloudAgentBoardShotHeight, b.mediaMetadata("image", prompt))
						if frameID != "" {
							node["parentId"] = frameID
						}
						b.add(node)
						b.keyframeOfRow[rowID] = id
						b.plan.Counts["keyframes"]++
					}
				}
				if b.videos && stringValue(row["videoNodeId"]) == "" {
					prompt := cloudAgentBoardRowPrompt(row, "videoMotionPrompt", "camera", "motion", "performanceBlocking")
					id := cloudAgentID(b.userID, fmt.Sprintf("board:%s:%s:video:%s", b.canvasID, b.args.StoryboardNodeID, rowID))
					videoY := y
					if b.keyframes {
						videoY = y + cloudAgentBoardShotHeight + 16
					}
					if prompt == "" {
						b.plan.Missing = append(b.plan.Missing, shot+" 视频")
					} else if !b.existing[id] {
						metadata := b.mediaMetadata("video", prompt)
						if keyframeID := b.keyframeOfRow[rowID]; keyframeID != "" {
							metadata["videoStartFrameNodeId"] = keyframeID
						}
						node := b.newNode(id, "video", shot+" · 视频", x, videoY, cloudAgentBoardShotWidth, cloudAgentBoardShotHeight, metadata)
						if frameID != "" {
							node["parentId"] = frameID
						}
						b.add(node)
						b.videoOfRow[rowID] = id
						b.plan.Counts["videos"]++
					}
				}
			}
			height := float64(lines)*b.cellHeight + float64(lines-1)*cloudAgentBoardGap*2
			if framed {
				height += cloudAgentBoardFramePad*2 + cloudAgentBoardFrameHeader
			}
			b.cursorY += height + 80
		}
	}

}

func (b *cloudAgentBoardBuilder) prepareConnections() {
	b.connect = func(from, to, fromHandle string) error {
		id := cloudAgentID(b.userID, fmt.Sprintf("board-edge:%s:%s:%s", from, to, fromHandle))
		for _, edge := range b.edges {
			if stringValue(edge["id"]) == id || (stringValue(edge["fromNodeId"]) == from && stringValue(edge["toNodeId"]) == to && stringValue(edge["fromHandleId"]) == fromHandle) {
				return nil
			}
		}
		if err := validateCloudAgentCanvasConnection(b.nodes, b.edges, agentCanvasOp{FromNodeID: from, ToNodeID: to, FromHandleID: fromHandle}); err != nil {
			return err
		}
		edge := map[string]any{"id": id, "fromNodeId": from, "toNodeId": to}
		if fromHandle != "" {
			edge["fromHandleId"] = fromHandle
		}
		b.edges = append(b.edges, edge)
		b.plan.Counts["edges"]++
		attachCloudAgentConnection(b.nodes, agentCanvasOp{FromNodeID: from, ToNodeID: to, FromHandleID: fromHandle})
		return nil
	}
}

func (b *cloudAgentBoardBuilder) connectRows() error {
	for _, row := range b.rows {
		rowID := stringValue(row["id"])
		if keyframeID := b.keyframeOfRow[rowID]; keyframeID != "" {
			if err := b.connect(b.args.StoryboardNodeID, keyframeID, "row:"+rowID); err != nil {
				return err
			}
		}
		if videoID := b.videoOfRow[rowID]; videoID != "" {
			if err := b.connect(b.args.StoryboardNodeID, videoID, "row:"+rowID); err != nil {
				return err
			}
			if keyframeID := b.keyframeOfRow[rowID]; keyframeID != "" {
				if err := b.connect(keyframeID, videoID, ""); err != nil {
					return err
				}
			}
		}
	}
	for _, asset := range b.args.Assets {
		if !cloudAgentContainsString(b.plan.Assets, asset.ID) {
			continue
		}
		for _, rowID := range asset.RowIDs {
			if keyframeID := b.keyframeOfRow[rowID]; keyframeID != "" {
				if err := b.connect(asset.ID, keyframeID, ""); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (b *cloudAgentBoardBuilder) finish() *cloudAgentBoardPlan {
	b.doc["nodes"] = b.nodes
	b.doc["connections"] = b.edges
	for _, row := range b.rows {
		rowID := stringValue(row["id"])
		entry := map[string]any{"rowId": rowID}
		if id := b.keyframeOfRow[rowID]; id != "" {
			entry["keyframeNodeId"] = id
		}
		if id := b.videoOfRow[rowID]; id != "" {
			entry["videoNodeId"] = id
		}
		if len(entry) > 1 {
			b.plan.Rows = append(b.plan.Rows, entry)
		}
	}
	b.plan.Preview = cloudAgentBoardPreview(b.plan, len(b.rows))
	return b.plan
}

func cloudAgentBoardSummary(plan *cloudAgentBoardPlan) string {
	parts := []string{}
	for _, entry := range []struct{ key, label string }{{"overview", "总控"}, {"assets", "资产"}, {"keyframes", "关键帧"}, {"videos", "视频"}, {"frames", "分组"}, {"edges", "连线"}} {
		if count := plan.Counts[entry.key]; count > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", entry.label, count))
		}
	}
	if len(parts) == 0 {
		return "看板已经完整，本次没有新增内容"
	}
	return "搭建看板：" + strings.Join(parts, "、")
}

func cloudAgentBoardPreview(plan *cloudAgentBoardPlan, rows int) cloudAgentApprovalPreview {
	summary := cloudAgentBoardSummary(plan)
	details := []string{fmt.Sprintf("分镜共 %d 个镜头行", rows), "只建空白占位并写入提示词，不提交收费生成"}
	if len(plan.Missing) > 0 {
		details = append(details, "缺提示词未建："+truncateRunes(strings.Join(plan.Missing, "、"), 160))
	}
	return cloudAgentApprovalPreview{
		Kind: "build_board", Title: "搭建制作看板", Description: summary + "；按分组着色排成镜头矩阵，可整体撤销。",
		Items: []cloudAgentApprovalPreviewItem{{Operation: "build_board", Summary: summary, Details: details}},
	}
}

func cloudAgentPreviewBuildBoard(repo *repository.Repository, userID, canvasID string, call cloudAgentCall) (cloudAgentApprovalPreview, error) {
	plan, err := prepareCloudAgentBuildBoard(repo, userID, canvasID, call)
	if err != nil {
		return cloudAgentApprovalPreview{}, err
	}
	return plan.Preview, nil
}

func applyCloudAgentBuildBoard(repo *repository.Repository, userID, canvasID string, call cloudAgentCall, policy RuntimePolicySetting, recorder ...cloudAgentMutationRecorder) (any, error) {
	plan, err := prepareCloudAgentBuildBoard(repo, userID, canvasID, call)
	if err != nil {
		return nil, err
	}
	total := 0
	for _, count := range plan.Counts {
		total += count
	}
	if total == 0 {
		return map[string]any{"canvasId": canvasID, "snapshotHash": plan.BeforeHash, "summary": cloudAgentBoardSummary(plan), "rows": plan.Rows}, nil
	}
	before := plan.Canvas.PayloadJSON
	if err := saveCloudAgentDocument(repo, plan.Canvas, plan.Document, policy); err != nil {
		return nil, err
	}
	if len(recorder) > 0 && recorder[0] != nil {
		if err := recorder[0](repo, cloudAgentMutationInput{
			UserID: userID, CanvasID: canvasID, StepID: call.ID, Operation: "canvas_build_board",
			BeforeSnapshotHash: plan.BeforeHash, AfterSnapshotHash: cloudAgentCanvasHash(plan.Document),
			BeforeJSON: before, Preview: &plan.Preview,
		}); err != nil {
			return nil, err
		}
	}
	result := map[string]any{
		"canvasId": canvasID, "snapshotHash": cloudAgentCanvasHash(plan.Document), "beforeSnapshotHash": plan.BeforeHash,
		"summary": cloudAgentBoardSummary(plan), "counts": plan.Counts, "assetNodeIds": plan.Assets, "rows": plan.Rows,
		"next": "先用 canvas_preflight 体检资产与关键帧（mode=image），再用 generate_media_batch 分批生成：资产 → 关键帧 → 视频",
	}
	if len(plan.Missing) > 0 {
		result["missingPrompts"] = plan.Missing
	}
	return result, nil
}
