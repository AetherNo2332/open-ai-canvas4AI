package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"yingce/backend/internal/canvas/capability"
	"yingce/backend/internal/canvas/document"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

const (
	canvasCommandMaxTitleRunes = 240
	canvasCommandMaxBodyRunes  = 16000
	canvasCommandCopyOffset    = 36.0
)

type cloudAgentCanvasCommandPlan struct {
	Canvas             *model.CanvasProject
	Document           map[string]any
	BeforeJSON         string
	BeforeSnapshotHash string
	Preview            cloudAgentApprovalPreview
	Steps              []map[string]any
	Output             string
	WriteCount         int
}

// cloudAgentCanvasCommandHasWrites 报告一条 canvas 调用是否含写命令。解析失败按写处理（fail closed）。
func cloudAgentCanvasCommandHasWrites(call cloudAgentCall) bool {
	var args struct {
		Command      string `json:"command"`
		SnapshotHash string `json:"snapshotHash"`
	}
	if decodeCloudAgentJSONObject(call.Function.Arguments, &args) != nil {
		return true
	}
	steps, err := canvasCommandCompile(args.Command)
	if err != nil {
		return true
	}
	for _, step := range steps {
		if step.sensitive() {
			return true
		}
	}
	return false
}

func cloudAgentCanvasCommandDestructive(call cloudAgentCall) bool {
	var args struct {
		Command      string `json:"command"`
		SnapshotHash string `json:"snapshotHash"`
	}
	if decodeCloudAgentJSONObject(call.Function.Arguments, &args) != nil {
		return false
	}
	steps, err := canvasCommandCompile(args.Command)
	if err != nil {
		trimmed := strings.TrimSpace(args.Command)
		return strings.HasPrefix(trimmed, "rm ") || strings.HasPrefix(trimmed, "rm	")
	}
	for _, step := range steps {
		if step.destructive() {
			return true
		}
	}
	return false
}

func cloudAgentCanvasCommandReadOnlyError(call cloudAgentCall) error {
	var args struct {
		Command      string `json:"command"`
		SnapshotHash string `json:"snapshotHash"`
	}
	_ = decodeCloudAgentJSONObject(call.Function.Arguments, &args)
	name := "写命令"
	if tokens, err := canvasCommandTokens(strings.TrimSpace(args.Command)); err == nil && len(tokens) > 0 {
		name = tokens[0]
	}
	reads := make([]string, 0, len(canvasCommandSpecs))
	for _, spec := range canvasCommandSpecs {
		if !spec.Sensitive {
			reads = append(reads, spec.Name)
		}
	}
	return canvasCommandError("command", "permission_denied", fmt.Sprintf("只读会话不能执行 %s：%s会修改画布或用户数据。当前可用的只读命令：%s。要写画布请让用户把权限模式切到 request_approval 或 auto", name, canvasCommandAction(name), strings.Join(reads, "、")))
}

