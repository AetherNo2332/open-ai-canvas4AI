package repository

import (
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"yingce/backend/internal/model"
	"yingce/backend/internal/shortplay"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// W04：SQLite 上的领取/去重只是开发反馈，真实部署是 PostgreSQL。
// 这里在隔离 schema 里复跑同样的仓储合同：多 worker 竞争只产生一个赢家、
// dedupe 唯一索引拒绝第二条在途 job、结束后同键可以重新入队。
func TestShortplayJobsClaimAndDedupePostgres(t *testing.T) {
	db := shortplayJobsTestPostgres(t)
	repo := New(db)
	assertShortplayJobClaimSingleWinner(t, repo)
	assertShortplayJobDedupeReleasesAfterFinish(t, repo)
}

// TestShortplayAssetAudioClaimAndLateCallbackPostgres 在真实 PostgreSQL 上复验 W05 音色链路：
// 两个 worker 只有一个领到同一条 job；延迟回调（已收口成 ready 后的第二次写入）被 CAS 拒绝；
// 失败记录不推进 current_audio_id。
func TestShortplayAssetAudioClaimAndLateCallbackPostgres(t *testing.T) {
	db := shortplayJobsTestPostgres(t)
	if err := db.AutoMigrate(&model.ShortplayAsset{}, &model.ShortplayAssetAudio{}); err != nil {
		t.Fatal(err)
	}
	repo := New(db)
	asset := &model.ShortplayAsset{ShortplayID: "pg-audio-sp", ID: 1, Kind: shortplay.AssetCharacter, Name: "沈青禾",
		Voice: "青年女声", AudioStatus: shortplay.ImageRunning, Revision: 1}
	if err := repo.CreateShortplayAsset(asset); err != nil {
		t.Fatal(err)
	}
	job := &model.ShortplayJob{ID: "pg-audio-job", ShortplayID: "pg-audio-sp", Kind: shortplay.JobAssetAudio,
		Status: shortplay.JobQueued, DedupeKey: shortplay.JobDedupeKey(shortplay.JobAssetAudio, "pg-audio-sp|1")}
	if err := repo.CreateShortplayJob(job); err != nil {
		t.Fatal(err)
	}
	claimed := make(chan string, 4)
	var wg sync.WaitGroup
	for worker := 0; worker < 2; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			row, err := repo.ClaimShortplayJob(shortplay.JobAssetAudio)
			if err == nil && row != nil {
				claimed <- row.ID
			}
		}()
	}
	wg.Wait()
	close(claimed)
	winners := 0
	for range claimed {
		winners++
	}
	if winners != 1 {
		t.Fatalf("audio job claimed %d times, want 1", winners)
	}
	applied, err := repo.SetShortplayAssetAudioFrom("pg-audio-sp", 1, ShortplayAssetAudioWrite{
		ResourceID: "res-audio-1", Prompt: "青年女声",
	}, shortplay.AudioInFlight())
	if err != nil || !applied {
		t.Fatalf("first audio write = %v, %v", applied, err)
	}
	// 延迟回调：状态已从 running 收口成 ready，第二次写入必须被拒。
	late, err := repo.SetShortplayAssetAudioFrom("pg-audio-sp", 1, ShortplayAssetAudioWrite{
		ResourceID: "res-audio-late", Prompt: "青年女声",
	}, shortplay.AudioInFlight())
	if err != nil || late {
		t.Fatalf("late audio callback = %v, %v", late, err)
	}
	if err := repo.RecordShortplayAssetAudioFailure("pg-audio-sp", 1, ShortplayAssetAudioWrite{Prompt: "青年女声"}); err != nil {
		t.Fatal(err)
	}
	audios, err := repo.ShortplayAssetAudios("pg-audio-sp", 1)
	if err != nil || len(audios) != 2 {
		t.Fatalf("audio history = %#v, %v", audios, err)
	}
	stored, err := repo.ShortplayAssetByID("pg-audio-sp", 1)
	if err != nil || stored.CurrentAudioID == 0 || stored.AudioVersion != 1 || stored.AudioStatus != shortplay.ImageReady {
		t.Fatalf("asset after late callback = %#v, %v", stored, err)
	}
}

