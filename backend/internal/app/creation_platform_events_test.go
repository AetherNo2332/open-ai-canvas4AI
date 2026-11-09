package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/model"
)

func platformEvents(t *testing.T, p cp.Platform, caller cp.Caller, query cp.EventQuery) cp.EventPage {
	t.Helper()
	page, err := p.Events(context.Background(), caller, query)
	if err != nil {
		t.Fatalf("events %+v: %v", query, err)
	}
	return page
}

// drainEvents 按游标读完所有事件，模拟消费者断线后续读。
func drainEvents(t *testing.T, p cp.Platform, caller cp.Caller, query cp.EventQuery) ([]cp.TaskEvent, string) {
	t.Helper()
	var all []cp.TaskEvent
	for range 50 {
		page := platformEvents(t, p, caller, query)
		if page.ResyncRequired {
			t.Fatalf("unexpected resync: %+v", page)
		}
		all = append(all, page.Events...)
		if len(page.Events) == 0 {
			return all, page.NextCursor
		}
		query.After = page.NextCursor
	}
	t.Fatal("event stream did not end")
	return nil, ""
}

// S3-A / S3-B：排队、执行、终态与费用变化都有版本化事件；事件与状态同事务可见；
// 断线续读、重复读、进程重启后都能接上，旧事件不让状态倒退。
func TestPlatformEventsResumeAndReplay(t *testing.T) {
	platformDialects(t, func(t *testing.T, f *platformImageFixture) {
		p := f.svc.CreationPlatform()
		caller := cp.Caller{UserID: "user-a"}
		receipt, err := p.Submit(context.Background(), caller, f.systemRequest("events-1"))
		if err != nil {
			t.Fatal(err)
		}
		taskID := receipt.Snapshot.TaskID
		submitted := platformEvents(t, p, caller, cp.EventQuery{TaskID: taskID})
		if len(submitted.Events) != 1 || submitted.Events[0].Type != cp.EventSubmitted || submitted.Events[0].Execution != cp.ExecutionQueued || submitted.Events[0].Billing != cp.BillingReserved {
			t.Fatalf("submit must publish exactly one submitted event: %+v", submitted.Events)
		}
		if receipt.Snapshot.Revision != submitted.Events[0].Revision {
			t.Fatalf("snapshot revision %d != event revision %d", receipt.Snapshot.Revision, submitted.Events[0].Revision)
		}
		cursor := submitted.NextCursor

		// 进程重启：新的 Service 只共享数据库，worker 完成任务。
		restarted := &Service{repo: f.svc.repo, dataDir: f.svc.dataDir, activeCancels: f.svc.activeCancels, coordinator: f.svc.coordinator}
		f.svc = restarted
		f.runQueued(t)
		p = restarted.CreationPlatform()

		events, _ := drainEvents(t, p, caller, cp.EventQuery{TaskID: taskID, After: cursor, Limit: 1})
		seen := map[cp.ExecutionState]bool{}
		var last cp.TaskEvent
		for index, event := range events {
			if event.TaskID != taskID || event.Revision <= submitted.Events[0].Revision {
				t.Fatalf("event %d out of order: %+v", index, event)
			}
			if index > 0 && (event.ID <= events[index-1].ID || event.Revision <= events[index-1].Revision) {
				t.Fatalf("events must strictly increase: %+v", events)
			}
			seen[event.Execution] = true
			last = event
		}
		if !seen[cp.ExecutionRunning] || last.Execution != cp.ExecutionSucceeded || last.Resources != cp.ResourcesReady || last.Billing != cp.BillingSettled {
			t.Fatalf("missing running/succeeded/settled events: %+v", events)
		}
		snapshot, err := p.Task(context.Background(), caller, taskID)
		if err != nil || snapshot.Revision != last.Revision {
			t.Fatalf("snapshot revision %d must equal latest event %d (%v)", snapshot.Revision, last.Revision, err)
		}

		// 重复投递：同一游标再读得到同样的事件，消费者按 ID 去重即可。
		again, _ := drainEvents(t, p, caller, cp.EventQuery{TaskID: taskID, After: cursor})
		if len(again) != len(events) || again[0].ID != events[0].ID {
			t.Fatalf("replay from the same cursor differs: %d vs %d", len(again), len(events))
		}
		// 业务维度订阅：按 application 读取包含该任务的事件。
		byApp, _ := drainEvents(t, p, caller, cp.EventQuery{Application: "ecommerce"})
		if len(byApp) != len(events)+1 {
			t.Fatalf("application stream = %d events, want %d", len(byApp), len(events)+1)
		}
		// 他人看不到这些事件。
		if other := platformEvents(t, p, cp.Caller{UserID: "user-b"}, cp.EventQuery{Application: "ecommerce"}); len(other.Events) != 0 {
			t.Fatalf("user B saw user A's events: %+v", other.Events)
		}
		if _, err := p.Events(context.Background(), caller, cp.EventQuery{}); platformErrorCode(err) != cp.CodeInvalidRequest {
			t.Fatalf("events without task or application: %v", err)
		}
		// 非平台任务的状态变化不产生平台事件。
		legacy, err := f.svc.CreateTask("user-a", CreateTaskRequest{Type: "canvas_image", Prompt: "旧入口", Input: map[string]any{"mode": "image", "prompt": "旧入口", "config": map[string]any{"channelId": f.channelID, "model": f.modelKey, "size": "1024x1024"}}})
		if err != nil {
			t.Fatal(err)
		}
		f.runQueued(t)
		if got := platformCount(t, f.db, &model.PlatformTaskEvent{}, "task_id = ?", legacy.ID); got != 0 {
			t.Fatalf("legacy task produced %d platform events", got)
		}
	})
}

