package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"yingce/backend/internal/canvas/contract"
	"yingce/backend/internal/model"
)

// 批量生成：一次审批覆盖一组占位节点的收费生成。批准后由服务端执行器按并发提交、
// 逐条回写画布，阶段结束（试跑完成或全部结束）再把结果交回 Agent。
// 每次实际提交仍走 CreateTask 的完整准入：模型能力、参考归属、额度与报价都重新校验，
// 批次授权只放宽"每条都要用户点一次"，不放宽任何业务校验。
const (
	cloudAgentMediaBatchMaxItems      = 40
	cloudAgentMediaBatchMaxRetries    = 2
	cloudAgentMediaBatchStateLimit    = 200 << 10
	cloudAgentMediaBatchLeaseLifetime = 24 * time.Hour
	cloudAgentMediaBatchTaskLease     = 30 * time.Minute
	cloudAgentMediaBatchPollInterval  = time.Second
	cloudAgentMediaBatchDefaultSlots  = 3
)

const (
	cloudAgentMediaBatchPhaseApproval   = "approval"
	cloudAgentMediaBatchPhaseCanary     = "canary"
	cloudAgentMediaBatchPhaseCanaryDone = "canary_done"
	cloudAgentMediaBatchPhaseRunning    = "running"
	cloudAgentMediaBatchPhaseDone       = "done"
)

const (
	cloudAgentMediaBatchItemPending   = "pending"
	cloudAgentMediaBatchItemSubmitted = "submitted"
	cloudAgentMediaBatchItemSucceeded = "succeeded"
	cloudAgentMediaBatchItemFailed    = "failed"
	cloudAgentMediaBatchItemCancelled = "cancelled"
)

// cloudAgentPiMediaBatchPauseText 是批次在后台执行时交给运行时的占位结果；
// 批次结束后由真实回执替换（见 cloudAgentToolResultIsPlaceholder）。
const cloudAgentPiMediaBatchPauseText = "批次正在后台执行，完成后会把结果发给你。"

var errCloudAgentMediaBatchWait = errors.New("media batch submission must wait")

type cloudAgentMediaBatch struct {
	ID              string                     `json:"id"`
	CallID          string                     `json:"callId"`
	Mode            string                     `json:"mode"`
	ModelName       string                     `json:"modelName,omitempty"`
	Phase           string                     `json:"phase"`
	Concurrency     int                        `json:"concurrency"`
	Canary          bool                       `json:"canary,omitempty"`
	MaxRetries      int                        `json:"maxRetries"`
	CapMicrocredits int64                      `json:"capMicrocredits"`
	Items           []cloudAgentMediaBatchItem `json:"items"`
}

type cloudAgentMediaBatchItem struct {
	NodeID          string   `json:"nodeId"`
	Title           string   `json:"title,omitempty"`
	Arguments       string   `json:"arguments"`
	ResolvedHash    string   `json:"resolvedHash"`
	SpecHash        string   `json:"specHash"`
	Amount          int64    `json:"amountMicrocredits"`
	Duration        int      `json:"durationSeconds,omitempty"`
	ResourceIDs     []string `json:"resourceIds,omitempty"`
	Status          string   `json:"status"`
	TaskIDs         []string `json:"taskIds,omitempty"`
	Attempts        int      `json:"attempts,omitempty"`
	PromptChanged   bool     `json:"promptChanged,omitempty"`
	PreviousNodeIDs []string `json:"previousNodeIds,omitempty"`
	Error           string   `json:"error,omitempty"`
}

func (b *cloudAgentMediaBatch) active() bool {
	return b != nil && (b.Phase == cloudAgentMediaBatchPhaseCanary || b.Phase == cloudAgentMediaBatchPhaseRunning)
}

func (item cloudAgentMediaBatchItem) taskID() string {
	if len(item.TaskIDs) == 0 {
		return ""
	}
	return item.TaskIDs[len(item.TaskIDs)-1]
}

func cloudAgentMediaBatchActive(state *cloudAgentRuntime) bool {
	return state != nil && state.MediaBatch.active()
}

func (b *cloudAgentMediaBatch) counts() map[string]int {
	counts := map[string]int{"total": len(b.Items)}
	for _, item := range b.Items {
		counts[item.Status]++
	}
	return counts
}

func (b *cloudAgentMediaBatch) inFlight() int {
	count := 0
	for _, item := range b.Items {
		if item.Status == cloudAgentMediaBatchItemSubmitted {
			count++
		}
	}
	return count
}

