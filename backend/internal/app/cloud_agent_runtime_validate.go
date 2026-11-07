package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	as "yingce/backend/internal/agentservice"
	"yingce/backend/internal/model"
	"yingce/backend/internal/platform"
	"yingce/backend/internal/prompts"
)

// validateCloudAgentRuntime checks a decoded checkpoint before it is executed
// or saved. Sections run in a fixed order, so for any state the first failing
// check (and its error message) is the same as in the original single function.
func validateCloudAgentRuntime(run *model.CloudAgentExecution, state *cloudAgentRuntime) error {
	if run == nil || state == nil {
		return errors.New("Agent runtime state is missing")
	}
	if run.ID == "" {
		// Package-level tool tests use an in-memory runtime without a durable
		// execution identity. Durable rows are always validated below.
		return nil
	}
	for _, check := range []func(*model.CloudAgentExecution, *cloudAgentRuntime) error{
		validateCloudAgentRuntimeRequest,
		validateCloudAgentRuntimeCounters,
		validateCloudAgentRuntimeCursor,
		validateCloudAgentRuntimeTaskHistory,
		validateCloudAgentRuntimeAutoMedia,
		validateCloudAgentRuntimeEvents,
		validateCloudAgentRuntimeCalls,
		validateCloudAgentRuntimeApproval,
	} {
		if err := check(run, state); err != nil {
			return err
		}
	}
	return nil
}

func validateCloudAgentRuntimeRequest(_ *model.CloudAgentExecution, state *cloudAgentRuntime) error {
	if state.Execution != nil {
		return validateCloudAgentExecutionState(state)
	}
	if state.Request.CanvasID == "" || state.Request.Prompt == "" || state.Request.PermissionMode == "" {
		return errors.New("Agent runtime request is incomplete")
	}
	// 运行中可以切换权限，但预算按开局请求校验（例如自动执行开局带生成预算、中途收紧为只读）。
	startRequest := state.Request
	startRequest.PermissionMode = cloudAgentStartPermissionMode(state)
	if err := validateCloudAgentRequest(&startRequest); err != nil {
		return fmt.Errorf("invalid Agent runtime request: %w", err)
	}
	if !cloudAgentValidPermissionMode(state.Request.PermissionMode) {
		return errors.New("Agent runtime permission mode is invalid")
	}
	return validateCloudAgentPolicySnapshotStructure(state.Policy)
}

// validateCloudAgentExecutionState validates the business-neutral identity of a
// generic Pi run. Canvas policy, canonical prompt and tool disclosure are not
// part of this contract; the frozen definition and execution binding are.
func validateCloudAgentExecutionState(state *cloudAgentRuntime) error {
	execution := state.Execution
	if execution.ProtocolVersion != as.PiExecutionProtocolVersion {
		return errors.New("Agent execution protocol is unsupported")
	}
	if strings.TrimSpace(execution.SessionID) == "" || strings.TrimSpace(execution.DefinitionID) == "" ||
		strings.TrimSpace(execution.DefinitionVersion) == "" || strings.TrimSpace(execution.TriggerMessageID) == "" {
		return errors.New("Agent execution identity is incomplete")
	}
	if execution.Role != as.RoleBusiness && execution.Role != as.RoleSupervisor {
		return errors.New("Agent execution role is invalid")
	}
	scope := execution.Scope.Normalized()
	if scope.Application == "" || scope.SpaceType == "" || scope.SpaceID == "" {
		return errors.New("Agent execution scope is incomplete")
	}
	if strings.TrimSpace(state.Request.Prompt) == "" || !cloudAgentValidPermissionMode(state.Request.PermissionMode) {
		return errors.New("Agent execution request is invalid")
	}
	if err := validateAgentExecutionBinding(execution.Binding); err != nil {
		return err
	}
	if execution.Role == as.RoleBusiness && len(execution.Tools) == 0 {
		return errors.New("Agent execution has no tools")
	}
	for _, tool := range execution.Tools {
		if strings.TrimSpace(tool.Name) == "" || len(tool.Parameters) == 0 {
			return errors.New("Agent execution tool is invalid")
		}
	}
	return nil
}

func validateAgentExecutionBinding(binding *as.ExecutionBinding) error {
	if binding == nil || strings.TrimSpace(binding.WorkItemID) == "" {
		return nil
	}
	if strings.TrimSpace(binding.AssignmentID) == "" || binding.AssignmentEpoch <= 0 {
		return errors.New("Agent execution assignment is incomplete")
	}
	if strings.TrimSpace(binding.BudgetGrantID) == "" || binding.AuthorizationVersion <= 0 {
		return errors.New("Agent execution work binding is incomplete")
	}
	return nil
}

