package database

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 合并上游 v1.5.7 之后，运行事件的活存储换成上游的 cloud_agent_event_records
// （一行一条整 JSON 的 EventJSON，主键 run_id + sequence）。我们自研的
// cloud_agent_run_events 随 v32 退役为 no-op 迁移：表与数据保留，代码不再读写。
//
// 但"保留数据"对已有部署不够：我们 v25 库里的 21 个 checkpoint_version=2 的运行，
// 事件行全在退役表里，上游事件表是空的。cloudAgentDecode 对 v2 运行要求
// len(Journal) == EventCount，读路径拿不到行就直接报
// "Agent execution journal is incomplete"，这些运行会永远读不出来。
//
// 这里做一次性、幂等、条件式的行搬运：只有当退役表里有该 run 的行、
// 且上游事件表里该 run 一行都没有、且 run 自报 event_count > 0 时才搬。
// 三种起点都必须是安全的 no-op：
//   - 全新库：退役表不存在 → 直接返回；
//   - 纯上游库：退役表不存在（或没有行）→ 直接返回；
//   - 我们的旧库：退役表有行 → 按 seq 转成 EventJSON 写进上游表。
const (
	// legacyCloudAgentEventTable 是退役的自研事件表。
	legacyCloudAgentEventTable = "cloud_agent_run_events"
	// cloudAgentEventTable 是上游的事件表（model.CloudAgentEventRecord 的表名）。
	cloudAgentEventTable = "cloud_agent_event_records"
	// cloudAgentExecutionTable 是运行行（model.CloudAgentExecution 的表名）。
	cloudAgentExecutionTable = "cloud_agent_executions"
	// legacyCloudAgentEventBatch 是单次搬运的 run 数：一次事务里不要把整库塞进来。
	legacyCloudAgentEventBatch = 200
)

// cloudAgentEventJSON 是搬运时写进 EventJSON 的形态。
//
// 字段顺序与 app.CloudAgentEvent 的声明顺序一致（eventId/runId/seq/type/payload/createdAt），
// 因此 json.Marshal 出来的字节与线上写路径产出的完全同构；
// Payload 用 RawMessage 原样透传，不重排退役表里已经落盘的 JSON。
type cloudAgentEventJSON struct {
	EventID   string          `json:"eventId"`
	RunID     string          `json:"runId"`
	Seq       int             `json:"seq"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"createdAt"`
}

// legacyCloudAgentEventRow 是退役表里的一行（只取搬运需要的列）。
type legacyCloudAgentEventRow struct {
	RunID     string    `gorm:"column:run_id"`
	UserID    string    `gorm:"column:user_id"`
	Seq       int       `gorm:"column:seq"`
	EventID   string    `gorm:"column:event_id"`
	Type      string    `gorm:"column:type"`
	Payload   string    `gorm:"column:payload"`
	CreatedAt time.Time `gorm:"column:created_at"`
}

// migrateLegacyCloudAgentEventRows 把退役表里的事件行搬进上游事件表。
//
// 返回搬运的 run 数与行数，供调用方与用例断言。任何"匹配不到"的情形都是 (0, 0, nil)。
func migrateLegacyCloudAgentEventRows(db *gorm.DB) (int, int, error) {
	if db == nil {
		return 0, 0, nil
	}
	// 目标表在全新库 / 纯上游库上要等 v28 迁移才建出来，此时不算异常，跳过即可。
	if !db.Migrator().HasTable(legacyCloudAgentEventTable) || !db.Migrator().HasTable(cloudAgentEventTable) {
		return 0, 0, nil
	}
	if !db.Migrator().HasTable(cloudAgentExecutionTable) {
		return 0, 0, nil
	}
	runs, rows := 0, 0
	for {
		ids, err := pendingLegacyCloudAgentEventRuns(db, legacyCloudAgentEventBatch)
		if err != nil {
			return runs, rows, err
		}
		if len(ids) == 0 {
			return runs, rows, nil
		}
		converted := 0
		for _, runID := range ids {
			moved, err := migrateLegacyCloudAgentEventRun(db, runID)
			if err != nil {
				return runs, rows, err
			}
			if moved > 0 {
				converted++
				runs++
				rows += moved
			}
		}
		// 这一批一个都没搬动（例如退役行 seq 全部非法）：不能再取同一批，
		// 否则会原地打转。留痕后退出，让上层继续跑迁移。
		if converted == 0 {
			log.Printf("[cloud-agent] 事件搬运未推进：本批 %d 个运行没有可取的行", len(ids))
			return runs, rows, nil
		}
	}
}

