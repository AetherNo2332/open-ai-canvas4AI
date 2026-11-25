package app

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func canvasCommandHandleRead(repo *repository.Repository, userID string, state *cloudAgentRuntime, call cloudAgentCall, services ...*Service) (any, error) {
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
	for _, step := range steps {
		if step.sensitive() {
			return nil, canvasCommandError("command", "invalid_value", fmt.Sprintf("%s 是写命令，不能走只读通道；这是服务端分派缺陷，请报告", step.Name))
		}
	}
	canvas, err := repo.CanvasProjectForUser(userID, state.Request.CanvasID)
	if err != nil {
		return nil, err
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		return nil, err
	}
	fs := newCanvasCommandFS(state.Request.CanvasID, doc, canvas.Revision)
	service := firstService(services...)
	outputs := make([]map[string]any, 0, len(steps))
	var combined strings.Builder
	for _, step := range steps {
		output, err := canvasCommandExecRead(repo, userID, state, fs, step, service)
		if err != nil {
			return nil, err
		}
		outputs = append(outputs, map[string]any{"command": step.Name, "output": output})
		if combined.Len() > 0 {
			combined.WriteString("\n")
		}
		combined.WriteString(output)
	}
	text, _ := canvasCommandTruncate(combined.String(), 64<<10)
	return map[string]any{
		"canvasId": state.Request.CanvasID, "command": strings.TrimSpace(args.Command),
		"output": text, "steps": outputs, "snapshotHash": cloudAgentCanvasHash(doc),
		"canvasRevision": canvas.Revision,
	}, nil
}

func firstService(services ...*Service) *Service {
	if len(services) > 0 {
		return services[0]
	}
	return nil
}

func canvasCommandExecRead(repo *repository.Repository, userID string, state *cloudAgentRuntime, fs *canvasCommandFS, step canvasCommandStep, service *Service) (string, error) {
	switch step.Name {
	case "pwd":
		return fs.canvasID, nil
	case "ls":
		return canvasCommandList(fs, step.Args)
	case "cat":
		return canvasCommandCat(fs, step.Args, step.Number)
	case "grep":
		return canvasCommandGrep(fs, step.Args, step.Number)
	case "wc":
		return canvasCommandWordCount(fs, step.Args)
	case "head":
		return canvasCommandPage(fs, step.Args, false, step.Number)
	case "tail":
		return canvasCommandPage(fs, step.Args, true, step.Number)
	case "find":
		return canvasCommandFind(fs, step.Args)
	case "help":
		return canvasCommandHelp(), nil
	case "log":
		runID := ""
		if state != nil {
			runID = state.RuntimeRunID
		}
		return canvasCommandLog(repo, userID, fs.canvasID, runID, step.Args)
	case "preset":
		return canvasCommandPreset(repo, userID, step.Args)
	case "run":
		return "", canvasCommandError("command", "invalid_value", "run 必须单独调用并走批次审批，不能在只读路径执行")
	default:
		return "", canvasCommandError("command", "unsupported", fmt.Sprintf("命令 %q 尚未实现；支持的命令：%s", step.Name, strings.Join(canvasCommandNames(false), "、")))
	}
}