func (b *cloudAgentMediaBatch) nextPending() int {
	for index, item := range b.Items {
		if item.Status == cloudAgentMediaBatchItemPending {
			if b.Phase == cloudAgentMediaBatchPhaseCanary && index != 0 {
				return -1
			}
			return index
		}
	}
	return -1
}

type cloudAgentMediaBatchArgs struct {
	Mode                       string `json:"mode"`
	LogicalModelID             string `json:"logicalModelId"`
	ChannelID                  string `json:"channelId"`
	ChannelModelKey            string `json:"channelModelKey"`
	Size                       string `json:"size"`
	Quality                    string `json:"quality"`
	ImageResolution            string `json:"imageResolution"`
	Duration                   int    `json:"durationSeconds"`
	VideoGenerateAudio         *bool  `json:"videoGenerateAudio"`
	ImageParameters            string `json:"imageParameters,omitempty"`
	VideoWatermark             *bool  `json:"videoWatermark,omitempty"`
	VideoArkPrivateAssetUpload *bool  `json:"videoArkPrivateAssetUpload,omitempty"`
	Concurrency                int    `json:"concurrency"`
	Canary                     bool   `json:"canary"`
	MaxRetries                 *int   `json:"maxRetriesPerItem"`
	Items                      []struct {
		NodeID   string `json:"nodeId"`
		Prompt   string `json:"prompt"`
		Duration int    `json:"durationSeconds"`
	} `json:"items"`
}

func decodeCloudAgentMediaBatchArgs(raw string) (cloudAgentMediaBatchArgs, error) {
	var args cloudAgentMediaBatchArgs
	if err := decodeCloudAgentJSONObject(raw, &args); err != nil {
		return args, cloudAgentJSONArgumentError(err)
	}
	args.Mode = strings.ToLower(strings.TrimSpace(args.Mode))
	if args.Mode != "image" && args.Mode != "video" && args.Mode != "audio" {
		return args, cloudAgentFieldError("mode", "invalid_value", "批量生成只支持 image、video 或 audio；同一批只能是一种模式")
	}
	if len(args.Items) == 0 {
		return args, cloudAgentFieldError("items", "required", "批量生成至少需要一个条目")
	}
	if len(args.Items) > cloudAgentMediaBatchMaxItems {
		return args, cloudAgentFieldError("items", "item_count", fmt.Sprintf("一批最多 %d 条，请拆成多批", cloudAgentMediaBatchMaxItems))
	}
	seen := map[string]bool{}
	for index, item := range args.Items {
		if err := validateCloudAgentID(item.NodeID, "占位节点 ID", 80); err != nil {
			return args, cloudAgentFieldError(fmt.Sprintf("items[%d].nodeId", index), "invalid_value", cloudAgentSafeToolError(err))
		}
		if seen[item.NodeID] {
			return args, cloudAgentFieldError(fmt.Sprintf("items[%d].nodeId", index), "duplicate", "同一批里每个占位节点只能出现一次")
		}
		seen[item.NodeID] = true
		if item.Duration < 0 || (item.Duration > 0 && args.Mode != "video") {
			return args, cloudAgentFieldError(fmt.Sprintf("items[%d].durationSeconds", index), "invalid_value", "只有视频条目可以单独设置时长")
		}
	}
	if args.Concurrency < 0 || args.Concurrency > 10 {
		return args, cloudAgentFieldError("concurrency", "invalid_value", "并发数必须在 1 到 10 之间")
	}
	if args.MaxRetries != nil && (*args.MaxRetries < 0 || *args.MaxRetries > cloudAgentMediaBatchMaxRetries) {
		return args, cloudAgentFieldError("maxRetriesPerItem", "invalid_value", fmt.Sprintf("每条最多重试 %d 次", cloudAgentMediaBatchMaxRetries))
	}
	return args, nil
}

// cloudAgentMediaBatchNodePrompt 读取占位节点里保存的提示词：生成合同优先，旧节点退回编辑器正文。
func cloudAgentMediaBatchNodePrompt(node map[string]any) string {
	meta, _ := node["metadata"].(map[string]any)
	if raw, exists := meta["generationSpec"]; exists {
		if encoded, err := json.Marshal(raw); err == nil {
			if spec, err := contract.Decode(encoded); err == nil && strings.TrimSpace(spec.Prompt) != "" {
				return strings.TrimSpace(spec.Prompt)
			}
		}
	}
	for _, key := range []string{"composerContent", "prompt"} {
		if text := strings.TrimSpace(stringValue(meta[key])); text != "" {
			return text
		}
	}
	return ""
}

