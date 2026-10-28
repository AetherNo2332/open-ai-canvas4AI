package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"yingce/backend/internal/model"
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

func TestLegacyWritesAdvanceCommandBaselinesAndRecoverableEvents(t *testing.T) {
	svc, db, _, before := parityCommandFixture(t)
	repo := repository.New(db)
	row, err := repo.CanvasProjectForUser("owner", before.CanvasID)
	if err != nil {
		t.Fatal(err)
	}
	document, err := cloneDocument(before.Document)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range maps(document["nodes"]) {
		if node["id"] == "a" {
			node["metadata"].(map[string]any)["content"] = "legacy"
		}
	}
	raw, _ := json.Marshal(document)
	row.PayloadJSON = string(raw)
	if err = repo.UpsertCanvasProject(row); err != nil {
		t.Fatal(err)
	}
	stale := edit(before, "a", "stale", "after-legacy")
	stale.ExpectedEntityVersions = before.EntityVersions
	_, err = svc.Execute(context.Background(), ownerContext().Principal, stale)
	assertReason(t, err, "entity_version_conflict")
	events, err := svc.ReadEvents(context.Background(), ownerContext().Principal, before.CanvasID, before.EventSeq, 10)
	if err != nil || len(events) != 1 || events[0].Reset != nil {
		t.Fatalf("legacy event: %+v %v", events, err)
	}
	event := events[0]
	if event.DocumentEpoch != before.DocumentEpoch || event.EntityVersions["node:a"] != before.Revision+1 || event.EntityVersions["node:b"] != 0 {
		t.Fatalf("entity versions: %+v", event)
	}
	after, err := svc.ReadSnapshot(context.Background(), ownerContext().Principal, before.CanvasID)
	projected, applyErr := ApplyChanges(before.Document, event.Changes)
	if err != nil || applyErr != nil || !reflect.DeepEqual(projected, after.Document) {
		t.Fatalf("recoverable reset differs: %+v %v", after, err)
	}
}

func TestLegacyEventFailureRollsBackDocumentAndVersions(t *testing.T) {
	svc, db, _, before := commandFixture(t)
	if err := db.Exec(`CREATE TRIGGER fail_legacy_event BEFORE INSERT ON canvas_command_events BEGIN SELECT RAISE(ABORT, 'event unavailable'); END;`).Error; err != nil {
		t.Fatal(err)
	}
	repo := repository.New(db)
	row, _ := repo.CanvasProjectForUser("owner", before.CanvasID)
	row.PayloadJSON = strings.Replace(row.PayloadJSON, `"content":""`, `"content":"failed"`, 1)
	row.Title = "failed"
	if err := repo.UpsertCanvasProject(row); err == nil {
		t.Fatal("acknowledged event failure")
	}
	after, err := svc.ReadSnapshot(context.Background(), ownerContext().Principal, before.CanvasID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("partial legacy commit: %+v %v", after, err)
	}
}

