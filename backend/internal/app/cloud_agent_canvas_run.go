package app

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"yingce/backend/internal/canvas/contract"
	"yingce/backend/internal/repository"
)

// canvas 的 run 命令只做寻址与参数归一化：把节点目录翻译成现有
// generate_media_batch 的 items，模型、规格、提示词、参考与报价仍由批次准入
// 逐条走 generate_media 的完整链路。run 不复制第二个任务提交路径。
type canvasRunArgs struct {
	Mode        string
	Glob        string
	Concurrency int
	MaxRetries  *int
	Canary      bool
	Refs        []string
}

func cloudAgentCanvasRunStep(call cloudAgentCall) (canvasCommandStep, bool, error) {
	var args struct {
		Command      string `json:"command"`
		SnapshotHash string `json:"snapshotHash"`
	}
	if decodeCloudAgentJSONObject(call.Function.Arguments, &args) != nil {
		return canvasCommandStep{}, false, nil
	}
	steps, err := canvasCommandCompile(args.Command)
	if err != nil {
		return canvasCommandStep{}, false, nil
	}
	var run *canvasCommandStep
	for index := range steps {
		if steps[index].Name != "run" {
			continue
		}
		if run != nil || len(steps) != 1 {
			return canvasCommandStep{}, true, canvasCommandError("command", "invalid_value", "一次 canvas 调用只能有一条 run，且不能和其他命令混用；请拆成多次调用")
		}
		run = &steps[index]
	}
	if run == nil {
		return canvasCommandStep{}, false, nil
	}
	return *run, true, nil
}

func cloudAgentCanvasRunBatchArgs(repo *repository.Repository, userID, canvasID string, call cloudAgentCall) (cloudAgentMediaBatchArgs, bool, error) {
	step, ok, err := cloudAgentCanvasRunStep(call)
	if err != nil || !ok {
		return cloudAgentMediaBatchArgs{}, ok, err
	}
	runArgs, err := canvasRunParseArgs(step.Args)
	if err != nil {
		return cloudAgentMediaBatchArgs{}, true, err
	}
	canvas, err := repo.CanvasProjectForUser(userID, canvasID)
	if err != nil {
		return cloudAgentMediaBatchArgs{}, true, err
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		return cloudAgentMediaBatchArgs{}, true, err
	}
	var envelope struct {
		SnapshotHash string `json:"snapshotHash"`
	}
	_ = decodeCloudAgentJSONObject(call.Function.Arguments, &envelope)
	if envelope.SnapshotHash != "" && envelope.SnapshotHash != cloudAgentCanvasHash(doc) {
		return cloudAgentMediaBatchArgs{}, true, cloudAgentFieldError("snapshotHash", "stale_snapshot", "画布已变化，本次 run 未提交；请重新读取画布")
	}
	fs := newCanvasCommandFS(canvasID, doc, canvas.Revision)
	targets, mode, err := canvasRunTargets(fs, runArgs)
	if err != nil {
		return cloudAgentMediaBatchArgs{}, true, err
	}
	args := cloudAgentMediaBatchArgs{
		Mode: mode, Concurrency: runArgs.Concurrency, Canary: runArgs.Canary, MaxRetries: runArgs.MaxRetries,
	}
	options := canvasRunOptions{}
	for _, entry := range targets {
		nodeOptions, duration, err := canvasRunNodeOptions(entry)
		if err != nil {
			return cloudAgentMediaBatchArgs{}, true, err
		}
		if err := options.merge(nodeOptions, entry.Path); err != nil {
			return cloudAgentMediaBatchArgs{}, true, err
		}
		args.Items = append(args.Items, struct {
			NodeID   string `json:"nodeId"`
			Prompt   string `json:"prompt"`
			Duration int    `json:"durationSeconds"`
		}{NodeID: entry.ID, Duration: duration})
	}
	args.LogicalModelID, args.ChannelID, args.ChannelModelKey = options.LogicalID, options.ChannelID, options.ModelKey
	args.Size, args.Quality, args.ImageResolution = options.Size, options.Quality, options.ImageResolution
	args.VideoGenerateAudio = options.GenerateAudio
	if err := canvasRunValidateBatchArgs(args); err != nil {
		return cloudAgentMediaBatchArgs{}, true, err
	}
	return args, true, nil
}

func canvasRunParseArgs(raw []string) (canvasRunArgs, error) {
	args := canvasRunArgs{}
	for index := 0; index < len(raw); index++ {
		token := strings.TrimSpace(raw[index])
		if token == "" {
			continue
		}
		if !strings.HasPrefix(token, "--") {
			args.Refs = append(args.Refs, token)
			continue
		}
		consumed, err := canvasRunParseFlag(&args, raw, index, token)
		if err != nil {
			return args, err
		}
		index += consumed
	}
	if args.Glob != "" && len(args.Refs) > 0 {
		return args, canvasCommandError("command", "invalid_value", "run 的 --glob 与位置节点目录二选一")
	}
	if args.Glob == "" && len(args.Refs) == 0 {
		return args, canvasCommandError("command", "required", "run 需要至少一个节点目录或 --glob：run ./submap-1/image-1/ 或 run --glob './*'")
	}
	return args, nil
}

