package app

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"gorm.io/gorm"

	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/model"
)

// 审查 F1：平台任务重试必须沿用提交时的单次费用上限，涨价超过上限时拒绝而不是按新价冻结。
func TestPlatformRetryKeepsCostCap(t *testing.T) {
	f := newPlatformImageFixture(t)
	p := f.svc.CreationPlatform()
	caller := cp.Caller{UserID: "user-a"}
	req := f.systemRequest("retry-cap")
	req.Cost.MaxMicrocredits = ptr[int64](100_000)
	receipt, err := p.Submit(context.Background(), caller, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Cancel(context.Background(), caller, receipt.Snapshot.TaskID); err != nil {
		t.Fatal(err)
	}
	// 同价重试：新订单必须带同样的上限。
	retried, err := p.Retry(context.Background(), caller, receipt.Snapshot.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	var order model.BillingOrder
	if err := f.db.Order("created_at DESC").First(&order, "task_id = ? AND status = ?", retried.TaskID, model.BillingStatusReserved).Error; err != nil {
		t.Fatal(err)
	}
	if !order.ChargeLimitSet || order.ChargeLimitMicrocredits != 100_000 {
		t.Fatalf("retried order lost the cap: set=%v limit=%d", order.ChargeLimitSet, order.ChargeLimitMicrocredits)
	}
	if _, err := p.Cancel(context.Background(), caller, retried.TaskID); err != nil {
		t.Fatal(err)
	}
	// 涨价后重试：超过授权上限，拒绝且不冻结。
	if err := f.db.Model(&model.ChannelModelPriceTier{}).Where("id = ?", "platform-tier").Updates(map[string]any{"unit_price_microcredits": 200_000, "price_version": 2}).Error; err != nil {
		t.Fatal(err)
	}
	f.svc.invalidateRouteCatalog()
	before := platformCount(t, f.db, &model.BillingOrder{}, "user_id = ?", "user-a")
	if _, err := p.Retry(context.Background(), caller, retried.TaskID); err == nil {
		t.Fatal("retry above the authorized cap must be refused")
	}
	if after := platformCount(t, f.db, &model.BillingOrder{}, "user_id = ?", "user-a"); after != before {
		t.Fatalf("refused retry reserved credits: %d -> %d orders", before, after)
	}
	// 旧入口 /api/tasks/:id/retry 走同一函数，同样受限。
	if _, err := f.svc.RetryTask("user-a", retried.TaskID); err == nil {
		t.Fatal("legacy retry entry must enforce the platform cap too")
	}
}

// 审查 F2：快照的 revision 不能比快照里的状态更新。任务行读取之后、revision 读取之前若 worker 完成任务，
// 旧状态不能配上终态 revision，否则 SDK 从该 revision 续读会永久错过终态。
func TestPlatformSnapshotRevisionNeverNewerThanState(t *testing.T) {
	f := newPlatformImageFixture(t)
	p := f.svc.CreationPlatform()
	caller := cp.Caller{UserID: "user-a"}
	receipt, err := p.Submit(context.Background(), caller, f.systemRequest("torn-read"))
	if err != nil {
		t.Fatal(err)
	}
	taskID := receipt.Snapshot.TaskID
	// 在快照读取任务行之后插入 worker 完成：模拟并发。
	injected := false
	callbackName := "test:complete_after_task_read"
	if err := f.db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if injected || tx.Statement.Table != "tasks" || tx.Statement.Dest == nil {
			return
		}
		if task, ok := tx.Statement.Dest.(*model.Task); ok && task.ID == taskID && task.Status == model.TaskStatusQueued {
			injected = true
			f.runQueued(t)
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer f.db.Callback().Query().Remove(callbackName)
	snapshot, err := p.Task(context.Background(), caller, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if !injected {
		t.Fatal("test did not inject the concurrent completion")
	}
	events := platformEvents(t, p, caller, cp.EventQuery{TaskID: taskID, After: strconv.FormatInt(snapshot.Revision, 10)})
	if snapshot.Execution != cp.ExecutionSucceeded && len(events.Events) == 0 {
		t.Fatalf("stale snapshot %s at revision %d has no later events to resume from", snapshot.Execution, snapshot.Revision)
	}
}

// 不同价格线路随机选线时，未变化的价格目录与生成规格必须得到相同报价签名。
func TestPlatformQuoteStableAcrossDifferentPriceRoutes(t *testing.T) {
	f := newPlatformImageFixture(t)
	if _, err := f.svc.UpdateFeatureAvailability(&model.User{ID: "admin", Role: model.UserRoleAdmin}, FeatureAvailability{TaskCenterEnabled: true, CreditsEnabled: true, FrontendModelsEnabled: true}); err != nil {
		t.Fatal(err)
	}
	// 第二条线路能力相同、价格不同。
	var original model.ChannelModel
	if err := f.db.First(&original, "id = ?", "platform-cm").Error; err != nil {
		t.Fatal(err)
	}
	second := original
	second.ID, second.ModelKey, second.ProviderModelKey = "platform-cm-2", "gpt-image-2-b", "gpt-image-2-b"
	second.PriceTiers = nil
	second.UnitPriceMicrocredits = 200_000
	for _, row := range []any{&second, &model.ChannelModelPriceTier{ID: "platform-tier-2", ChannelModelID: "platform-cm-2", SelectorKey: "{}", SelectorJSON: "{}", BillingMode: "fixed_request", UnitPriceMicrocredits: 200_000, PriceConfigured: true, Enabled: true}, &model.LogicalModelRoute{ID: "platform-route-2", LogicalModelRevisionID: "platform-revision", ChannelModelID: "platform-cm-2", Enabled: true, Weight: 1}} {
		if err := f.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	f.svc.invalidateRouteCatalog()
	p := f.svc.CreationPlatform()
	caller := cp.Caller{UserID: "user-a"}
	for attempt := 0; attempt < 24; attempt++ {
		req := f.systemRequest(fmt.Sprintf("multi-route-%d", attempt))
		req.Model = cp.ModelSelection{LogicalModelID: f.logicalID}
		quote, err := p.Quote(context.Background(), caller, req)
		if err != nil {
			t.Fatal(err)
		}
		req.Cost.QuoteHash = quote.QuoteHash
		receipt, err := p.Submit(context.Background(), caller, req)
		if err != nil {
			t.Fatalf("attempt %d: unchanged price rejected as %v", attempt, err)
		}
		// 取消释放活动任务名额，循环只考察报价签名。
		if _, err := p.Cancel(context.Background(), caller, receipt.Snapshot.TaskID); err != nil {
			t.Fatal(err)
		}
	}
}

// 审查 F7：同键同内容的并发输家若在准备阶段被拒（例如赢家刚好占满活动任务名额），必须回放赢家的任务，
// 而不是把“响应丢失后的重投”报告成准入失败。
func TestPlatformSameKeyLoserReplaysWinnerWhenPrepareRejects(t *testing.T) {
	f := newPlatformImageFixture(t)
	actor := &model.User{ID: "admin", Role: model.UserRoleAdmin}
	policy, err := f.svc.RuntimePolicy()
	if err != nil {
		t.Fatal(err)
	}
	policy.Task.ActiveTaskLimit = 1
	if _, err := f.svc.UpdateRuntimePolicySetting(actor, policy); err != nil {
		t.Fatal(err)
	}
	p := f.svc.CreationPlatform()
	caller := cp.Caller{UserID: "user-a"}
	req := f.systemRequest("prepare-loser")
	// 屏障：输家完成“查幂等记录（不存在）”之后、进入准备阶段之前，让赢家完整提交。
	var winner cp.SubmitReceipt
	var winnerErr error
	armed := true
	callbackName := "test:winner_between_lookup_and_prepare"
	if err := f.db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if !armed || tx.Statement.Table != "platform_submissions" {
			return
		}
		armed = false
		winner, winnerErr = p.Submit(context.Background(), caller, req)
	}); err != nil {
		t.Fatal(err)
	}
	loser, err := p.Submit(context.Background(), caller, req)
	f.db.Callback().Query().Remove(callbackName)
	if winnerErr != nil {
		t.Fatalf("winner: %v", winnerErr)
	}
	if err != nil {
		t.Fatalf("same-key loser must replay the winner, got %v", err)
	}
	if !loser.Replayed || loser.Snapshot.TaskID != winner.Snapshot.TaskID {
		t.Fatalf("loser=%+v winner=%s", loser, winner.Snapshot.TaskID)
	}
}

// 审查 F8：读取清理水位与读取事件之间若发生清理，返回的页会静默缺失中间事件；必须要求重新读取快照。
func TestPlatformEventsDetectPruneBetweenWatermarkAndRead(t *testing.T) {
	f := newPlatformImageFixture(t)
	p := f.svc.CreationPlatform()
	caller := cp.Caller{UserID: "user-a"}
	receipt, err := p.Submit(context.Background(), caller, f.systemRequest("prune-race"))
	if err != nil {
		t.Fatal(err)
	}
	f.runQueued(t)
	if err := f.db.Model(&model.PlatformTaskEvent{}).Where("1 = 1").Update("occurred_at_ms", time.Now().Add(-30*24*time.Hour).UnixMilli()).Error; err != nil {
		t.Fatal(err)
	}
	armed := true
	callbackName := "test:prune_after_watermark"
	if err := f.db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if !armed || tx.Statement.Table != "platform_event_counters" {
			return
		}
		armed = false
		if _, err := f.svc.PrunePlatformTaskEvents(time.Now().Add(-7 * 24 * time.Hour)); err != nil {
			t.Error(err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	page, err := p.Events(context.Background(), caller, cp.EventQuery{TaskID: receipt.Snapshot.TaskID, After: "1"})
	f.db.Callback().Query().Remove(callbackName)
	if err != nil {
		t.Fatal(err)
	}
	if armed {
		t.Fatal("test did not inject the concurrent prune")
	}
	if !page.ResyncRequired {
		t.Fatalf("events after a concurrent prune must require resync, got %d events", len(page.Events))
	}
}