func TestIndependentServicesReplayConcurrentOperation(t *testing.T) {
	first, db, path, snapshot := parityCommandFixture(t)
	second := NewService(independentCommandRepository(t, path), ServiceOptions{})
	req := edit(snapshot, "a", "once", "concurrent-retry")
	start := make(chan struct{})
	results := make([]Receipt, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i, svc := range []*Service{first, second} {
		wg.Add(1)
		go func(i int, svc *Service) {
			defer wg.Done()
			<-start
			results[i], errs[i] = svc.Execute(context.Background(), ownerContext().Principal, req)
		}(i, svc)
	}
	close(start)
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || !reflect.DeepEqual(results[0], results[1]) {
		t.Fatalf("concurrent replay: %+v %+v", results, errs)
	}
	var count int64
	if err := db.Model(&model.CanvasCommandEvent{}).Where("operation_id = ?", req.OperationID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("event count %d: %v", count, err)
	}
}

func TestIndependentServicesInitializeOneEpoch(t *testing.T) {
	first, db, path, _ := parityCommandFixture(t)
	row := model.CanvasProject{ID: "uninitialized", UserID: "owner", Revision: 1, PayloadJSON: `{"id":"uninitialized","referenceFieldsVersion":1,"nodes":[],"connections":[]}`}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	second := NewService(independentCommandRepository(t, path), ServiceOptions{})
	start := make(chan struct{})
	results := make([]Snapshot, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i, svc := range []*Service{first, second} {
		wg.Add(1)
		go func(i int, svc *Service) {
			defer wg.Done()
			<-start
			results[i], errs[i] = svc.ReadSnapshot(context.Background(), ownerContext().Principal, row.ID)
		}(i, svc)
	}
	close(start)
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || results[0].DocumentEpoch == "" || results[0].DocumentEpoch != results[1].DocumentEpoch {
		t.Fatalf("epoch fork: %+v %+v", results, errs)
	}
}

func TestAccountQuotaSerializesCommandAndLegacyGrowth(t *testing.T) {
	_, db, path, first := parityCommandFixture(t)
	if err := db.AutoMigrate(&model.Asset{}); err != nil {
		t.Fatal(err)
	}
	r1 := repository.New(db)
	r2 := independentCommandRepository(t, path)
	s1 := NewService(r1, ServiceOptions{})
	s2 := NewService(r2, ServiceOptions{})
	second, err := s2.Create(context.Background(), ownerContext().Principal, CreateCanvasRequest{ClientID: "client", OperationID: "second"})
	if err != nil {
		t.Fatal(err)
	}
	usage, err := r1.CanvasStructuredStorageUsage("owner")
	if err != nil {
		t.Fatal(err)
	}
	limit := usage.CanvasBytes + 500
	quota := errors.New("account bytes exceeded")
	guard := func(tx *repository.Repository, owner string, creating bool, delta int64) error {
		current, err := tx.CanvasStructuredStorageUsage(owner)
		if err != nil {
			return err
		}
		if current.CanvasBytes+delta > limit {
			return quota
		}
		return nil
	}
	r1.SetCanvasWriteQuota(guard)
	r2.SetCanvasWriteQuota(guard)
	row, err := r2.CanvasProjectForUser("owner", second.CanvasID)
	if err != nil {
		t.Fatal(err)
	}
	second.Document["description"] = strings.Repeat("x", 300)
	raw, _ := json.Marshal(second.Document)
	row.PayloadJSON = string(raw)
	start := make(chan struct{})
	errs := make([]error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, errs[0] = s1.Execute(context.Background(), ownerContext().Principal, edit(first, "a", strings.Repeat("x", 300), "quota-growth"))
	}()
	go func() { defer wg.Done(); <-start; errs[1] = r2.UpsertCanvasProject(row) }()
	close(start)
	wg.Wait()
	if (errs[0] == nil) == (errs[1] == nil) || (errs[0] != nil && !errors.Is(errs[0], quota)) || (errs[1] != nil && !errors.Is(errs[1], quota)) {
		t.Fatalf("quota results: %+v", errs)
	}
	usage, err = r1.CanvasStructuredStorageUsage("owner")
	if err != nil || usage.CanvasBytes > limit {
		t.Fatalf("quota overspent: %+v %v", usage, err)
	}
}

func TestLegacyRestoreAndDeleteInvalidateEpoch(t *testing.T) {
	svc, db, _, before := commandFixture(t)
	if err := db.AutoMigrate(&model.CanvasSnapshot{}, &model.CanvasSnapshotResource{}, &model.CanvasShare{}, &model.CanvasUnitLink{}); err != nil {
		t.Fatal(err)
	}
	repo := repository.New(db)
	row, _ := repo.CanvasProjectForUser("owner", before.CanvasID)
	prior := *row
	snap := &model.CanvasSnapshot{ID: "restore-backup", CanvasID: row.ID, UserID: row.UserID, Revision: row.Revision, PayloadJSON: row.PayloadJSON, Reason: "before_restore", CreatedAt: time.Now()}
	if err := repo.SaveCanvasWithSnapshot(row, snap, nil, nil, time.Now(), 20, true); err != nil {
		t.Fatal(err)
	}
	after, err := svc.ReadSnapshot(context.Background(), ownerContext().Principal, row.ID)
	if err != nil || after.DocumentEpoch == before.DocumentEpoch {
		t.Fatalf("restore epoch: %+v %v", after, err)
	}
	if err = repo.DeleteCanvasProject("owner", row.ID); err != nil {
		t.Fatal(err)
	}
	events, err := svc.ReadEvents(context.Background(), ownerContext().Principal, row.ID, after.EventSeq, 10)
	if err != nil || len(events) != 1 || events[0].Reset == nil || events[0].Reset.Document["_deleted"] != true {
		t.Fatalf("delete event: %+v %v", events, err)
	}
	prior.Revision = 0
	if err = repo.UpsertCanvasProject(&prior); err != nil {
		t.Fatal(err)
	}
	reborn, err := svc.ReadSnapshot(context.Background(), ownerContext().Principal, row.ID)
	if err != nil || reborn.DocumentEpoch == after.DocumentEpoch || reborn.EventSeq <= events[0].EventSeq {
		t.Fatalf("recreated epoch: %+v %v", reborn, err)
	}
}

func TestAccountQuotaSerializesAssetAndCanvasGrowth(t *testing.T) {
	_, db, path, before := parityCommandFixture(t)
	if err := db.AutoMigrate(&model.Asset{}); err != nil {
		t.Fatal(err)
	}
	r1, r2 := repository.New(db), independentCommandRepository(t, path)
	svc := NewService(r1, ServiceOptions{})
	usage, err := r1.CanvasStructuredStorageUsage("owner")
	if err != nil {
		t.Fatal(err)
	}
	limit := usage.CanvasBytes + 500
	quota := errors.New("structured account quota")
	guard := func(tx *repository.Repository, owner, kind string, countDelta, byteDelta int64) error {
		current, err := tx.CanvasStructuredStorageUsage(owner)
		if err != nil {
			return err
		}
		if current.CanvasBytes+current.AssetBytes+byteDelta > limit {
			return quota
		}
		return nil
	}
	r1.SetStructuredWriteQuota(guard)
	r2.SetStructuredWriteQuota(guard)
	asset := model.Asset{ID: "quota-asset", UserID: "owner", PayloadJSON: strings.Repeat("x", 300)}
	start := make(chan struct{})
	errs := make([]error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, errs[0] = svc.Execute(context.Background(), ownerContext().Principal, edit(before, "a", strings.Repeat("x", 300), "asset-quota-growth"))
	}()
	go func() { defer wg.Done(); <-start; errs[1] = r2.UpsertAsset(&asset) }()
	close(start)
	wg.Wait()
	if (errs[0] == nil) == (errs[1] == nil) || (errs[0] != nil && !errors.Is(errs[0], quota)) || (errs[1] != nil && !errors.Is(errs[1], quota)) {
		t.Fatalf("asset/canvas quota results: %+v", errs)
	}
	usage, err = r1.CanvasStructuredStorageUsage("owner")
	if err != nil || usage.CanvasBytes+usage.AssetBytes > limit {
		t.Fatalf("structured quota overspent: %+v %v", usage, err)
	}
}

func TestAccountCanvasCountQuotaAndCreateReplayAcrossServices(t *testing.T) {
	_, db, path, _ := parityCommandFixture(t)
	if err := db.AutoMigrate(&model.Asset{}); err != nil {
		t.Fatal(err)
	}
	r1, r2 := repository.New(db), independentCommandRepository(t, path)
	quota := errors.New("canvas count exceeded")
	guard := func(tx *repository.Repository, owner string, creating bool, delta int64) error {
		usage, err := tx.CanvasStructuredStorageUsage(owner)
		if err != nil {
			return err
		}
		if creating && usage.CanvasCount >= 2 {
			return quota
		}
		return nil
	}
	r1.SetCanvasWriteQuota(guard)
	r2.SetCanvasWriteQuota(guard)
	services := []*Service{NewService(r1, ServiceOptions{}), NewService(r2, ServiceOptions{})}
	start := make(chan struct{})
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i, svc := range services {
		wg.Add(1)
		go func(i int, svc *Service) {
			defer wg.Done()
			<-start
			_, errs[i] = svc.Create(context.Background(), ownerContext().Principal, CreateCanvasRequest{ClientID: "client", OperationID: fmt.Sprintf("count-%d", i)})
		}(i, svc)
	}
	close(start)
	wg.Wait()
	if (errs[0] == nil) == (errs[1] == nil) || (errs[0] != nil && !errors.Is(errs[0], quota)) || (errs[1] != nil && !errors.Is(errs[1], quota)) {
		t.Fatalf("count quota results: %+v", errs)
	}
	winner := 0
	if errs[0] != nil {
		winner = 1
	}
	replayed, err := services[1-winner].Create(context.Background(), ownerContext().Principal, CreateCanvasRequest{ClientID: "client", OperationID: fmt.Sprintf("count-%d", winner)})
	if err != nil || replayed.CanvasID == "" {
		t.Fatalf("quota blocked durable create replay: %+v %v", replayed, err)
	}
}

func TestQuotaGuardSurvivesNestedRepositoryTransactions(t *testing.T) {
	_, db, _, before := commandFixture(t)
	repo := repository.New(db)
	quota := errors.New("nested quota guard")
	repo.SetCanvasWriteQuota(func(*repository.Repository, string, bool, int64) error { return quota })
	err := repo.CommandTransaction(context.Background(), func(tx *repository.Repository) error {
		row, err := tx.CanvasProjectForUser("owner", before.CanvasID)
		if err != nil {
			return err
		}
		return tx.UpsertCanvasProject(row)
	})
	if !errors.Is(err, quota) {
		t.Fatalf("lost nested transaction quota: %v", err)
	}
}

func TestQuotaRejectionRollsBackHostAndRestore(t *testing.T) {
	svc, db, _, before := commandFixture(t)
	quota := errors.New("write quota rejected")
	svc.options.Quota = func(*repository.Repository, string, bool, int64) error { return quota }
	req := edit(before, "a", "host", "host-quota")
	_, fresh, err := svc.ExecuteHost(context.Background(), ownerContext().Principal, req, "task_result", nil, false, func(tx *repository.Repository, doc Document) (Document, map[string]bool, error) {
		if err := tx.Create(&model.Task{ID: "quota-host-task", UserID: "owner"}); err != nil {
			return nil, nil, err
		}
		for _, node := range maps(doc["nodes"]) {
			if node["id"] == "a" {
				node["metadata"].(map[string]any)["content"] = "host"
			}
		}
		return doc, nil, nil
	})
	if !errors.Is(err, quota) || fresh {
		t.Fatalf("host quota acknowledged: %v fresh=%v", err, fresh)
	}
	var count int64
	if err = db.Model(&model.Task{}).Where("id = ?", "quota-host-task").Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("host side effect survived quota: %d %v", count, err)
	}
	restored, err := cloneDocument(before.Document)
	if err != nil {
		t.Fatal(err)
	}
	restored["title"] = "restore"
	_, err = svc.RestoreVersion(context.Background(), ownerContext().Principal, before.CanvasID, before.DocumentEpoch, "client", "restore-quota", before.Revision, restored)
	if !errors.Is(err, quota) {
		t.Fatalf("restore quota acknowledged: %v", err)
	}
	after, err := svc.ReadSnapshot(context.Background(), ownerContext().Principal, before.CanvasID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("quota partially changed state: %+v %v", after, err)
	}
	if err = db.Model(&model.CanvasActorHistory{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("restore backup survived quota: %d %v", count, err)
	}
}
