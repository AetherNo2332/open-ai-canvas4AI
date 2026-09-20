package database

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

// 合并上游 v1.5.7 时，我们的 cloud_agent_run_events / cloud_agent_transcript 从 v24/v25
// 让位到 v32/v33（登记为 no-op，表与数据保留）。已经升到我们 v25 的库必须先把自己那两条
// 记录改写到 32/33，否则 validateMigrationRecord 会拿上游 v24（channel_model_label）比对
// 并拒绝启动（实测报错：数据库迁移 24 名称不一致：记录为 cloud_agent_run_events，程序期望 channel_model_label）。
//
// 覆盖三种起点：全新库 / 上游 v31 库 / 我们 v25 库。
func TestLegacyCloudAgentMigrationRelocation(t *testing.T) {
	t.Run("全新库：无可搬迁记录，迁移到 33 即可用", func(t *testing.T) {
		db := openRelocationTestDB(t, "fresh")
		if err := relocateLegacyCloudAgentMigrations(db); err != nil {
			t.Fatalf("relocate on fresh db: %v", err)
		}
		if err := MigrateSchema(db); err != nil {
			t.Fatalf("migrate fresh db: %v", err)
		}
		assertSchemaReady(t, db)
	})

	t.Run("上游 v31 库：无可搬迁记录，补到 33", func(t *testing.T) {
		db := openRelocationTestDB(t, "upstream31")
		seedMigrationRecords(t, db, upstreamRecordsThrough(PreviousUpstreamSchemaVersion)...)
		if err := relocateLegacyCloudAgentMigrations(db); err != nil {
			t.Fatalf("relocate on upstream v31 db: %v", err)
		}
		if err := MigrateSchema(db); err != nil {
			t.Fatalf("migrate upstream v31 db: %v", err)
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

		// 不手工调用搬迁：MigrateSchema 必须自己搞定（这就是"三种起点都能跑通"的要求）。
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

	t.Run("只有我们 v24 的半升级库：同样能搬迁并补到 33", func(t *testing.T) {
		db := openRelocationTestDB(t, "ours24")
		resetToOurV25Layout(t, db)
		// 再退回"只升到我们 v24"的状态：删掉 v25 那条。
		item := legacyCloudAgentMigrationRelocations[1]
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
		item := legacyCloudAgentMigrationRelocations[0]
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
// 先把库迁到 33，再把它改造成"我们 v25 库"的样子（v24/v25 是我们的名字，
// v32/v33 记录不存在），然后要求 MigrateSchema 自己搬迁并回到 ready。
//
// 这条用例覆盖了 SQLite 纯逻辑用例覆盖不到的部分：v24–v31 的 DDL 需要真实表存在，
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

	// 先把上游 24–31 的记录清掉，模拟"我们 v25 库"（我们的 v24/v25 覆盖了同样的版本号）。
	if err := db.Where("version >= ? AND version <= ?", int64(24), PreviousUpstreamSchemaVersion).Delete(&schemaMigration{}).Error; err != nil {
		t.Fatalf("clear upstream records: %v", err)
	}

	// 逐条记录搬迁前的 applied_at，用来证明 v32/v33 没有被重跑。
	legacyAppliedAt := map[int64]time.Time{}
	for _, item := range legacyCloudAgentMigrationRelocations {
		var appliedAt time.Time
		if err := db.Model(&schemaMigration{}).Where("version = ?", item.to).Pluck("applied_at", &appliedAt).Error; err != nil {
			t.Fatalf("read applied_at: %v", err)
		}
		legacyAppliedAt[item.to] = appliedAt
		if err := db.Where("version = ?", item.to).Delete(&schemaMigration{}).Error; err != nil {
			t.Fatalf("drop relocated record %d: %v", item.to, err)
		}
		record := schemaMigration{Version: item.from, Name: item.name, Checksum: item.checksum, AppliedAt: appliedAt}
		if err := db.Create(&record).Error; err != nil {
			t.Fatalf("seed legacy record %d: %v", item.from, err)
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
	// v32/v33 是搬迁过来的老记录：applied_at 必须还是搬迁前的值。
	for _, item := range legacyCloudAgentMigrationRelocations {
		var applied schemaMigration
		if err := db.First(&applied, "version = ?", item.to).Error; err != nil {
			t.Fatalf("missing relocated record %d: %v", item.to, err)
		}
		if want := legacyAppliedAt[item.to]; !applied.AppliedAt.Equal(want) {
			t.Fatalf("v%d 被重新执行了：applied_at = %v，期望沿用 %v", item.to, applied.AppliedAt, want)
		}
	}
}

// resetToOurV25Layout 把已经迁到 33 的库改造成"我们 v25 库"的样子：
// 版本号 24/25 上是我们的两条记录，上游 24–31 与让位后的 32/33 记录都不存在。
// 表结构保持不动（真实库也是这个形态：表在，只是记录号与上游撞车）。
func resetToOurV25Layout(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := MigrateSchema(db); err != nil {
		t.Fatalf("bootstrap schema: %v", err)
	}
	if err := db.Where("version >= ?", int64(24)).Delete(&schemaMigration{}).Error; err != nil {
		t.Fatalf("clear migration records: %v", err)
	}
	seedMigrationRecords(t, db, ourCloudAgentVersionsAtLegacyVersions()...)
}

// ourCloudAgentVersionsAtLegacyVersions 返回"我们 v25 库"里那两条记录（版本号仍是 24/25）。
func ourCloudAgentVersionsAtLegacyVersions() []schemaMigration {
	records := []schemaMigration{}
	for _, item := range legacyCloudAgentMigrationRelocations {
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

// upstreamRecordsThrough 取 plan 里 1..max 的上游记录（用于模拟"上游 v31 库"）。
func upstreamRecordsThrough(max int64) []schemaMigration {
	records := []schemaMigration{}
	for _, item := range schemaMigrations {
		if item.version <= max {
			records = append(records, schemaMigration{Version: item.version, Name: item.name, Checksum: item.checksum})
		}
	}
	return records
}

// ourRecordsThrough 模拟"我们 v25 库"：1..23 与上游一致，24/25 是我们自己的名字与校验和。
func ourRecordsThrough(max int64) []schemaMigration {
	records := upstreamRecordsThrough(23)
	for _, item := range legacyCloudAgentMigrationRelocations {
		if item.from <= max {
			records = append(records, schemaMigration{Version: item.from, Name: item.name, Checksum: item.checksum})
		}
	}
	return records
}

func upstreamCloudAgentVersions() []schemaMigration {
	records := []schemaMigration{}
	for _, item := range schemaMigrations {
		if item.version >= 24 && item.version <= PreviousUpstreamSchemaVersion {
			records = append(records, schemaMigration{Version: item.version, Name: item.name, Checksum: item.checksum})
		}
	}
	return records
}

func ourCloudAgentVersions() []schemaMigration {
	records := []schemaMigration{}
	for _, item := range legacyCloudAgentMigrationRelocations {
		records = append(records, schemaMigration{Version: item.to, Name: item.name, Checksum: item.checksum})
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

// assertRelocated 校验：库版本到 33，且 v24/v25 上不再残留我们的名字。
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
