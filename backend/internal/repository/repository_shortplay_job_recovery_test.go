package repository

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"yingce/backend/internal/model"
	"yingce/backend/internal/shortplay"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// W09 孤儿回收的仓储合同：结构化关联、歧义不取最早、恢复写回的条件更新。

func newShortplayRecoveryRepo(t *testing.T) *Repository {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "w09-recovery.db")+"?_busy_timeout=5000&_journal_mode=WAL&_foreign_keys=on"),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(4)
	if err := db.AutoMigrate(&model.ShortplayJob{}, &model.Task{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = sqlDB.Close()
	})
	return New(db)
}

func w09Task(id, inputJSON, userID, projectID, taskType string) *model.Task {
	return &model.Task{
		ID: id, UserID: userID, ProjectID: projectID, Type: taskType,
		Operation: "text", Status: model.TaskStatusSucceeded, InputJSON: inputJSON,
	}
}

func TestShortplayJobTasksMatchesStructuredMetadataOnly(t *testing.T) {
	assertShortplayJobTasksMatchesStructuredMetadataOnly(t, newShortplayRecoveryRepo(t))
}

func assertShortplayJobTasksMatchesStructuredMetadataOnly(t *testing.T, repo *Repository) {
	t.Helper()
	rows := []*model.Task{
		// 紧凑格式：命中。
		w09Task("task-compact", `{"metadata":{"shortplayJobId":"job-1"}}`, "user-1", "proj-1", "canvas_text"),
		// 缩进 + 键序不同：同一结构，仍应命中。
		w09Task("task-pretty", "{\n\t\"config\": {\"mode\": \"text\"},\n\t\"metadata\": {\n\t\t\"source\": \"shortplay_extract\",\n\t\t\"shortplayJobId\": \"job-1\"\n\t}\n}", "user-1", "proj-1", "canvas_text"),
		// 字段挂在顶层而不是 metadata 下：不算。
		w09Task("task-wrong-nesting", `{"shortplayJobId":"job-1"}`, "user-1", "proj-1", "canvas_text"),
		// 值不同：不算。
		w09Task("task-other-value", `{"metadata":{"shortplayJobId":"job-2"}}`, "user-1", "proj-1", "canvas_text"),
		// 非 JSON：不算。
		w09Task("task-not-json", `shortplayJobId=job-1`, "user-1", "proj-1", "canvas_text"),
		// 用户不同：不算。
		w09Task("task-other-user", `{"metadata":{"shortplayJobId":"job-1"}}`, "user-2", "proj-1", "canvas_text"),
		// 项目不同：不算。
		w09Task("task-other-project", `{"metadata":{"shortplayJobId":"job-1"}}`, "user-1", "proj-2", "canvas_text"),
		// 任务类型不同：不算（出图/音频任务不能用来收口提取 job）。
		w09Task("task-other-type", `{"metadata":{"shortplayJobId":"job-1"}}`, "user-1", "proj-1", "canvas_image"),
	}
	for _, row := range rows {
		if err := repo.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}

	got, err := repo.ShortplayJobTasks("job-1", "user-1", "proj-1", "canvas_text")
	if err != nil {
		t.Fatal(err)
	}
	ids := make(map[string]bool, len(got))
	for _, row := range got {
		ids[row.ID] = true
	}
	if len(got) != 2 || !ids["task-compact"] || !ids["task-pretty"] {
		t.Fatalf("结构化匹配结果 = %v，期望 task-compact 与 task-pretty", ids)
	}
}

func TestShortplayJobTasksReturnsEveryCandidate(t *testing.T) {
	assertShortplayJobTasksReturnsEveryCandidate(t, newShortplayRecoveryRepo(t))
}

func assertShortplayJobTasksReturnsEveryCandidate(t *testing.T, repo *Repository) {
	t.Helper()
	for _, id := range []string{"task-a", "task-b"} {
		if err := repo.db.Create(w09Task(id, `{"metadata":{"shortplayJobId":"job-dup"}}`, "user-1", "proj-1", "canvas_text")).Error; err != nil {
			t.Fatal(err)
		}
	}
	got, err := repo.ShortplayJobTasks("job-dup", "user-1", "proj-1", "canvas_text")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("候选数=%d，期望把每条都交回调用方处理歧义", len(got))
	}
}

