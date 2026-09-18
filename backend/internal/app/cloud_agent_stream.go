package app

import (
	"errors"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

const (
	// cloudAgentStreamFlushBytes / cloudAgentStreamFlushInterval 是画布 Agent 流式留痕的批量阈值。
	cloudAgentStreamFlushBytes    = 8 << 10
	cloudAgentStreamFlushInterval = 2 * time.Second
)

// Publish upstream deltas to the same durable, revisioned transcript used by
// Agent SSE/replay. Each flush re-reads state; never overwrite a scheduler's
// checkpoint with the snapshot from when the model request started.
func newCloudAgentStreamPublisher(s *Service, userID, taskID, kind string) *taskTextStreamPublisher {
	p := newTaskTextStreamPublisher(s, userID, taskID)
	// 实测（22 步 659 条 reasoning_delta）：500ms 定时器主导落库节奏，单条只带 ~132 字符文本，
	// 而信封固定 206 字节——事件条数才是状态膨胀的主因。SSE 读取端本身就是 1s 批量下发，
	// 所以放大到 2s / 8KB 对界面几乎无感，事件条数降到约 1/4。
	p.flushBytes = cloudAgentStreamFlushBytes
	p.flushInterval = cloudAgentStreamFlushInterval
	written := 0
	p.sink = func(delta string) error {
		remaining := 32000 - written
		if remaining <= 0 {
			return nil
		}
		if len(delta) > remaining {
			delta = delta[:remaining]
			for !utf8.ValidString(delta) {
				delta = delta[:len(delta)-1]
			}
		}
		if delta == "" {
			return nil
		}
		for attempt := 0; attempt < 3; attempt++ {
			run, err := s.repo.CloudAgentForActiveTask(userID, taskID)
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			err = s.repo.MutateCloudAgent(userID, run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
				state, err := cloudAgentDecode(current)
				if err != nil {
					return err
				}
				if state.ActiveTaskID != taskID || (current.Status != "running" && current.Status != "queued") {
					return nil
				}
				messageID := taskID
				if kind == "reasoning_delta" {
					messageID += ":reasoning"
				}
				state.event(run.ID, kind, map[string]any{"messageId": messageID, "text": delta})
				return cloudAgentSave(current, &state)
			})
			if errors.Is(err, repository.ErrCreationConflict) {
				continue
			}
			if err == nil {
				written += len(delta)
			}
			return err
		}
		return repository.ErrCreationConflict
	}
	return p
}
