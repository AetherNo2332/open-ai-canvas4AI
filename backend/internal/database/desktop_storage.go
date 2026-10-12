package database

import (
	"fmt"
	"gorm.io/gorm"
	"strings"
)

// Check effective settings on a real connection, rather than treating DSN
// options as evidence. FULL still depends on the filesystem/device honoring
// flushes; process-crash tests do not validate a power-loss guarantee.
func CheckDesktopStorage(db *gorm.DB) error {
	var mode string
	var sync, fk int
	if err := db.Raw("PRAGMA journal_mode").Scan(&mode).Error; err != nil {
		return err
	}
	if err := db.Raw("PRAGMA synchronous").Scan(&sync).Error; err != nil {
		return err
	}
	if err := db.Raw("PRAGMA foreign_keys").Scan(&fk).Error; err != nil {
		return err
	}
	if !strings.EqualFold(mode, "wal") || sync != 2 || fk != 1 {
		return fmt.Errorf("desktop SQLite requires WAL, FULL and foreign keys (actual %s/%d/%d)", mode, sync, fk)
	}
	return nil
}
