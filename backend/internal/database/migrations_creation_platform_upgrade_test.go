package database

import (
	"fmt"
	"testing"

	"yingce/backend/internal/model"
)

// S8-D：从 v83（平台之前）升级时，在途旧任务不被改写；升级后它们照常推进且不产生平台事件；
// 回退旧代码时新表与触发器可以原样保留（旧代码不读不写），已收费任务与账单不受影响。
func TestCreationPlatformUpgradeKeepsInFlightLegacyTasks(t *testing.T) {
	db, err := Open(Config{Driver: "sqlite", DSN: fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())})
	if err != nil {
		t.Fatal(err)
	}
	if err := MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	// 模拟 v83 旧库：删除平台表、触发器与迁移记录，写入在途旧任务与冻结订单。
	for _, statement := range []string{
		"DROP TRIGGER IF EXISTS platform_task_event_on_task",
		"DROP TRIGGER IF EXISTS platform_task_event_on_billing",
		"DROP TRIGGER IF EXISTS platform_task_event_on_billing_insert",
		"DROP TABLE platform_task_events", "DROP TABLE platform_event_counters", "DROP TABLE platform_submissions",
		"DELETE FROM schema_migrations WHERE version >= 84",
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(statement, err)
		}
	}
	for _, row := range []any{
		&model.Task{ID: "legacy-running", UserID: "user", Type: "canvas_image", Status: model.TaskStatusRunning, BillingOrderID: "legacy-order"},
		&model.BillingOrder{ID: "legacy-order", UserID: "user", IdempotencyKey: "legacy", TaskID: "legacy-running", Status: model.BillingStatusRunning, AmountMicrocredits: 100},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := MigrateSchema(db); err != nil {
		t.Fatalf("upgrade from v83: %v", err)
	}
	status, err := ReadSchemaStatus(db)
	if err != nil || status.Current != CurrentSchemaVersion {
		t.Fatalf("schema after upgrade: %+v %v", status, err)
	}
	if err := db.Model(&model.Task{}).Where("id = ?", "legacy-running").Updates(map[string]any{"status": model.TaskStatusSucceeded, "result_json": `{"images":[{"resourceId":"r"}]}`}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.BillingOrder{}).Where("id = ?", "legacy-order").Update("status", model.BillingStatusSettled).Error; err != nil {
		t.Fatal(err)
	}
	var events int64
	db.Model(&model.PlatformTaskEvent{}).Count(&events)
	if events != 0 {
		t.Fatalf("legacy task produced %d platform events", events)
	}
	var task model.Task
	var order model.BillingOrder
	db.First(&task, "id = ?", "legacy-running")
	db.First(&order, "id = ?", "legacy-order")
	if task.Status != model.TaskStatusSucceeded || order.Status != model.BillingStatusSettled || order.AmountMicrocredits != 100 {
		t.Fatalf("legacy task/order changed by upgrade: %+v %+v", task, order)
	}
	// 再次迁移（例如回退后重新升级）是幂等的。
	if err := MigrateSchema(db); err != nil {
		t.Fatalf("re-run migrations: %v", err)
	}
}
