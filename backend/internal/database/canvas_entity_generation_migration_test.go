package database

import (
	"path/filepath"
	"testing"
)

func TestCanvasEntityGenerationMigrationPreservesLegacyZero(t *testing.T) {
	db, err := Open(Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "generation.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw, _ := db.DB(); _ = raw.Close() })
	if err := db.Exec("CREATE TABLE canvas_entity_versions (canvas_id TEXT, epoch TEXT, entity_key TEXT, version INTEGER NOT NULL, deleted NUMERIC NOT NULL DEFAULT 0, PRIMARY KEY(canvas_id,epoch,entity_key))").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO canvas_entity_versions(canvas_id,epoch,entity_key,version,deleted) VALUES ('canvas','epoch','node:a',9,0)").Error; err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range schemaMigrations {
		if item.version == 64 {
			found = true
			for range 2 {
				if err := item.apply(db); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if !found {
		t.Fatal("generation forward migration 64 missing")
	}
	var stored struct{ Version, Generation int64 }
	if err := db.Raw("SELECT version,generation FROM canvas_entity_versions WHERE entity_key='node:a'").Scan(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Version != 9 || stored.Generation != 0 {
		t.Fatalf("legacy row changed: %+v", stored)
	}
	if err := db.Exec("INSERT INTO canvas_entity_versions(canvas_id,epoch,entity_key,version,deleted) VALUES ('canvas','epoch','node:b',10,0)").Error; err != nil {
		t.Fatal(err)
	}
}