// S3-B：过期游标明确要求重新读取快照，而不是静默丢事件。
func TestPlatformEventsExpiredCursorRequiresResync(t *testing.T) {
	platformDialects(t, func(t *testing.T, f *platformImageFixture) {
		p := f.svc.CreationPlatform()
		caller := cp.Caller{UserID: "user-a"}
		receipt, err := p.Submit(context.Background(), caller, f.systemRequest("expired-1"))
		if err != nil {
			t.Fatal(err)
		}
		f.runQueued(t)
		all, latest := drainEvents(t, p, caller, cp.EventQuery{TaskID: receipt.Snapshot.TaskID})
		if len(all) < 3 {
			t.Fatalf("need several events, got %+v", all)
		}
		// 把除最后一条外的事件都标为过期并清理；每个任务的最新事件必须保留。
		if err := f.db.Model(&model.PlatformTaskEvent{}).Where("1 = 1").Update("occurred_at_ms", time.Now().Add(-30*24*time.Hour).UnixMilli()).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := f.svc.PrunePlatformTaskEvents(time.Now().Add(-7 * 24 * time.Hour)); err != nil {
			t.Fatal(err)
		}
		if got := platformCount(t, f.db, &model.PlatformTaskEvent{}, "task_id = ?", receipt.Snapshot.TaskID); got != 1 {
			t.Fatalf("prune must keep the latest event per task, kept %d", got)
		}
		stale := platformEvents(t, p, caller, cp.EventQuery{TaskID: receipt.Snapshot.TaskID, After: "0"})
		if !stale.ResyncRequired || len(stale.Events) != 0 || stale.NextCursor != latest {
			t.Fatalf("expired cursor must ask for resync from %s: %+v", latest, stale)
		}
		fresh := platformEvents(t, p, caller, cp.EventQuery{TaskID: receipt.Snapshot.TaskID, After: latest})
		if fresh.ResyncRequired || len(fresh.Events) != 0 {
			t.Fatalf("current cursor must continue normally: %+v", fresh)
		}
		snapshot, err := p.Task(context.Background(), caller, receipt.Snapshot.TaskID)
		if err != nil || snapshot.Revision != all[len(all)-1].Revision {
			t.Fatalf("revision must not go backwards after prune: %d vs %d (%v)", snapshot.Revision, all[len(all)-1].Revision, err)
		}
		if _, err := p.Events(context.Background(), caller, cp.EventQuery{TaskID: receipt.Snapshot.TaskID, After: "not-a-cursor"}); platformErrorCode(err) != cp.CodeInvalidRequest {
			t.Fatalf("malformed cursor: %v", err)
		}
	})
}

