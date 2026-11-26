package repository

import (
	"sync"
	"testing"

	"yingce/backend/internal/model"
	"yingce/backend/internal/shortplay"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newShortplayJobsRepo(t *testing.T) *Repository {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(4)
	}
	if err := db.AutoMigrate(&model.ShortplayJob{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Exec("DROP TABLE shortplay_jobs").Error })
	return New(db)
}

func TestClaimShortplayJobHandsEachJobToOneWorker(t *testing.T) {
	repo := newShortplayJobsRepo(t)
	for _, id := range []string{"job-1", "job-2"} {
		row := &model.ShortplayJob{ID: id, ShortplayID: "sp", Kind: shortplay.JobExtract, Status: shortplay.JobQueued,
			DedupeKey: shortplay.JobDedupeKey(shortplay.JobExtract, id)}
		if err := repo.CreateShortplayJob(row); err != nil {
			t.Fatal(err)
		}
	}
	claimed := make(chan string, 4)
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
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
	if len(seen) != 2 {
		t.Fatalf("claimed %v, want two distinct jobs", seen)
	}
	if job, err := repo.ClaimShortplayJob(shortplay.JobExtract); err != nil || job != nil {
		t.Fatalf("empty queue claim = %#v, %v", job, err)
	}
}

func TestDedupeKeyBlocksSecondActiveExtract(t *testing.T) {
	repo := newShortplayJobsRepo(t)
	first := &model.ShortplayJob{ID: "job-a", ShortplayID: "sp", Kind: shortplay.JobExtract, Status: shortplay.JobQueued,
		DedupeKey: shortplay.JobDedupeKey(shortplay.JobExtract, "sp")}
	if err := repo.CreateShortplayJob(first); err != nil {
		t.Fatal(err)
	}
	duplicate := &model.ShortplayJob{ID: "job-b", ShortplayID: "sp", Kind: shortplay.JobExtract, Status: shortplay.JobQueued,
		DedupeKey: shortplay.JobDedupeKey(shortplay.JobExtract, "sp")}
	if err := repo.CreateShortplayJob(duplicate); err == nil {
		t.Fatal("second active extract was accepted")
	}
	active, err := repo.ActiveShortplayJob("sp", shortplay.JobExtract)
	if err != nil || active == nil || active.ID != "job-a" {
		t.Fatalf("active job = %#v, %v", active, err)
	}
	if err := repo.FinishShortplayJob("job-a", shortplay.JobSucceeded, "", ""); err != nil {
		t.Fatal(err)
	}
	// 结束后去重键释放，可以再次入队。
	third := &model.ShortplayJob{ID: "job-c", ShortplayID: "sp", Kind: shortplay.JobExtract, Status: shortplay.JobQueued,
		DedupeKey: shortplay.JobDedupeKey(shortplay.JobExtract, "sp")}
	if err := repo.CreateShortplayJob(third); err != nil {
		t.Fatal(err)
	}
}
