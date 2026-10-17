package desktop

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"infinite-canvas/backend/internal/app"
	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/repository"
)

func TestBootstrapOwnerConcurrent(t *testing.T) {
	db, err := database.Open(database.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	t.Cleanup(func() { _ = sql.Close() })
	if err := database.MigrateDesktop(db); err != nil {
		t.Fatal(err)
	}
	repo := repository.New(db)
	var owners [8]identity.Principal
	var failures [8]error
	var wg sync.WaitGroup
	for i := range owners {
		wg.Add(1)
		go func(i int) { defer wg.Done(); owners[i], failures[i] = BootstrapOwner(context.Background(), repo) }(i)
	}
	wg.Wait()
	for i := range owners {
		if failures[i] != nil {
			t.Fatal(failures[i])
		}
		if owners[i].ID == "" || owners[i].ID != owners[0].ID {
			t.Fatalf("owner changed: %+v", owners)
		}
	}
	var count int64
	if err := db.Table("local_profiles").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("profiles=%d", count)
	}
	again, err := BootstrapOwner(context.Background(), repo)
	if err != nil || again.ID != owners[0].ID {
		t.Fatalf("restart changed owner: %v", err)
	}
}

func TestReadyAfterDatabaseFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(path, []byte("retain"), 0600); err != nil {
		t.Fatal(err)
	}
	var ready bytes.Buffer
	err := Run(context.Background(), Config{DataDir: path, WebDir: dir, MasterKey: bytes.Repeat([]byte{1}, 32), Ready: &ready})
	if err == nil || ready.Len() != 0 {
		t.Fatalf("err=%v ready=%s", err, ready.String())
	}
}

type failedSecretProvider struct{}

func (failedSecretProvider) MasterKey() ([]byte, error) {
	return nil, errors.New("OS storage unavailable")
}

func TestSecretProviderFailure(t *testing.T) {
	dir := t.TempDir()
	svc, err := app.NewDesktop(nil, dir, failedSecretProvider{})
	if err == nil || svc != nil {
		t.Fatalf("did not fail closed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".settings-key")); !os.IsNotExist(err) {
		t.Fatal("plaintext fallback created")
	}
}
