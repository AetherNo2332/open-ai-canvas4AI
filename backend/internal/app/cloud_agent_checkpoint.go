package app

import (
	"encoding/json"
	"errors"
	"fmt"

	"yingce/backend/internal/agentcontext"
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
	if err := agentcontext.ValidateMessageCount(len(run.Transcript), run.MessageCount); err != nil {
		return err
	}
	if c := state.ContextCompaction; c != nil && c.PiNative != nil && len(c.PiNative.Preparation) == 0 {
		return errors.New("Pi compaction preparation is missing")
	}
	events, eventSeqBase, err := cloudAgentRestoreJournal(run)
	if err != nil {
		return err
	}
	state.EventSeqBase = eventSeqBase
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

func cloudAgentRestoreJournal(run *model.CloudAgentExecution) ([]CloudAgentEvent, int, error) {
	events := make([]CloudAgentEvent, 0, len(run.Journal))
	window := make([]agentcontext.JournalEvent, 0, len(run.Journal))
	for _, row := range run.Journal {
		var event CloudAgentEvent
		if err := json.Unmarshal([]byte(row.EventJSON), &event); err != nil {
			return nil, 0, fmt.Errorf("decode Agent event: %w", err)
		}
		events = append(events, event)
		journalEvent := agentcontext.JournalEvent{
			RowSequence: row.Sequence,
			Sequence:    event.Seq,
			RunID:       event.RunID,
			EventID:     event.EventID,
		}
		if err := agentcontext.ValidateJournalEvent(run.ID, journalEvent); err != nil {
			return nil, 0, err
		}
		window = append(window, journalEvent)
	}
	eventSeqBase, err := agentcontext.JournalWatermark(run.EventCount, window)
	if err != nil {
		return nil, 0, err
	}
	return events, eventSeqBase, nil
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
	window := make([]agentcontext.JournalEvent, 0, len(state.Events))
	for _, event := range state.Events {
		window = append(window, agentcontext.JournalEvent{
			RowSequence: event.Seq,
			Sequence:    event.Seq,
			RunID:       event.RunID,
			EventID:     event.EventID,
		})
	}
	if err := agentcontext.ValidateJournalSequence(run.ID, state.EventSeqBase, window); err != nil {
		return fmt.Errorf("%w: %v", errCloudAgentCheckpoint, err)
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
