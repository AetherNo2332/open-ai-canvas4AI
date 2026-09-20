package database

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"gorm.io/gorm"
)

// 上游 v1.5.7 起，运行事件的活存储是 cloud_agent_event_records（EventJSON 整条 JSON，
// 主键 run_id + sequence）。我们自研的 cloud_agent_run_events 随 v32 退役为 no-op：
// 表与数据保留、代码不再读写。已有部署（我们 v25）里 checkpoint_version=2 的运行，
// 事件行全在退役表里，不搬运就会被 cloudAgentDecode 判 "Agent execution journal is incomplete"。
//
// 这里覆盖三种起点：全新库 / 上游 v31 库 / 我方 v25 等价库（含退役表数据）。
func TestLegacyCloudAgentEventRowMigration(t *testing.T) {
	t.Run("全新库：退役表不存在，搬运是 no-op", func(t *testing.T) {
		db := openRelocationTestDB(t, "events-fresh")
		if err := MigrateSchema(db); err != nil {
			t.Fatalf("migrate fresh db: %v", err)
		}
		if db.Migrator().HasTable(legacyCloudAgentEventTable) {
			t.Fatalf("全新库不该出现退役表 %s", legacyCloudAgentEventTable)
		}
		runs, rows, err := migrateLegacyCloudAgentEventRows(db)
		if err != nil {
			t.Fatalf("migrate legacy events on fresh db: %v", err)
		}
		if runs != 0 || rows != 0 {
			t.Fatalf("全新库搬运了 run=%d rows=%d，期望 0/0", runs, rows)
		}
	})

	t.Run("上游 v31 库：没有退役表，搬运是 no-op 且上游行不被改动", func(t *testing.T) {
		db := openRelocationTestDB(t, "events-upstream31")
		if err := MigrateSchema(db); err != nil {
			t.Fatalf("migrate upstream v31 db: %v", err)
		}
		if db.Migrator().HasTable(legacyCloudAgentEventTable) {
			t.Fatalf("上游库不该有退役表 %s", legacyCloudAgentEventTable)
		}
		if err := seedCloudAgentEventRecord(t, db, "upstream-run", 1); err != nil {
			t.Fatalf("seed upstream event row: %v", err)
		}
		runs, rows, err := migrateLegacyCloudAgentEventRows(db)
		if err != nil {
			t.Fatalf("migrate legacy events on upstream db: %v", err)
		}
		if runs != 0 || rows != 0 {
			t.Fatalf("上游库搬运了 run=%d rows=%d，期望 0/0", runs, rows)
		}
		if got := countCloudAgentEventRecords(t, db, "upstream-run"); got != 1 {
			t.Fatalf("上游库的事件行被动了：%d 行", got)
		}
	})

	t.Run("我方 v25 等价库：MigrateSchema 自己把退役行搬成上游 EventJSON，且可重复执行", func(t *testing.T) {
		db := openRelocationTestDB(t, "events-ours25")
		resetToOurV25Layout(t, db)
		createLegacyCloudAgentEventTable(t, db)

		const runID = "run-ours-25"
		total := 7
		seedLegacyCloudAgentEventRun(t, db, runID, total)
		// 已入库的运行：checkpoint_version=2 且 event_count 与表里行数一致。
		if err := db.Exec("INSERT INTO "+cloudAgentExecutionTable+
			" (id, user_id, status, revision, checkpoint_version, event_count, message_count, state_json, created_at, updated_at)"+
			" VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			runID, "user-1", "completed", 3, 2, total, 0, "{}", time.Now().UTC(), time.Now().UTC()).Error; err != nil {
			t.Fatalf("seed run row: %v", err)
		}

		if err := MigrateSchema(db); err != nil {
			t.Fatalf("migrate our v25 db: %v", err)
		}
		assertSchemaReady(t, db)

		// 幂等：再跑一次搬运与迁移都不该多写一行。
		if _, _, err := migrateLegacyCloudAgentEventRows(db); err != nil {
			t.Fatalf("legacy event migration is not idempotent: %v", err)
		}
		if err := MigrateSchema(db); err != nil {
			t.Fatalf("migrate is not idempotent: %v", err)
		}
		if got := countCloudAgentEventRecords(t, db, runID); got != total {
			t.Fatalf("转换后事件行数 = %d，期望 %d", got, total)
		}

		// 转换后该 run 必须"能被解码"：上游解码口径是
		// len(Journal) == EventCount、seq 从 1 连续、事件身份自洽。
		records := loadCloudAgentEventRecords(t, db, runID)
		var eventCount int
		if err := db.Table(cloudAgentExecutionTable).Where("id = ?", runID).Pluck("event_count", &eventCount).Error; err != nil {
			t.Fatalf("read event_count: %v", err)
		}
		if len(records) != eventCount {
			t.Fatalf("len(Journal)=%d != EventCount=%d → 解码会报 journal is incomplete", len(records), eventCount)
		}
		for index, record := range records {
			event, err := decodeLegacyCloudAgentEvent(record.EventJSON)
			if err != nil {
				t.Fatalf("事件 %d 无法解码：%v（body=%s）", index+1, err, record.EventJSON)
			}
			if event.Seq != index+1 || event.RunID != runID || event.EventID != fmt.Sprintf("%s:%d", runID, index+1) {
				t.Fatalf("事件 %d 身份不自洽：%#v", index+1, event)
			}
			if len(event.Payload) == 0 {
				t.Fatalf("事件 %d 的 payload 为空：%s", index+1, record.EventJSON)
			}
		}

		// 退役表保持原样：数据保留、不再读写。
		var legacyRows int64
		if err := db.Table(legacyCloudAgentEventTable).Where("run_id = ?", runID).Count(&legacyRows).Error; err != nil {
			t.Fatalf("count legacy rows: %v", err)
		}
		if int(legacyRows) != total {
			t.Fatalf("退役表被改动了：%d 行，期望 %d", legacyRows, total)
		}
	})

	t.Run("上游表已有该 run 的行时不动它", func(t *testing.T) {
		db := openRelocationTestDB(t, "events-partial")
		if err := MigrateSchema(db); err != nil {
			t.Fatalf("migrate: %v", err)
		}
		createLegacyCloudAgentEventTable(t, db)
		const runID = "run-already-migrated"
		seedLegacyCloudAgentEventRun(t, db, runID, 3)
		if err := db.Exec("INSERT INTO "+cloudAgentExecutionTable+
			" (id, user_id, status, revision, checkpoint_version, event_count, message_count, state_json, created_at, updated_at)"+
			" VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			runID, "user-1", "completed", 1, 2, 3, 0, "{}", time.Now().UTC(), time.Now().UTC()).Error; err != nil {
			t.Fatalf("seed run row: %v", err)
		}
		if err := seedCloudAgentEventRecord(t, db, runID, 3); err != nil {
			t.Fatalf("seed existing upstream row: %v", err)
		}
		if _, _, err := migrateLegacyCloudAgentEventRows(db); err != nil {
			t.Fatalf("migrate legacy events: %v", err)
		}
		if got := countCloudAgentEventRecords(t, db, runID); got != 1 {
			t.Fatalf("已有上游行的 run 被重复搬运：%d 行，期望 1", got)
		}
	})

	t.Run("event_count 为 0 的运行不搬", func(t *testing.T) {
		db := openRelocationTestDB(t, "events-zerocount")
		if err := MigrateSchema(db); err != nil {
			t.Fatalf("migrate: %v", err)
		}
		createLegacyCloudAgentEventTable(t, db)
		const runID = "run-zero"
		seedLegacyCloudAgentEventRun(t, db, runID, 2)
		if err := db.Exec("INSERT INTO "+cloudAgentExecutionTable+
			" (id, user_id, status, revision, checkpoint_version, event_count, message_count, state_json, created_at, updated_at)"+
			" VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			runID, "user-1", "completed", 1, 0, 0, 0, "{}", time.Now().UTC(), time.Now().UTC()).Error; err != nil {
			t.Fatalf("seed run row: %v", err)
		}
		if _, _, err := migrateLegacyCloudAgentEventRows(db); err != nil {
			t.Fatalf("migrate legacy events: %v", err)
		}
		if got := countCloudAgentEventRecords(t, db, runID); got != 0 {
			t.Fatalf("event_count=0 的运行被搬运了：%d 行", got)
		}
	})
}

