package app

import (
	"encoding/json"
	"errors"
	"fmt"

	"yingce/backend/internal/model"
)

// cloudAgentRestoreTranscript 从消息表与事件窗口重建内存态。
//
// 事件全量在上游 cloud_agent_event_records；hydrate 只载入最近一窗
// （repository.CloudAgentJournalWindow），所以这里校验的是"窗口的一致性"而不是全量：
//   - 消息按 kind 分别连续 1..n，且条数等于 run.MessageCount；
//   - 事件窗口的**最后一条** seq 必须等于 run.EventCount —— 否则说明有行没读到
//     （删一半、写失败），宁可判"记录不完整"也不带着缺口继续跑；
//   - EventSeqBase = EventCount - len(窗口)，窗口为空时只能是 EventCount==0。
func cloudAgentRestoreTranscript(run *model.CloudAgentExecution, state *cloudAgentRuntime) error {
	canonical, history, err := cloudAgentRestoreMessages(run, state)
	if err != nil {
		return err
	}
	if len(run.Transcript) != run.MessageCount {
		return fmt.Errorf("Agent execution transcript is incomplete: rows=%d count=%d", len(run.Transcript), run.MessageCount)
	}
	if c := state.ContextCompaction; c != nil && c.PiNative != nil && len(c.PiNative.Preparation) == 0 {
		return errors.New("Pi compaction preparation is missing")
	}
	events, err := cloudAgentRestoreJournal(run)
	if err != nil {
		return err
	}
	state.EventSeqBase = run.EventCount - len(events)
	if state.EventSeqBase < 0 {
		return fmt.Errorf("Agent execution journal watermark is invalid: count=%d rows=%d", run.EventCount, len(events))
	}
	state.Canonical.Messages, state.TextHistory = canonical, history
	state.Events = events
	return nil
}

func cloudAgentRestoreMessages(run *model.CloudAgentExecution, state *cloudAgentRuntime) ([]map[string]any, []providerTextMessage, error) {
	canonical := make([]map[string]any, 0, len(run.Transcript))
	history := make([]providerTextMessage, 0, len(run.Transcript))
	piCount := 0
	for _, record := range run.Transcript {
		switch record.Kind {
		case cloudAgentMessageKindCanonical:
			var message map[string]any
			if err := json.Unmarshal([]byte(record.MessageJSON), &message); err != nil {
				return nil, nil, fmt.Errorf("decode Agent message: %w", err)
			}
			if record.Sequence != len(canonical)+1 {
				return nil, nil, errors.New("Agent message sequence is incomplete")
			}
			canonical = append(canonical, message)
		case cloudAgentMessageKindHistory:
			var message providerTextMessage
			if err := json.Unmarshal([]byte(record.MessageJSON), &message); err != nil {
				return nil, nil, fmt.Errorf("decode Agent history: %w", err)
			}
			if record.Sequence != len(history)+1 {
				return nil, nil, errors.New("Agent history sequence is incomplete")
			}
			history = append(history, message)
		case cloudAgentMessageKindPi:
			if !json.Valid([]byte(record.MessageJSON)) || record.Sequence != piCount+1 {
				return nil, nil, errors.New("Pi Agent message sequence is incomplete")
			}
			piCount++
		case cloudAgentMessageKindCompaction:
			c := state.ContextCompaction
			if c == nil || c.PiNative == nil || record.Sequence != 1 || len(c.PiNative.Preparation) > 0 ||
				!json.Valid([]byte(record.MessageJSON)) || cloudAgentTextDigest(record.MessageJSON) != c.PiNative.PreparationDigest {
				return nil, nil, errors.New("Pi compaction preparation is invalid")
			}
			c.PiNative.Preparation = json.RawMessage(record.MessageJSON)
		default:
			return nil, nil, errors.New("Agent message kind is unsupported")
		}
	}
	return canonical, history, nil
}

func cloudAgentRestoreJournal(run *model.CloudAgentExecution) ([]CloudAgentEvent, error) {
	events := make([]CloudAgentEvent, 0, len(run.Journal))
	for _, row := range run.Journal {
		var event CloudAgentEvent
		if err := json.Unmarshal([]byte(row.EventJSON), &event); err != nil {
			return nil, fmt.Errorf("decode Agent event: %w", err)
		}
		// 事件身份必须自洽：seq/runId/eventId 任何一处与行不一致，就说明记录被动过。
		if event.Seq != row.Sequence || event.RunID != run.ID || event.EventID != fmt.Sprintf("%s:%d", run.ID, row.Sequence) {
			return nil, errors.New("Agent event identity is invalid")
		}
		events = append(events, event)
	}
	// 上游修正（#600）：窗口为空但水位非零 = 有行没读到，不能把"一行都没有"当成空日志。
	if len(events) == 0 && run.EventCount != 0 {
		return nil, errors.New("Agent execution journal is incomplete")
	}
	if len(events) > 0 && events[len(events)-1].Seq != run.EventCount {
		return nil, fmt.Errorf("Agent execution journal is incomplete: lastSeq=%d count=%d", events[len(events)-1].Seq, run.EventCount)
	}
	return events, nil
}

