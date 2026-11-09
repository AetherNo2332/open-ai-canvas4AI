package database

import (
	"fmt"

	"gorm.io/gorm"
)

// 平台事件触发器。两种方言记录同样的原始事实：任务状态、媒体阶段、是否有未完成的媒体检查点、结果是否含资源、
// 当前费用状态。合同状态的映射留在 Go（与快照同一函数）。
//
// SQLite 的触发器只引用触发表本身（NEW）与平台自己的三张表：gorm 的 SQLite 改列会“建临时表→删旧表→改名”，
// 一个表的触发器若引用正被重建的表会让重建失败，被重建表上的触发器也会随旧表一起删除。所以 SQLite 侧的另一半
// 状态（任务事件里的费用、费用事件里的任务）沿用该任务上一条事件的值，并在每次 MigrateSchema 结束时重建触发器。
// PostgreSQL 的 ALTER TABLE 不重建表，触发器可以直接读另一张表的当前值。

const platformEventInsertColumns = "id, position, task_id, user_id, application, project_ref, type, task_status, media_stage, media_pending, has_resources, billing_status, occurred_at_ms"

const sqlitePlatformEventPosition = `INSERT OR IGNORE INTO platform_event_counters (id, value, pruned_before) VALUES (1, 0, 0);
	  UPDATE platform_event_counters SET value = value + 1 WHERE id = 1;`

const sqlitePlatformEventNow = `CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER)`

// sqlitePlatformLatest 读取同一任务上一条事件的某一列；没有上一条时返回空串或 0。
func sqlitePlatformLatest(column, taskExpr, empty string) string {
	return "COALESCE((SELECT " + column + " FROM platform_task_events WHERE task_id = " + taskExpr + " AND position IS NOT NULL ORDER BY position DESC LIMIT 1), " + empty + ")"
}