func TestW09RecoveryPostgres(t *testing.T) {
	for name, check := range map[string]func(*testing.T, *Repository){
		"FinishUnboundShortplayJobPreservesAdvancedRows":        assertFinishUnboundShortplayJobPreservesAdvancedRows,
		"RestoreShortplayJobMediaRunningPreservesTerminalState": assertRestoreShortplayJobMediaRunningPreservesTerminalState,
		"ShortplayJobTasksMatchesStructuredMetadataOnly":        assertShortplayJobTasksMatchesStructuredMetadataOnly,
		"ShortplayJobTasksReturnsEveryCandidate":                assertShortplayJobTasksReturnsEveryCandidate,
		"LinkShortplayJobTaskSkipsAdvancedRows":                 assertLinkShortplayJobTaskSkipsAdvancedRows,
		"FailOrphanedShortplayJobOnlyTouchesStaleEmptyRunning":  assertFailOrphanedShortplayJobOnlyTouchesStaleEmptyRunning,
		"TaskCreateHookLinksShortplayJobInSameTransaction":      assertTaskCreateHookLinksShortplayJobInSameTransaction,
		"TaskCreateHookRollsBackWhenJobAlreadySettled":          assertTaskCreateHookRollsBackWhenJobAlreadySettled,
		"SlowTaskCreateFencesRecoveryWriter":                    assertSlowTaskCreateFencesRecoveryWriter,
	} {
		t.Run(name, func(t *testing.T) {
			db := shortplayJobsTestPostgres(t)
			if err := db.AutoMigrate(&model.Task{}); err != nil {
				t.Fatal(err)
			}
			check(t, New(db))
		})
	}
}