// canvasRunParseFlag 解析一个 --flag，返回它额外消费的值参数个数。
func canvasRunParseFlag(args *canvasRunArgs, raw []string, index int, token string) (int, error) {
	value := func(label, hint string) (string, error) {
		if index+1 >= len(raw) {
			return "", canvasCommandError("command", "invalid_value", fmt.Sprintf("run %s 后面要跟%s", label, hint))
		}
		return strings.TrimSpace(raw[index+1]), nil
	}
	switch token {
	case "--glob":
		glob, err := value("--glob", "节点目录通配，例如 run --glob './image-*'")
		args.Glob = glob
		return 1, err
	case "--mode":
		mode, err := value("--mode", "image、video 或 audio")
		if err != nil {
			return 0, err
		}
		mode = strings.ToLower(mode)
		if mode != "image" && mode != "video" && mode != "audio" {
			return 0, canvasCommandError("command", "invalid_value", "run --mode 只支持 image、video 或 audio")
		}
		args.Mode = mode
		return 1, nil
	case "--concurrency":
		rawValue, err := value("--concurrency", "1 到 10 的整数")
		if err != nil {
			return 0, err
		}
		number, err := strconv.Atoi(rawValue)
		if err != nil || number < 1 || number > 10 {
			return 0, canvasCommandError("command", "invalid_value", "run --concurrency 必须是 1 到 10 的整数")
		}
		args.Concurrency = number
		return 1, nil
	case "--max-retries":
		rawValue, err := value("--max-retries", fmt.Sprintf("0 到 %d 的整数", cloudAgentMediaBatchMaxRetries))
		if err != nil {
			return 0, err
		}
		number, err := strconv.Atoi(rawValue)
		if err != nil || number < 0 || number > cloudAgentMediaBatchMaxRetries {
			return 0, canvasCommandError("command", "invalid_value", fmt.Sprintf("run --max-retries 必须是 0 到 %d 的整数", cloudAgentMediaBatchMaxRetries))
		}
		args.MaxRetries = &number
		return 1, nil
	case "--canary":
		args.Canary = true
		return 0, nil
	default:
		return 0, canvasCommandError("command", "invalid_value", fmt.Sprintf("run 不支持参数 %s；支持 --glob、--mode、--concurrency、--max-retries、--canary", token))
	}
}

func canvasRunTargets(fs *canvasCommandFS, args canvasRunArgs) ([]canvasCommandNode, string, error) {
	entries := make([]canvasCommandNode, 0, 4)
	seen := map[string]bool{}
	add := func(entry canvasCommandNode) {
		if !seen[entry.ID] {
			seen[entry.ID] = true
			entries = append(entries, entry)
		}
	}
	if args.Glob != "" {
		pattern := canvasCommandPathKey(args.Glob)
		for _, entry := range fs.nodes {
			if !canvasRunMediaType(entry.Type) {
				continue
			}
			if canvasCommandGlobMatch(pattern, canvasCommandPathKey(entry.Path)) {
				add(entry)
			}
		}
		if len(entries) == 0 {
			return nil, "", canvasCommandError("command", "not_found", fmt.Sprintf("run --glob %q 没有命中 image/video/audio 节点目录", args.Glob))
		}
	} else {
		for _, ref := range args.Refs {
			entry, ok := fs.lookupNode(ref)
			if !ok {
				return nil, "", canvasCommandError("command", "not_found", canvasCommandPathMissing(fs, ref))
			}
			if !canvasRunMediaType(entry.Type) {
				return nil, "", canvasCommandError("command", "invalid_value", fmt.Sprintf("run 只能触发 image/video/audio 节点，%s 是 %q", entry.Path, entry.Type))
			}
			add(entry)
		}
	}
	mode := args.Mode
	for _, entry := range entries {
		nodeMode := strings.ToLower(strings.TrimSpace(entry.Type))
		if mode == "" {
			mode = nodeMode
		}
		if nodeMode != mode {
			return nil, "", canvasCommandError("command", "invalid_value", fmt.Sprintf("run 同一批只能是单一模式，%s 是 %s，本批是 %s；请拆成多次 run", entry.Path, nodeMode, mode))
		}
	}
	return entries, mode, nil
}

func canvasRunMediaType(nodeType string) bool {
	switch strings.ToLower(strings.TrimSpace(nodeType)) {
	case "image", "video", "audio":
		return true
	default:
		return false
	}
}

type canvasRunOptions struct {
	LogicalID       string
	ChannelID       string
	ModelKey        string
	Size            string
	Quality         string
	ImageResolution string
	GenerateAudio   *bool
}