// legacyCloudAgentEventTableDDL 是退役表的建表语句（与 dev v24 迁移一致，去掉清理用的索引）。
const legacyCloudAgentEventTableDDL = `CREATE TABLE IF NOT EXISTS ` + legacyCloudAgentEventTable + ` (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	run_id TEXT NOT NULL,
	user_id TEXT NOT NULL,
	canvas_id TEXT,
	seq INTEGER NOT NULL,
	event_id TEXT NOT NULL,
	type TEXT NOT NULL,
	payload TEXT NOT NULL,
	created_at DATETIME,
	expires_at DATETIME
)`

// createLegacyCloudAgentEventTable 手工建出退役表：v32 起它不再由迁移创建，
// 但真实旧库里它是存在的（这正是本用例要模拟的起点）。
func createLegacyCloudAgentEventTable(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Exec(legacyCloudAgentEventTableDDL).Error; err != nil {
		t.Fatalf("create legacy event table: %v", err)
	}
}

// seedLegacyCloudAgentEventRun 往退役表里写 total 条事件（payload 是真 JSON）。
func seedLegacyCloudAgentEventRun(t *testing.T, db *gorm.DB, runID string, total int) {
	t.Helper()
	for seq := 1; seq <= total; seq++ {
		payload, err := json.Marshal(map[string]any{"step": seq, "text": fmt.Sprintf("第 %d 步", seq)})
		if err != nil {
			t.Fatalf("encode legacy payload: %v", err)
		}
		if err := db.Exec("INSERT INTO "+legacyCloudAgentEventTable+
			" (run_id, user_id, canvas_id, seq, event_id, type, payload, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
			runID, "user-1", "canvas-1", seq, fmt.Sprintf("%s:%d", runID, seq), "tool_completed",
			string(payload), time.Now().UTC(), time.Now().UTC().Add(30*24*time.Hour)).Error; err != nil {
			t.Fatalf("seed legacy event row %d: %v", seq, err)
		}
	}
}