// S3-C：上游成功但转存失败时，快照区分执行结果与资源未就绪；恢复后同一任务交付，不重新生成。
func TestPlatformMediaRecoveryWithoutRegeneration(t *testing.T) {
	platformDialects(t, func(t *testing.T, f *platformImageFixture) {
		p := f.svc.CreationPlatform()
		caller := cp.Caller{UserID: "user-a"}
		f.urlResults.Store(true)
		f.downloadFailures.Store(100)
		receipt, err := p.Submit(context.Background(), caller, f.systemRequest("recover-1"))
		if err != nil {
			t.Fatal(err)
		}
		taskID := receipt.Snapshot.TaskID
		_ = f.runQueuedResult(t)
		delivering, err := p.Task(context.Background(), caller, taskID)
		if err != nil {
			t.Fatal(err)
		}
		if delivering.Execution != cp.ExecutionSucceeded || delivering.Resources != cp.ResourcesDelivering || delivering.Result != nil {
			t.Fatalf("after first download failure: %+v", delivering)
		}
		// 自动恢复窗口耗尽后进入可手动恢复的失败态。
		if err := f.db.Model(&model.Task{}).Where("id = ?", taskID).Update("next_poll_at", time.Now().Add(-time.Second)).Error; err != nil {
			t.Fatal(err)
		}
		checkpointExhaust(t, f, taskID)
		_ = f.runQueuedResult(t)
		failed, err := p.Task(context.Background(), caller, taskID)
		if err != nil {
			t.Fatal(err)
		}
		if failed.Execution != cp.ExecutionSucceeded || failed.Resources != cp.ResourcesFailed || !failed.Allows(cp.ActionRecoverMedia) || failed.Allows(cp.ActionRetry) || failed.Error == nil || failed.Error.Code != cp.CodeMediaDeliveryFailed {
			t.Fatalf("delivery failure snapshot: %+v", failed)
		}
		if failed.Billing.Status != cp.BillingPendingReview || failed.Billing.ChargedMicrocredits != nil {
			t.Fatalf("delivery failure must not refund or show a charge: %+v", failed.Billing)
		}
		// 交付失败时费用进入待核对（作品已生成，费用属于原生成），重试按待核对拒绝。
		if err := retryErr(p, caller, taskID); platformErrorCode(err) != cp.CodeBillingPendingReview {
			t.Fatalf("retry must be refused while media can be recovered: %v", err)
		}
		generations := f.calls.Load()
		f.downloadFailures.Store(0)
		recovering, err := p.RecoverMedia(context.Background(), caller, taskID)
		if err != nil {
			t.Fatal(err)
		}
		if recovering.TaskID != taskID || recovering.Allows(cp.ActionRecoverMedia) {
			t.Fatalf("recovery snapshot: %+v", recovering)
		}
		f.runQueued(t)
		done, err := p.Task(context.Background(), caller, taskID)
		if err != nil {
			t.Fatal(err)
		}
		if done.Execution != cp.ExecutionSucceeded || done.Resources != cp.ResourcesReady || done.Result == nil || len(done.Result.Resources) != 1 {
			t.Fatalf("recovered snapshot: %+v", done)
		}
		if f.calls.Load() != generations {
			t.Fatalf("recovery called the generation API again: %d -> %d", generations, f.calls.Load())
		}
		if got := platformCount(t, f.db, &model.BillingOrder{}, "user_id = ?", "user-a"); got != 1 {
			t.Fatalf("recovery created a new billing order: %d", got)
		}
		if done.Billing.Status != cp.BillingSettled || done.Billing.ChargedMicrocredits == nil || *done.Billing.ChargedMicrocredits != 100_000 {
			t.Fatalf("recovered billing: %+v", done.Billing)
		}
	})
}

func retryErr(p cp.Platform, caller cp.Caller, taskID string) error {
	_, err := p.Retry(context.Background(), caller, taskID)
	return err
}

// checkpointExhaust 用完自动恢复次数，使下一次失败进入终态（与真实 20 分钟窗口等价）。
func checkpointExhaust(t *testing.T, f *platformImageFixture, taskID string) {
	t.Helper()
	task, err := f.svc.repo.Task(taskID)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := f.svc.decodeMediaCheckpoint(task)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint.Attempts = len(mediaRecoveryDelays)
	if err := f.svc.saveMediaCheckpoint(task, checkpoint, "download"); err != nil {
		t.Fatal(err)
	}
}

