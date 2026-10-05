package app

import (
	"encoding/json"
	"testing"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

// saveAgentStateForTest 用真实保存路径持久化运行状态（消息、事件与检查点同事务），
// 替代测试里 db.Save(run) / Update("state_json", …) 这类绕过持久化层的写法：
// 检查点从 v2 起不再承载消息与事件，直接写 blob 会让行上的计数与实际行数脱节。
func saveAgentStateForTest(t *testing.T, s *Service, run *model.CloudAgentExecution, state *cloudAgentRuntime, mutate ...func(*model.CloudAgentExecution)) *model.CloudAgentExecution {
	t.Helper()
	if err := s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		for _, apply := range mutate {
			apply(current)
		}
		return cloudAgentSave(current, state)
	}); err != nil {
		t.Fatalf("保存 Agent 状态失败: %v", err)
	}
	updated, err := s.repo.CloudAgent(run.UserID, run.ID)
	if err != nil {
		t.Fatalf("重新读取 Agent 运行失败: %v", err)
	}
	return updated
}

// writeLegacyAgentStateForTest 把状态写成"旧检查点"形态（消息与事件仍在 blob 里、版本 0），
// 用来验证旧运行可读以及下一次保存时的懒迁移。
func writeLegacyAgentStateForTest(t *testing.T, db *gorm.DB, run *model.CloudAgentExecution, state *cloudAgentRuntime) string {
	t.Helper()
	return writeLegacyAgentBlobForTest(t, db, run, state, nil)
}

// writeLegacyAgentBlobForTest 同上，但允许在写库前改写 blob（例如删掉新增字段）。
func writeLegacyAgentBlobForTest(t *testing.T, db *gorm.DB, run *model.CloudAgentExecution, state *cloudAgentRuntime, edit func(map[string]any)) string {
	t.Helper()
	blob, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	stored := map[string]any{}
	if err := json.Unmarshal(blob, &stored); err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		edit(stored)
	}
	encoded, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", run.ID).Updates(map[string]any{
		"state_json": string(encoded), "checkpoint_version": 0, "message_count": 0, "event_count": 0,
	}).Error; err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// markAgentRunTerminalForTest 把运行置为终态。旧 Go 驱动（advanceCloudAgentByID）删除后，
// 测试不能再靠它把上一轮推进到终态；生产里这一步由 agent/ 的 Pi worker 完成。
// 续聊类用例真正要断言的是"上一轮终结后能否继续"，因此显式构造终态即可。
func markAgentRunTerminalForTest(t *testing.T, db *gorm.DB, runID, status string) {
	t.Helper()
	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", runID).Update("status", status).Error; err != nil {
		t.Fatal(err)
	}
}

// completePiRunWithAssistantForTest creates the durable final-message facts a
// Pi continuation consumes. A holding task's ResultJSON is only a reservation
// envelope and must never be used as the assistant reply.
func completePiRunWithAssistantForTest(t *testing.T, s *Service, runID, reply string) {
	t.Helper()
	run, err := s.repo.CloudAgent("user", runID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.repo.MutateCloudAgent("user", runID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		state, err := cloudAgentDecode(current)
		if err != nil {
			return err
		}
		state.Canonical.Messages = append(state.Canonical.Messages, map[string]any{"role": "assistant", "content": reply})
		state.event(runID, "assistant_message", map[string]any{"messageId": runID + ":final", "text": reply, "final": true})
		current.Status = "completed"
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
}
