package commands

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"yingce/backend/internal/repository"

	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func independentCommandRepository(t *testing.T, path string) *repository.Repository {
	t.Helper()
	var dialect gorm.Dialector = sqlite.Open(path + "?_busy_timeout=5000&_journal_mode=WAL&_foreign_keys=on")
	if strings.HasPrefix(path, "postgres://") || strings.HasPrefix(path, "postgresql://") {
		dialect = postgres.Open(path)
	}
	db, err := gorm.Open(dialect, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB, _ := db.DB(); _ = sqlDB.Close() })
	return repository.New(db)
}

func parityCommandFixture(t *testing.T) (*Service, *gorm.DB, string, Snapshot) {
	t.Helper()
	dsn := os.Getenv("CANVAS_TEST_POSTGRES_DSN")
	if dsn == "" {
		return commandFixture(t)
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("canvas_write_boundary_%d", time.Now().UnixNano())
	if err = admin.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := admin.Exec("DROP SCHEMA " + schema + " CASCADE").Error; err != nil {
			t.Errorf("cleanup isolated schema: %v", err)
		}
		sqlDB, _ := admin.DB()
		_ = sqlDB.Close()
	})
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(parsed.String()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB, _ := db.DB(); _ = sqlDB.Close() })
	t.Log("using isolated PostgreSQL schema", schema)
	return seedCommandFixture(t, db, parsed.String())
}
