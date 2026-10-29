package tasks

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"yingce/backend/internal/canvas/commands"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func TestCanvasMediaAdmissionPostgres(t *testing.T) {
	dsn := os.Getenv("CANVAS_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("CANVAS_TEST_POSTGRES_DSN is required")
	}
	open := func(dsn string) *gorm.DB {
		t.Helper()
		db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn, _ := db.DB(); _ = conn.Close() })
		return db
	}
	admin := open(dsn)
	schema := fmt.Sprintf("canvas_media_%d", time.Now().UnixNano())
	if err := admin.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := admin.Exec("DROP SCHEMA " + schema + " CASCADE").Error; err != nil {
			t.Error(err)
		}
	}()
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	firstDB, secondDB := open(parsed.String()), open(parsed.String())
	first, _, _, cs, p, req, one := seedTaskFixture(t, firstDB)
	snap, err := cs.ReadSnapshot(context.Background(), p, req.Command.CanvasID)
	if err != nil {
		t.Fatal(err)
	}
	seed := req.Command
	seed.OperationID = "image-target"
	seed.BaseRevision = snap.Revision
	_, _, err = cs.ExecuteHost(context.Background(), p, seed, "task_submit", nil, false, func(_ *repository.Repository, doc commands.Document) (commands.Document, map[string]bool, error) {
		findNode(doc, "target")["type"] = "image"
		return doc, nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	snap, err = cs.ReadSnapshot(context.Background(), p, req.Command.CanvasID)
	if err != nil {
		t.Fatal(err)
	}
	req.Command.BaseRevision = snap.Revision
	req.Task.Mode = "image"
	otherRepo := repository.New(secondDB)
	two := &stubSubmitter{}
	second := NewService(otherRepo, commands.NewService(otherRepo, commands.ServiceOptions{}), two)
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() { <-start; _, err := first.Submit(context.Background(), p, req); results <- err }()
	go func() {
		<-start
		p2, req2 := p, req
		p2.ClientID = "device2"
		req2.Command.ClientID = "device2"
		req2.Command.OperationID = "other-attempt"
		_, err := second.Submit(context.Background(), p2, req2)
		results <- err
	}()
	close(start)
	accepted := 0
	for range 2 {
		if err := <-results; err == nil {
			accepted++
		}
	}
	if accepted != 1 || one.calls+two.calls != 1 || one.wakes+two.wakes != 1 {
		t.Fatalf("duplicate cross-device admission: accepted=%d prepared=%d wakes=%d", accepted, one.calls+two.calls, one.wakes+two.wakes)
	}
	for _, table := range []any{&model.Task{}, &model.CanvasTaskBinding{}} {
		var count int64
		if err := firstDB.Model(table).Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("atomic task/version admission %T=%d err=%v", table, count, err)
		}
	}
}
