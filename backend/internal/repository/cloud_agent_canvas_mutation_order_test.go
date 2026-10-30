package repository

import (
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
)

func newMutationOrderRepository(t *testing.T) *Repository {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+kernel.NewID()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&model.CloudAgentCanvasMutation{}); err != nil {
		t.Fatal(err)
	}
	return New(db)
}

func mutationOrderRecord(id string, createdAt time.Time, hasSubmittedTask bool) *model.CloudAgentCanvasMutation {
	return &model.CloudAgentCanvasMutation{
		ID: id, RunID: "run", UserID: "user", CanvasID: "canvas", StepID: "step-" + id,
		Operation: "canvas_apply_ops", BeforeSnapshotHash: "before-" + id, AfterSnapshotHash: "after-" + id,
		Status: "applied", CreatedAt: createdAt, HasSubmittedTask: hasSubmittedTask,
	}
}

// 撤销取"最近一次画布变更"。两条记录拿到同一个时间戳时（Windows 的时钟粒度可达毫秒，
// 连续两次画布操作实测完全相同），排序不能退化成按随机 ID 比较：ID 是随机十六进制，
// 一旦它把更早的编辑排在前面，撤销就会越过"已提交任务"栅栏去撤那次编辑。
func TestCloudAgentCanvasMutationUndoOrderSurvivesEqualTimestamps(t *testing.T) {
	repo := newMutationOrderRepository(t)
	at := time.Now().UTC()
	// ID 故意与时间顺序相反：修好之前 id DESC 会让更早的那条胜出。
	older := mutationOrderRecord("zzz-older", at, false)
	newer := mutationOrderRecord("aaa-newer", at, true)
	for _, mutation := range []*model.CloudAgentCanvasMutation{older, newer} {
		if err := repo.CreateCloudAgentCanvasMutation(mutation); err != nil {
			t.Fatal(err)
		}
	}
	undo, err := repo.CloudAgentCanvasMutationForUndo("user", "run", "canvas")
	if err != nil {
		t.Fatal(err)
	}
	if undo.ID != newer.ID {
		t.Fatalf("撤销选中了 %s，应为后写入的 %s（两条记录时间戳相同）", undo.ID, newer.ID)
	}
	if !undo.HasSubmittedTask {
		t.Fatalf("撤销拿到了没有栅栏标记的旧记录：%+v", undo)
	}
}

// 重做取"最近一次撤销"。撤销时间相同时按创建时间升序兜底会重放更早的那次撤销，
// 而用户看到的顺序是后撤销的先重做。
func TestCloudAgentCanvasMutationRedoOrderSurvivesEqualTimestamps(t *testing.T) {
	repo := newMutationOrderRepository(t)
	at := time.Now().UTC()
	older := mutationOrderRecord("zzz-older", at, false)
	newer := mutationOrderRecord("aaa-newer", at.Add(time.Second), false)
	for _, mutation := range []*model.CloudAgentCanvasMutation{older, newer} {
		if err := repo.CreateCloudAgentCanvasMutation(mutation); err != nil {
			t.Fatal(err)
		}
	}
	undoneAt := time.Now().UTC()
	for _, id := range []string{older.ID, newer.ID} {
		if err := repo.MarkCloudAgentCanvasMutationUndone("user", "run", id, undoneAt); err != nil {
			t.Fatal(err)
		}
	}
	redo, err := repo.CloudAgentCanvasMutationForRedo("user", "run", "canvas")
	if err != nil {
		t.Fatal(err)
	}
	if redo.ID != newer.ID {
		t.Fatalf("重做选中了 %s，应为最后被撤销的 %s（两次撤销时间戳相同）", redo.ID, newer.ID)
	}
}