// pendingLegacyCloudAgentEventRuns 找出"退役表里有行、上游表里一行都没有"的运行。
//
// 条件是三条同时成立，缺一不可：
//   - 运行自报 event_count > 0（没有事件的运行不需要补）；
//   - 退役表里存在该 run 的行（否则没有可搬的东西）；
//   - 上游表里不存在该 run 的行（已经有行就不动，避免重复写与覆盖）。
func pendingLegacyCloudAgentEventRuns(db *gorm.DB, limit int) ([]string, error) {
	if limit <= 0 {
		limit = legacyCloudAgentEventBatch
	}
	ids := []string{}
	err := db.Table(cloudAgentExecutionTable+" AS execution").
		Select("execution.id").
		Where("execution.event_count > 0").
		Where("EXISTS (SELECT 1 FROM "+legacyCloudAgentEventTable+" AS legacy WHERE legacy.run_id = execution.id)").
		Where("NOT EXISTS (SELECT 1 FROM "+cloudAgentEventTable+" AS record WHERE record.run_id = execution.id)").
		Order("execution.id").
		Limit(limit).
		Pluck("execution.id", &ids).Error
	if err != nil {
		return nil, fmt.Errorf("查找待搬运事件的 Agent 运行：%w", err)
	}
	return ids, nil
}

// migrateLegacyCloudAgentEventRun 搬运单个运行的事件行。
func migrateLegacyCloudAgentEventRun(db *gorm.DB, runID string) (int, error) {
	rows := []legacyCloudAgentEventRow{}
	if err := db.Table(legacyCloudAgentEventTable).
		Where("run_id = ?", runID).Order("seq ASC").Find(&rows).Error; err != nil {
		return 0, fmt.Errorf("读取退役事件行（%s）：%w", runID, err)
	}
	if len(rows) == 0 {
		return 0, nil
	}
	records, lastSeq := make([]legacyCloudAgentEventRecord, 0, len(rows)), 0
	for _, row := range rows {
		if row.Seq <= 0 {
			continue
		}
		payload := json.RawMessage(row.Payload)
		if !json.Valid(payload) {
			// 退役表里的 payload 理论上都是 json.Marshal 的产物；损坏时给一个空对象，
			// 保住 seq 连续性（缺一条会让整轮判"记录不完整"，比丢内容更糟）。
			payload = json.RawMessage("{}")
		}
		body, err := json.Marshal(cloudAgentEventJSON{
			EventID: row.EventID, RunID: runID, Seq: row.Seq, Type: row.Type,
			Payload: payload, CreatedAt: row.CreatedAt,
		})
		if err != nil {
			return 0, fmt.Errorf("编码搬运事件（%s#%d）：%w", runID, row.Seq, err)
		}
		records = append(records, legacyCloudAgentEventRecord{
			RunID: runID, Sequence: row.Seq, UserID: row.UserID,
			EventJSON: string(body), CreatedAt: row.CreatedAt,
		})
		if row.Seq > lastSeq {
			lastSeq = row.Seq
		}
	}
	if len(records) == 0 {
		return 0, nil
	}
	// 主键 (run_id, sequence)：并发/重跑时 DoNothing，保证幂等。
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(records, 200).Error; err != nil {
		return 0, fmt.Errorf("写入搬运事件（%s）：%w", runID, err)
	}
	// 行数对不上时留痕：读路径会以此判"记录不完整"，运维要能一眼看到是哪一轮。
	var count int64
	if err := db.Table(cloudAgentExecutionTable).Where("id = ?", runID).Pluck("event_count", &count).Error; err != nil {
		return len(records), nil
	}
	if int(count) != lastSeq {
		log.Printf("[cloud-agent] 事件搬运后水位不一致：run=%s event_count=%d last_seq=%d", runID, count, lastSeq)
	}
	return len(records), nil
}

// legacyCloudAgentEventRecord 是上游事件表的行结构（与 model.CloudAgentEventRecord 同构）。
// 这里单独声明是为了让 database 包不依赖 app 包的重建逻辑，也避免误用 model 的关联标签。
type legacyCloudAgentEventRecord struct {
	RunID     string `gorm:"primaryKey;size:80"`
	Sequence  int    `gorm:"primaryKey;autoIncrement:false"`
	UserID    string `gorm:"index;size:36"`
	EventJSON string `gorm:"type:text;not null"`
	CreatedAt time.Time
}

func (legacyCloudAgentEventRecord) TableName() string { return cloudAgentEventTable }
