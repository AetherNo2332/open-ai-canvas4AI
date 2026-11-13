package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// fastVideoPollPolicy 供视频/协议专项测试快速推进轮询循环。
func fastVideoPollPolicy() videoPollPolicy {
	policy := defaultVideoPollPolicy()
	policy.InitialDelay = time.Millisecond
	policy.Interval = time.Millisecond
	policy.Sleep = func(context.Context, time.Duration) error { return nil }
	return policy
}

func runVideoTaskForTest(ctx context.Context, input canvasGenerationInput) (map[string]interface{}, error) {
	return runVideoTaskWithPolicy(ctx, input, fastVideoPollPolicy())
}

// notifyTestService 只建任务与任务日志表：验证轮询通知经 providerAnalyticsContext
// 写入现有日志入口，而不初始化完整创作运行。
func notifyTestService(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "notify.db")+"?_journal_mode=WAL&_busy_timeout=5000"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Task{}, &model.TaskLog{}); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return &Service{repo: repository.New(db)}, db
}

// 轮询循环已迁 internal/generation/polling；Notify 回调由 app 注入并写任务日志。
// 该接线测试确认通知抵达现有日志入口：重试为 warn、恢复为 info。
func TestVideoPollNotifyWritesTaskLog(t *testing.T) {
	s, db := notifyTestService(t)
	ctx := context.WithValue(context.Background(), providerAnalyticsKey{}, providerAnalyticsContext{Service: s, UserID: "user", TaskID: "task-1"})
	notifyTaskVideoPollEvent(ctx, "task-1", videoPollEventRetrying, errors.New("upstream 502"))
	notifyTaskVideoPollEvent(ctx, "task-1", videoPollEventRecovered, nil)
	var logs []model.TaskLog
	if err := db.Where("task_id = ?", "task-1").Order("created_at ASC").Find(&logs).Error; err != nil {
		t.Fatal(err)
	}
	if len(logs) != 2 {
		t.Fatalf("task logs = %d, want retry + recover", len(logs))
	}
	if logs[0].Level != "warn" || logs[0].Message != "上游视频查询暂时异常，将继续轮询原任务" || logs[0].Payload != "upstream 502" {
		t.Fatalf("retry log mismatch: %+v", logs[0])
	}
	if logs[1].Level != "info" || logs[1].Message != "上游视频查询已恢复" {
		t.Fatalf("recover log mismatch: %+v", logs[1])
	}
	// 没有任务上下文（如管理端探测调用）时不写日志。
	notifyTaskVideoPollEvent(context.Background(), "task-1", videoPollEventRetrying, errors.New("ignored"))
	var count int64
	if err := db.Model(&model.TaskLog{}).Where("task_id = ?", "task-1").Count(&count).Error; err != nil || count != 2 {
		t.Fatalf("logs after context-less notify = %d err=%v, want 2", count, err)
	}
}