func canvasCommandList(fs *canvasCommandFS, args []string) (string, error) {
	long := false
	rest := make([]string, 0, len(args))
	for _, arg := range args {
		switch arg {
		case "-l":
			long = true
		case "--types":
			return canvasCommandTypes(), nil
		default:
			rest = append(rest, arg)
		}
	}
	if len(rest) == 0 {
		return canvasCommandRootListing(fs, long), nil
	}
	if len(rest) > 1 {
		return "", canvasCommandError("command", "invalid_value", "ls 只接受一个路径；要列出字段请给 ./<节点ID>/")
	}
	path := rest[0]
	if entry, ok := fs.lookupNode(path); ok {
		if entry.Type == "submap" {
			children := fs.children(entry.ID)
			if len(children) == 0 {
				return entry.Path + "（没有子节点）", nil
			}
			var out strings.Builder
			for _, child := range children {
				out.WriteString(child.Path + "	" + child.Type + "	" + strings.TrimSpace(stringValue(child.Node["title"])) + "\n")
			}
			return strings.TrimRight(out.String(), "\n"), nil
		}
		return canvasCommandNodeFields(entry, long), nil
	}
	if entry, key, err := fs.resolveFieldPath(path); err == nil && key != "" {
		value, _ := canvasCommandFieldValue(entry, key)
		return canvasCommandValueText(value), nil
	}
	// 类型目录：./text 列出该类型顶层节点。
	nodeType := strings.TrimPrefix(canvasCommandPathKey(path), "./")
	entries := make([]string, 0)
	for _, entry := range fs.topLevel() {
		if entry.Type == nodeType {
			entries = append(entries, entry.Path+"	"+strings.TrimSpace(stringValue(entry.Node["title"])))
		}
	}
	if len(entries) > 0 {
		return strings.Join(entries, "\n"), nil
	}
	return "", canvasCommandError("command", "not_found", canvasCommandPathMissing(fs, path))
}

func canvasCommandTypes() string {
	var out strings.Builder
	for _, descriptor := range canvasCapabilityRegistry.AgentList() {
		out.WriteString(descriptor.Type + "	" + descriptor.Label + "	" + descriptor.Purpose + "\n")
	}
	return strings.TrimRight(out.String(), "\n")
}

func canvasCommandRootListing(fs *canvasCommandFS, long bool) string {
	var out strings.Builder
	out.WriteString("画布 " + fs.canvasID + "（revision " + strconv.FormatInt(fs.revision, 10) + "）\n")
	byType := map[string][]canvasCommandNode{}
	for _, entry := range fs.topLevel() {
		byType[entry.Type] = append(byType[entry.Type], entry)
	}
	types := make([]string, 0, len(byType))
	for nodeType := range byType {
		types = append(types, nodeType)
	}
	sort.Strings(types)
	for _, nodeType := range types {
		out.WriteString("./" + nodeType + "/（" + strconv.Itoa(len(byType[nodeType])) + "）\n")
		for _, entry := range byType[nodeType] {
			line := "  " + entry.Path
			if long {
				line += "	" + strings.TrimSpace(stringValue(entry.Node["title"]))
				if entry.Type == "submap" {
					line += "（" + strconv.Itoa(len(fs.children(entry.ID))) + " 个子节点）"
				}
			}
			out.WriteString(line + "\n")
		}
	}
	if len(types) == 0 {
		out.WriteString("（画布上没有节点）\n")
	}
	return strings.TrimRight(out.String(), "\n")
}

func canvasCommandNodeFields(entry canvasCommandNode, long bool) string {
	fields := canvasCommandFields(entry)
	if len(fields) == 0 {
		return entry.Path + "（没有可读字段）"
	}
	var out strings.Builder
	for _, field := range fields {
		line := field.Key + canvasCommandFieldExtension(field)
		if long {
			value, _ := canvasCommandFieldValue(entry, field.Key)
			text := canvasCommandValueText(value)
			line += "	" + field.Label + "	" + truncateRunes(strings.ReplaceAll(text, "\n", "↵"), 80)
		}
		out.WriteString(line + "\n")
	}
	return strings.TrimRight(out.String(), "\n")
}

func canvasCommandCat(fs *canvasCommandFS, args []string, numbered bool) (string, error) {
	if len(args) != 1 {
		return "", canvasCommandError("command", "invalid_value", "cat 需要一个路径：节点目录或字段文件")
	}
	entry, key, err := fs.resolveFieldPath(args[0])
	if err != nil {
		return "", err
	}
	var text string
	if key == "" {
		text, err = canvasCommandRenderNode(entry)
		if err != nil {
			return "", err
		}
		if entry.Type == "submap" {
			text += canvasCommandSubmapIndex(fs, entry.ID)
		}
	} else {
		value, _ := canvasCommandFieldValue(entry, key)
		text = canvasCommandValueText(value)
	}
	if numbered {
		return canvasCommandNumberLines(text), nil
	}
	return text, nil
}