// validateCloudAgentRuntimeCounters checks step, budget and call-limit counters
// and the per-key read and inspection histories.
func validateCloudAgentRuntimeCounters(_ *model.CloudAgentExecution, state *cloudAgentRuntime) error {
	if state.Step < 0 || state.Generations < 0 || state.VideoSeconds < 0 {
		return errors.New("Agent runtime budget or step is invalid")
	}
	if state.ImageInspectCalls < 0 {
		return errors.New("Agent runtime image inspection budget is invalid")
	}
	if state.ReadToolCalls < 0 {
		return errors.New("Agent runtime read tool budget is invalid")
	}
	limits := state.CallLimits
	if limits.ToolCallsPerStep < 0 || limits.ToolCallsPerStep > platform.MaxRuntimeAgentToolCallsPerStep ||
		limits.ReadCallsPerRun < 0 || limits.ReadCallsPerRun > platform.MaxRuntimeAgentReadCallsPerRun ||
		limits.StepsPerRun < 0 || limits.StepsPerRun > platform.MaxRuntimeAgentStepsPerRun {
		return errors.New("Agent runtime call limits are invalid")
	}
	return validateCloudAgentRuntimeHistoryCounts(state)
}

// validateCloudAgentRuntimeHistoryCounts checks the per-node and per-key
// inspection and read replay counters, then the generation budget.
func validateCloudAgentRuntimeHistoryCounts(state *cloudAgentRuntime) error {
	for nodeID, count := range state.ImageInspectCounts {
		if strings.TrimSpace(nodeID) == "" || count < 0 {
			return errors.New("Agent runtime image inspection counts are invalid")
		}
	}
	for key, count := range state.ImageInspectionReads {
		if strings.TrimSpace(key) == "" || count < 0 {
			return errors.New("Agent runtime image inspection read history is invalid")
		}
	}
	for key, count := range state.ToolReadReplays {
		if strings.TrimSpace(key) == "" || count < 0 {
			return errors.New("Agent runtime read replay history is invalid")
		}
	}
	if (state.Request.Budget.MaxGenerationTasks > 0 && state.Generations > state.Request.Budget.MaxGenerationTasks) || (state.Request.Budget.MaxVideoSeconds > 0 && state.VideoSeconds > state.Request.Budget.MaxVideoSeconds) {
		return errors.New("Agent runtime generation budget is invalid")
	}
	return nil
}

// validateCloudAgentRuntimeCursor checks the call cursor, tool repair groups,
// environment conflicts and the compacted-history bound.
func validateCloudAgentRuntimeCursor(_ *model.CloudAgentExecution, state *cloudAgentRuntime) error {
	// Checkpoint structure uses the absolute policy bound. A lowered live limit
	// must not make a previously admitted batch impossible to decode or replay.
	if state.CallIndex < 0 || state.CallIndex > len(state.Calls) || len(state.Calls) > platform.MaxRuntimeAgentToolCallsPerStep {
		return errors.New("Agent runtime call cursor is invalid")
	}
	for toolName, repair := range state.ToolRepairs {
		if toolName == "" || utf8.RuneCountInString(toolName) > 80 || repair.Attempt < 1 || repair.Attempt > cloudAgentToolAttemptLimit || repair.GroupID == "" {
			return errors.New("Agent runtime tool repair state is invalid")
		}
		if err := validateCloudAgentID(repair.GroupID, "工具纠错组 ID", 240); err != nil {
			return err
		}
	}
	if err := validateCloudAgentRuntimeEnvironmentConflicts(state); err != nil {
		return err
	}
	if state.HistoryIncludesCurrent && len(state.Canonical.Messages) < len(state.TextHistory) {
		return errors.New("Agent compacted history exceeds canonical transcript")
	}
	return nil
}

// validateCloudAgentRuntimeTaskHistory checks the first-step wait, task ID
// uniqueness and that active, media and pending tasks are in the history.
func validateCloudAgentRuntimeTaskHistory(_ *model.CloudAgentExecution, state *cloudAgentRuntime) error {
	if state.Execution != nil && len(state.TaskIDs) == 0 {
		return validateGenericPiTaskHistory(state)
	}
	if len(state.TaskIDs) == 0 {
		return cloudAgentFirstStepWaitError(state)
	} else if cloudAgentAwaitingFirstStep(state) {
		if err := validateFirstStepTaskHistory(state); err != nil {
			return err
		}
	}
	return validateTaskHistoryIDs(state)
}