var sqlitePlatformEventTriggers = []string{
	`DROP TRIGGER IF EXISTS platform_task_event_on_task`,
	`DROP TRIGGER IF EXISTS platform_task_event_on_billing`,
	`DROP TRIGGER IF EXISTS platform_task_event_on_billing_insert`,
	`CREATE TRIGGER platform_task_event_on_task AFTER UPDATE ON tasks
	WHEN (OLD.status IS NOT NEW.status OR OLD.media_stage IS NOT NEW.media_stage OR OLD.billing_order_id IS NOT NEW.billing_order_id
	      OR (COALESCE(OLD.media_recovery_json, '') = '') IS NOT (COALESCE(NEW.media_recovery_json, '') = ''))
	 AND EXISTS (SELECT 1 FROM platform_submissions WHERE task_id = NEW.id)
	BEGIN
	  ` + sqlitePlatformEventPosition + `
	  INSERT INTO platform_task_events (` + platformEventInsertColumns + `)
	  SELECT lower(hex(randomblob(16))), (SELECT value FROM platform_event_counters WHERE id = 1), NEW.id, ps.user_id, ps.application, COALESCE(ps.project_ref, ''),
	         'task.state_changed', COALESCE(NEW.status, ''), COALESCE(NEW.media_stage, ''),
	         COALESCE(NEW.media_recovery_json, '') <> '' AND COALESCE(NEW.media_stage, '') <> 'completed',
	         instr(COALESCE(NEW.result_json, ''), '"resourceId"') > 0,
	         ` + sqlitePlatformLatest("billing_status", "NEW.id", "''") + `, ` + sqlitePlatformEventNow + `
	  FROM platform_submissions ps WHERE ps.task_id = NEW.id;
	END`,
	`CREATE TRIGGER platform_task_event_on_billing AFTER UPDATE OF status ON billing_orders
	WHEN OLD.status IS NOT NEW.status AND EXISTS (SELECT 1 FROM platform_submissions WHERE task_id = NEW.task_id)
	BEGIN
	  ` + sqlitePlatformEventPosition + `
	  INSERT INTO platform_task_events (` + platformEventInsertColumns + `)
	  SELECT lower(hex(randomblob(16))), (SELECT value FROM platform_event_counters WHERE id = 1), NEW.task_id, ps.user_id, ps.application, COALESCE(ps.project_ref, ''),
	         'task.billing_changed', ` + sqlitePlatformLatest("task_status", "NEW.task_id", "''") + `, ` + sqlitePlatformLatest("media_stage", "NEW.task_id", "''") + `,
	         ` + sqlitePlatformLatest("media_pending", "NEW.task_id", "0") + `, ` + sqlitePlatformLatest("has_resources", "NEW.task_id", "0") + `,
	         NEW.status, ` + sqlitePlatformEventNow + `
	  FROM platform_submissions ps WHERE ps.task_id = NEW.task_id;
	END`,
	// 重试会先冻结一份新订单、再把任务改回排队；新订单的 reserved 状态由这个事件带出。
	// 首次提交时提交记录尚未写入，EXISTS 为假，提交事件由仓储写入。
	`CREATE TRIGGER platform_task_event_on_billing_insert AFTER INSERT ON billing_orders
	WHEN EXISTS (SELECT 1 FROM platform_submissions WHERE task_id = NEW.task_id)
	BEGIN
	  ` + sqlitePlatformEventPosition + `
	  INSERT INTO platform_task_events (` + platformEventInsertColumns + `)
	  SELECT lower(hex(randomblob(16))), (SELECT value FROM platform_event_counters WHERE id = 1), NEW.task_id, ps.user_id, ps.application, COALESCE(ps.project_ref, ''),
	         'task.billing_changed', ` + sqlitePlatformLatest("task_status", "NEW.task_id", "''") + `, ` + sqlitePlatformLatest("media_stage", "NEW.task_id", "''") + `,
	         ` + sqlitePlatformLatest("media_pending", "NEW.task_id", "0") + `, ` + sqlitePlatformLatest("has_resources", "NEW.task_id", "0") + `,
	         NEW.status, ` + sqlitePlatformEventNow + `
	  FROM platform_submissions ps WHERE ps.task_id = NEW.task_id;
	END`,
}

const postgresPlatformEventNow = `(extract(epoch from clock_timestamp()) * 1000)::bigint`

const postgresPlatformEventID = `md5(random()::text || clock_timestamp()::text)`

// postgresBillingEventSelect 从任务当前行与触发的订单生成一条费用事件。
const postgresBillingEventSelect = `INSERT INTO platform_task_events (` + platformEventInsertColumns + `)
	    SELECT ` + postgresPlatformEventID + `, NULL, t.id, ps.user_id, ps.application, COALESCE(ps.project_ref, ''),
	           'task.billing_changed', COALESCE(t.status, ''), COALESCE(t.media_stage, ''),
	           COALESCE(t.media_recovery_json, '') <> '' AND COALESCE(t.media_stage, '') <> 'completed',
	           position('"resourceId"' in COALESCE(t.result_json, '')) > 0,
	           NEW.status, ` + postgresPlatformEventNow + `
	    FROM tasks t JOIN platform_submissions ps ON ps.task_id = t.id WHERE t.id = NEW.task_id;`