// S3-D：费用待核对时实收未知，不显示成 0 或已退款；费用终态变化可被消费者观察；不能重试。
func TestPlatformPendingBillingIsNotZero(t *testing.T) {
	platformDialects(t, func(t *testing.T, f *platformImageFixture) {
		p := f.svc.CreationPlatform()
		caller := cp.Caller{UserID: "user-a"}
		receipt, err := p.Submit(context.Background(), caller, f.systemRequest("pending-1"))
		if err != nil {
			t.Fatal(err)
		}
		taskID := receipt.Snapshot.TaskID
		_, cursor := drainEvents(t, p, caller, cp.EventQuery{TaskID: taskID})
		// 运行中取消：上游可能已经调用，费用进入待核对。
		if _, err := f.svc.repo.ClaimNextTask("platform-worker", time.Minute); err != nil {
			t.Fatal(err)
		}
		if err := f.db.Model(&model.Task{}).Where("id = ?", taskID).Update("stage", "后端接管任务").Error; err != nil {
			t.Fatal(err)
		}
		cancelled, err := p.Cancel(context.Background(), caller, taskID)
		if err != nil {
			t.Fatal(err)
		}
		if cancelled.Execution != cp.ExecutionCancelled || cancelled.Billing.Status != cp.BillingPendingReview || cancelled.Billing.ChargedMicrocredits != nil || cancelled.Billing.RefundedMicrocredits != 0 {
			t.Fatalf("running cancel must leave billing pending review: %+v", cancelled.Billing)
		}
		if cancelled.Allows(cp.ActionRetry) || cancelled.Allows(cp.ActionCancel) {
			t.Fatalf("pending review allows no retry or cancel: %+v", cancelled.Actions)
		}
		if _, err := p.Retry(context.Background(), caller, taskID); platformErrorCode(err) != cp.CodeBillingPendingReview {
			t.Fatalf("retry during billing review: %v", err)
		}
		raw, _ := json.Marshal(cancelled.Billing)
		if strings.Contains(string(raw), "chargedMicrocredits") {
			t.Fatalf("charged must be absent, not zero: %s", raw)
		}
		// 管理员核对为退款：消费者能观察到费用终态。
		order, err := f.svc.repo.BillingOrder(cancelledOrderID(t, f, taskID))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.svc.resolveBillingOrder(&model.User{ID: "admin", Role: model.UserRoleAdmin}, order.ID, "refund", "核对后退款"); err != nil {
			t.Fatal(err)
		}
		events, _ := drainEvents(t, p, caller, cp.EventQuery{TaskID: taskID, After: cursor})
		var billingStates []cp.BillingState
		for _, event := range events {
			billingStates = append(billingStates, event.Billing)
		}
		if len(events) == 0 || events[len(events)-1].Billing != cp.BillingRefunded {
			t.Fatalf("billing resolution must publish an event: %v", billingStates)
		}
		final, err := p.Task(context.Background(), caller, taskID)
		if err != nil || final.Billing.Status != cp.BillingRefunded || final.Billing.RefundedMicrocredits != 100_000 {
			t.Fatalf("resolved billing: %+v %v", final.Billing, err)
		}
	})
}

func cancelledOrderID(t *testing.T, f *platformImageFixture, taskID string) string {
	t.Helper()
	task, err := f.svc.repo.Task(taskID)
	if err != nil || task.BillingOrderID == "" {
		t.Fatalf("task billing order: %+v %v", task, err)
	}
	return task.BillingOrderID
}

