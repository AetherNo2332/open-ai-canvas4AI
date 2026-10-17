package database

import (
	"path/filepath"
	"testing"
)

func TestDesktopChecksEffectiveWALFullForeignKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "desktop.db")
	db, err := Open(Config{DSN: path + "?_journal_mode=WAL&_synchronous=FULL&_foreign_keys=on"})
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	defer sql.Close()
	if err = CheckDesktopStorage(db); err != nil {
		t.Fatal(err)
	}
	if err = db.Exec("PRAGMA synchronous = NORMAL").Error; err != nil {
		t.Fatal(err)
	}
	if err = CheckDesktopStorage(db); err == nil {
		t.Fatal("weaker durability silently accepted")
	}
}
