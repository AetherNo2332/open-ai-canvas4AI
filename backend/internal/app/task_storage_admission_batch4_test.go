package app

import (
	"sync"
	"testing"
	"time"
)

func TestRequireStoredFileCapacityForTaskDoesNotSelfLock(t *testing.T) {
	svc, _ := newTimelineTaskTestService(t)
	policy := defaultRuntimePolicy()

	svc.storageMu.Lock()
	locked := true
	defer func() {
		if locked {
			svc.storageMu.Unlock()
		}
	}()

	done := make(chan error, 1)
	go func() {
		done <- svc.requireStoredFileCapacityForTask("usr-admission-lock", "canvas_image", policy)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("requireStoredFileCapacityForTask() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("requireStoredFileCapacityForTask() blocked while caller held storageMu")
	}

	svc.storageMu.Unlock()
	locked = false
}

func TestReserveUserStoredFileQuotaDoesNotOversubscribePendingStorage(t *testing.T) {
	svc, _ := newTimelineTaskTestService(t)
	const (
		userID      = "usr-pending-concurrent"
		requested   = int64(6)
		storedLimit = int64(10)
	)

	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := svc.reserveUserStoredFileQuota(userID, requested, 100, 1000, storedLimit, "too large")
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	successes := 0
	failures := 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			failures++
		}
	}
	if successes != 1 || failures != 1 {
		t.Fatalf("reservations: success=%d failure=%d, want 1/1", successes, failures)
	}
	if got := svc.pendingStorage[userID]; got != requested {
		t.Fatalf("pending storage = %d, want %d", got, requested)
	}
}

func TestReserveUserStoredFileQuotaReleasesPendingAfterDailyFailure(t *testing.T) {
	svc, _ := newTimelineTaskTestService(t)
	const (
		userID = "usr-pending-daily-failure"
		size   = int64(6)
	)

	if _, err := svc.reserveUserStoredFileQuota(userID, size, 100, 5, 100, "too large"); err == nil {
		t.Fatal("reserveUserStoredFileQuota() error = nil, want daily quota rejection")
	}
	if got := svc.pendingStorage[userID]; got != 0 {
		t.Fatalf("pending storage after daily failure = %d, want 0", got)
	}
}