// cloudAgentMediaBatchItemArgs 把一个占位节点换算成等价的 generate_media 参数：
// 提示词来自条目覆盖或节点本身，参考来自节点现有入边（按连线顺序），首尾帧来自节点字段。
func cloudAgentMediaBatchItemArgs(doc map[string]any, args cloudAgentMediaBatchArgs, index int) (cloudAgentMediaArgs, error) {
	field := func(name string) string { return fmt.Sprintf("items[%d].%s", index, name) }
	item := args.Items[index]
	nodes, err := creationObjects(doc["nodes"])
	if err != nil {
		return cloudAgentMediaArgs{}, err
	}
	node := nodes[item.NodeID]
	if node == nil {
		return cloudAgentMediaArgs{}, cloudAgentFieldError(field("nodeId"), "not_found", "占位节点不在当前画布；请重新读取画布并使用真实节点 ID")
	}
	if stringValue(node["type"]) != args.Mode {
		return cloudAgentMediaArgs{}, cloudAgentFieldError(field("nodeId"), "invalid_value", "占位节点类型与本批生成模式不一致；图片、视频、音频请分批")
	}
	if reason, issue := cloudAgentMediaTargetIssue(node, args.Mode); reason != "" {
		return cloudAgentMediaArgs{}, cloudAgentFieldError(field("nodeId"), reason, issue+"；请把它移出本批")
	}
	prompt := strings.TrimSpace(item.Prompt)
	if prompt == "" {
		prompt = cloudAgentMediaBatchNodePrompt(node)
	}
	if prompt == "" {
		return cloudAgentMediaArgs{}, cloudAgentFieldError(field("prompt"), "required", "占位节点没有提示词；请在条目里提供 prompt，或先用 canvas_apply_ops 写入节点提示词")
	}
	a := cloudAgentMediaArgs{
		Mode: args.Mode, Prompt: prompt, LogicalModelID: args.LogicalModelID, ChannelID: args.ChannelID, ChannelModelKey: args.ChannelModelKey,
		Size: args.Size, Quality: args.Quality, ImageResolution: args.ImageResolution, ImageParameters: args.ImageParameters, NodeID: item.NodeID,
		Title: strings.TrimSpace(stringValue(node["title"])), ReferenceNodeIDs: []string{},
	}
	if a.Title == "" {
		a.Title = map[string]string{"image": "图片创作", "video": "视频创作", "audio": "音频创作"}[args.Mode]
	}
	if args.Mode == "video" {
		a.Duration = args.Duration
		if item.Duration > 0 {
			a.Duration = item.Duration
		}
		a.VideoGenerateAudio = args.VideoGenerateAudio
		a.VideoWatermark = args.VideoWatermark
		a.VideoArkPrivateAssetUpload = args.VideoArkPrivateAssetUpload
	}
	cloudAgentBatchItemReferences(doc, nodes, item.NodeID, &a)
	if len(a.ReferenceNodeIDs) > 16 {
		return cloudAgentMediaArgs{}, cloudAgentFieldError(field("nodeId"), "item_count", "占位节点的参考连线超过 16 个；请减少参考后再生成")
	}
	if args.Mode == "video" {
		meta, _ := node["metadata"].(map[string]any)
		for _, frame := range []struct {
			id     string
			target *string
		}{{stringValue(meta["videoStartFrameNodeId"]), &a.VideoStartFrameNodeID}, {stringValue(meta["videoEndFrameNodeId"]), &a.VideoEndFrameNodeID}} {
			if frame.id != "" && cloudAgentContainsString(a.ReferenceNodeIDs, frame.id) {
				*frame.target = frame.id
			}
		}
	}
	return a, nil
}

func cloudAgentMediaBatchItemCall(callID string, index int, a cloudAgentMediaArgs) (cloudAgentCall, error) {
	raw, err := json.Marshal(a)
	if err != nil {
		return cloudAgentCall{}, err
	}
	call := cloudAgentCall{ID: fmt.Sprintf("%s-item-%d", callID, index)}
	call.Function.Name, call.Function.Arguments = "generate_media", string(raw)
	return call, nil
}

