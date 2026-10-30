package database

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"yingce/backend/internal/agentmemory"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

// lessonsToMemoryMigrationVersion 退役 fork 经验库（recall_lessons / remember_lesson）：
// 每个用户已批准的经验一次性并入他自己的长期记忆文件（上游 v76/v77 建的 agent_memory_documents），
// 规则与运行时首次建文件导入相同（agentmemory.AppendLessons）。待审、已拒绝的经验不迁移。
// agent_lessons 表保留只读，不删数据。
//
// 幂等：已经带有导入条目（source=lesson_import）的记忆文件不再追加；导入条目与已有条目重复时也跳过。
// 失败即整个迁移事务回滚（fail closed），不会留下只导了一半的文件。
const lessonsToMemoryMigrationVersion int64 = 82

const lessonsToMemoryChecksum = "sha256:fork-lessons-to-memory-document-v82-20261030"

// agentMemoryCompactOperation 是已下线的经验压缩任务；没有执行者，排队/运行中的标记失败。
const agentMemoryCompactOperation = "agent_memory_compact"

const agentMemoryCompactRetiredError = "经验压缩已下线：经验已并入长期记忆文件，Agent 会自己整理记忆"

const lessonsToMemoryRevisionKeep = 100

func migrateLessonsToMemoryDocument(tx *gorm.DB) error {
	now := time.Now().UTC()
	if err := tx.Model(&model.Task{}).
		Where("operation = ? AND status IN ?", agentMemoryCompactOperation, []model.TaskStatus{model.TaskStatusQueued, model.TaskStatusRunning}).
		Updates(map[string]any{"status": model.TaskStatusFailed, "error": agentMemoryCompactRetiredError, "stage": "已下线", "completed_at": now, "updated_at": now}).Error; err != nil {
		return fmt.Errorf("退役经验压缩任务：%w", err)
	}
	if !tx.Migrator().HasTable(&model.AgentLesson{}) {
		return nil
	}
	var owners []string
	if err := tx.Model(&model.AgentLesson{}).Where("status = ? AND author_user_id <> ''", model.AgentLessonStatusApproved).
		Distinct("author_user_id").Order("author_user_id").Pluck("author_user_id", &owners).Error; err != nil {
		return fmt.Errorf("读取经验作者：%w", err)
	}
	for _, owner := range owners {
		if err := migrateOwnerLessons(tx, owner, now); err != nil {
			return fmt.Errorf("迁移用户 %s 的经验：%w", owner, err)
		}
	}
	return nil
}

func migrateOwnerLessons(tx *gorm.DB, owner string, now time.Time) error {
	var lessons []model.AgentLesson
	if err := tx.Where("author_user_id = ? AND status = ?", owner, model.AgentLessonStatusApproved).
		Order("hits DESC").Order("updated_at DESC").Order("id").Find(&lessons).Error; err != nil {
		return err
	}
	if len(lessons) == 0 {
		return nil
	}
	var doc model.AgentMemoryDocument
	err := tx.First(&doc, "user_id = ?", owner).Error
	exists := err == nil
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	existing := []model.AgentMemoryEntry{}
	if exists && doc.EntriesJSON != "" {
		if err := json.Unmarshal([]byte(doc.EntriesJSON), &existing); err != nil {
			return fmt.Errorf("记忆文件无法解析：%w", err)
		}
	}
	for _, entry := range existing {
		if entry.Source == agentmemory.LessonImportSource {
			return nil // 已导入过（运行时首次建文件或上次迁移）
		}
	}
	// 导入过但用户之后撤销或删掉了这些条目：导入修订仍在（无论已撤销与否），不能再导一次。
	var seeded int64
	if err := tx.Model(&model.AgentMemoryDocumentRevision{}).Where("user_id = ? AND actor = ?", owner, model.AgentMemoryActorSeed).Count(&seeded).Error; err != nil {
		return err
	}
	if seeded > 0 {
		return nil
	}
	capacity := doc.Capacity
	if capacity <= 0 {
		capacity = agentmemory.DefaultCapacity
	}
	added, nextSeq := agentmemory.AppendLessons(existing, agentmemory.NextSeq(doc.NextEntrySeq, existing), lessons, capacity, now)
	if len(added) == 0 {
		return nil
	}
	after := append(append([]model.AgentMemoryEntry{}, existing...), added...)
	beforeJSON, err := json.Marshal(existing)
	if err != nil {
		return err
	}
	afterJSON, err := json.Marshal(after)
	if err != nil {
		return err
	}
	revision := model.AgentMemoryDocumentRevision{
		ID: kernel.NewID(), UserID: owner, Actor: model.AgentMemoryActorSeed, Status: model.AgentMemoryRevisionApplied,
		Summary: fmt.Sprintf("从旧版经验库导入 %d 条", len(added)), OpsJSON: "[]",
		BeforeJSON: string(beforeJSON), AfterJSON: string(afterJSON), CreatedAt: now,
	}
	fields := map[string]any{
		"entries_json": string(afterJSON), "next_entry_seq": nextSeq, "hash": agentmemory.Hash(afterJSON),
		"char_count": agentmemory.CharCount(after), "updated_at": now,
	}
	if exists {
		revision.BaseRevision, revision.Revision = doc.Revision, doc.Revision+1
		fields["revision"] = revision.Revision
		result := tx.Model(&model.AgentMemoryDocument{}).Where("user_id = ? AND revision = ?", owner, doc.Revision).Updates(fields)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("记忆文件 revision 已变化")
		}
	} else {
		revision.BaseRevision, revision.Revision = 0, 1
		created := model.AgentMemoryDocument{
			UserID: owner, EntriesJSON: string(afterJSON), NextEntrySeq: nextSeq, Revision: 1, Hash: agentmemory.Hash(afterJSON),
			CharCount: agentmemory.CharCount(after), Mode: model.AgentMemoryDocumentModeAuto, Capacity: capacity, CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.Create(&created).Error; err != nil {
			return err
		}
	}
	if err := tx.Create(&revision).Error; err != nil {
		return err
	}
	return pruneMigratedMemoryRevisions(tx, owner)
}

// 与 repository.pruneAgentMemoryDocumentRevisions 同一口径：只保留最近 100 条已结束的修订。
func pruneMigratedMemoryRevisions(tx *gorm.DB, owner string) error {
	var stale []string
	if err := tx.Model(&model.AgentMemoryDocumentRevision{}).Where("user_id = ? AND status <> ?", owner, model.AgentMemoryRevisionPending).
		Order("created_at DESC").Order("id DESC").Offset(lessonsToMemoryRevisionKeep).Limit(500).Pluck("id", &stale).Error; err != nil {
		return err
	}
	if len(stale) == 0 {
		return nil
	}
	return tx.Where("user_id = ? AND id IN ?", owner, stale).Delete(&model.AgentMemoryDocumentRevision{}).Error
}