var postgresPlatformEventTriggers = []string{
	// 游标在提交时分配：计数器行只在事务最后被锁，持有期间不再等待其他锁，回滚的事务不消耗游标。
	// 已带游标的行（SQLite → PostgreSQL 导入的历史事件）保持原值，计数器随导入的计数器行一起复制。
	`CREATE OR REPLACE FUNCTION platform_task_event_assign_position() RETURNS trigger AS $$
	DECLARE next_position BIGINT;
	BEGIN
	  IF NEW.position IS NOT NULL THEN
	    RETURN NULL;
	  END IF;
	  INSERT INTO platform_event_counters (id, value, pruned_before) VALUES (1, 0, 0) ON CONFLICT (id) DO NOTHING;
	  UPDATE platform_event_counters SET value = value + 1 WHERE id = 1 RETURNING value INTO next_position;
	  UPDATE platform_task_events SET position = next_position WHERE id = NEW.id;
	  RETURN NULL;
	END; $$ LANGUAGE plpgsql`,
	`DROP TRIGGER IF EXISTS platform_task_event_position ON platform_task_events`,
	`CREATE CONSTRAINT TRIGGER platform_task_event_position AFTER INSERT ON platform_task_events
	DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION platform_task_event_assign_position()`,
	`CREATE OR REPLACE FUNCTION platform_task_event_on_task() RETURNS trigger AS $$
	BEGIN
	  IF (OLD.status IS DISTINCT FROM NEW.status OR OLD.media_stage IS DISTINCT FROM NEW.media_stage OR OLD.billing_order_id IS DISTINCT FROM NEW.billing_order_id
	      OR (COALESCE(OLD.media_recovery_json, '') = '') IS DISTINCT FROM (COALESCE(NEW.media_recovery_json, '') = '')) THEN
	    INSERT INTO platform_task_events (` + platformEventInsertColumns + `)
	    SELECT ` + postgresPlatformEventID + `, NULL, NEW.id, ps.user_id, ps.application, COALESCE(ps.project_ref, ''),
	           'task.state_changed', COALESCE(NEW.status, ''), COALESCE(NEW.media_stage, ''),
	           COALESCE(NEW.media_recovery_json, '') <> '' AND COALESCE(NEW.media_stage, '') <> 'completed',
	           position('"resourceId"' in COALESCE(NEW.result_json, '')) > 0,
	           COALESCE((SELECT status FROM billing_orders WHERE id = NEW.billing_order_id), ''), ` + postgresPlatformEventNow + `
	    FROM platform_submissions ps WHERE ps.task_id = NEW.id;
	  END IF;
	  RETURN NULL;
	END; $$ LANGUAGE plpgsql`,
	`DROP TRIGGER IF EXISTS platform_task_event_on_task ON tasks`,
	`CREATE TRIGGER platform_task_event_on_task AFTER UPDATE ON tasks FOR EACH ROW EXECUTE FUNCTION platform_task_event_on_task()`,
	`CREATE OR REPLACE FUNCTION platform_task_event_on_billing() RETURNS trigger AS $$
	BEGIN
	  IF TG_OP = 'INSERT' OR OLD.status IS DISTINCT FROM NEW.status THEN
	    ` + postgresBillingEventSelect + `
	  END IF;
	  RETURN NULL;
	END; $$ LANGUAGE plpgsql`,
	`DROP TRIGGER IF EXISTS platform_task_event_on_billing ON billing_orders`,
	`CREATE TRIGGER platform_task_event_on_billing AFTER INSERT OR UPDATE OF status ON billing_orders FOR EACH ROW EXECUTE FUNCTION platform_task_event_on_billing()`,
}

// ensurePlatformEventTriggers 幂等地重建平台事件触发器。它在迁移 v85 与每次 MigrateSchema 结束时运行，
// 因此任何迁移在 SQLite 上重建 tasks / billing_orders 后，事件触发器都会恢复。
func ensurePlatformEventTriggers(tx *gorm.DB) error {
	if !tx.Migrator().HasTable("platform_task_events") {
		return nil
	}
	statements := sqlitePlatformEventTriggers
	if tx.Dialector.Name() == "postgres" {
		statements = postgresPlatformEventTriggers
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

// finishSchemaMigration 是 MigrateSchema 的最后一步：重建平台事件触发器后再校验版本。
func finishSchemaMigration(tx *gorm.DB) error {
	if err := ensurePlatformEventTriggers(tx); err != nil {
		return fmt.Errorf("重建平台事件触发器：%w", err)
	}
	return RequireSchemaVersion(tx)
}