func TestLinkShortplayJobTaskSkipsAdvancedRows(t *testing.T) {
	assertLinkShortplayJobTaskSkipsAdvancedRows(t, newShortplayRecoveryRepo(t))
}

func assertLinkShortplayJobTaskSkipsAdvancedRows(t *testing.T, repo *Repository) {
	t.Helper()
	job := &model.ShortplayJob{
		ID: "job-link", ShortplayID: "sp", Kind: shortplay.JobExtract,
		Status: shortplay.JobRunning, DedupeKey: shortplay.JobDedupeKey(shortplay.JobExtract, "sp"),
	}
	if err := repo.db.Create(job).Error; err != nil {
		t.Fatal(err)
	}

	applied, err := repo.LinkShortplayJobTask(job.ID, "task-a", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if applied {
		t.Fatal("未超过静默期的行被写回，活跃提交会被覆盖")
	}

	if err := repo.db.Exec("UPDATE shortplay_jobs SET updated_at = ? WHERE id = ?",
		time.Now().Add(-10*time.Minute), job.ID).Error; err != nil {
		t.Fatal(err)
	}
	applied, err = repo.LinkShortplayJobTask(job.ID, "task-a", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !applied {
		t.Fatal("超期孤儿行没有被写回 taskId")
	}
	var got model.ShortplayJob
	if err := repo.db.First(&got, "id = ?", job.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.TaskID != "task-a" {
		t.Fatalf("task_id=%q，期望 task-a", got.TaskID)
	}

	applied, err = repo.LinkShortplayJobTask(job.ID, "task-b", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if applied {
		t.Fatal("已有 taskId 的行被第二次写回")
	}
}

func TestFailOrphanedShortplayJobOnlyTouchesStaleEmptyRunning(t *testing.T) {
	assertFailOrphanedShortplayJobOnlyTouchesStaleEmptyRunning(t, newShortplayRecoveryRepo(t))
}

func assertFailOrphanedShortplayJobOnlyTouchesStaleEmptyRunning(t *testing.T, repo *Repository) {
	t.Helper()
	create := func(id, status, taskID string, updated time.Time) {
		job := &model.ShortplayJob{
			ID: id, ShortplayID: "sp", Kind: shortplay.JobExtract,
			Status: status, TaskID: taskID,
			DedupeKey: shortplay.JobDedupeKey(shortplay.JobExtract, "sp-"+id),
		}
		if err := repo.db.Create(job).Error; err != nil {
			t.Fatal(err)
		}
		if err := repo.db.Exec("UPDATE shortplay_jobs SET updated_at = ? WHERE id = ?", updated, id).Error; err != nil {
			t.Fatal(err)
		}
	}
	stale := time.Now().Add(-10 * time.Minute)
	create("job-stale", shortplay.JobRunning, "", stale)
	create("job-fresh", shortplay.JobRunning, "", time.Now())
	create("job-linked", shortplay.JobRunning, "task-a", stale)
	create("job-done", shortplay.JobFailed, "", stale)

	cases := map[string]bool{"job-stale": true, "job-fresh": false, "job-linked": false, "job-done": false}
	staleBefore := time.Now().Add(-time.Minute)
	for id, want := range cases {
		applied, err := repo.FailOrphanedShortplayJob(id, "结果待核对", "", staleBefore)
		if err != nil {
			t.Fatal(err)
		}
		if applied != want {
			t.Fatalf("%s applied=%v，期望 %v", id, applied, want)
		}
	}
	var stale1 model.ShortplayJob
	if err := repo.db.First(&stale1, "id = ?", "job-stale").Error; err != nil {
		t.Fatal(err)
	}
	if stale1.Status != shortplay.JobFailed || stale1.LastError != "结果待核对" {
		t.Fatalf("孤儿行收口结果 = %s/%q", stale1.Status, stale1.LastError)
	}
}

func TestTaskCreateHookLinksShortplayJobInSameTransaction(t *testing.T) {
	assertTaskCreateHookLinksShortplayJobInSameTransaction(t, newShortplayRecoveryRepo(t))
}

func TestFinishUnboundShortplayJobPreservesAdvancedRows(t *testing.T) {
	assertFinishUnboundShortplayJobPreservesAdvancedRows(t, newShortplayRecoveryRepo(t))
}

func assertFinishUnboundShortplayJobPreservesAdvancedRows(t *testing.T, repo *Repository) {
	t.Helper()
	for _, state := range []struct {
		id, status, taskID string
		want               bool
	}{
		{"submitting", shortplay.JobRunning, "", true},
		{"bound", shortplay.JobRunning, "task-1", false},
		{"recovered", shortplay.JobFailed, "", false},
		{"completed", shortplay.JobSucceeded, "task-2", false},
	} {
		job := model.ShortplayJob{ID: state.id, ShortplayID: "sp", Kind: shortplay.JobExtract,
			Status: state.status, TaskID: state.taskID, DedupeKey: state.id, LastError: "original evidence"}
		if err := repo.db.Create(&job).Error; err != nil {
			t.Fatal(err)
		}
		applied, err := repo.FinishUnboundShortplayJob(job.ID, shortplay.JobFailed, "late failure", "")
		if err != nil || applied != state.want {
			t.Fatalf("%s applied=%v err=%v", state.id, applied, err)
		}
		if err := repo.db.First(&job, "id = ?", state.id).Error; err != nil {
			t.Fatal(err)
		}
		if !state.want && (job.Status != state.status || job.TaskID != state.taskID || job.LastError != "original evidence") {
			t.Fatalf("advanced job overwritten: %+v", job)
		}
	}
}

func TestRestoreShortplayJobMediaRunningPreservesTerminalState(t *testing.T) {
	assertRestoreShortplayJobMediaRunningPreservesTerminalState(t, newShortplayRecoveryRepo(t))
}

func assertRestoreShortplayJobMediaRunningPreservesTerminalState(t *testing.T, repo *Repository) {
	t.Helper()
	if err := repo.db.AutoMigrate(&model.ShortplayAsset{}); err != nil {
		t.Fatal(err)
	}
	for i, status := range []string{"", shortplay.ImageQueued, shortplay.ImageRunning, shortplay.ImageReady, shortplay.ImageFailed} {
		asset := model.ShortplayAsset{ShortplayID: "restore", ID: i + 1, Kind: shortplay.AssetCharacter,
			Name: "asset", ImageStatus: status, AudioStatus: status, CurrentImageID: 7, CurrentAudioID: 8}
		if err := repo.db.Create(&asset).Error; err != nil {
			t.Fatal(err)
		}
		for _, kind := range []string{shortplay.JobAssetImage, shortplay.JobAssetAudio} {
			job := model.ShortplayJob{ShortplayID: asset.ShortplayID, Kind: kind,
				PayloadJSON: shortplay.AssetImageJobPayload{AssetID: asset.ID}.Encode()}
			if err := repo.RestoreShortplayJobMediaRunning(&job); err != nil {
				t.Fatal(err)
			}
		}
		if err := repo.db.First(&asset, "shortplay_id = ? AND id = ?", asset.ShortplayID, asset.ID).Error; err != nil {
			t.Fatal(err)
		}
		want := shortplay.ImageRunning
		if status == shortplay.ImageReady || status == shortplay.ImageFailed {
			want = status
		}
		if asset.ImageStatus != want || asset.AudioStatus != want || asset.CurrentImageID != 7 || asset.CurrentAudioID != 8 {
			t.Fatalf("restore %q changed terminal state or media pointer: %+v", status, asset)
		}
	}
}

func assertTaskCreateHookLinksShortplayJobInSameTransaction(t *testing.T, repo *Repository) {
	t.Helper()
	job := &model.ShortplayJob{
		ID: "job-atomic", ShortplayID: "sp", Kind: shortplay.JobExtract,
		Status: shortplay.JobRunning, DedupeKey: shortplay.JobDedupeKey(shortplay.JobExtract, "sp"),
	}
	if err := repo.db.Create(job).Error; err != nil {
		t.Fatal(err)
	}
	task := &model.Task{
		ID: "task-atomic", UserID: "user-1", ProjectID: "proj-1",
		Type: "canvas_text", Status: model.TaskStatusQueued,
		InputJSON: `{"metadata":{"shortplayJobId":"job-atomic"}}`,
	}
	err := repo.CreateTaskWithActiveLimitAndHook(task, 10, func(tx *Repository, created *model.Task) error {
		applied, err := tx.LinkShortplayJobTaskIfEmpty(job.ID, created.ID, `{"cursor":3}`)
		if err != nil {
			return err
		}
		if !applied {
			return ErrShortplayJobNotSubmitting
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var got model.ShortplayJob
	if err := repo.db.First(&got, "id = ?", job.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.TaskID != task.ID || got.PayloadJSON != `{"cursor":3}` {
		t.Fatalf("事务内绑定结果 = taskId %q payload %q", got.TaskID, got.PayloadJSON)
	}
	var tasks int64
	if err := repo.db.Model(&model.Task{}).Count(&tasks).Error; err != nil {
		t.Fatal(err)
	}
	if tasks != 1 {
		t.Fatalf("任务数=%d，应为 1", tasks)
	}
}

func TestTaskCreateHookRollsBackWhenJobAlreadySettled(t *testing.T) {
	assertTaskCreateHookRollsBackWhenJobAlreadySettled(t, newShortplayRecoveryRepo(t))
}

func assertTaskCreateHookRollsBackWhenJobAlreadySettled(t *testing.T, repo *Repository) {
	t.Helper()
	job := &model.ShortplayJob{
		ID: "job-settled", ShortplayID: "sp", Kind: shortplay.JobExtract,
		Status: shortplay.JobFailed, LastError: "结果待核对",
		DedupeKey: shortplay.JobDedupeKey(shortplay.JobExtract, "sp-settled"),
	}
	if err := repo.db.Create(job).Error; err != nil {
		t.Fatal(err)
	}
	task := &model.Task{
		ID: "task-settled", UserID: "user-1", ProjectID: "proj-1",
		Type: "canvas_text", Status: model.TaskStatusQueued,
		InputJSON: `{"metadata":{"shortplayJobId":"job-settled"}}`,
	}
	err := repo.CreateTaskWithActiveLimitAndHook(task, 10, func(tx *Repository, created *model.Task) error {
		applied, err := tx.LinkShortplayJobTaskIfEmpty(job.ID, created.ID, "")
		if err != nil {
			return err
		}
		if !applied {
			return ErrShortplayJobNotSubmitting
		}
		return nil
	})
	if !errors.Is(err, ErrShortplayJobNotSubmitting) {
		t.Fatalf("事务错误 = %v，期望 ErrShortplayJobNotSubmitting", err)
	}
	var tasks int64
	if err := repo.db.Model(&model.Task{}).Count(&tasks).Error; err != nil {
		t.Fatal(err)
	}
	if tasks != 0 {
		t.Fatalf("job 已收口后仍提交了 %d 条任务", tasks)
	}
}

func TestSlowTaskCreateFencesRecoveryWriter(t *testing.T) {
	assertSlowTaskCreateFencesRecoveryWriter(t, newShortplayRecoveryRepo(t))
}

func assertSlowTaskCreateFencesRecoveryWriter(t *testing.T, repo *Repository) {
	t.Helper()
	job := &model.ShortplayJob{
		ID: "job-slow-atomic", ShortplayID: "sp", Kind: shortplay.JobExtract,
		Status: shortplay.JobRunning, DedupeKey: shortplay.JobDedupeKey(shortplay.JobExtract, "sp-slow"),
	}
	if err := repo.db.Create(job).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.db.Exec("UPDATE shortplay_jobs SET updated_at = ? WHERE id = ?",
		time.Now().Add(-10*time.Minute), job.ID).Error; err != nil {
		t.Fatal(err)
	}

	entered := make(chan struct{})
	release := make(chan struct{})
	createDone := make(chan error, 1)
	task := &model.Task{
		ID: "task-slow-atomic", UserID: "user-1", ProjectID: "proj-1",
		Type: "canvas_text", Status: model.TaskStatusQueued,
		InputJSON: `{"metadata":{"shortplayJobId":"job-slow-atomic"}}`,
	}
	create := func(hook TaskCreateHook) error { return repo.CreateTaskWithActiveLimitAndHook(task, 10, hook) }
	if repo.db.Dialector.Name() == "postgres" {
		if err := repo.db.AutoMigrate(&model.CreditAccount{}, &model.BillingOrder{}, &model.CreditLedgerEntry{}, &model.PlatformSubmission{}); err != nil {
			t.Fatal(err)
		}
		if err := repo.db.Create(&model.CreditAccount{UserID: task.UserID, AvailableMicrocredits: 1000}).Error; err != nil {
			t.Fatal(err)
		}
		order := &model.BillingOrder{ID: "slow-order", UserID: task.UserID, TaskID: task.ID, IdempotencyKey: "slow-order", Status: model.BillingStatusReserved, AmountMicrocredits: 100}
		create = func(hook TaskCreateHook) error {
			return repo.CreateTaskWithCreditReservationAndHook(task, order, 10, hook)
		}
	}
	go func() {
		createDone <- create(func(tx *Repository, created *model.Task) error {
			close(entered)
			<-release
			applied, err := tx.LinkShortplayJobTaskIfEmpty(job.ID, created.ID, "")
			if err != nil {
				return err
			}
			if !applied {
				return ErrShortplayJobNotSubmitting
			}
			return nil
		})
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("任务事务没有进入绑定阶段")
	}

	failDone := make(chan bool, 1)
	failErr := make(chan error, 1)
	go func() {
		applied, err := repo.FailOrphanedShortplayJob(job.ID, "结果待核对，未自动重发", "", time.Now().Add(-time.Minute))
		if err != nil {
			failErr <- err
			return
		}
		failDone <- applied
	}()
	if repo.db.Dialector.Name() == "postgres" {
		select {
		case err := <-failErr:
			close(release)
			t.Fatal(err)
		case applied := <-failDone:
			close(release)
			if !applied {
				t.Fatal("recovery should win before the job row is locked")
			}
		case <-time.After(5 * time.Second):
			close(release)
			t.Fatal("recovery unexpectedly blocked before binding")
		}
		if err := <-createDone; !errors.Is(err, ErrShortplayJobNotSubmitting) {
			t.Fatalf("expected fenced rollback: %v", err)
		}
		var count int64
		if err := repo.db.Model(&model.Task{}).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		var got model.ShortplayJob
		if err := repo.db.First(&got, "id = ?", job.ID).Error; err != nil {
			t.Fatal(err)
		}
		if count != 0 || got.Status != shortplay.JobFailed || got.TaskID != "" {
			t.Fatalf("rollback count=%d job=%+v", count, got)
		}
		for _, table := range []any{&model.BillingOrder{}, &model.CreditLedgerEntry{}, &model.PlatformSubmission{}} {
			if err := repo.db.Model(table).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf("%T retained %d rows after rollback", table, count)
			}
		}
		var account model.CreditAccount
		if err := repo.db.First(&account, "user_id = ?", task.UserID).Error; err != nil {
			t.Fatal(err)
		}
		if account.AvailableMicrocredits != 1000 || account.ReservedMicrocredits != 0 {
			t.Fatalf("billing rollback failed: %+v", account)
		}
		t.Log("PostgreSQL: recovery wins before binding UPDATE; job failed; task/order/ledger/platform=0; available=1000 reserved=0. Repository-only: no dispatcher or outbound exercised.")
		return
	}
	time.Sleep(100 * time.Millisecond)
	close(release)
	if err := <-createDone; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-failErr:
		t.Fatal(err)
	case applied := <-failDone:
		if applied {
			t.Fatal("慢任务事务提交前后，恢复 writer 把 job 收口了")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("恢复 writer 没有返回")
	}

	var got model.ShortplayJob
	if err := repo.db.First(&got, "id = ?", job.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != shortplay.JobRunning || got.TaskID != task.ID {
		t.Fatalf("慢任务绑定被覆盖：status=%s taskId=%q lastError=%q", got.Status, got.TaskID, got.LastError)
	}
}
