package app

import (
	"encoding/json"
	"log"
	"time"

	"infinite-canvas/backend/internal/model"
)

// cloudAgentEventRetention 是运行事件的保留期：过了就由清理任务删除。
// 事件不再占状态体积，因此保留期只服务审计与界面回溯，不影响 Agent 能跑多少轮。
const cloudAgentEventRetention = 30 * 24 * time.Hour

// cloudAgentRunEventDeltaLimit 是增量拉取（sinceSeq）单次返回的条数上限。
const cloudAgentRunEventDeltaLimit = 500

// cloudAgentRunEventsForView 组装运行详情要返回的事件。
//
//   - sinceSeq > 0：只返回该序号之后的增量（重连 / 滚动拉取），从事件表按 seq 取，
//     再用状态里的尾部缓存补齐尚未入库的部分；表里没有记录（升级前的旧运行）时回退到状态。
//   - sinceSeq == 0：默认返回最近 cloudAgentRunEventPageLimit 条 —— 尾部缓存 + 事件表里更早的部分。
func (s *Service) cloudAgentRunEventsForView(userID string, run *model.CloudAgentExecution, state *cloudAgentRuntime, sinceSeq, limit int) []CloudAgentEvent {
	if sinceSeq > 0 {
		rows, err := s.repo.CloudAgentRunEvents(userID, run.ID, sinceSeq, cloudAgentRunEventDeltaLimit)
		if err != nil {
			log.Printf("agent event delta %s: %v", run.ID, err)
			rows = nil
		}
		events := make([]CloudAgentEvent, 0, len(rows)+len(state.Events))
		last := sinceSeq
		for _, row := range rows {
			events = append(events, cloudAgentEventFromRow(row))
			last = row.Seq
		}
		for _, event := range state.Events {
			if event.Seq > last {
				events = append(events, event)
			}
		}
		if len(events) > 0 {
			return events
		}
		// 表里与尾部都没有更新的内容：走下面按游标过滤的兼容分支。
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
	rows, err := s.repo.CloudAgentRunEventsBefore(userID, run.ID, state.EventSeqBase+1, limit-len(tail))
	if err != nil {
		log.Printf("agent event page %s: %v", run.ID, err)
		return append([]CloudAgentEvent(nil), tail...)
	}
	events := make([]CloudAgentEvent, 0, len(rows)+len(tail))
	for _, row := range rows {
		events = append(events, cloudAgentEventFromRow(row))
	}
	return append(events, tail...)
}

func cloudAgentEventFromRow(row model.CloudAgentRunEvent) CloudAgentEvent {
	payload := map[string]any{}
	_ = json.Unmarshal([]byte(row.Payload), &payload)
	return CloudAgentEvent{EventID: row.EventID, RunID: row.RunID, Seq: row.Seq, Type: row.Type, Payload: payload, CreatedAt: row.CreatedAt}
}

// cloudAgentRunEventCount 给出"这个运行一共产生过多少事件"：以事件表条数为准，
// 再补上本次转移还没落库的那部分（内存里 seq 高于水位的事件）。
func (s *Service) cloudAgentRunEventCount(userID string, run *model.CloudAgentExecution, state *cloudAgentRuntime) int {
	stored, err := s.repo.CloudAgentRunEventCount(userID, run.ID)
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