func canvasCommandNumberLines(text string) string {
	lines := strings.Split(text, "\n")
	var out strings.Builder
	for index, line := range lines {
		out.WriteString(strconv.Itoa(index+1) + "	" + line + "\n")
	}
	return strings.TrimRight(out.String(), "\n")
}

func canvasCommandGrep(fs *canvasCommandFS, args []string, numbered bool) (string, error) {
	if len(args) < 2 {
		return "", canvasCommandError("command", "invalid_value", "grep 需要 <字段文件> <pattern...>")
	}
	entry, key, err := fs.resolveFieldPath(args[0])
	if err != nil {
		return "", err
	}
	if key == "" {
		return "", canvasCommandDirectoryError(entry)
	}
	value, _ := canvasCommandFieldValue(entry, key)
	text := canvasCommandValueText(value)
	patterns := args[1:]
	out := make([]string, 0)
	for index, line := range strings.Split(text, "\n") {
		for _, pattern := range patterns {
			if strings.Contains(line, pattern) {
				prefix := entry.Path + key
				if numbered {
					prefix += ":" + strconv.Itoa(index+1)
				}
				out = append(out, prefix+":"+line)
				break
			}
		}
	}
	return strings.Join(out, "\n"), nil
}

func canvasCommandWordCount(fs *canvasCommandFS, args []string) (string, error) {
	if len(args) != 1 {
		return "", canvasCommandError("command", "invalid_value", "wc 需要一个路径")
	}
	entry, key, err := fs.resolveFieldPath(args[0])
	if err != nil {
		return "", err
	}
	text := ""
	if key == "" {
		text, err = canvasCommandRenderNode(entry)
		if err != nil {
			return "", err
		}
	} else {
		value, _ := canvasCommandFieldValue(entry, key)
		text = canvasCommandValueText(value)
	}
	lines := canvasCommandSplitLines(text)
	words := 0
	for _, line := range lines {
		words += len(strings.Fields(line))
	}
	return fmt.Sprintf("%d %d %d %s", len(lines), words, utf8.RuneCountInString(text), args[0]), nil
}

func canvasCommandPage(fs *canvasCommandFS, args []string, tail bool, numbered bool) (string, error) {
	count := 10
	rest := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		if args[index] == "-n" && index+1 < len(args) {
			value, err := strconv.Atoi(args[index+1])
			if err != nil || value < 0 {
				return "", canvasCommandError("command", "invalid_value", "head/tail 的 -n 需要非负整数")
			}
			count = value
			index++
			continue
		}
		rest = append(rest, args[index])
	}
	if len(rest) != 1 {
		return "", canvasCommandError("command", "invalid_value", "head/tail 需要一个路径")
	}
	entry, key, err := fs.resolveFieldPath(rest[0])
	if err != nil {
		return "", err
	}
	text := ""
	if key == "" {
		text, err = canvasCommandRenderNode(entry)
		if err != nil {
			return "", err
		}
	} else {
		value, _ := canvasCommandFieldValue(entry, key)
		text = canvasCommandValueText(value)
	}
	lines := canvasCommandSplitLines(text)
	if count < len(lines) {
		if tail {
			lines = lines[len(lines)-count:]
		} else {
			lines = lines[:count]
		}
	}
	result := strings.Join(lines, "\n")
	if numbered {
		result = canvasCommandNumberLines(result)
	}
	return result, nil
}

func canvasCommandFind(fs *canvasCommandFS, args []string) (string, error) {
	patterns := args
	if len(patterns) == 0 {
		patterns = []string{"**"}
	}
	paths := make([]string, 0, len(fs.nodes)*4)
	for _, entry := range fs.nodes {
		paths = append(paths, canvasCommandPathKey(entry.Path))
	}
	for _, fieldPath := range canvasCommandFieldPaths(fs) {
		paths = append(paths, canvasCommandPathKey(fieldPath))
	}
	sort.Strings(paths)
	out := make([]string, 0)
	for _, path := range paths {
		for _, pattern := range patterns {
			if canvasCommandGlobMatch(canvasCommandPathKey(pattern), path) {
				out = append(out, "./"+path)
				break
			}
		}
	}
	return strings.Join(out, "\n"), nil
}