// cloudAgentSave validates the runtime and writes it into the execution row:
// state JSON, row indexes, the event window and the transcript. Nothing is
// assigned to run before validation and encoding of the state succeed.
func cloudAgentSave(run *model.CloudAgentExecution, state *cloudAgentRuntime) error {
	if run == nil || state == nil {
		return errors.New("Agent runtime state is missing")
	}
	if err := cloudAgentValidateSaveState(run, state); err != nil {
		return err
	}
	checkpoint := *state
	checkpoint.Canonical.Messages = nil
	checkpoint.TextHistory = nil
	checkpoint.Events = nil
	raw, err := json.Marshal(checkpoint)
	if err != nil {
		return fmt.Errorf("%w: %v", errCloudAgentCheckpoint, err)
	}
	if len(raw) > cloudAgentStateHardLimitBytes {
		return fmt.Errorf("%w: Agent 状态超过 512KB 上限", errCloudAgentCheckpoint)
	}
	cloudAgentApplySaveIndexes(run, state)
	if err := cloudAgentEncodeJournal(run, state); err != nil {
		return err
	}
	if err := cloudAgentEncodeTranscript(run, state); err != nil {
		return err
	}
	// 水位 = 窗口之前已入库的条数 + 本次载入/新增的窗口条数，必须与事件表里的
	// 最大 seq 一致，否则下一次载入的窗口与水位会对不上。
	run.CheckpointVersion, run.EventCount, run.MessageCount = cloudAgentCheckpointVersion, state.EventSeqBase+len(state.Events), len(run.Transcript)
	run.StateJSON = string(raw)
	return nil
}

func cloudAgentValidateSaveState(run *model.CloudAgentExecution, state *cloudAgentRuntime) error {
	if run.ID == "" {
		return nil
	}
	if err := validateCloudAgentRuntime(run, state); err != nil {
		return fmt.Errorf("%w: %v", errCloudAgentCheckpoint, err)
	}
	for index, event := range state.Events {
		sequence := state.EventSeqBase + index + 1
		if event.Seq != sequence || event.RunID != run.ID || event.EventID != fmt.Sprintf("%s:%d", run.ID, sequence) {
			return fmt.Errorf("%w: Agent event sequence or identity is invalid", errCloudAgentCheckpoint)
		}
	}
	return nil
}

func cloudAgentApplySaveIndexes(run *model.CloudAgentExecution, state *cloudAgentRuntime) {
	run.CanvasID, run.ActiveTaskID = state.Request.CanvasID, state.ActiveTaskID
	// media_task_id 列是“仍有待回写生成”的索引：巡检据此找出需要回写的运行（含已结束的运行）。
	run.MediaTaskID = cloudAgentPendingGenerationIndexID(state)
	run.ParentID = state.ParentID
	if run.Status == "waiting_approval" && state.Approval != nil {
		run.RuntimePhase = "waiting_approval"
		run.WaitKind = "approval"
		run.WaitID = state.Approval.ID
		run.WaitReason = "等待用户审批"
	}
	if run.Title == "" {
		run.Title = truncateRunes(state.Request.Prompt, 80)
	}
}

// 事件按"窗口 + 水位"落库：run.Journal 只是内存窗口的映射（窗口之外的历史仍在
// 事件表里），写入侧（repository.MutateCloudAgent）只追加 seq > 旧水位的行，
// 因此窗口左移不会丢历史。
func cloudAgentEncodeJournal(run *model.CloudAgentExecution, state *cloudAgentRuntime) error {
	run.Journal = make([]model.CloudAgentEventRecord, 0, len(state.Events))
	for _, event := range state.Events {
		body, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("%w: encode Agent event: %v", errCloudAgentCheckpoint, err)
		}
		run.Journal = append(run.Journal, model.CloudAgentEventRecord{RunID: run.ID, UserID: run.UserID, Sequence: event.Seq, EventJSON: string(body), CreatedAt: event.CreatedAt})
	}
	return nil
}

// cloudAgentEncodeTranscript keeps the Pi rows as they are, then writes the Pi
// native compaction preparation, canonical messages and text history.
func cloudAgentEncodeTranscript(run *model.CloudAgentExecution, state *cloudAgentRuntime) error {
	piRecords := make([]model.CloudAgentMessageRecord, 0)
	for _, record := range run.Transcript {
		if record.Kind == cloudAgentMessageKindPi {
			piRecords = append(piRecords, record)
		}
	}
	run.Transcript = make([]model.CloudAgentMessageRecord, 0, len(state.Canonical.Messages)+len(state.TextHistory)+len(piRecords))
	run.Transcript = append(run.Transcript, piRecords...)
	if c := state.ContextCompaction; c != nil && c.PiNative != nil {
		if len(c.PiNative.Preparation) == 0 || cloudAgentTextDigest(string(c.PiNative.Preparation)) != c.PiNative.PreparationDigest {
			return errors.New("Pi compaction preparation is missing or changed")
		}
		run.Transcript = append(run.Transcript, model.CloudAgentMessageRecord{RunID: run.ID, UserID: run.UserID, Kind: cloudAgentMessageKindCompaction, Sequence: 1, MessageJSON: string(c.PiNative.Preparation)})
	}
	for index, message := range state.Canonical.Messages {
		body, err := json.Marshal(message)
		if err != nil {
			return fmt.Errorf("%w: encode Agent message: %v", errCloudAgentCheckpoint, err)
		}
		run.Transcript = append(run.Transcript, model.CloudAgentMessageRecord{RunID: run.ID, UserID: run.UserID, Kind: cloudAgentMessageKindCanonical, Sequence: index + 1, MessageJSON: string(body)})
	}
	for index, message := range state.TextHistory {
		body, err := json.Marshal(message)
		if err != nil {
			return fmt.Errorf("%w: encode Agent history: %v", errCloudAgentCheckpoint, err)
		}
		run.Transcript = append(run.Transcript, model.CloudAgentMessageRecord{RunID: run.ID, UserID: run.UserID, Kind: cloudAgentMessageKindHistory, Sequence: index + 1, MessageJSON: string(body)})
	}
	return nil
}