// validateGenericPiTaskHistory permits a generic run to exist before its first
// Go model task is admitted. It has no canvas holding task to place in TaskIDs.
func validateGenericPiTaskHistory(state *cloudAgentRuntime) error {
	if state.ActiveTaskID != "" || state.MediaTaskID != "" || state.PiNoToolTaskID != "" ||
		state.Step != 0 || state.CallIndex != 0 || len(state.Calls) != 0 {
		return errors.New("Agent runtime first-step wait has advanced work")
	}
	return nil
}

func validateFirstStepTaskHistory(state *cloudAgentRuntime) error {
	// The awaiting phase may contain an internal compaction task, but the model
	// step itself must not have advanced before the first-step contract freezes.
	if state.Step != 0 || state.CallIndex != 0 || len(state.Calls) != 0 {
		return errors.New("Agent runtime first-step wait has advanced work")
	}
	return nil
}

func validateTaskHistoryIDs(state *cloudAgentRuntime) error {
	seenTasks := make(map[string]struct{}, len(state.TaskIDs))
	for _, taskID := range state.TaskIDs {
		if err := validateCloudAgentID(taskID, "任务 ID", 80); err != nil {
			return err
		}
		if _, exists := seenTasks[taskID]; exists {
			return errors.New("Agent runtime task history contains duplicates")
		}
		seenTasks[taskID] = struct{}{}
	}
	if state.ActiveTaskID != "" && !cloudAgentContainsString(state.TaskIDs, state.ActiveTaskID) {
		return errors.New("Agent runtime active task is not in task history")
	}
	if state.MediaTaskID != "" && !cloudAgentContainsString(state.TaskIDs, state.MediaTaskID) {
		return errors.New("Agent runtime media task is not in task history")
	}
	for _, item := range state.PendingGenerations {
		if !cloudAgentContainsString(state.TaskIDs, item.TaskID) {
			return errors.New("Agent runtime pending generation is not in task history")
		}
	}
	return nil
}

func validateCloudAgentRuntimeAutoMedia(_ *model.CloudAgentExecution, state *cloudAgentRuntime) error {
	if state.AutoPreparedMedia == nil {
		return nil
	}
	if state.AutoPreparedCallHash == "" || state.CallIndex < 0 || state.CallIndex >= len(state.Calls) {
		return errors.New("Agent runtime auto media preparation is invalid")
	}
	current := state.Calls[state.CallIndex]
	if current.Function.Name != "generate_media" {
		return errors.New("Agent runtime auto media preparation is not attached to a media call")
	}
	if state.AutoPreparedCallHash != cloudAgentApprovalCallHash(current) {
		return errors.New("Agent runtime auto media preparation does not match current call")
	}
	return nil
}

func validateCloudAgentRuntimeEvents(run *model.CloudAgentExecution, state *cloudAgentRuntime) error {
	if state.Decisions == nil || state.Events == nil {
		return errors.New("Agent runtime maps are missing")
	}
	if state.EventSeqBase < 0 {
		return errors.New("Agent runtime event watermark is invalid")
	}
	// 内存里只有"最近一窗 + 本次转移新产生的部分"，超过健全上限说明窗口没有按
	// CloudAgentJournalWindow 载入（或序号基准算错），此时不能继续推进。
	if len(state.Events) > cloudAgentEventWindowSanityLimit {
		return errors.New("Agent runtime event window is too large")
	}
	for index, event := range state.Events {
		if event.RunID != run.ID || event.Seq != state.EventSeqBase+index+1 || event.EventID == "" || event.Type == "" || event.Payload == nil || event.CreatedAt.IsZero() {
			return errors.New("Agent runtime event history is invalid")
		}
		if err := validateCloudAgentID(event.EventID, "事件 ID", 240); err != nil {
			return err
		}
		raw, err := json.Marshal(event.Payload)
		if err != nil || len(raw) > cloudAgentEventPayloadLimitBytes {
			return errors.New("Agent runtime event payload is too large")
		}
	}
	return nil
}

func validateCloudAgentRuntimeCalls(run *model.CloudAgentExecution, state *cloudAgentRuntime) error {
	for index, call := range state.Calls {
		if err := validateCloudAgentID(call.ID, "工具调用 ID", 160); err != nil || call.Function.Name == "" || utf8.RuneCountInString(call.Function.Name) > 80 || !utf8.ValidString(call.Function.Name) {
			return errors.New("Agent runtime tool call is invalid")
		}
		if len(call.Function.Arguments) > 32000 {
			return errors.New("Agent runtime tool arguments are too large")
		}
		if err := decodeCloudAgentJSONObject(call.Function.Arguments, &map[string]any{}); err != nil && (run.Engine != "pi" || !piHasRejectedArgumentState(state, index)) {
			return errors.New("Agent runtime tool arguments are invalid")
		}
	}
	return nil
}