func prepareCloudAgentCanvasCommandPlan(repo *repository.Repository, userID, canvasID string, call cloudAgentCall) (*cloudAgentCanvasCommandPlan, error) {
	var args struct {
		Command      string `json:"command"`
		SnapshotHash string `json:"snapshotHash"`
	}
	if err := decodeCloudAgentJSONObject(call.Function.Arguments, &args); err != nil {
		return nil, cloudAgentJSONArgumentError(err)
	}
	steps, err := canvasCommandCompile(args.Command)
	if err != nil {
		return nil, err
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
	if args.SnapshotHash != "" && args.SnapshotHash != beforeHash {
		return nil, cloudAgentFieldError("snapshotHash", "stale_snapshot", "画布已变化，本次未写入；请重新读取并核对目标字段")
	}
	working, err := canvasCommandCloneDocument(doc)
	if err != nil {
		return nil, err
	}
	counters := canvasCommandLoadCounters(working)
	plan := &cloudAgentCanvasCommandPlan{
		Canvas: canvas, Document: working, BeforeJSON: canvas.PayloadJSON, BeforeSnapshotHash: beforeHash,
	}
	items := make([]cloudAgentApprovalPreviewItem, 0, len(steps))
	outputs := make([]string, 0, len(steps))
	for _, step := range steps {
		fs := newCanvasCommandFS(canvasID, working, canvas.Revision)
		if step.sensitive() {
			item, output, err := canvasCommandExecWrite(repo, userID, canvasID, working, counters, fs, step)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
			outputs = append(outputs, output)
			plan.WriteCount++
			plan.Steps = append(plan.Steps, map[string]any{"command": step.Name, "output": output, "write": true})
			continue
		}
		output, err := canvasCommandExecRead(repo, userID, nil, fs, step, nil)
		if err != nil {
			return nil, err
		}
		outputs = append(outputs, output)
		plan.Steps = append(plan.Steps, map[string]any{"command": step.Name, "output": output, "write": false})
	}
	if plan.WriteCount == 0 {
		return nil, canvasCommandError("command", "invalid_value", "这条命令没有写操作，不能走写管道；这是服务端分派缺陷，请报告")
	}
	canvasCommandStoreCounters(working, counters)
	plan.Output = strings.Join(outputs, "\n")
	plan.Preview = cloudAgentCanvasCommandPreview(items)
	return plan, nil
}

func cloudAgentCanvasCommandPreview(items []cloudAgentApprovalPreviewItem) cloudAgentApprovalPreview {
	counts := map[string]int{}
	for _, item := range items {
		counts[item.Operation]++
	}
	parts := make([]string, 0, 4)
	for _, entry := range []struct {
		operation string
		label     string
	}{
		{"new", "新建节点"}, {"dup", "复制节点"}, {"update", "重命名节点"},
		{"delete", "删除节点"}, {"edit", "写字段"}, {"sed", "替换正文"},
	} {
		if counts[entry.operation] > 0 {
			parts = append(parts, fmt.Sprintf("%s %d 项", entry.label, counts[entry.operation]))
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "修改画布")
	}
	return cloudAgentApprovalPreview{
		Kind: "canvas_mutation", Title: "确认画布命令写入",
		Description: fmt.Sprintf("Agent 准备%s。请确认目标节点和修改字段；批准后才会写入画布。", strings.Join(parts, "，")),
		Items:       items,
	}
}

// resolveCloudAgentCanvasCommandApproval 在审批前把 snapshotHash 固化进调用参数，
// 保证审批预览、批准执行与检查点重放使用同一份命令和同一张快照。
func resolveCloudAgentCanvasCommandApproval(repo *repository.Repository, userID, canvasID string, call cloudAgentCall) (cloudAgentCall, cloudAgentApprovalPreview, error) {
	var args map[string]any
	if err := decodeCloudAgentJSONObject(call.Function.Arguments, &args); err != nil {
		return call, cloudAgentApprovalPreview{}, cloudAgentJSONArgumentError(err)
	}
	if stringValue(args["snapshotHash"]) == "" {
		canvas, err := repo.CanvasProjectForUser(userID, canvasID)
		if err != nil {
			return call, cloudAgentApprovalPreview{}, err
		}
		doc, err := creationDocument(canvas.PayloadJSON)
		if err != nil {
			return call, cloudAgentApprovalPreview{}, err
		}
		args["snapshotHash"] = cloudAgentCanvasHash(doc)
		encoded, err := json.Marshal(args)
		if err != nil {
			return call, cloudAgentApprovalPreview{}, err
		}
		call.Function.Arguments = string(encoded)
	}
	plan, err := prepareCloudAgentCanvasCommandPlan(repo, userID, canvasID, call)
	if err != nil {
		return call, cloudAgentApprovalPreview{}, err
	}
	return call, plan.Preview, nil
}

func applyCloudAgentCanvasCommand(repo *repository.Repository, userID, canvasID string, call cloudAgentCall, policy RuntimePolicySetting, recorder ...cloudAgentMutationRecorder) (any, error) {
	plan, err := prepareCloudAgentCanvasCommandPlan(repo, userID, canvasID, call)
	if err != nil {
		return nil, err
	}
	if err := saveCloudAgentDocument(repo, plan.Canvas, plan.Document, policy); err != nil {
		return nil, err
	}
	afterHash := cloudAgentCanvasHash(plan.Document)
	if len(recorder) > 0 && recorder[0] != nil {
		if err := recorder[0](repo, cloudAgentMutationInput{
			UserID:             userID,
			CanvasID:           canvasID,
			StepID:             call.ID,
			Operation:          "canvas_command",
			BeforeSnapshotHash: plan.BeforeSnapshotHash,
			AfterSnapshotHash:  afterHash,
			BeforeJSON:         plan.BeforeJSON,
			Preview:            &plan.Preview,
		}); err != nil {
			return nil, err
		}
	}
	return map[string]any{
		"canvasId": canvasID, "snapshotHash": afterHash, "beforeSnapshotHash": plan.BeforeSnapshotHash,
		"output": plan.Output, "steps": plan.Steps,
		"summary": fmt.Sprintf("已执行 %d 项画布命令", plan.WriteCount), "preview": plan.Preview,
	}, nil
}

func canvasCommandExecWrite(repo *repository.Repository, userID, canvasID string, doc map[string]any, counters map[string]int64, fs *canvasCommandFS, step canvasCommandStep) (cloudAgentApprovalPreviewItem, string, error) {
	item := cloudAgentApprovalPreviewItem{Operation: step.Name, NodeTitle: step.Name}
	var output string
	var err error
	switch step.Name {
	case "new":
		item, output, err = canvasCommandNew(doc, counters, fs, step)
	case "dup":
		item, output, err = canvasCommandDup(doc, counters, fs, step)
	case "mv":
		item, output, err = canvasCommandMove(doc, fs, step)
	case "rm":
		item, output, err = canvasCommandRemove(doc, fs, step)
	case "edit":
		item, output, err = canvasCommandEdit(doc, fs, step)
	case "sed":
		item, output, err = canvasCommandSed(doc, fs, step)
	case "cat":
		if step.Redirect == "" {
			err = canvasCommandError("command", "invalid_value", "cat 写通道只接受 cat > <字段文件> <<'EOF'")
			break
		}
		item, output, err = canvasCommandHeredocWrite(doc, fs, step)
	case "run":
		err = canvasCommandError("command", "dependency", "run 依赖 S10 的真实生成任务与回填；本片不提交收费任务。请先用 generate_media/wait_generations 完成单条生成，或在 S10 接入后使用 run")
	default:
		err = canvasCommandError("command", "unsupported", fmt.Sprintf("命令 %q 是写命令但尚未实现", step.Name))
	}
	return item, output, err
}

func canvasCommandNew(doc map[string]any, counters map[string]int64, fs *canvasCommandFS, step canvasCommandStep) (cloudAgentApprovalPreviewItem, string, error) {
	scopeID, workflowID, nodeType, title, err := canvasCommandNewArgs(step.Args)
	if err != nil {
		return cloudAgentApprovalPreviewItem{}, "", err
	}
	descriptor, err := canvasCommandCreateDescriptor(fs, nodeType, scopeID, workflowID)
	if err != nil {
		return cloudAgentApprovalPreviewItem{}, "", err
	}
	title, err = canvasCommandCreateTitle(doc, scopeID, title, descriptor)
	if err != nil {
		return cloudAgentApprovalPreviewItem{}, "", err
	}
	if err := canvasCommandValidateCreateBody(descriptor, step.Body); err != nil {
		return cloudAgentApprovalPreviewItem{}, "", err
	}
	prefix := descriptor.Type
	if scopeID != "" {
		prefix = scopeID + "__" + descriptor.Type
	}
	id := canvasCommandNextID(doc, counters, prefix)
	position := canvasCommandNextPosition(doc, scopeID)
	metadata := descriptor.Metadata(step.Body)
	if metadata == nil {
		metadata = map[string]any{}
	}
	node := map[string]any{
		"id": id, "type": descriptor.Type, "title": title,
		"position": position, "width": descriptor.DefaultWidth, "height": descriptor.DefaultHeight,
		"metadata": metadata,
	}
	if scopeID != "" {
		node["scopeId"] = scopeID
	}
	nodes := creationMaps(doc["nodes"])
	nodes = append(nodes, node)
	doc["nodes"] = nodes
	path := canvasCommandEntryPath(scopeID, id)
	rendered, renderErr := document.Render(descriptor, node)
	if renderErr != nil {
		rendered = canvasCommandValueText(metadata)
	}
	rendered, _ = canvasCommandTruncate(rendered, 4000)
	item := cloudAgentApprovalPreviewItem{
		Operation: "new", NodeID: id, NodeTitle: title, NodeType: descriptor.Type, NodeTypeLabel: descriptor.Label,
		Summary: fmt.Sprintf("新建%s《%s》（%s）", descriptor.Label, title, path),
	}
	return item, fmt.Sprintf("已创建 %s（%s）\n%s", path, id, rendered), nil
}

func canvasCommandCreateDescriptor(fs *canvasCommandFS, nodeType, scopeID, workflowID string) (capability.Descriptor, error) {
	if workflowID != "" {
		return capability.Descriptor{}, canvasCommandError("command", "dependency", "new submap --workflow 需要在服务端实例化子画布模板；请使用 S07 的模板插入或页面「子画布形式」，本片不复制前端模板实例化逻辑")
	}
	descriptor, ok := cloudAgentNodeCapabilityForType(nodeType)
	if !ok {
		return capability.Descriptor{}, canvasCommandError("command", "invalid_type", fmt.Sprintf("未注册的节点类型 %q：可建类型从 canvas ls --types 里取", nodeType))
	}
	if descriptor.VisualOnly {
		return capability.Descriptor{}, canvasCommandError("command", "visual_only", fmt.Sprintf("%s 是人类视觉专用容器，Agent 不创建", descriptor.Label))
	}
	if nodeType == "submap" && scopeID != "" {
		return capability.Descriptor{}, canvasCommandError("command", "invalid_value", "v1 不支持嵌套 submap：submap 不能建在另一个 submap 里")
	}
	if scopeID != "" {
		parent, ok := fs.lookupNodeID(scopeID)
		if !ok || parent.Type != "submap" {
			return capability.Descriptor{}, canvasCommandError("command", "not_found", fmt.Sprintf("--in %s 不是已存在的 submap；先 ls ./submap 取真实 ID", scopeID))
		}
		if canvasCommandLocked(parent.Node) {
			return capability.Descriptor{}, canvasCommandError("command", "locked", fmt.Sprintf("子画布 %s 已锁定；请让用户解锁后再创建子节点", parent.Path))
		}
		if nodeType == "submap" {
			return capability.Descriptor{}, canvasCommandError("command", "invalid_value", "v1 不支持嵌套 submap")
		}
	}
	return descriptor, nil
}

func canvasCommandCreateTitle(doc map[string]any, scopeID, title string, descriptor capability.Descriptor) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		title = descriptor.Label
	}
	if utf8.RuneCountInString(title) > canvasCommandMaxTitleRunes {
		return "", canvasCommandError("command", "too_long", fmt.Sprintf("标题超过 %d 字", canvasCommandMaxTitleRunes))
	}
	if strings.Contains(title, "$") {
		return "", canvasCommandError("command", "invalid_value", "标题不能含 $：本命令面没有变量展开")
	}
	if canvasCommandTitleTaken(doc, scopeID, title, "") {
		return "", canvasCommandError("command", "invalid_value", fmt.Sprintf("同一作用域已有标题《%s》的节点；请换标题或先读取目标", title))
	}
	return title, nil
}

