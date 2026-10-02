package database_test

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

func openAgentAdmissionAuditPostgresDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("TEST_AGENT_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("TEST_AGENT_POSTGRES_DSN is not set")
	}
	base, err := database.Open(database.Config{Driver: "postgres", DSN: dsn})
	if err != nil {
		t.Fatal("could not open the configured Agent admission integration database")
	}
	baseSQL, err := base.DB()
	if err != nil {
		t.Fatal("could not inspect the Agent admission integration database")
	}
	t.Cleanup(func() { _ = baseSQL.Close() })
	var name string
	if err := base.Raw("SELECT current_database()").Scan(&name).Error; err != nil {
		t.Fatal("could not verify the Agent admission integration database")
	}
	if name != "canvas_event_integration" {
		t.Skip("TEST_AGENT_POSTGRES_DSN does not point to canvas_event_integration")
	}
	schema := fmt.Sprintf("agent_admission_audit_%d", time.Now().UnixNano())
	if err := base.Exec(`CREATE SCHEMA "` + schema + `"`).Error; err != nil {
		t.Fatal("could not create isolated Agent admission schema")
	}
	t.Cleanup(func() {
		if err := base.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`).Error; err != nil {
			t.Errorf("could not drop isolated Agent admission schema: %v", err)
		}
	})
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal("could not parse Agent admission test connection")
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := database.Open(database.Config{Driver: "postgres", DSN: parsed.String()})
	if err != nil {
		t.Fatal("could not open isolated Agent admission schema")
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal("could not inspect isolated Agent admission schema")
	}
	sqlDB.SetMaxOpenConns(8)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func seedAuditRun(t *testing.T, db *gorm.DB, id, user, canvas string, expires *time.Time, epoch int64) {
	t.Helper()
	run := model.CloudAgentExecution{ID: id, UserID: user, CanvasID: canvas, ConversationID: id, Engine: "pi", Status: "queued", LeaseExpiresAt: expires}
	if err := db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	session := model.CloudAgentPiSession{ID: model.PiSessionStorageID(user, id), UserID: user, ConversationID: id, CanvasID: canvas, HeaderJSON: `{}`, ActiveRunID: id, LeaseEpoch: epoch, LeaseExpiresAt: expires}
	if err := db.Create(&session).Error; err != nil {
		t.Fatal(err)
	}
}

func agentAdmissionAuditID() string { return fmt.Sprintf("aa%030x", time.Now().UnixNano()) }

func TestAgentAdmissionAuditSQLiteRollbackAndExpiredLeaseFencing(t *testing.T) {
	repo, db := openAgentSchedulerRepositoryDB(t)
	if err := db.AutoMigrate(&model.AgentSchedulerSetting{}, &model.AgentAdmissionCounter{}, &model.AgentCanvasAdmission{}); err != nil {
		t.Fatal("could not prepare Agent admission tables")
	}
	suffix := fmt.Sprintf("audit-sqlite-%d", time.Now().UnixNano())
	expired := time.Now().Add(-time.Minute)
	seedAuditRun(t, db, suffix, suffix+"-user", "canvas", &expired, 8)
	if err := db.Exec(`CREATE TRIGGER reject_agent_admission BEFORE INSERT ON agent_canvas_admissions BEGIN SELECT RAISE(ABORT, 'audit forced rollback'); END`).Error; err != nil {
		t.Fatal("could not install forced rollback trigger")
	}
	p := model.DefaultAgentSchedulerSetting()
	if run, err := repo.ClaimPiAgentFair(suffix+"-rollback", time.Now().Add(time.Minute), p); err == nil || run != nil {
		t.Fatalf("forced admission failure did not abort claim: run=%v err=%v", run, err)
	}
	var run model.CloudAgentExecution
	if err := db.First(&run, "id = ?", suffix).Error; err != nil {
		t.Fatal("could not reread execution after rollback")
	}
	var session model.CloudAgentPiSession
	if err := db.Where("active_run_id = ?", suffix).First(&session).Error; err != nil {
		t.Fatal("could not reread session after rollback")
	}
	var counter model.AgentAdmissionCounter
	if err := db.First(&counter, 1).Error; err != nil && err != gorm.ErrRecordNotFound {
		t.Fatal("could not reread admission counter after rollback")
	}
	if run.LeaseOwner != "" || session.LeaseEpoch != 8 || session.LeaseOwner != "" || counter.Value != 0 {
		t.Fatalf("failed transaction left partial admission: runOwner=%q sessionOwner=%q epoch=%d counter=%d", run.LeaseOwner, session.LeaseOwner, session.LeaseEpoch, counter.Value)
	}
	if err := db.Exec("DROP TRIGGER reject_agent_admission").Error; err != nil {
		t.Fatal("could not remove forced rollback trigger")
	}
	claimed, err := repo.ClaimPiAgentFair(suffix+"-current", time.Now().Add(time.Minute), p)
	if err != nil || claimed == nil {
		t.Fatalf("expired lease was not recoverable: run=%v err=%v", claimed, err)
	}
	var recovered model.CloudAgentPiSession
	if err := db.Where("active_run_id = ?", suffix).First(&recovered).Error; err != nil {
		t.Fatal("could not read recovered session")
	}
	if recovered.LeaseEpoch != 9 || recovered.LeaseOwner != suffix+"-current" {
		t.Fatalf("takeover did not advance fencing epoch: owner=%q epoch=%d", recovered.LeaseOwner, recovered.LeaseEpoch)
	}
	if ok, err := repo.RenewPiAgentLease(claimed.UserID, claimed.ID, recovered.LeaseOwner, 8, time.Now().Add(2*time.Minute)); err != nil || ok {
		t.Fatalf("old epoch renewed recovered lease: ok=%v err=%v", ok, err)
	}
	if ok, err := repo.RenewPiAgentLease(claimed.UserID, claimed.ID, recovered.LeaseOwner, recovered.LeaseEpoch, time.Now().Add(2*time.Minute)); err != nil || !ok {
		t.Fatalf("current epoch failed to renew lease: ok=%v err=%v", ok, err)
	}
	seedAuditRun(t, db, suffix+"-u1", suffix+"-user1", "shared", nil, 0)
	seedAuditRun(t, db, suffix+"-u2", suffix+"-user2", "shared", nil, 0)
	p.MaxResidentPerCanvas = 1
	first, err := repo.ClaimPiAgentFair(suffix+"-worker-1", time.Now().Add(time.Minute), p)
	if err != nil || first == nil {
		t.Fatalf("first user could not claim shared canvas ID: run=%v err=%v", first, err)
	}
	second, err := repo.ClaimPiAgentFair(suffix+"-worker-2", time.Now().Add(time.Minute), p)
	if err != nil || second == nil || first.UserID == second.UserID {
		t.Fatalf("same canvas ID across users shared a quota bucket: first=%v second=%v err=%v", first, second, err)
	}
}

func TestAgentAdmissionAuditPostgresCompetingExecutorsAndUserIsolation(t *testing.T) {
	db := openAgentAdmissionAuditPostgresDB(t)
	if err := db.AutoMigrate(&model.AgentSchedulerSetting{}, &model.AgentAdmissionCounter{}, &model.AgentCanvasAdmission{}, &model.CloudAgentExecution{}, &model.CloudAgentPiSession{}); err != nil {
		t.Fatal("could not prepare Agent admission tables")
	}
	suffix := agentAdmissionAuditID()
	// Identical canvas IDs belong to independent per-user quota buckets.
	seedAuditRun(t, db, suffix+"-u1", suffix+"1", "shared", nil, 3)
	seedAuditRun(t, db, suffix+"-u2", suffix+"2", "shared", nil, 7)
	p := model.DefaultAgentSchedulerSetting()
	p.MaxResidentPerCanvas = 1
	r := repository.New(db)
	start := make(chan struct{})
	var wg sync.WaitGroup
	got := make(chan *model.CloudAgentExecution, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			run, err := r.ClaimPiAgentFair(fmt.Sprintf("%s-worker-%d", suffix, i), time.Now().Add(time.Minute), p)
			got <- run
			errs <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(got)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("competing claim failed: %v", err)
		}
	}
	users := map[string]bool{}
	for run := range got {
		if run == nil {
			t.Fatal("cross-user canvas quota incorrectly blocked an independent user")
		}
		users[run.UserID] = true
	}
	if !users[suffix+"1"] || !users[suffix+"2"] {
		t.Fatalf("claims crossed or omitted a user bucket: %+v", users)
	}
}

func TestAgentAdmissionAuditPostgresEnforcesCanvasCapUnderRace(t *testing.T) {
	db := openAgentAdmissionAuditPostgresDB(t)
	if err := db.AutoMigrate(&model.AgentSchedulerSetting{}, &model.AgentAdmissionCounter{}, &model.AgentCanvasAdmission{}, &model.CloudAgentExecution{}, &model.CloudAgentPiSession{}); err != nil {
		t.Fatal("could not prepare Agent admission tables")
	}
	suffix := agentAdmissionAuditID()
	for i := 0; i < 2; i++ {
		seedAuditRun(t, db, fmt.Sprintf("%s-%d", suffix, i), suffix+"u", "canvas", nil, 0)
	}
	p := model.DefaultAgentSchedulerSetting()
	p.MaxResidentPerCanvas = 1
	r := repository.New(db)
	start := make(chan struct{})
	results := make(chan *model.CloudAgentExecution, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			run, err := r.ClaimPiAgentFair(fmt.Sprintf("%s-worker-%d", suffix, i), time.Now().Add(time.Minute), p)
			results <- run
			errs <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("competing claim failed: %v", err)
		}
	}
	claimed := 0
	for run := range results {
		if run != nil {
			claimed++
		}
	}
	if claimed != 1 {
		t.Fatalf("canvas cap under race admitted %d leases, want 1", claimed)
	}
}

func TestAgentAdmissionAuditPostgresExpiredLeaseTakeoverFencesEpoch(t *testing.T) {
	db := openAgentAdmissionAuditPostgresDB(t)
	if err := db.AutoMigrate(&model.AgentSchedulerSetting{}, &model.AgentAdmissionCounter{}, &model.AgentCanvasAdmission{}, &model.CloudAgentExecution{}, &model.CloudAgentPiSession{}); err != nil {
		t.Fatal("could not prepare Agent admission tables")
	}
	suffix := agentAdmissionAuditID()
	expired := time.Now().Add(-time.Minute)
	seedAuditRun(t, db, suffix, suffix+"u", "canvas", &expired, 8)
	p := model.DefaultAgentSchedulerSetting()
	run, err := repository.New(db).ClaimPiAgentFair(suffix+"-new", time.Now().Add(time.Minute), p)
	if err != nil || run == nil {
		t.Fatalf("expired lease was not recoverable: run=%v err=%v", run, err)
	}
	var session model.CloudAgentPiSession
	if err := db.Where("active_run_id = ?", suffix).First(&session).Error; err != nil {
		t.Fatal("could not read recovered session")
	}
	if session.LeaseOwner != suffix+"-new" || session.LeaseEpoch != 9 {
		t.Fatalf("takeover did not advance fencing epoch: owner=%q epoch=%d", session.LeaseOwner, session.LeaseEpoch)
	}
}

func TestAgentAdmissionAuditPostgresConfigSaveWaitsForAdmissionCommit(t *testing.T) {
	db := openAgentAdmissionAuditPostgresDB(t)
	if err := db.AutoMigrate(&model.AgentSchedulerSetting{}, &model.AgentAdmissionCounter{}, &model.AdminAuditEvent{}, &model.AgentEventCounter{}, &model.AgentWakeEvent{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.AgentAdmissionCounter{ID: 1}).Error; err != nil {
		t.Fatal(err)
	}
	held, release := make(chan struct{}), make(chan struct{})
	holder := make(chan error, 1)
	go func() {
		holder <- db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&model.AgentAdmissionCounter{}).Where("id = ?", 1).UpdateColumn("value", gorm.Expr("value + 0")).Error; err != nil {
				return err
			}
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	saved := make(chan error, 1)
	go func() {
		p := model.DefaultAgentSchedulerSetting()
		saved <- repository.New(db).SaveAgentSchedulerSetting(&p, 0, &model.AdminAuditEvent{ID: "admission-config-audit"})
	}()
	select {
	case err := <-saved:
		close(release)
		<-holder
		t.Fatalf("save overtook uncommitted admission: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	close(release)
	if err := <-holder; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-saved:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("config save remained blocked after admission commit")
	}
}

func TestAgentAdmissionAuditPostgresSettingsCASHasOneWinner(t *testing.T) {
	db := openAgentAdmissionAuditPostgresDB(t)
	if err := db.AutoMigrate(&model.AgentSchedulerSetting{}, &model.AgentAdmissionCounter{}, &model.AgentWakeEvent{}, &model.AgentEventCounter{}, &model.AdminAuditEvent{}); err != nil {
		t.Fatal("could not prepare scheduler settings tables")
	}
	r := repository.New(db)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p := model.DefaultAgentSchedulerSetting()
			p.DispatchConcurrency = 5 + i
			p.UpdatedBy = agentAdmissionAuditID()
			<-start
			results <- r.SaveAgentSchedulerSetting(&p, 0, &model.AdminAuditEvent{ID: fmt.Sprintf("%s-%02d", agentAdmissionAuditID(), i)})
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	wins, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			wins++
		case err == repository.ErrCreationConflict:
			conflicts++
		default:
			t.Fatalf("unexpected concurrent settings save error: %v", err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("CAS results: winners=%d conflicts=%d, want one each", wins, conflicts)
	}
	var stored model.AgentSchedulerSetting
	if err := db.First(&stored, 1).Error; err != nil {
		t.Fatal("could not read saved scheduler settings")
	}
	var audits, events int64
	if err := db.Model(&model.AdminAuditEvent{}).Count(&audits).Error; err != nil {
		t.Fatal("could not count scheduler audits")
	}
	if err := db.Model(&model.AgentWakeEvent{}).Where("kind = ?", "scheduler_config_changed").Count(&events).Error; err != nil {
		t.Fatal("could not count scheduler config events")
	}
	if stored.Revision != 1 || audits != 1 || events != 1 {
		t.Fatalf("CAS side effects were not atomic: revision=%d audits=%d configEvents=%d", stored.Revision, audits, events)
	}
}

func TestAgentAdmissionAuditPostgresConfigWritesAndClaimsDoNotDeadlock(t *testing.T) {
	db := openAgentAdmissionAuditPostgresDB(t)
	if err := db.AutoMigrate(&model.AgentSchedulerSetting{}, &model.AgentAdmissionCounter{}, &model.AgentCanvasAdmission{}, &model.AgentWakeEvent{}, &model.AgentEventCounter{}, &model.AdminAuditEvent{}, &model.CloudAgentExecution{}, &model.CloudAgentPiSession{}); err != nil {
		t.Fatal("could not prepare scheduler and admission tables")
	}
	database.InstallAgentEventCallbacks(db)
	suffix := agentAdmissionAuditID()
	for i := 0; i < 12; i++ {
		seedAuditRun(t, db, fmt.Sprintf("%s-%02d", suffix, i), suffix+"u", "canvas", nil, 0)
	}
	r := repository.New(db)
	start := make(chan struct{})
	errs := make(chan error, 5)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for revision := int64(0); revision < 8; revision++ {
			p := model.DefaultAgentSchedulerSetting()
			p.DispatchConcurrency = 4 + int(revision%4)
			p.UpdatedBy = agentAdmissionAuditID()
			if err := r.SaveAgentSchedulerSetting(&p, revision, &model.AdminAuditEvent{ID: agentAdmissionAuditID()}); err != nil {
				errs <- fmt.Errorf("settings revision %d: %w", revision, err)
				return
			}
		}
		errs <- nil
	}()
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			<-start
			for claim := 0; claim < 3; claim++ {
				run, err := r.ClaimPiAgentFair(fmt.Sprintf("%s-worker-%d", suffix, worker), time.Now().Add(time.Minute), model.DefaultAgentSchedulerSetting())
				if err != nil || run == nil {
					errs <- fmt.Errorf("worker %d claim %d: run=%v err=%v", worker, claim, run, err)
					return
				}
			}
			errs <- nil
		}(worker)
	}
	close(start)
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("scheduler setting updates and admissions deadlocked or exceeded 10 seconds")
	}
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}