// cloudAgentMediaBatchProbeState 让条目准入按"未进入审批"的路径解析模型与规格：
// 批次没有单条 Prepared，也不能复用单条审批的报价。
func cloudAgentMediaBatchProbeState(state *cloudAgentRuntime) *cloudAgentRuntime {
	probe := *state
	probe.Approval = nil
	probe.AutoPreparedMedia = nil
	probe.AutoPreparedCallHash = ""
	return &probe
}

// cloudAgentResolvedMediaSpecHash 与 cloudAgentResolvedMediaHash 相同但不含提示词：
// 重试允许改写提示词，模型、规格和参考必须保持批准时的样子。
func cloudAgentResolvedMediaSpecHash(task *model.Task) (string, error) {
	var input map[string]any
	if err := json.Unmarshal([]byte(task.InputJSON), &input); err != nil {
		return "", err
	}
	delete(input, "prompt")
	raw, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	clone := *task
	clone.InputJSON = string(raw)
	return cloudAgentResolvedMediaHash(&clone)
}

func cloudAgentMediaBatchResourceIDs(input map[string]any) []string {
	ids := []string{}
	seen := map[string]bool{}
	for _, field := range []string{"referenceImages", "referenceVideos", "referenceAudios"} {
		for _, ref := range creationMaps(input[field]) {
			key := stringValue(ref["storageKey"])
			if !strings.HasPrefix(key, "resource:") {
				continue
			}
			id := strings.TrimPrefix(key, "resource:")
			if id != "" && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	return ids
}

func cloudAgentCreditsLabel(micro int64) string {
	value := float64(micro) / float64(CreditScale)
	text := strconv.FormatFloat(math.Round(value*100)/100, 'f', -1, 64)
	return text + " 积分"
}

// cloudAgentMediaBatchItemAdmissionError 只把"去掉这一条就能继续"的问题交回模型修正；
// 预算、权限和模型不可用等问题保持原分类，由通用准入路径决定是否终止。
func cloudAgentMediaBatchItemAdmissionError(index int, err error) error {
	var argumentErr *cloudAgentArgumentError
	if errors.As(err, &argumentErr) {
		var fieldErr *cloudAgentFieldArgumentError
		if errors.As(err, &fieldErr) && !strings.HasPrefix(fieldErr.Field, "items[") {
			return cloudAgentFieldError(fmt.Sprintf("items[%d].%s", index, fieldErr.Field), fieldErr.Issue, cloudAgentSafeToolError(err))
		}
		return err
	}
	var admissionErr *cloudAgentMediaAdmissionError
	if errors.As(err, &admissionErr) {
		switch admissionErr.Reason {
		case "task_bound", "output_exists", "target_locked", "target_not_idle", "target_type_mismatch", "snapshot_conflict":
			return cloudAgentFieldError(fmt.Sprintf("items[%d].nodeId", index), admissionErr.Reason, cloudAgentSafeToolError(err)+"；请把它移出本批或重新读取画布")
		}
	}
	return err
}

type cloudAgentMediaBatchPrepared struct {
	plan  *cloudAgentMediaPlan
	item  cloudAgentMediaBatchItem
	model string
}

// requestCloudAgentMediaBatchApproval 逐条做无扣费准入，建好草稿节点后发出一张审批卡。

func cloudAgentBatchItemReferences(doc map[string]any, nodes map[string]map[string]any, nodeID string, a *cloudAgentMediaArgs) {
	seen := map[string]bool{}
	for _, edge := range creationMaps(doc["connections"]) {
		if stringValue(edge["toNodeId"]) != nodeID || stringValue(edge["fromHandleId"]) != "" || stringValue(edge["toHandleId"]) != "" {
			continue
		}
		fromID := stringValue(edge["fromNodeId"])
		source := nodes[fromID]
		if source == nil || seen[fromID] {
			continue
		}
		seen[fromID] = true
		switch {
		case cloudAgentCharacterNode(source):
			a.ReferenceNodeIDs = append(a.ReferenceNodeIDs, fromID)
		case stringValue(source["type"]) == "image" || stringValue(source["type"]) == "video" || stringValue(source["type"]) == "audio":
			a.ReferenceNodeIDs = append(a.ReferenceNodeIDs, fromID)
		case (stringValue(source["type"]) == "text" || stringValue(source["type"]) == "markdown") && a.SourceNodeID == "":
			a.SourceNodeID = fromID
		}
	}
}
