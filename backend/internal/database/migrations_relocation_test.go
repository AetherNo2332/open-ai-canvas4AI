package database

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

// 我们的 cloud_agent_run_events / cloud_agent_transcript 从 v24/v25（自研时期的原始登记）
// 让位到 v33/v34（登记为 no-op，表与数据保留）。这条让位线挪过一次：合并上游 v1.5.7 时先落在
// v32/v33，上游随后把 v32 用给了 channel_model_tags，于是两条各自再挪一位。
//
// 两种库都需要自动搬迁：
//   - 已升到我们 v25 的库：v24/v25 上是我们的名字与校验和，而 validateMigrationRecord 会拿
//     上游 v24（channel_model_label）比对并拒绝启动（实测报错：数据库迁移 24 名称不一致：
//     记录为 cloud_agent_run_events，程序期望 channel_model_label）；
//   - 已跑过上一版合并线二进制的库：记录停在 v32/v33，而上游 v32 是 channel_model_tags。
//
// 覆盖四种起点：全新库 / 上游库 / 我们 v25 库 / 上一版合并线库。
func TestLegacyCloudAgentMigrationRelocation(t *testing.T) {
	t.Run("全新库：无可搬迁记录，迁移到 34 即可用", func(t *testing.T) {
		db := openRelocationTestDB(t, "fresh")
		if err := relocateLegacyCloudAgentMigrations(db); err != nil {
			t.Fatalf("relocate on fresh db: %v", err)
		}
		if err := MigrateSchema(db); err != nil {
			t.Fatalf("migrate fresh db: %v", err)
		}
		assertSchemaReady(t, db)
	})

	t.Run("上游库：无可搬迁记录，补到 34", func(t *testing.T) {
		db := openRelocationTestDB(t, "upstream")
		seedMigrationRecords(t, db, upstreamRecordsThrough(PreviousUpstreamSchemaVersion)...)
		if err := relocateLegacyCloudAgentMigrations(db); err != nil {
			t.Fatalf("relocate on upstream db: %v", err)
		}
		if err := MigrateSchema(db); err != nil {
			t.Fatalf("migrate upstream db: %v", err)
		}
		assertSchemaReady(t, db)
		// 上游段不能被搬迁动过。
		for _, want := range upstreamCloudAgentVersions() {
			assertMigrationRecord(t, db, want)
		}
	})

	t.Run("我们 v25 库：MigrateSchema 自己完成搬迁，且可重复执行", func(t *testing.T) {
		db := openRelocationTestDB(t, "ours25")
		resetToOurV25Layout(t, db)

		// 不手工调用搬迁：MigrateSchema 必须自己搞定（这就是"四种起点都能跑通"的要求）。
		if err := MigrateSchema(db); err != nil {
			t.Fatalf("migrate our v25 db: %v", err)
		}
		assertRelocated(t, db)
		for _, want := range append(upstreamCloudAgentVersions(), ourCloudAgentVersions()...) {
			assertMigrationRecord(t, db, want)
		}
		// 幂等：搬迁与迁移各再跑一次都必须无事发生。
		if err := relocateLegacyCloudAgentMigrations(db); err != nil {
			t.Fatalf("relocate is not idempotent: %v", err)
		}
		if err := MigrateSchema(db); err != nil {
			t.Fatalf("migrate is not idempotent: %v", err)
		}
		assertRelocated(t, db)
	})

	t.Run("上一版合并线库：记录停在 v32/v33，搬迁到 33/34 并补上上游 v32", func(t *testing.T) {
		db := openRelocationTestDB(t, "mergedline")
		resetToOurMergedLineLayout(t, db)

		if err := MigrateSchema(db); err != nil {
			t.Fatalf("migrate previous merged-line db: %v", err)
		}
		assertRelocated(t, db)
		for _, want := range append(upstreamCloudAgentVersions(), ourCloudAgentVersions()...) {
			assertMigrationRecord(t, db, want)
		}
		// 关键回归：上游 v32 必须真的被执行成 channel_model_tags，而不是被我们停在 v32 的
		// 旧记录顶掉（顶掉的表现是启动时报"名称不一致"）。
		var applied schemaMigration
		if err := db.First(&applied, "version = ?", int64(32)).Error; err != nil {
			t.Fatalf("missing upstream v32: %v", err)
		}
		if applied.Name != "channel_model_tags" {
			t.Fatalf("v32 名称 = %s，期望上游的 channel_model_tags", applied.Name)
		}
		if err := MigrateSchema(db); err != nil {
			t.Fatalf("migrate is not idempotent: %v", err)
		}
		assertRelocated(t, db)
	})

	t.Run("只有我们 v24 的半升级库：同样能搬迁并补到 34", func(t *testing.T) {
		db := openRelocationTestDB(t, "ours24")
		resetToOurV25Layout(t, db)
		// 再退回"只升到我们 v24"的状态：删掉 v25 那条。
		item := legacyRelocationForFrom(25)
		if err := db.Where("version = ? AND name = ?", item.from, item.name).Delete(&schemaMigration{}).Error; err != nil {
			t.Fatalf("drop legacy v25 record: %v", err)
		}
		if err := MigrateSchema(db); err != nil {
			t.Fatalf("migrate our v24 db: %v", err)
		}
		assertRelocated(t, db)
	})

	t.Run("搬迁不会误伤同名不同校验和的历史记录", func(t *testing.T) {
		db := openRelocationTestDB(t, "foreign")
		seedMigrationRecords(t, db, upstreamRecordsThrough(23)...)
		item := legacyRelocationForFrom(24)
		seedMigrationRecords(t, db, schemaMigration{Version: item.from, Name: item.name, Checksum: "sha256:somebody-elses-history"})
		if err := relocateLegacyCloudAgentMigrations(db); err != nil {
			t.Fatalf("relocate: %v", err)
		}
		var applied schemaMigration
		if err := db.First(&applied, "version = ?", item.from).Error; err != nil {
			t.Fatalf("foreign record must stay put: %v", err)
		}
		if applied.Checksum != "sha256:somebody-elses-history" {
			t.Fatalf("foreign record was rewritten: %#v", applied)
		}
	})
}

