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
