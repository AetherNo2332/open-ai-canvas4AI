package app

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"infinite-canvas/backend/internal/model"
)

// cloudAgentEventRetention 是运行事件的保留期：过了就由清理任务删除。
// 事件不再占状态体积，因此保留期只服务审计与界面回溯，不影响 Agent 能跑多少轮。
const cloudAgentEventRetention = 30 * 24 * time.Hour

// cloudAgentRunEventDeltaLimit 是增量拉取（sinceSeq）单次返回的条数上限。
const cloudAgentRunEventDeltaLimit = 500

// cloudAgentFlushRunEvents 把内存里尚未入库的事件写进 cloud_agent_run_events，然后把状态里的
// 事件裁剪到尾部缓存并推进水位。
//
// 顺序刻意是"先入库、后裁剪"：插入失败时尾巴还留在状态里，下一次再试，不会丢记录；
// 唯一索引 (run_id, seq) + DO NOTHING 让重复写入天然幂等。升级前的事件全都躺在 state_json 里
// （水位为 0），第一次调用会把它们整批补齐入库，因此不需要一次性数据迁移。
func (s *Service) cloudAgentFlushRunEvents(run *model.CloudAgentExecution, state *cloudAgentRuntime) error {
	if s == nil || s.repo == nil || run == nil || state == nil || run.ID == "" || len(state.Events) == 0 {
		return nil
	}
	pending := make([]model.CloudAgentRunEvent, 0, len(state.Events))
	expiresAt := time.Now().UTC().Add(cloudAgentEventRetention)
	for _, event := range state.Events {
		if event.Seq <= state.EventFlushedSeq {
			continue
		}
		payload, err := json.Marshal(event.Payload)
		if err != nil {
			return fmt.Errorf("encode Agent event %s:%d: %w", event.RunID, event.Seq, err)
		}
		pending = append(pending, model.CloudAgentRunEvent{
			RunID: event.RunID, UserID: run.UserID, CanvasID: run.CanvasID,
			Seq: event.Seq, EventID: event.EventID, Type: event.Type,
			Payload: string(payload), CreatedAt: event.CreatedAt, ExpiresAt: expiresAt,
		})
	}
	if len(pending) > 0 {
		if err := s.repo.AppendCloudAgentRunEvents(pending); err != nil {
			return err
		}
		state.EventFlushedSeq = pending[len(pending)-1].Seq
	}
	// 裁剪到尾部缓存：只调整内存态，由本次（或下一次）状态保存落库。
	if len(state.Events) > cloudAgentEventTailLimit {
		drop := len(state.Events) - cloudAgentEventTailLimit
		state.Events = append([]CloudAgentEvent(nil), state.Events[drop:]...)
		state.EventSeqBase += drop
	}
	return nil
}

// cloudAgentFlushRunEventsLogged 是调度路径上的容错包装：事件入库失败不该阻断运行
// （尾巴仍在状态里），但必须留痕。
func (s *Service) cloudAgentFlushRunEventsLogged(run *model.CloudAgentExecution, state *cloudAgentRuntime) {
	if err := s.cloudAgentFlushRunEvents(run, state); err != nil {
		log.Printf("agent event flush %s: %v", run.ID, err)
	}
}

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
	// 尾部不够：从事件表补齐更早的部分。表里为空（升级前结束的旧运行）时保持只有尾部。
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

// cloudAgentRunEventCount 尽量给出"这个运行一共产生过多少事件"（事件表条数 + 尚未入库的尾巴）。
func (s *Service) cloudAgentRunEventCount(userID string, run *model.CloudAgentExecution, state *cloudAgentRuntime) int {
	stored, err := s.repo.CloudAgentRunEventCount(userID, run.ID)
	if err != nil {
		stored = 0
	}
	pending := 0
	for _, event := range state.Events {
		if event.Seq > state.EventFlushedSeq {
			pending++
		}
	}
	total := int(stored) + pending
	if base := state.EventSeqBase + len(state.Events) + pending; base > total {
		total = base
	}
	return total
}