// TestLegacyCloudAgentMigrationRelocationPostgres 在真实 Postgres 上做一次端到端：
// 先把库迁到 34，再把它改造成"我们 v25 库"的样子（v24/v25 是我们的名字，
// 上游 24–32 与让位后的 v33/v34 记录不存在），然后要求 MigrateSchema 自己搬迁并回到 ready。
//
// 这条用例覆盖了 SQLite 纯逻辑用例覆盖不到的部分：v24–v32 的 DDL 需要真实表存在，
// 而搬迁后这些迁移会被真的执行一次。
func TestLegacyCloudAgentMigrationRelocationPostgres(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("CANVAS_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("CANVAS_TEST_POSTGRES_DSN is not configured")
	}
	base, err := Open(Config{Driver: "postgres", DSN: dsn})
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	baseSQL, err := base.DB()
	if err != nil {
		t.Fatalf("postgres sql db: %v", err)
	}
	defer baseSQL.Close()

	schemaName := fmt.Sprintf("agent_migration_relocation_%d", time.Now().UnixNano())
	if err := base.Exec(`CREATE SCHEMA "` + schemaName + `"`).Error; err != nil {
		t.Fatalf("create test schema: %v", err)
	}
	defer func() {
		if err := base.Exec(`DROP SCHEMA IF EXISTS "` + schemaName + `" CASCADE`).Error; err != nil {
			t.Errorf("drop test schema: %v", err)
		}
	}()
	testDSN, err := postgresDSNWithSearchPath(dsn, schemaName)
	if err != nil {
		t.Fatalf("build dsn: %v", err)
	}
	db, err := Open(Config{Driver: "postgres", DSN: testDSN})
	if err != nil {
		t.Fatalf("open schema db: %v", err)
	}
	if err := MigrateSchema(db); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	assertSchemaReady(t, db)

	// 先把上游 24–32 的记录清掉，模拟"我们 v25 库"（我们的 v24/v25 覆盖了同样的版本号）。
	if err := db.Where("version >= ? AND version <= ?", int64(24), PreviousUpstreamSchemaVersion).Delete(&schemaMigration{}).Error; err != nil {
		t.Fatalf("clear upstream records: %v", err)
	}

	// 把让位后的两条记录退回原始登记版本 24/25，并逐条记下搬迁前的 applied_at：
	// 搬迁只改版本号，目标版本不许被重新执行。
	legacyAppliedAt := map[int64]time.Time{}
	for _, from := range []int64{24, 25} {
		legacy := legacyRelocationForFrom(from)
		var appliedAt time.Time
		if err := db.Model(&schemaMigration{}).Where("version = ?", legacy.to).Pluck("applied_at", &appliedAt).Error; err != nil {
			t.Fatalf("read applied_at: %v", err)
		}
		legacyAppliedAt[legacy.to] = appliedAt
		if err := db.Where("version = ?", legacy.to).Delete(&schemaMigration{}).Error; err != nil {
			t.Fatalf("drop relocated record %d: %v", legacy.to, err)
		}
		record := schemaMigration{Version: legacy.from, Name: legacy.name, Checksum: legacy.checksum, AppliedAt: appliedAt}
		if err := db.Create(&record).Error; err != nil {
			t.Fatalf("seed legacy record %d: %v", legacy.from, err)
		}
	}
	// 这一步在修复前会报：数据库迁移 24 名称不一致：记录为 cloud_agent_run_events，程序期望 channel_model_label
	if err := MigrateSchema(db); err != nil {
		t.Fatalf("migrate our-v25-shaped db: %v", err)
	}
	assertRelocated(t, db)
	assertSchemaReady(t, db)
	for _, want := range append(upstreamCloudAgentVersions(), ourCloudAgentVersions()...) {
		assertMigrationRecord(t, db, want)
	}
	// v33/v34 是搬迁过来的老记录：applied_at 必须还是搬迁前的值。
	for _, from := range []int64{24, 25} {
		legacy := legacyRelocationForFrom(from)
		var applied schemaMigration
		if err := db.First(&applied, "version = ?", legacy.to).Error; err != nil {
			t.Fatalf("missing relocated record %d: %v", legacy.to, err)
		}
		if want := legacyAppliedAt[legacy.to]; !applied.AppliedAt.Equal(want) {
			t.Fatalf("v%d 被重新执行了：applied_at = %v，期望沿用 %v", legacy.to, applied.AppliedAt, want)
		}
	}
}

