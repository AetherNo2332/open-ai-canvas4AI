package database_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"yingce/backend/internal/database"
	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

const lessonsToMemoryMigrationVersion int64 = 82

func openLessonsToMemorySQLite(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := database.Open(database.Config{Driver: "sqlite", DSN: "file:" + t.Name() + "?mode=memory&cache=shared"})
	if err != nil {
		t.Fatal(err)
	}
	if sqlDB, err := db.DB(); err == nil {
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	if err := database.MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func openLessonsToMemoryPostgres(t *testing.T) *gorm.DB {
	t.Helper()
	db := openAgentAdmissionAuditPostgresDB(t)
	if err := database.MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func rerunLessonsToMemoryMigration(db *gorm.DB) error {
	if err := db.Exec("DELETE FROM schema_migrations WHERE version = ?", lessonsToMemoryMigrationVersion).Error; err != nil {
		return err
	}
	return database.MigrateSchema(db)
}

func seedLesson(t *testing.T, db *gorm.DB, lesson model.AgentLesson) {
	t.Helper()
	if lesson.CreatedAt.IsZero() {
		lesson.CreatedAt = time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	}
	lesson.UpdatedAt = lesson.CreatedAt
	if err := db.Create(&lesson).Error; err != nil {
		t.Fatal(err)
	}
}

func memoryDocument(t *testing.T, db *gorm.DB, userID string) (model.AgentMemoryDocument, []model.AgentMemoryEntry) {
	t.Helper()
	var doc model.AgentMemoryDocument
	if err := db.First(&doc, "user_id = ?", userID).Error; err != nil {
		t.Fatalf("memory document for %s: %v", userID, err)
	}
	var entries []model.AgentMemoryEntry
	if err := json.Unmarshal([]byte(doc.EntriesJSON), &entries); err != nil {
		t.Fatal(err)
	}
	return doc, entries
}

func checkLessonsMigrateIntoMemoryDocuments(t *testing.T, db *gorm.DB) {
	steps, _ := json.Marshal([]model.AgentLessonStep{{Tool: "canvas_read_storyboard", Action: "先读分镜"}, {Tool: "generate_media", Action: "逐镜生成"}})
	seedLesson(t, db, model.AgentLesson{ID: "l-pref", Topic: "称呼", Category: "other", Situation: "每次首次回答用户时", Lesson: "称呼用户为长官", Source: "user_preference", Status: model.AgentLessonStatusApproved, AuthorUserID: "lesson-a", Hits: 9})
	seedLesson(t, db, model.AgentLesson{ID: "l-steps", Topic: "storyboard.flow", Category: "storyboard", Situation: "按分镜批量出图", StepsJSON: string(steps), Status: model.AgentLessonStatusApproved, AuthorUserID: "lesson-a", Hits: 3})
	seedLesson(t, db, model.AgentLesson{ID: "l-dup", Topic: "称呼-重复", Category: "other", Situation: "每次首次回答用户时", Lesson: "称呼用户为“长官”。", Status: model.AgentLessonStatusApproved, AuthorUserID: "lesson-a", Hits: 1})
	seedLesson(t, db, model.AgentLesson{ID: "l-pending", Topic: "pending", Category: "other", Situation: "未批准", Lesson: "不应导入", Status: model.AgentLessonStatusPending, AuthorUserID: "lesson-a"})
	seedLesson(t, db, model.AgentLesson{ID: "l-rejected", Topic: "rejected", Category: "other", Situation: "已拒绝", Lesson: "不应导入", Status: model.AgentLessonStatusRejected, AuthorUserID: "lesson-a"})
	seedLesson(t, db, model.AgentLesson{ID: "l-secret", Topic: "secret", Category: "other", Situation: "调用外部接口", Lesson: "api_key: sk-abcdefghijklmnopqrstuvwx", Status: model.AgentLessonStatusApproved, AuthorUserID: "lesson-a"})
	seedLesson(t, db, model.AgentLesson{ID: "l-other", Topic: "video", Category: "video", Situation: "生成竖屏视频", Lesson: "默认 9:16", Status: model.AgentLessonStatusApproved, AuthorUserID: "lesson-b"})
	seedLesson(t, db, model.AgentLesson{ID: "l-only-pending", Topic: "p", Category: "other", Situation: "只有待审", Lesson: "不应建文件", Status: model.AgentLessonStatusPending, AuthorUserID: "lesson-c"})
	// lesson-b 已经有一份记忆文件：导入条目追加在后面，序号接着文件计数，不覆盖已有内容。
	existing, _ := json.Marshal([]model.AgentMemoryEntry{{ID: "u4", Section: model.AgentMemorySectionUser, Text: "偏好暖色调", Source: model.AgentMemoryActorUser}})
	if err := db.Create(&model.AgentMemoryDocument{UserID: "lesson-b", EntriesJSON: string(existing), NextEntrySeq: 5, Revision: 3, Mode: model.AgentMemoryDocumentModeReview, Capacity: 5000, CharCount: 5}).Error; err != nil {
		t.Fatal(err)
	}

	if err := rerunLessonsToMemoryMigration(db); err != nil {
		t.Fatal(err)
	}
	doc, entries := memoryDocument(t, db, "lesson-a")
	if len(entries) != 2 {
		t.Fatalf("approved, valid, non-duplicate lessons must become entries: %+v", entries)
	}
	if entries[0].ID != "u1" || entries[0].Section != model.AgentMemorySectionUser || entries[0].Text != "每次首次回答用户时：称呼用户为长官" || entries[0].Source != "lesson_import" {
		t.Fatalf("preference lesson mapped wrongly: %+v", entries[0])
	}
	if entries[1].ID != "n2" || entries[1].Section != model.AgentMemorySectionNotes || entries[1].Text != "按分镜批量出图：先读分镜 → 逐镜生成" {
		t.Fatalf("step lesson mapped wrongly: %+v", entries[1])
	}
	if doc.Mode != model.AgentMemoryDocumentModeAuto || doc.Capacity != 5000 || doc.Revision != 1 || doc.NextEntrySeq != 3 || doc.CharCount != len([]rune(entries[0].Text+entries[1].Text)) || doc.Hash == "" {
		t.Fatalf("new document fields: %+v", doc)
	}
	var revisions []model.AgentMemoryDocumentRevision
	if err := db.Where("user_id = ?", "lesson-a").Find(&revisions).Error; err != nil || len(revisions) != 1 {
		t.Fatalf("import must leave one undoable revision: %+v %v", revisions, err)
	}
	if r := revisions[0]; r.Actor != model.AgentMemoryActorSeed || r.Status != model.AgentMemoryRevisionApplied || r.BaseRevision != 0 || r.Revision != 1 || r.BeforeJSON != "[]" || r.AfterJSON != doc.EntriesJSON || !strings.Contains(r.Summary, "2") {
		t.Fatalf("seed revision: %+v", r)
	}

	merged, mergedEntries := memoryDocument(t, db, "lesson-b")
	if len(mergedEntries) != 2 || mergedEntries[0].ID != "u4" || mergedEntries[1].ID != "n5" || mergedEntries[1].Text != "生成竖屏视频：默认 9:16" {
		t.Fatalf("existing document was not merged: %+v", mergedEntries)
	}
	if merged.Revision != 4 || merged.NextEntrySeq != 6 || merged.Mode != model.AgentMemoryDocumentModeReview || merged.CharCount != len([]rune("偏好暖色调"+mergedEntries[1].Text)) {
		t.Fatalf("merged document fields: %+v", merged)
	}
	var mergeRevision model.AgentMemoryDocumentRevision
	if err := db.First(&mergeRevision, "user_id = ?", "lesson-b").Error; err != nil || mergeRevision.BaseRevision != 3 || mergeRevision.Revision != 4 || !strings.Contains(mergeRevision.BeforeJSON, "偏好暖色调") {
		t.Fatalf("merge revision: %+v %v", mergeRevision, err)
	}
	var count int64
	if err := db.Model(&model.AgentMemoryDocument{}).Where("user_id = ?", "lesson-c").Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("users without approved lessons must not get a document: %d %v", count, err)
	}
	if err := db.Model(&model.AgentLesson{}).Count(&count).Error; err != nil || count != 8 {
		t.Fatalf("lesson rows must be kept: %d %v", count, err)
	}

	// 重跑：没有新增条目、不改 revision、不多写修订。
	if err := rerunLessonsToMemoryMigration(db); err != nil {
		t.Fatal(err)
	}
	again, againEntries := memoryDocument(t, db, "lesson-a")
	if again.Revision != doc.Revision || len(againEntries) != 2 || again.EntriesJSON != doc.EntriesJSON {
		t.Fatalf("re-run changed lesson-a: %+v", again)
	}
	if mergedAgain, _ := memoryDocument(t, db, "lesson-b"); mergedAgain.Revision != merged.Revision {
		t.Fatalf("re-run changed lesson-b revision %d -> %d", merged.Revision, mergedAgain.Revision)
	}
	if err := db.Model(&model.AgentMemoryDocumentRevision{}).Count(&count).Error; err != nil || count != 2 {
		t.Fatalf("re-run wrote extra revisions: %d %v", count, err)
	}
}

// 导入受单条长度和整份容量约束：超长条目截断，超出容量的条目跳过（按命中次数优先）。
func checkLessonsMigrationRespectsCapacity(t *testing.T, db *gorm.DB) {
	seedLesson(t, db, model.AgentLesson{ID: "cap-long", Topic: "long", Category: "other", Situation: "长条目", Lesson: strings.Repeat("长", 600), Status: model.AgentLessonStatusApproved, AuthorUserID: "cap-user", Hits: 100})
	for i := 0; i < 20; i++ {
		seedLesson(t, db, model.AgentLesson{ID: "cap-" + string(rune('a'+i)), Topic: "t", Category: "other", Situation: "场景" + string(rune('a'+i)), Lesson: strings.Repeat(string(rune('甲'+i)), 380), Status: model.AgentLessonStatusApproved, AuthorUserID: "cap-user", Hits: int64(50 - i)})
	}
	if err := rerunLessonsToMemoryMigration(db); err != nil {
		t.Fatal(err)
	}
	doc, entries := memoryDocument(t, db, "cap-user")
	if len(entries) == 0 || len([]rune(entries[0].Text)) > 400 || !strings.HasPrefix(entries[0].Text, "长条目：") {
		t.Fatalf("long lesson must be truncated to one entry: %+v", entries)
	}
	total := 0
	for _, entry := range entries {
		total += len([]rune(entry.Text))
	}
	if total > 5000 || doc.CharCount != total || len(entries) >= 21 {
		t.Fatalf("capacity not respected: %d chars, %d entries", total, len(entries))
	}
}

// 经验压缩已下线：还在排队或运行的压缩任务没有执行者，迁移时标记失败。
func checkLessonsMigrationRetiresCompactTasks(t *testing.T, db *gorm.DB) {
	for _, task := range []model.Task{
		{ID: "compact-queued", UserID: "cap-user", Type: "canvas_text", Operation: "agent_memory_compact", Status: model.TaskStatusQueued},
		{ID: "compact-done", UserID: "cap-user", Type: "canvas_text", Operation: "agent_memory_compact", Status: model.TaskStatusSucceeded},
		{ID: "other-queued", UserID: "cap-user", Type: "canvas_text", Operation: "cloud_agent_step", Status: model.TaskStatusQueued},
	} {
		if err := db.Create(&task).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := rerunLessonsToMemoryMigration(db); err != nil {
		t.Fatal(err)
	}
	statuses := map[string]model.TaskStatus{}
	var tasks []model.Task
	if err := db.Find(&tasks).Error; err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		statuses[task.ID] = task.Status
	}
	if statuses["compact-queued"] != model.TaskStatusFailed || statuses["compact-done"] != model.TaskStatusSucceeded || statuses["other-queued"] != model.TaskStatusQueued {
		t.Fatalf("compact task retirement: %+v", statuses)
	}
}

func TestMigrateLessonsToMemoryDocuments(t *testing.T) {
	db := openLessonsToMemorySQLite(t)
	checkLessonsMigrateIntoMemoryDocuments(t, db)
	status, err := database.ReadSchemaStatus(db)
	if err != nil || !status.Ready || database.CurrentSchemaVersion < lessonsToMemoryMigrationVersion {
		t.Fatalf("schema status = %+v %v", status, err)
	}
}

func TestMigrateLessonsToMemoryRespectsCapacity(t *testing.T) {
	checkLessonsMigrationRespectsCapacity(t, openLessonsToMemorySQLite(t))
}

func TestMigrateLessonsToMemoryRetiresCompactTasks(t *testing.T) {
	checkLessonsMigrationRetiresCompactTasks(t, openLessonsToMemorySQLite(t))
}

func TestMigrateLessonsToMemoryPostgres(t *testing.T) {
	checkLessonsMigrateIntoMemoryDocuments(t, openLessonsToMemoryPostgres(t))
}

func TestMigrateLessonsToMemoryPostgresRespectsCapacity(t *testing.T) {
	checkLessonsMigrationRespectsCapacity(t, openLessonsToMemoryPostgres(t))
}

func TestMigrateLessonsToMemoryPostgresRetiresCompactTasks(t *testing.T) {
	checkLessonsMigrationRetiresCompactTasks(t, openLessonsToMemoryPostgres(t))
}
