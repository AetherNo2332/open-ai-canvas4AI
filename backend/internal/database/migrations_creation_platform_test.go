package database

import (
	"fmt"
	"testing"

	"yingce/backend/internal/model"
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