// resetToOurV25Layout 把已经迁到 34 的库改造成"我们 v25 库"的样子：
// 版本号 24/25 上是我们的两条记录，上游 24–32 与让位后的 33/34 记录都不存在。
// 表结构保持不动（真实库也是这个形态：表在，只是记录号与上游撞车）。
func resetToOurV25Layout(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := MigrateSchema(db); err != nil {
		t.Fatalf("bootstrap schema: %v", err)
	}
	if err := db.Where("version >= ?", int64(24)).Delete(&schemaMigration{}).Error; err != nil {
		t.Fatalf("clear migration records: %v", err)
	}
	seedMigrationRecords(t, db, ourLegacyRecords()...)
}

// resetToOurMergedLineLayout 把已经迁到 34 的库改造成"上一版合并线二进制"的样子：
// 上游 1–31 在位，v32/v33 是我们的两条（上游 v32 channel_model_tags 与 33/34 都不存在）。
func resetToOurMergedLineLayout(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := MigrateSchema(db); err != nil {
		t.Fatalf("bootstrap schema: %v", err)
	}
	if err := db.Where("version >= ?", int64(32)).Delete(&schemaMigration{}).Error; err != nil {
		t.Fatalf("clear merged-line records: %v", err)
	}
	seedMigrationRecords(t, db, ourMergedLineRecords()...)
}

// legacyRelocationForFrom 取"某个历史版本号上那条让位记录"：同一份记录有多个历史落点
// （自研时期的 24/25 与上一版合并线的 32/33）。
func legacyRelocationForFrom(from int64) legacyCloudAgentMigrationRelocation {
	for _, item := range legacyCloudAgentMigrationRelocations {
		if item.from == from {
			return item
		}
	}
	panic(fmt.Sprintf("缺少 from=%d 的让位搬迁条目", from))
}

