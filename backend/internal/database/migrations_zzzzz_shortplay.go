package database

import (
	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

// shortplayWorkbenchMigrationVersion 建立独立 shortplay 工作台的六张表：
// 作品域配置、分集（集行即画布归属权威）、资产（项目内整数身份）、资产图片与音色
// 历史行、后台 job 队列。表在空库由基线 AutoMigrate 建出，本迁移只负责已存在库的升级。
const shortplayWorkbenchMigrationVersion int64 = 112

const shortplayWorkbenchChecksum = "sha256:shortplay-workbench-tables-v112-20261126"

// v112 的登记放在本文件的 init 里：migrations.go 已到单文件行数上限，
// 该包其它迁移（creation_platform、user_submap_preset 等）同样用 init 追加。
// 文件名必须排在 migrations_zzzz_user_submap_preset.go（v111）之后，
// 因为 Go 按文件名顺序执行 init，追加顺序就是迁移计划顺序。
func init() {
	schemaMigrations = append(schemaMigrations, migration{
		version:  shortplayWorkbenchMigrationVersion,
		name:     "shortplay_workbench_tables",
		checksum: shortplayWorkbenchChecksum,
		apply:    migrateShortplayWorkbench,
	})
}

func migrateShortplayWorkbench(tx *gorm.DB) error {
	if err := tx.AutoMigrate(
		&model.Shortplay{},
		&model.ShortplayEpisode{},
		&model.ShortplayAsset{},
		&model.ShortplayAssetImage{},
		&model.ShortplayAssetAudio{},
		&model.ShortplayJob{},
	); err != nil {
		return err
	}
	// 去重键唯一索引只约束未结束的 job：结束后同键可以再次入队。GORM 的 where 标签
	// 已在模型上声明，这里为已存在的库补一次幂等创建（PostgreSQL 与 SQLite 语法一致）。
	if tx.Migrator().HasTable(&model.ShortplayJob{}) && !tx.Migrator().HasIndex(&model.ShortplayJob{}, "idx_shortplay_jobs_dedupe_active") {
		return tx.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_shortplay_jobs_dedupe_active ON shortplay_jobs(dedupe_key) WHERE dedupe_key <> '' AND (status = 'queued' OR status = 'running')").Error
	}
	return nil
}