func canvasCommandHelp() string {
	var out strings.Builder
	out.WriteString("canvas 把画布投影成目录：节点是目录，字段是文件。\n")
	out.WriteString("路径：./<节点ID>/、./<节点ID>/<字段>.<ext>；submap 子节点 ./<submapID>/<localId>/<字段>.<ext>。\n")
	out.WriteString("读命令不写画布；写命令按权限审批。rm 在任何权限模式下都要审批。\n\n")
	for _, spec := range canvasCommandSpecs {
		marker := ""
		if spec.Destructive {
			marker = " [破坏性]"
		} else if spec.Sensitive {
			marker = " [写]"
		}
		out.WriteString(spec.Usage + marker + "\n  " + spec.Summary + "\n")
	}
	out.WriteString("\n分隔：支持 &&、; 和换行；不支持 ||。管道只支持 cat/head/tail 接 head/tail/grep/wc。\n")
	out.WriteString("正文写入用 heredoc：cat > ./image-1/prompt.txt <<'EOF'\n文本\nEOF\n")
	return out.String()
}

func canvasCommandLog(repo *repository.Repository, userID, canvasID, runID string, args []string) (string, error) {
	count := 10
	for index := 0; index < len(args); index++ {
		if args[index] == "-n" && index+1 < len(args) {
			value, err := strconv.Atoi(args[index+1])
			if err != nil || value < 1 || value > 50 {
				return "", canvasCommandError("command", "invalid_value", "log 的 -n 需要在 1 到 50 之间")
			}
			count = value
			index++
		}
	}
	var mutations []model.CloudAgentCanvasMutation
	var err error
	if runID != "" {
		mutations, err = repo.CloudAgentCanvasMutationChain(userID, runID, canvasID)
	} else {
		var latest *model.CloudAgentCanvasMutation
		latest, err = repo.LatestCloudAgentCanvasMutationForCanvas(userID, canvasID)
		if latest != nil {
			mutations = append(mutations, *latest)
		}
	}
	if err != nil {
		return "", err
	}
	if len(mutations) > count {
		mutations = mutations[len(mutations)-count:]
	}
	lines := make([]string, 0, len(mutations))
	for _, mutation := range mutations {
		lines = append(lines, fmt.Sprintf("%s	%s	%s	%s	%s", mutation.CreatedAt.Format("2006-01-02 15:04:05"), mutation.RunID, mutation.Operation, mutation.Status, mutation.StepID))
	}
	return strings.Join(lines, "\n"), nil
}

func canvasCommandPreset(repo *repository.Repository, userID string, args []string) (string, error) {
	if len(args) == 0 || args[0] == "list" {
		presets, err := repo.UserSubmapPresets(userID)
		if err != nil {
			return "", err
		}
		lines := make([]string, 0, len(presets))
		for _, preset := range presets {
			lines = append(lines, preset.ID+"	"+preset.Name+"	revision "+strconv.FormatInt(preset.Revision, 10))
		}
		return strings.Join(lines, "\n"), nil
	}
	switch args[0] {
	case "delete":
		return "", canvasCommandError("command", "unsupported", "模板删除需要用户在模板管理页面操作；preset 只提供 list，不能通过只读画布命令删除模板")
	case "save":
		return "", canvasCommandError("command", "dependency", "preset save 需要把 submap 子树转成局部 ID 快照；请使用 S07 的子画布标题栏「另存为用户模板」或 POST /api/user-submap-presets，本片不复制前端快照逻辑")
	default:
		return "", canvasCommandError("command", "invalid_value", "preset 只支持 list；模板保存和删除请使用页面入口")
	}
}