// ourLegacyRecords 返回"我们 v25 库"里那两条记录（版本号仍是 24/25）。
func ourLegacyRecords() []schemaMigration {
	records := []schemaMigration{}
	for _, from := range []int64{24, 25} {
		item := legacyRelocationForFrom(from)
		records = append(records, schemaMigration{Version: item.from, Name: item.name, Checksum: item.checksum})
	}
	return records
}

// ourMergedLineRecords 返回"上一版合并线库"里那两条记录（版本号停在 32/33）。
func ourMergedLineRecords() []schemaMigration {
	records := []schemaMigration{}
	for _, from := range []int64{32, 33} {
		item := legacyRelocationForFrom(from)
		records = append(records, schemaMigration{Version: item.from, Name: item.name, Checksum: item.checksum})
	}
	return records
}

func openRelocationTestDB(t *testing.T, name string) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:relocation-%s?mode=memory&cache=shared", name)
	db, err := Open(Config{Driver: "sqlite", DSN: dsn})
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	if err := db.AutoMigrate(&schemaMigration{}); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	return db
}

// seedMigrationRecords 把给定 (version, name, checksum) 当作"库里已经应用过"的记录写进去。
func seedMigrationRecords(t *testing.T, db *gorm.DB, records ...schemaMigration) {
	t.Helper()
	for _, record := range records {
		if err := db.Create(&record).Error; err != nil {
			t.Fatalf("seed migration %d (%s): %v", record.Version, record.Name, err)
		}
	}
}

// upstreamRecordsThrough 取 plan 里 1..max 的记录（用于模拟"上游库"）。
func upstreamRecordsThrough(max int64) []schemaMigration {
	records := []schemaMigration{}
	for _, item := range schemaMigrations {
		if item.version <= max {
			records = append(records, schemaMigration{Version: item.version, Name: item.name, Checksum: item.checksum})
		}
	}
	return records
}

// upstreamCloudAgentVersions 取上游段（24..PreviousUpstreamSchemaVersion）的记录。
func upstreamCloudAgentVersions() []schemaMigration {
	records := []schemaMigration{}
	for _, item := range schemaMigrations {
		if item.version >= 24 && item.version <= PreviousUpstreamSchemaVersion {
			records = append(records, schemaMigration{Version: item.version, Name: item.name, Checksum: item.checksum})
		}
	}
	return records
}

// ourCloudAgentVersions 取我们那两条 no-op 迁移的**当前登记版本**（从 plan 现读，不写死数字）。
func ourCloudAgentVersions() []schemaMigration {
	records := []schemaMigration{}
	for _, item := range schemaMigrations {
		if item.name != "cloud_agent_run_events" && item.name != "cloud_agent_transcript" {
			continue
		}
		records = append(records, schemaMigration{Version: item.version, Name: item.name, Checksum: item.checksum})
	}
	return records
}

func assertSchemaReady(t *testing.T, db *gorm.DB) {
	t.Helper()
	status, err := ReadSchemaStatus(db)
	if err != nil {
		t.Fatalf("read schema status: %v", err)
	}
	if !status.Ready || status.Current != CurrentSchemaVersion {
		t.Fatalf("unexpected schema status: %#v", status)
	}
}

func assertMigrationRecord(t *testing.T, db *gorm.DB, want schemaMigration) {
	t.Helper()
	var applied schemaMigration
	if err := db.First(&applied, "version = ?", want.Version).Error; err != nil {
		t.Fatalf("missing migration %d (%s): %v", want.Version, want.Name, err)
	}
	if applied.Name != want.Name || applied.Checksum != want.Checksum {
		t.Fatalf("migration %d = (%s, %s)，期望 (%s, %s)", want.Version, applied.Name, applied.Checksum, want.Name, want.Checksum)
	}
}

// assertRelocated 校验：库版本到 34，且所有历史落点上都不再残留我们的名字。
func assertRelocated(t *testing.T, db *gorm.DB) {
	t.Helper()
	assertSchemaReady(t, db)
	for _, item := range legacyCloudAgentMigrationRelocations {
		var count int64
		if err := db.Model(&schemaMigration{}).Where("version = ? AND name = ?", item.from, item.name).Count(&count).Error; err != nil {
			t.Fatalf("count legacy rows: %v", err)
		}
		if count != 0 {
			t.Fatalf("v%d 上仍残留 %s", item.from, item.name)
		}
	}
}
