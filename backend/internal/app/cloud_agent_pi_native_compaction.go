package app

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"infinite-canvas/backend/internal/agentcontext"
	"infinite-canvas/backend/internal/kernel"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

// The preparation and model call receipts survive worker loss. SDK summarization
// runs in Node, but every provider call and checkpoint remains Go-owned.
type cloudAgentPiNativeCompaction struct {
	Preparation       json.RawMessage                   `json:"-"`
	PreparationDigest string                            `json:"preparationDigest"`
	MaxTokens         int                               `json:"maxTokens"`
	ActiveCall        string                            `json:"activeCall,omitempty"`
	Calls             map[string]cloudAgentPiNativeCall `json:"calls"`
	Checkpoint        *agentcontext.Checkpoint          `json:"checkpoint,omitempty"`
	Mode              string                            `json:"mode,omitempty"`
	Reason            string                            `json:"reason,omitempty"`
	Usage             json.RawMessage                   `json:"usage,omitempty"`
}

type cloudAgentPiNativeCall struct {
	Fingerprint string `json:"fingerprint"`
	TaskID      string `json:"taskId,omitempty"`
}

type piNativePreparation struct {
	ProtocolVersion  string            `json:"protocolVersion"`
	FirstKeptEntryID string            `json:"firstKeptEntryId"`
	TokensBefore     int               `json:"tokensBefore"`
	IsSplitTurn      bool              `json:"isSplitTurn"`
	Messages         []json.RawMessage `json:"messagesToSummarize"`
	Prefix           []json.RawMessage `json:"turnPrefixMessages"`
	PreviousSummary  string            `json:"previousSummary"`
	FileOps          struct {
		Read    []string `json:"read"`
		Written []string `json:"written"`
		Edited  []string `json:"edited"`
	} `json:"fileOps"`
}