func canvasRunNodeOptions(entry canvasCommandNode) (canvasRunOptions, int, error) {
	meta, _ := entry.Node["metadata"].(map[string]any)
	out := canvasRunOptions{}
	duration := 0
	if raw, exists := meta["generationSpec"]; exists && raw != nil {
		encoded, err := json.Marshal(raw)
		if err != nil {
			return out, 0, err
		}
		spec, err := contract.Decode(encoded)
		if err != nil {
			return out, 0, canvasCommandError("command", "invalid_value", fmt.Sprintf("%s 的 generationSpec 无法解析：%s", entry.Path, err.Error()))
		}
		if spec.Mode != strings.ToLower(entry.Type) {
			return out, 0, canvasCommandError("command", "invalid_value", fmt.Sprintf("%s 的 generationSpec 模式 %s 与节点类型 %s 不一致", entry.Path, spec.Mode, entry.Type))
		}
		if spec.ModelSelection != nil {
			switch spec.ModelSelection.Kind {
			case "logical":
				out.LogicalID = strings.TrimSpace(spec.ModelSelection.LogicalModelID)
			case "channel":
				out.ChannelID = strings.TrimSpace(spec.ModelSelection.ChannelID)
				out.ModelKey = strings.TrimSpace(spec.ModelSelection.ModelKey)
			}
		}
		out.Size = stringPtrValue(spec.Options.Size)
		out.ImageResolution = stringPtrValue(spec.Options.ImageResolution)
		if spec.Mode == "video" {
			out.Quality = stringPtrValue(spec.Options.Resolution)
			out.GenerateAudio = spec.Options.GenerateAudio
			if spec.Options.DurationSeconds != nil {
				duration = *spec.Options.DurationSeconds
			}
		} else if spec.Mode == "image" {
			out.Quality = stringPtrValue(spec.Options.Quality)
		}
	}
	if duration == 0 {
		duration = canvasRunInt(meta["seconds"])
	}
	if out.Size == "" {
		out.Size = strings.TrimSpace(stringValue(meta["size"]))
	}
	if out.Quality == "" {
		key := "quality"
		if strings.ToLower(entry.Type) == "video" {
			key = "vquality"
		}
		out.Quality = strings.TrimSpace(stringValue(meta[key]))
	}
	if out.ImageResolution == "" {
		out.ImageResolution = strings.TrimSpace(stringValue(meta["imageResolution"]))
	}
	if out.GenerateAudio == nil {
		if value, ok := meta["generateAudio"].(bool); ok {
			out.GenerateAudio = &value
		}
	}
	return out, duration, nil
}

func (options *canvasRunOptions) merge(next canvasRunOptions, path string) error {
	if err := canvasRunMergeSelection(&options.LogicalID, next.LogicalID, path, "模型"); err != nil {
		return err
	}
	if err := canvasRunMergeSelection(&options.ChannelID, next.ChannelID, path, "渠道模型"); err != nil {
		return err
	}
	if err := canvasRunMergeSelection(&options.ModelKey, next.ModelKey, path, "渠道模型键"); err != nil {
		return err
	}
	if err := canvasRunMergeSelection(&options.Size, next.Size, path, "画幅"); err != nil {
		return err
	}
	if err := canvasRunMergeSelection(&options.Quality, next.Quality, path, "清晰度"); err != nil {
		return err
	}
	if err := canvasRunMergeSelection(&options.ImageResolution, next.ImageResolution, path, "图片分辨率"); err != nil {
		return err
	}
	if next.GenerateAudio != nil {
		if options.GenerateAudio != nil && *options.GenerateAudio != *next.GenerateAudio {
			return canvasCommandError("command", "invalid_value", fmt.Sprintf("%s 的音频开关与本批其他条目不一致；请拆成多次 run", path))
		}
		options.GenerateAudio = next.GenerateAudio
	}
	return nil
}

func canvasRunMergeSelection(current *string, next, path, label string) error {
	next = strings.TrimSpace(next)
	if next == "" {
		return nil
	}
	if *current != "" && *current != next {
		return canvasCommandError("command", "invalid_value", fmt.Sprintf("%s 的%s %q 与本批其他条目不一致；请拆成多次 run", path, label, next))
	}
	*current = next
	return nil
}

func canvasRunValidateBatchArgs(args cloudAgentMediaBatchArgs) error {
	if len(args.Items) == 0 {
		return canvasCommandError("command", "required", "run 没有可提交的节点")
	}
	if len(args.Items) > cloudAgentMediaBatchMaxItems {
		return canvasCommandError("command", "too_long", fmt.Sprintf("run 一次最多 %d 个节点；请用 --glob 拆批", cloudAgentMediaBatchMaxItems))
	}
	if args.LogicalModelID != "" && (args.ChannelID != "" || args.ChannelModelKey != "") {
		return canvasCommandError("command", "invalid_value", "run 的目标节点混用了逻辑模型和渠道模型；请拆成多次 run")
	}
	if (args.ChannelID == "") != (args.ChannelModelKey == "") {
		return canvasCommandError("command", "invalid_value", "run 的渠道模型必须同时给 channelId 和 channelModelKey")
	}
	return nil
}

func stringPtrValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func canvasRunInt(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case int64:
		return int(typed)
	case json.Number:
		number, _ := typed.Int64()
		return int(number)
	case string:
		number, _ := strconv.Atoi(strings.TrimSpace(typed))
		return number
	default:
		return 0
	}
}
