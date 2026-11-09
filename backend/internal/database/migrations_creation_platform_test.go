package database

import (
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

// SQLite 改列会重建表（建临时表→删旧表→改名）。重建 tasks 或 billing_orders 后，平台事件触发器必须仍在，
// 并且重建过程本身不能因触发器引用了被重建的表而失败。
func TestPlatformEventTriggersSurviveSQLiteTableRebuild(t *testing.T) {
	db, err := Open(Config{Driver: "sqlite", DSN: fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())})
	if err != nil {
		t.Fatal(err)
	}
	if err := MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	for _, rebuild := range []struct {
		entity any
		field  string
	}{{&model.BillingOrder{}, "UsageSource"}, {&model.Task{}, "CreatedAtEpoch"}} {
		if err := db.Migrator().DropColumn(rebuild.entity, rebuild.field); err != nil {
			t.Fatalf("rebuild %T: %v", rebuild.entity, err)
		}
	}
	// 重新执行迁移会补回列，并在结束时重建触发器。
	if err := db.Where("version IN ?", []int64{25, 56}).Delete(&schemaMigration{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := MigrateSchema(db); err != nil {
		t.Fatalf("re-migrate after rebuild: %v", err)
	}
	for _, row := range []any{
		&model.Task{ID: "task-1", UserID: "user", Type: "canvas_image", Status: model.TaskStatusQueued, BillingOrderID: "order-1"},
		&model.BillingOrder{ID: "order-1", UserID: "user", IdempotencyKey: "k", TaskID: "task-1", Status: model.BillingStatusReserved},
		&model.PlatformSubmission{ID: "sub-1", UserID: "user", Application: "test", IdempotencyKey: "k", Fingerprint: "f", TaskID: "task-1", Capability: "image"},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Model(&model.Task{}).Where("id = ?", "task-1").Update("status", model.TaskStatusRunning).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.BillingOrder{}).Where("id = ?", "order-1").Update("status", model.BillingStatusRunning).Error; err != nil {
		t.Fatal(err)
	}
	var events []model.PlatformTaskEvent
	if err := db.Order("position").Find(&events, "task_id = ?", "task-1").Error; err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].TaskStatus != "running" || events[1].Type != "task.billing_changed" || events[1].BillingStatus != "running" || events[1].TaskStatus != "running" {
		t.Fatalf("triggers lost after rebuild: %+v", events)
	}
	for index, event := range events {
		if event.Position == nil || *event.Position != int64(index+1) {
			t.Fatalf("positions must be 1..n: %+v", events)
		}
	}
}

// 审查 F3：SQLite → PostgreSQL 导入时，事件行已带源库游标；PostgreSQL 的提交时取号触发器不能重写它们，
// 否则历史顺序会倒退、旧游标语义改变、迁移工具的逐字段校验在重复执行时失败。
func TestPostgresPlatformEventTriggerKeepsImportedPositions(t *testing.T) {
	dsn := os.Getenv("CANVAS_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("CANVAS_TEST_POSTGRES_DSN is not configured")
	}
	base, err := Open(Config{Driver: "postgres", DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	baseSQL, _ := base.DB()
	t.Cleanup(func() { _ = baseSQL.Close() })
	schema := fmt.Sprintf("platform_import_%d", time.Now().UnixNano())
	if err := base.Exec(`CREATE SCHEMA "` + schema + `"`).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`).Error })
	parsed, _ := url.Parse(dsn)
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := Open(Config{Driver: "postgres", DSN: parsed.String()})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	// 模拟迁移工具：在一个事务里按主键复制带游标的事件与计数器。主键顺序与游标顺序故意相反。
	two, one := int64(2), int64(1)
	imported := []model.PlatformTaskEvent{
		{ID: "a-final", Position: &two, TaskID: "t", UserID: "u", Application: "test", Type: "task.state_changed", TaskStatus: "succeeded", OccurredAtMs: 2},
		{ID: "z-submitted", Position: &one, TaskID: "t", UserID: "u", Application: "test", Type: "task.submitted", TaskStatus: "queued", OccurredAtMs: 1},
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&imported).Error; err != nil {
			return err
		}
		return tx.Create(&model.PlatformEventCounter{ID: 1, Value: 2}).Error
	}); err != nil {
		t.Fatal(err)
	}
	var events []model.PlatformTaskEvent
	if err := db.Order("id").Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || *events[0].Position != 2 || *events[1].Position != 1 {
		t.Fatalf("imported positions were rewritten at commit: a=%d z=%d", *events[0].Position, *events[1].Position)
	}
	var counter model.PlatformEventCounter
	if err := db.First(&counter, "id = 1").Error; err != nil || counter.Value != 2 {
		t.Fatalf("counter after import = %+v %v, want 2", counter, err)
	}
	// 导入后的运行时事件继续从计数器之后取号。
	runtime := model.PlatformTaskEvent{ID: "runtime", TaskID: "t", UserID: "u", Application: "test", Type: "task.state_changed", OccurredAtMs: 3}
	if err := db.Create(&runtime).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&runtime, "id = ?", "runtime").Error; err != nil || runtime.Position == nil || *runtime.Position != 3 {
		t.Fatalf("runtime event after import got position %v", runtime.Position)
	}
}