func cloudAgentNativeUsageMatches(sent, stored json.RawMessage) bool {
	if len(sent) == 0 || string(sent) == "null" {
		return len(stored) == 0 || string(stored) == "null"
	}
	var a, b any
	if json.Unmarshal(sent, &a) != nil || json.Unmarshal(stored, &b) != nil {
		return false
	}
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func cloudAgentPrepareNativeCompaction(raw json.RawMessage, tokens int, source *cloudAgentPiCompactionSource, branch []cloudAgentPiCompactionEntry) (*cloudAgentPiNativeCompaction, error) {
	var prep piNativePreparation
	if len(raw) > 1<<20 || json.Unmarshal(raw, &prep) != nil || prep.FirstKeptEntryID == "" || prep.TokensBefore != tokens {
		return nil, fmt.Errorf("Pi 原生压缩准备信息无效")
	}
	index := -1
	for i, id := range source.MessageEntryIDs {
		if id == prep.FirstKeptEntryID && (stringField(source.Messages[i], "role") == "user" || stringField(source.Messages[i], "role") == "assistant") {
			index = i
			break
		}
	}
	if index < 0 {
		// Pi may retain only an invisible recovery context_edit suffix. No
		// context-producing entry may follow that boundary.
		for i, entry := range branch {
			if entry.ID != prep.FirstKeptEntryID || entry.Type != "context_edit" {
				continue
			}
			visibleAfter := false
			for _, suffix := range branch[i:] {
				for _, id := range source.MessageEntryIDs {
					if suffix.ID == id {
						visibleAfter = true
					}
				}
			}
			if !visibleAfter {
				index = len(source.Messages)
			}
		}
	}
	if index < 0 {
		return nil, fmt.Errorf("Pi 保留切点不在当前可见消息边界")
	}
	previous := ""
	for _, entry := range branch {
		if entry.Type == "compaction" {
			previous = entry.Summary
		}
	}
	if prep.PreviousSummary != previous {
		return nil, fmt.Errorf("Pi 原生压缩旧摘要与活动分支不一致")
	}
	if prep.ProtocolVersion != "" && prep.ProtocolVersion != "canvas-pi-preparation/v1" {
		return nil, fmt.Errorf("Pi 压缩准备协议不匹配")
	}
	start := 0
	if previous != "" {
		start = 2
	} // Go projects the previous checkpoint as user + acknowledgement.
	if index < start {
		return nil, fmt.Errorf("Pi 压缩不能重新切入旧检查点")
	}
	historyEnd := index
	if prep.IsSplitTurn {
		historyEnd = -1
		for i := index - 1; i >= start; i-- {
			if stringField(source.Messages[i], "role") == "user" {
				historyEnd = i
				break
			}
		}
		if historyEnd < start || len(prep.Prefix) == 0 {
			return nil, fmt.Errorf("Pi 长轮压缩缺少有效的轮次前缀")
		}
	} else if len(prep.Prefix) != 0 {
		return nil, fmt.Errorf("Pi 普通压缩不能包含轮次前缀")
	}
	match := func(sent []json.RawMessage, expected []map[string]any) bool {
		if len(sent) != len(expected) {
			return false
		}
		for i, raw := range sent {
			message, ok, err := cloudAgentPiEntryMessage(cloudAgentPiCompactionEntry{Type: "message", Message: raw})
			if err != nil || !ok {
				return false
			}
			want := map[string]any{}
			for k, v := range expected[i] {
				if k != cloudAgentContextSourceKey {
					want[k] = v
				}
			}
			a, _ := json.Marshal(message.Value)
			b, _ := json.Marshal(want)
			if string(a) != string(b) {
				return false
			}
		}
		return true
	}
	if !match(prep.Messages, source.Messages[start:historyEnd]) || !match(prep.Prefix, source.Messages[historyEnd:index]) {
		return nil, fmt.Errorf("Pi 压缩准备材料与持久会话不一致")
	}
	// A retained result must keep its call, and each retained call must keep its result.
	pending := map[string]bool{}
	for _, message := range source.Messages[index:] {
		for _, call := range creationMaps(message["tool_calls"]) {
			id := stringField(call, "id")
			if id == "" || pending[id] {
				return nil, fmt.Errorf("Pi 保留工具调用无效")
			}
			pending[id] = true
		}
		if stringField(message, "role") == "tool" {
			id := stringField(message, "tool_call_id")
			if !pending[id] {
				return nil, fmt.Errorf("Pi 保留工具结果缺少调用")
			}
			delete(pending, id)
		}
	}
	if len(pending) != 0 {
		return nil, fmt.Errorf("Pi 保留工具调用缺少结果")
	}
	source.FirstKeptEntryID, source.FirstKeptIndex = prep.FirstKeptEntryID, index
	return &cloudAgentPiNativeCompaction{Preparation: append(json.RawMessage(nil), raw...), PreparationDigest: cloudAgentTextDigest(string(raw)), Calls: map[string]cloudAgentPiNativeCall{}}, nil
}

func cloudAgentNativeCompactionView(compaction *cloudAgentContextCompaction) (*PiContextCompactionView, error) {
	native := compaction.PiNative
	view := &PiContextCompactionView{OperationID: compaction.PiOperationID, Status: "prepared",
		FirstKeptEntryID: compaction.PiFirstKeptEntryID, TokensBefore: compaction.PiTokensBefore,
		SourceDigest: compaction.PiSourceDigest, SessionRevision: compaction.PiSessionRevision,
		NativePreparation: native.Preparation, SummaryMaxTokens: native.MaxTokens}
	if native.Checkpoint == nil {
		return view, nil
	}
	framed, err := agentcontext.Frame(*native.Checkpoint)
	if err != nil {
		return nil, err
	}
	view.Status, view.Summary, view.CheckpointDigest = "succeeded", framed, cloudAgentTextDigest(framed)
	view.Mode, view.Reason, view.Fallback = native.Mode, native.Reason, native.Mode != "pi-native"
	view.Usage = native.Usage
	view.Details = map[string]any{"protocolVersion": cloudAgentPiCompactionProtocol, "operationId": view.OperationID,
		"sourceDigest": view.SourceDigest, "checkpointDigest": view.CheckpointDigest, "engine": "pi-native"}
	return view, nil
}

type PiNativeCompactionModelRequest struct {
	CallID       string `json:"callId"`
	SystemPrompt string `json:"systemPrompt"`
	Prompt       string `json:"prompt"`
	MaxTokens    int    `json:"maxTokens"`
}

type PiNativeCompactionModelView struct {
	Status string          `json:"status"`
	TaskID string          `json:"taskId"`
	Result json.RawMessage `json:"result,omitempty"`
}

func (s *Service) piNativeCompactionRun(userID, runID, owner, operationID string) (*model.CloudAgentExecution, cloudAgentRuntime, error) {
	run, err := s.piAgentLeasedRun(userID, runID, owner)
	if err != nil {
		return nil, cloudAgentRuntime{}, err
	}
	if cloudAgentRunTerminal(run.Status) {
		return nil, cloudAgentRuntime{}, kernel.Forbidden("Agent 运行已结束")
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		return nil, state, err
	}
	c := state.ContextCompaction
	if c == nil || c.PiOperationID != operationID || c.PiNative == nil {
		return nil, state, kernel.NotFound("Pi 原生压缩操作不存在")
	}
	session, _, err := s.repo.CloudAgentPiSession(userID, run.ConversationID)
	if err != nil {
		return nil, state, err
	}
	if session.Revision != c.PiSessionRevision || session.ActiveLeafID != c.PiSourceLeafID || session.ActiveRunID != runID {
		return nil, state, kernel.Forbidden("Pi 原生压缩源会话已变化")
	}
	return run, state, nil
}

func nativeCompactionCallIDs(prep piNativePreparation) []string {
	if prep.IsSplitTurn && len(prep.Prefix) > 0 {
		if len(prep.Messages) > 0 {
			return []string{"history", "turnPrefix"}
		}
		return []string{"turnPrefix"}
	}
	return []string{"history"}
}

func (s *Service) PiNativeContextCompactionModel(userID, runID, owner, operationID string, input PiNativeCompactionModelRequest) (*PiNativeCompactionModelView, error) {
	run, state, err := s.piNativeCompactionRun(userID, runID, owner, operationID)
	if err != nil {
		return nil, err
	}
	native := state.ContextCompaction.PiNative
	var prep piNativePreparation
	_ = json.Unmarshal(native.Preparation, &prep)
	allowed := false
	for _, id := range nativeCompactionCallIDs(prep) {
		if id == input.CallID {
			allowed = true
		}
	}
	if !allowed || input.MaxTokens < 1 || input.MaxTokens > native.MaxTokens || len(input.SystemPrompt) == 0 || len(input.SystemPrompt) > 16<<10 || len(input.Prompt) == 0 || len(input.Prompt) > 1<<20 {
		return nil, BadAuthRequest("Pi 摘要子调用参数无效")
	}
	raw, _ := json.Marshal(input)
	fingerprint := cloudAgentTextDigest(string(raw))
	call, exists := native.Calls[input.CallID]
	if exists && call.Fingerprint != fingerprint {
		return nil, kernel.Forbidden("Pi 摘要子调用重试内容不一致")
	}
	if native.Checkpoint != nil && !exists {
		return nil, kernel.Forbidden("Pi 原生压缩已完成")
	}
	if !exists {
		// Only one active task at a time; a restart replays completed calls before
		// advancing to the next native split-turn call.
		for _, previous := range native.Calls {
			task, e := s.repo.TaskForUser(userID, previous.TaskID)
			if e != nil {
				return nil, e
			}
			if task.Status == model.TaskStatusQueued || task.Status == model.TaskStatusRunning {
				return nil, kernel.Forbidden("Pi 上一个摘要子调用仍在执行")
			}
		}
		native.ActiveCall = input.CallID
		native.Calls[input.CallID] = cloudAgentPiNativeCall{Fingerprint: fingerprint}
		taskInput := map[string]any{"mode": "text", "prompt": input.Prompt,
			"agentRequests": map[string]any{"canonical": map[string]any{"systemPrompt": input.SystemPrompt,
				"messages": []map[string]any{{"role": "user", "content": input.Prompt}}, "tools": []any{}, "toolChoice": "auto"}},
			"config": map[string]any{"channelId": state.Request.ChannelID, "channelModelKey": state.Request.ChannelModelKey,
				"model": firstNonEmpty(state.Request.ChannelModelKey, state.Request.Model), "systemPrompt": input.SystemPrompt},
			"textOptions": map[string]any{"stream": false, "thinking": false, "maxOutputTokens": input.MaxTokens}}
		req := CreateTaskRequest{ProjectID: state.Request.CanvasID, Type: "canvas_text", Operation: cloudAgentContextCompactionOperation,
			Prompt: input.Prompt, Model: state.Request.Model, LogicalModelID: state.Request.LogicalModelID, Input: taskInput}
		if err := s.enqueueCloudAgentTask(run, &state, req, nil); err != nil {
			return nil, err
		}
		call = native.Calls[input.CallID]
	}
	task, err := s.repo.TaskForUser(userID, call.TaskID)
	if err != nil {
		return nil, err
	}
	view := &PiNativeCompactionModelView{Status: string(task.Status), TaskID: task.ID}
	if cloudAgentStepTimedOut(task) {
		view.Status = "failed"
		return view, s.failCloudAgentStepTimeout(run, &state, task)
	}
	if task.Status == model.TaskStatusSucceeded {
		if _, err := nativeSummaryText(task); err != nil {
			view.Status = "failed"
		} else {
			view.Result = json.RawMessage(task.ResultJSON)
		}
	}
	return view, nil
}

type PiNativeCompactionComplete struct {
	Summary  string          `json:"summary"`
	Fallback bool            `json:"fallback"`
	Usage    json.RawMessage `json:"usage,omitempty"`
}

func nativeSummaryText(task *model.Task) (string, error) {
	if task.Status != model.TaskStatusSucceeded {
		return "", fmt.Errorf("summary task failed")
	}
	var result struct {
		Text        string            `json:"text"`
		StopReason  string            `json:"stopReason"`
		StopKind    string            `json:"stopReasonKind"`
		Calls       []json.RawMessage `json:"toolCalls"`
		LegacyCalls []json.RawMessage `json:"tool_calls"`
	}
	if json.Unmarshal([]byte(task.ResultJSON), &result) != nil {
		return "", fmt.Errorf("invalid summary result")
	}
	stop := firstNonEmpty(result.StopKind, normalizeCloudAgentStopReason(result.StopReason))
	if (stop != "" && stop != "stop") || len(result.Calls)+len(result.LegacyCalls) > 0 || strings.TrimSpace(result.Text) == "" {
		return "", fmt.Errorf("incomplete or tool-bearing summary")
	}
	return result.Text, nil
}

func nativeFileSuffix(prep piNativePreparation) string {
	modified := map[string]bool{}
	for _, path := range append(prep.FileOps.Written, prep.FileOps.Edited...) {
		modified[path] = true
	}
	read := map[string]bool{}
	for _, path := range prep.FileOps.Read {
		if !modified[path] {
			read[path] = true
		}
	}
	sections := []string{}
	for _, group := range []struct {
		name  string
		paths map[string]bool
	}{{"read-files", read}, {"modified-files", modified}} {
		paths := []string{}
		for path := range group.paths {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		if len(paths) > 0 {
			sections = append(sections, "<"+group.name+">\n"+strings.Join(paths, "\n")+"\n</"+group.name+">")
		}
	}
	if len(sections) == 0 {
		return ""
	}
	return "\n\n" + strings.Join(sections, "\n\n")
}

func (s *Service) PiFinishNativeContextCompaction(userID, runID, owner, operationID string, input PiNativeCompactionComplete) (*PiContextCompactionView, error) {
	run, state, err := s.piNativeCompactionRun(userID, runID, owner, operationID)
	if err != nil {
		return nil, err
	}
	c := state.ContextCompaction
	native := c.PiNative
	var prep piNativePreparation
	_ = json.Unmarshal(native.Preparation, &prep)
	texts := map[string]string{}
	failed := false
	usage := map[string]float64{}
	for _, id := range nativeCompactionCallIDs(prep) {
		call, ok := native.Calls[id]
		if !ok {
			if input.Fallback && failed {
				break
			}
			return nil, BadAuthRequest("Pi 摘要子调用尚未提交")
		}
		task, e := s.repo.TaskForUser(userID, call.TaskID)
		if e != nil {
			return nil, e
		}
		if task.Status == model.TaskStatusQueued || task.Status == model.TaskStatusRunning {
			return nil, BadAuthRequest("Pi 摘要子调用尚未完成")
		}
		text, e := nativeSummaryText(task)
		if e != nil {
			failed = true
		}
		texts[id] = text
		var result struct {
			Usage map[string]json.RawMessage `json:"usage"`
		}
		if json.Unmarshal([]byte(task.ResultJSON), &result) == nil {
			for _, key := range []string{"input", "output", "cacheRead", "cacheWrite", "totalTokens"} {
				var count float64
				if raw, ok := result.Usage[key]; ok && json.Unmarshal(raw, &count) == nil && count >= 0 {
					usage[key] += count
				}
			}
		}
	}
	checkpoint := cloudAgentFallbackCheckpoint(&state)
	mode, reason := "pi-native", ""
	if failed {
		if !input.Fallback {
			return nil, BadAuthRequest("Pi 摘要结果不完整")
		}
		mode, reason = "fallback", "原生摘要未成功，已使用服务端保底检查点"
	} else {
		if input.Fallback {
			return nil, BadAuthRequest("成功摘要不能被未验证的降级请求替代")
		}
		expected := texts["history"]
		if prep.IsSplitTurn && len(prep.Prefix) > 0 {
			if len(prep.Messages) == 0 {
				expected = prep.PreviousSummary
				if expected == "" {
					expected = "No prior history."
				}
			}
			expected += "\n\n---\n\n**Turn Context (split turn):**\n\n" + texts["turnPrefix"]
		}
		expected += nativeFileSuffix(prep)
		if input.Summary != expected || !utf8.ValidString(input.Summary) {
			return nil, BadAuthRequest("Pi 合并摘要与模型子调用结果不一致或超限")
		}
		if len(input.Summary) > 32<<10 {
			mode, reason = "fallback", "原生摘要超过检查点预算，已使用服务端保底检查点"
		} else {
			checkpoint.HistorySummary = input.Summary
		}
	}
	checkpoint.CompactedTurnCount = c.TurnCount
	checkpoint = cloudAgentBoundCheckpoint(checkpoint)
	// Preserve the native narrative without the legacy per-field truncation; the
	// frame's total size remains bounded and execution facts come from Go only.
	if mode == "pi-native" {
		checkpoint.HistorySummary = input.Summary
		// The native narrative already carries creative history. Keep only the
		// server anchor here, rather than duplicating the raw history in two fields.
		checkpoint.ScriptDesign = truncateRunes(state.CreativeAnchor.UserPrompt, 1500)
	}
	if _, err := agentcontext.Frame(checkpoint); err != nil {
		return nil, BadAuthRequest("Pi 摘要检查点超限")
	}
	if native.Checkpoint != nil {
		a, _ := json.Marshal(native.Checkpoint)
		b, _ := json.Marshal(checkpoint)
		if string(a) != string(b) || native.Mode != mode {
			return nil, kernel.Forbidden("Pi 完成重试内容不一致")
		}
		return cloudAgentNativeCompactionView(c)
	}
	native.Checkpoint, native.Mode, native.Reason = &checkpoint, mode, reason
	if len(usage) > 0 {
		native.Usage, _ = json.Marshal(usage)
	}
	c.Status = "ready"
	if err := s.repo.MutateCloudAgent(userID, runID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		return cloudAgentSave(current, &state)
	}); err != nil {
		return nil, err
	}
	return cloudAgentNativeCompactionView(c)
}