func canvasCommandValidateCreateBody(descriptor capability.Descriptor, body string) error {
	if utf8.RuneCountInString(body) > canvasCommandMaxBodyRunes {
		return canvasCommandError("command", "too_long", fmt.Sprintf("正文超过单次写入上限 %d 字", canvasCommandMaxBodyRunes))
	}
	if body != "" && !canvasCommandHasContentField(descriptor) {
		return canvasCommandError("command", "unsupported", fmt.Sprintf("%s（%s）没有可预填的正文位；请先 new 建空节点，再写可写字段", descriptor.Label, descriptor.Type))
	}
	return nil
}

func canvasCommandEntryPath(scopeID, id string) string {
	if scopeID == "" {
		return "./" + id + "/"
	}
	return "./" + scopeID + "/" + strings.TrimPrefix(id, scopeID+"__") + "/"
}

func canvasCommandNewArgs(args []string) (scopeID, workflowID, nodeType, title string, err error) {
	rest := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--in":
			if index+1 >= len(args) {
				return "", "", "", "", canvasCommandError("command", "required", "new --in 缺少 submap ID")
			}
			scopeID = args[index+1]
			index++
		case "--workflow":
			if index+1 >= len(args) {
				return "", "", "", "", canvasCommandError("command", "required", "new --workflow 缺少模板 ID")
			}
			workflowID = args[index+1]
			index++
		default:
			rest = append(rest, args[index])
		}
	}
	if len(rest) == 0 {
		return "", "", "", "", canvasCommandError("command", "required", "new 缺少节点类型；用法：new [--in <submap>] <type> <title...>")
	}
	nodeType = rest[0]
	title = strings.Join(rest[1:], " ")
	return scopeID, workflowID, nodeType, title, nil
}

func canvasCommandHasContentField(descriptor capability.Descriptor) bool {
	if field, ok := descriptor.Fields["content"]; ok {
		return (field.Writable || field.Editable) && field.Kind == capability.KindString
	}
	if field, ok := descriptor.PatchFields["content"]; ok {
		return field.Kind == "string"
	}
	return false
}