func TestW09PostgresBoundTransactionFencesRecovery(t *testing.T) {
	db := shortplayJobsTestPostgres(t)
	if err := db.AutoMigrate(&model.Task{}); err != nil {
		t.Fatal(err)
	}
	repo := New(db)
	job := model.ShortplayJob{ID: "bound-first", ShortplayID: "sp", Kind: shortplay.JobExtract, Status: shortplay.JobRunning, DedupeKey: "bound-first"}
	if err := db.Create(&job).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&job).Update("updated_at", time.Now().Add(-10*time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	created := make(chan error, 1)
	task := model.Task{ID: "bound-task", UserID: "owner", Type: "canvas_text", Status: model.TaskStatusQueued}
	go func() {
		created <- repo.CreateTaskWithActiveLimitAndHook(&task, 10, func(tx *Repository, task *model.Task) error {
			applied, err := tx.LinkShortplayJobTaskIfEmpty(job.ID, task.ID, "")
			if err != nil {
				return err
			}
			if !applied {
				return ErrShortplayJobNotSubmitting
			}
			close(entered)
			<-release
			return nil
		})
	}()
	select {
	case <-entered:
	case err := <-created:
		t.Fatalf("creation ended: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("binding not reached")
	}
	type result struct {
		applied bool
		err     error
	}
	recovered := make(chan result, 1)
	go func() {
		applied, err := repo.FailOrphanedShortplayJob(job.ID, "review required", "", time.Now().Add(-time.Minute))
		recovered <- result{applied, err}
	}()
	select {
	case got := <-recovered:
		t.Fatalf("recovery did not wait for binding lock: %+v", got)
	case <-time.After(150 * time.Millisecond):
	}
	unblock()
	select {
	case err := <-created:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("create did not finish")
	}
	select {
	case got := <-recovered:
		if got.err != nil || got.applied {
			t.Fatalf("recovery=%+v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("recovery did not finish")
	}
	if err := db.First(&job, "id = ?", job.ID).Error; err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&model.Task{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if job.Status != shortplay.JobRunning || job.TaskID != task.ID || count != 1 {
		t.Fatalf("job=%+v tasks=%d", job, count)
	}
	t.Log("binding UPDATE owns job row lock; recovery waits then predicate fails after commit; one bound task")
}

func shortplayJobsTestPostgres(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("CANVAS_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set CANVAS_TEST_POSTGRES_DSN to an isolated test database")
	}
	config := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
	admin, err := gorm.Open(postgres.Open(dsn), config)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("shortplay_jobs_test_%d", time.Now().UnixNano())
	if err := admin.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := admin.Exec("DROP SCHEMA " + schema + " CASCADE").Error; err != nil {
			t.Error(err)
		}
		conn, _ := admin.DB()
		_ = conn.Close()
	})
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(parsed.String()), config)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := db.AutoMigrate(&model.ShortplayJob{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func assertShortplayJobClaimSingleWinner(t *testing.T, repo *Repository) {
	t.Helper()
	for _, id := range []string{"pg-job-1", "pg-job-2", "pg-job-3"} {
		row := &model.ShortplayJob{ID: id, ShortplayID: "sp-" + id, Kind: shortplay.JobExtract, Status: shortplay.JobQueued,
			DedupeKey: shortplay.JobDedupeKey(shortplay.JobExtract, id)}
		if err := repo.CreateShortplayJob(row); err != nil {
			t.Fatal(err)
		}
	}
	claimed := make(chan string, 8)
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			job, err := repo.ClaimShortplayJob(shortplay.JobExtract)
			if err != nil || job == nil {
				return
			}
			claimed <- job.ID
		}()
	}
	wg.Wait()
	close(claimed)
	seen := map[string]bool{}
	for id := range claimed {
		if seen[id] {
			t.Fatalf("job %s claimed twice", id)
		}
		seen[id] = true
	}
	if len(seen) != 3 {
		t.Fatalf("claimed %v, want three distinct jobs", seen)
	}
	if job, err := repo.ClaimShortplayJob(shortplay.JobExtract); err != nil || job != nil {
		t.Fatalf("empty queue claim = %#v, %v", job, err)
	}
}

func assertShortplayJobDedupeReleasesAfterFinish(t *testing.T, repo *Repository) {
	t.Helper()
	first := &model.ShortplayJob{ID: "pg-dedupe-a", ShortplayID: "pg-dedupe-sp", Kind: shortplay.JobExtract,
		Status: shortplay.JobQueued, DedupeKey: shortplay.JobDedupeKey(shortplay.JobExtract, "pg-dedupe-sp")}
	if err := repo.CreateShortplayJob(first); err != nil {
		t.Fatal(err)
	}
	duplicate := &model.ShortplayJob{ID: "pg-dedupe-b", ShortplayID: "pg-dedupe-sp", Kind: shortplay.JobExtract,
		Status: shortplay.JobQueued, DedupeKey: shortplay.JobDedupeKey(shortplay.JobExtract, "pg-dedupe-sp")}
	if err := repo.CreateShortplayJob(duplicate); err == nil {
		t.Fatal("second active extract was accepted on PostgreSQL")
	}
	active, err := repo.ActiveShortplayJob("pg-dedupe-sp", shortplay.JobExtract)
	if err != nil || active == nil || active.ID != first.ID {
		t.Fatalf("active job = %#v, %v", active, err)
	}
	if err := repo.FinishShortplayJob(first.ID, shortplay.JobSucceeded, "", shortplay.ExtractCursorPayload(0)); err != nil {
		t.Fatal(err)
	}
	third := &model.ShortplayJob{ID: "pg-dedupe-c", ShortplayID: "pg-dedupe-sp", Kind: shortplay.JobExtract,
		Status: shortplay.JobQueued, DedupeKey: shortplay.JobDedupeKey(shortplay.JobExtract, "pg-dedupe-sp")}
	if err := repo.CreateShortplayJob(third); err != nil {
		t.Fatalf("re-enqueue after terminal state: %v", err)
	}
}