// S3：允许动作由服务端决定；排队可取消，上游已提交后普通用户不能取消，失败可重试且走新冻结。
func TestPlatformActionsFollowServerRules(t *testing.T) {
	f := newPlatformImageFixture(t)
	p := f.svc.CreationPlatform()
	caller := cp.Caller{UserID: "user-a"}
	queued, err := p.Submit(context.Background(), caller, f.systemRequest("actions-queued"))
	if err != nil {
		t.Fatal(err)
	}
	if !queued.Snapshot.Allows(cp.ActionCancel) || queued.Snapshot.Allows(cp.ActionRetry) {
		t.Fatalf("queued actions: %+v", queued.Snapshot.Actions)
	}
	cancelled, err := p.Cancel(context.Background(), caller, queued.Snapshot.TaskID)
	if err != nil || cancelled.Execution != cp.ExecutionCancelled || cancelled.Billing.Status != cp.BillingRefunded {
		t.Fatalf("queued cancel refunds: %+v %v", cancelled, err)
	}
	if !cancelled.Allows(cp.ActionRetry) {
		t.Fatalf("refunded cancelled task should allow retry: %+v", cancelled.Actions)
	}
	if _, err := p.Cancel(context.Background(), cp.Caller{UserID: "user-b"}, queued.Snapshot.TaskID); platformErrorCode(err) != cp.CodeNotFound {
		t.Fatalf("user B cancel: %v", err)
	}
	retried, err := p.Retry(context.Background(), caller, queued.Snapshot.TaskID)
	if err != nil || retried.Execution != cp.ExecutionQueued || retried.Billing.Status != cp.BillingReserved || retried.TaskID != queued.Snapshot.TaskID {
		t.Fatalf("retry: %+v %v", retried, err)
	}
	if retried.Revision <= cancelled.Revision {
		t.Fatalf("retry must advance revision: %d -> %d", cancelled.Revision, retried.Revision)
	}
	f.runQueued(t)

	submitted, err := p.Submit(context.Background(), caller, f.systemRequest("actions-submitted"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.Task{}).Where("id = ?", submitted.Snapshot.TaskID).Updates(map[string]any{"status": model.TaskStatusRunning, "provider_request_id": "upstream-1", "stage": "生成中"}).Error; err != nil {
		t.Fatal(err)
	}
	running, err := p.Task(context.Background(), caller, submitted.Snapshot.TaskID)
	if err != nil || running.Allows(cp.ActionCancel) {
		t.Fatalf("upstream-submitted task must not offer cancel: %+v %v", running.Actions, err)
	}
	if _, err := p.Cancel(context.Background(), caller, submitted.Snapshot.TaskID); platformErrorCode(err) != cp.CodeActionNotAllowed {
		t.Fatalf("cancel after upstream submit: %v", err)
	}
	if _, err := p.RecoverMedia(context.Background(), caller, submitted.Snapshot.TaskID); platformErrorCode(err) != cp.CodeActionNotAllowed {
		t.Fatalf("recover without checkpoint: %v", err)
	}
}

// S3-E：快照与事件不含密钥或上游受保护配置；同一资源被多处引用不复制物理文件。
func TestPlatformSnapshotsAndEventsCarryNoSecrets(t *testing.T) {
	f := newPlatformImageFixture(t)
	p := f.svc.CreationPlatform()
	caller := cp.Caller{UserID: "user-a"}
	receipt, err := p.Submit(context.Background(), caller, f.systemRequest("secrets-1"))
	if err != nil {
		t.Fatal(err)
	}
	f.runQueued(t)
	snapshot, err := p.Task(context.Background(), caller, receipt.Snapshot.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	events, _ := drainEvents(t, p, caller, cp.EventQuery{TaskID: receipt.Snapshot.TaskID})
	for _, value := range []any{snapshot, events} {
		raw, _ := json.Marshal(value)
		for _, secret := range []string{"upstream-test-key", f.upstream.URL, "apiKey", "baseUrl", "providerRequestId", "priceTierId"} {
			if strings.Contains(string(raw), secret) {
				t.Fatalf("%T leaked %q: %s", value, secret, raw)
			}
		}
	}
	resourceID := snapshot.Result.Resources[0].ResourceID
	reuse := f.systemRequest("secrets-reuse")
	reuse.Operation = "image_to_image"
	reuse.References = []cp.ReferenceResource{{ResourceID: resourceID, Kind: "image"}}
	before := platformCount(t, f.db, &model.Resource{}, "user_id = ?", "user-a")
	if _, err := p.Submit(context.Background(), caller, reuse); err != nil {
		t.Fatal(err)
	}
	if after := platformCount(t, f.db, &model.Resource{}, "user_id = ?", "user-a"); after != before {
		t.Fatalf("referencing a generated resource copied it: %d -> %d", before, after)
	}
}
