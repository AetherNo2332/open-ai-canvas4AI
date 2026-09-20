package app

import (
	"encoding/json"
	"fmt"
	"log"

	"infinite-canvas/backend/internal/model"
)

// cloudAgentRunEventDeltaLimit 是增量拉取（sinceSeq）单次返回的条数上限。
const cloudAgentRunEventDeltaLimit = 500

// cloudAgentRunEventsForView 组装运行详情要返回的事件。
//
// 事件全量落在上游的 cloud_agent_event_records（EventJSON 整条 JSON，主键 run_id+sequence）。
// 内存里只有最近一窗（repository.CloudAgentJournalWindow），因此：
//
//   - sinceSeq > 0：只返回该序号之后的增量（重连 / 滚动拉取），从事件表按 seq 取，
//     再用内存窗口里尚未入库的部分补齐；表里没有记录时回退到窗口过滤。
//   - sinceSeq == 0：默认返回最近 cloudAgentRunEventPageLimit 条 —— 内存窗口 + 事件表里更早的部分。
//
// 一律走 `WHERE run_id=? AND sequence > ? ORDER BY sequence LIMIT ?` 与计数查询，
// 不为这些字段全量加载 journal。
func (s *Service) cloudAgentRunEventsForView(userID string, run *model.CloudAgentExecution, state *cloudAgentRuntime, sinceSeq, limit int) []CloudAgentEvent {
	if sinceSeq > 0 {
		rows, err := s.repo.CloudAgentEventRecords(userID, run.ID, sinceSeq, cloudAgentRunEventDeltaLimit)
		if err != nil {
			log.Printf("agent event delta %s: %v", run.ID, err)
			rows = nil
		}
		events := make([]CloudAgentEvent, 0, len(rows)+len(state.Events))
		last := sinceSeq
		for _, row := range rows {
			events = append(events, cloudAgentEventFromRecord(row))
			last = row.Sequence
		}
		for _, event := range state.Events {
			if event.Seq > last {
				events = append(events, event)
			}
		}
		if len(events) > 0 {
			return events
		}
		// 表里与窗口都没有更新的内容：走下面按游标过滤的兼容分支。
		events = make([]CloudAgentEvent, 0, len(state.Events))
		for _, event := range state.Events {
			if event.Seq > sinceSeq {
				events = append(events, event)
			}
		}
		return events
	}
	tail := state.Events
	if limit <= 0 {
		limit = cloudAgentRunEventPageLimit
	}
	if len(tail) >= limit {
		return append([]CloudAgentEvent(nil), tail[len(tail)-limit:]...)
	}
	// 窗口不够：从事件表补齐更早的部分。表里为空（旧检查点的运行）时保持只有窗口。
	rows, err := s.repo.CloudAgentEventRecordsBefore(userID, run.ID, state.EventSeqBase+1, limit-len(tail))
	if err != nil {
		log.Printf("agent event page %s: %v", run.ID, err)
		return append([]CloudAgentEvent(nil), tail...)
	}
	events := make([]CloudAgentEvent, 0, len(rows)+len(tail))
	for _, row := range rows {
		events = append(events, cloudAgentEventFromRecord(row))
	}
	return append(events, tail...)
}

// cloudAgentEventFromRecord 把上游事件行（EventJSON 整条 JSON）解成对外契约结构。
func cloudAgentEventFromRecord(row model.CloudAgentEventRecord) CloudAgentEvent {
	var event CloudAgentEvent
	if err := json.Unmarshal([]byte(row.EventJSON), &event); err != nil || event.EventID == "" {
		// 行存在但解不出来：至少按行给出身份，避免整页读失败（内容确实不可用）。
		return CloudAgentEvent{EventID: fmt.Sprintf("%s:%d", row.RunID, row.Sequence), RunID: row.RunID, Seq: row.Sequence, Type: "unreadable_event", Payload: map[string]any{"text": "该事件记录无法解析，请以服务端画布与任务状态为准"}, CreatedAt: row.CreatedAt}
	}
	if event.RunID == "" {
		event.RunID = row.RunID
	}
	if event.Seq == 0 {
		event.Seq = row.Sequence
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = row.CreatedAt
	}
	return event
}

// cloudAgentRunEventCount 给出"这个运行一共产生过多少事件"：以事件表条数为准，
// 再补上本次转移还没落库的那部分（内存里 seq 高于水位的事件）。
func (s *Service) cloudAgentRunEventCount(userID string, run *model.CloudAgentExecution, state *cloudAgentRuntime) int {
	stored, err := s.repo.CloudAgentEventRecordCount(userID, run.ID)
	if err != nil {
		stored = 0
	}
	total := int(stored)
	if pending := state.EventSeqBase + len(state.Events); pending > total {
		total = pending
	}
	if run != nil && run.EventCount > total {
		total = run.EventCount
	}
	return total
}