func validateCloudAgentRuntimeApproval(_ *model.CloudAgentExecution, state *cloudAgentRuntime) error {
	if state.Approval != nil {
		if err := validateCloudAgentID(state.Approval.ID, "审批 ID", 200); err != nil || state.CallIndex >= len(state.Calls) {
			return errors.New("Agent runtime approval is invalid")
		}
		current := state.Calls[state.CallIndex]
		if state.Approval.Call.ID != current.ID || state.Approval.Call.Function.Name != current.Function.Name || state.Approval.Call.Function.Arguments != current.Function.Arguments {
			return errors.New("Agent runtime approval does not match current call")
		}
		if state.Approval.Decision != "" && state.Approval.Decision != "approve" && state.Approval.Decision != "reject" {
			return errors.New("Agent runtime approval decision is invalid")
		}
	}
	if state.CallIndex == len(state.Calls) && state.Approval != nil {
		return errors.New("Agent runtime has approval without a pending call")
	}
	return nil
}

func validateCloudAgentPolicySnapshot(snapshot cloudAgentPolicySnapshot) error {
	if err := validateCloudAgentPolicySnapshotStructure(snapshot); err != nil {
		return err
	}
	if snapshot.CompilerVersion != cloudAgentCompilerVersion {
		return errors.New("Agent runtime policy compiler is unsupported")
	}
	if snapshot.CapabilitySetVersion != cloudAgentCapabilitySetVersion {
		return errors.New("Agent runtime capability contract is unsupported")
	}
	if snapshot.ReasoningMode != "off" && snapshot.ReasoningMode != "auto" && snapshot.ReasoningMode != "deep" {
		return errors.New("Agent runtime reasoning mode is invalid")
	}
	system, media, err := prompts.LoadAgentPolicies()
	if err != nil {
		return fmt.Errorf("load Agent runtime policies: %w", err)
	}
	if snapshot.SystemPolicyID != system.ID || snapshot.SystemPolicyVersion != system.Version || snapshot.MediaPolicyID != media.ID || snapshot.MediaPolicyVersion != media.Version {
		return errors.New("Agent runtime policy version is unsupported")
	}
	// An existing run keeps its admitted prompt and tool schema. Never resume it
	// against changed policies or capabilities under an unchanged version label.
	if snapshot.SystemPolicyHash != system.Hash || snapshot.MediaPolicyHash != media.Hash || snapshot.CapabilitySetHash != cloudAgentCapabilitySetHash() {
		return errors.New("Agent runtime policy or capability contract has changed")
	}
	return nil
}

// validateCloudAgentPolicySnapshotStructure validates the frozen historical
// record without treating the current binary as its authorization source.
// Version/hash equality with the current runtime is checked separately, only
// when an old run is about to execute again.
func validateCloudAgentPolicySnapshotStructure(snapshot cloudAgentPolicySnapshot) error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{"policy compiler", snapshot.CompilerVersion},
		{"system policy ID", snapshot.SystemPolicyID},
		{"media policy ID", snapshot.MediaPolicyID},
		{"capability set version", snapshot.CapabilitySetVersion},
	} {
		if strings.TrimSpace(field.value) == "" || !utf8.ValidString(field.value) || utf8.RuneCountInString(field.value) > 120 {
			return fmt.Errorf("Agent runtime %s is invalid", field.name)
		}
	}
	if snapshot.SystemPolicyVersion <= 0 || snapshot.MediaPolicyVersion <= 0 {
		return errors.New("Agent runtime policy version is invalid")
	}
	if snapshot.ReasoningMode != "off" && snapshot.ReasoningMode != "auto" && snapshot.ReasoningMode != "deep" {
		return errors.New("Agent runtime reasoning mode is invalid")
	}
	for _, field := range []struct {
		name  string
		value string
	}{
		{"system policy hash", snapshot.SystemPolicyHash},
		{"media policy hash", snapshot.MediaPolicyHash},
		{"capability set hash", snapshot.CapabilitySetHash},
	} {
		if !cloudAgentSHA256(field.value) {
			return fmt.Errorf("Agent runtime %s is invalid", field.name)
		}
	}
	return nil
}

func cloudAgentSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}

func cloudAgentContainsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