// seedCloudAgentEventRecord 往上游事件表里写一条最小可用的行。
func seedCloudAgentEventRecord(t *testing.T, db *gorm.DB, runID string, seq int) error {
	t.Helper()
	body, err := json.Marshal(cloudAgentEventJSON{
		EventID: fmt.Sprintf("%s:%d", runID, seq), RunID: runID, Seq: seq, Type: "tool_completed",
		Payload: json.RawMessage(`{"text":"上游既有行"}`), CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("encode upstream event: %v", err)
	}
	return db.Exec("INSERT INTO "+cloudAgentEventTable+
		" (run_id, sequence, user_id, event_json, created_at) VALUES (?, ?, ?, ?, ?)",
		runID, seq, "user-1", string(body), time.Now().UTC()).Error
}

func countCloudAgentEventRecords(t *testing.T, db *gorm.DB, runID string) int {
	t.Helper()
	var count int64
	if err := db.Table(cloudAgentEventTable).Where("run_id = ?", runID).Count(&count).Error; err != nil {
		t.Fatalf("count event rows: %v", err)
	}
	return int(count)
}

func loadCloudAgentEventRecords(t *testing.T, db *gorm.DB, runID string) []legacyCloudAgentEventRecord {
	t.Helper()
	records := []legacyCloudAgentEventRecord{}
	if err := db.Table(cloudAgentEventTable).Where("run_id = ?", runID).Order("sequence ASC").Find(&records).Error; err != nil {
		t.Fatalf("load event rows: %v", err)
	}
	return records
}

// decodeLegacyCloudAgentEvent 按 app.CloudAgentEvent 的字段与顺序解码，
// 用来断言搬运产出的 EventJSON 与线上写路径同构。
func decodeLegacyCloudAgentEvent(body string) (cloudAgentEventJSON, error) {
	var event cloudAgentEventJSON
	if err := json.Unmarshal([]byte(body), &event); err != nil {
		return event, err
	}
	if event.EventID == "" || event.RunID == "" || event.Seq <= 0 || event.Type == "" || event.CreatedAt.IsZero() {
		return event, fmt.Errorf("事件字段不完整：%s", body)
	}
	return event, nil
}
