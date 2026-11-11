package main

import (
	"fmt"
	"log"

	"yingce/backend/internal/database"

	"gorm.io/gorm"
)

// runMigration keeps initialization, copying and reconciliation in one transaction.
// Only a successful COMMIT permits the final success message.
func runMigration(source, target *gorm.DB) error {
	var copied bool
	total := 0
	err := target.Transaction(func(tx *gorm.DB) error {
		tables, err := publicTableNames(tx)
		if err != nil {
			return err
		}
		copied = len(tables) == 0
		if copied {
			if err := database.MigrateSchema(tx); err != nil {
				return fmt.Errorf("创建目标表结构：%w", err)
			}
		} else if err := validateTargetTableNames(tables); err != nil {
			return err
		}
		for _, migration := range migrations() {
			count, err := migration.run(source, tx, copied)
			if err != nil {
				return fmt.Errorf("迁移表 %s：%w", migration.name, err)
			}
			total += count
			log.Printf("事务内已核对 %s：%d 行", migration.name, count)
		}
		// An existing target is verification-only, including its sequence rows.
		if copied {
			if err := database.ReconcilePrefixedIDSequences(tx); err != nil {
				return fmt.Errorf("校准可读 ID 序列：%w", err)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if copied {
		log.Printf("全量迁移核对完成：%d 张表，%d 行", len(migrations()), total)
	} else {
		log.Printf("目标库已有完整迁移结果，未重复写入：%d 张表，%d 行", len(migrations()), total)
	}
	return nil
}
