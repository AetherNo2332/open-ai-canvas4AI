package app

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

const (
	platformEventPageLimit = 200
	platformEventRetention = 7 * 24 * time.Hour
)

func (p *creationPlatform) Events(_ context.Context, caller cp.Caller, query cp.EventQuery) (cp.EventPage, error) {
	if err := requireCaller(caller); err != nil {
		return cp.EventPage{}, err
	}
	after, err := parsePlatformCursor(query.After)
	if err != nil {
		return cp.EventPage{}, err
	}
	scope := repository.PlatformEventQuery{UserID: caller.UserID, TaskID: strings.TrimSpace(query.TaskID), After: after, Limit: query.Limit}
	if scope.TaskID == "" {
		if scope.Application, err = platformApplication(query.Application); err != nil {
			return cp.EventPage{}, cp.NewError(cp.CodeInvalidRequest, "按任务或 application 读取事件")
		}
		scope.ProjectRef = strings.TrimSpace(query.ProjectID)
	}
	if scope.Limit <= 0 || scope.Limit > platformEventPageLimit {
		scope.Limit = platformEventPageLimit
	}
	current, prunedBefore, err := p.s.repo.PlatformEventWatermarks()
	if err != nil {
		return cp.EventPage{}, err
	}
	// 游标早于清理水位（含从头读取的 0）：中间的事件可能已删除，消费者必须先读快照，再从当前游标继续。
	// 正常订阅以快照的 revision 作为起始游标，不需要从 0 读。
	if after < prunedBefore {
		return cp.EventPage{Events: []cp.TaskEvent{}, NextCursor: strconv.FormatInt(current, 10), ResyncRequired: true}, nil
	}
	rows, err := p.s.repo.PlatformEvents(scope)
	if err != nil {
		return cp.EventPage{}, err
	}
	// 清理可能发生在两次读取之间：读完事件后再看一次水位，越过了起始游标就说明这一页可能缺事件。
	current, prunedBefore, err = p.s.repo.PlatformEventWatermarks()
	if err != nil {
		return cp.EventPage{}, err
	}
	if after < prunedBefore {
		return cp.EventPage{Events: []cp.TaskEvent{}, NextCursor: strconv.FormatInt(current, 10), ResyncRequired: true}, nil
	}
	page := cp.EventPage{Events: make([]cp.TaskEvent, 0, len(rows)), NextCursor: strconv.FormatInt(after, 10)}
	for _, row := range rows {
		page.Events = append(page.Events, platformEvent(row))
		page.NextCursor = strconv.FormatInt(*row.Position, 10)
	}
	return page, nil
}

func parsePlatformCursor(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	cursor, err := strconv.ParseInt(value, 10, 64)
	if err != nil || cursor < 0 {
		return 0, cp.NewError(cp.CodeInvalidRequest, "事件游标无效")
	}
	return cursor, nil
}

// platformEvent 用与快照相同的映射把触发器记录的原始状态转换为合同状态。
func platformEvent(row model.PlatformTaskEvent) cp.TaskEvent {
	task := model.Task{Status: model.TaskStatus(row.TaskStatus), MediaStage: row.MediaStage}
	if row.MediaPending {
		task.MediaRecoveryJSON = "pending"
	}
	execution := platformExecution(task)
	resources := cp.ResourcesNone
	switch {
	case row.MediaPending && task.Status == model.TaskStatusFailed:
		resources = cp.ResourcesFailed
	case row.MediaPending:
		resources = cp.ResourcesDelivering
	case execution == cp.ExecutionSucceeded && row.HasResources:
		resources = cp.ResourcesReady
	}
	billing := cp.BillingFree
	if row.BillingStatus != "" {
		billing = platformBilling(&model.BillingOrder{Status: model.BillingStatus(row.BillingStatus)}, model.PlatformSubmission{}).Status
	}
	return cp.TaskEvent{
		ID: *row.Position, TaskID: row.TaskID, Revision: *row.Position, Type: cp.EventType(row.Type),
		Execution: execution, Resources: resources, Billing: billing, OccurredAt: time.UnixMilli(row.OccurredAtMs).UTC(),
	}
}

// PrunePlatformTaskEvents 清理早于 before 的事件（保留每个任务的最新一条）。
func (s *Service) PrunePlatformTaskEvents(before time.Time) (int64, error) {
	return s.repo.PrunePlatformTaskEvents(before)
}

func (s *Service) startPlatformEventPrune(ctx context.Context) {
	s.runWorkerLoop(func(ctx context.Context) {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = s.PrunePlatformTaskEvents(time.Now().Add(-platformEventRetention))
			}
		}
	})
}

// platformActions 只列出服务端此刻会接受的操作；规则与既有取消、重试和媒体恢复入口一致。
func platformActions(task model.Task, snapshot cp.TaskSnapshot) []cp.Action {
	actions := []cp.Action{}
	switch task.Status {
	case model.TaskStatusQueued, model.TaskStatusRunning:
		if !taskUpstreamSubmitted(&task) {
			actions = append(actions, cp.ActionCancel)
		}
	case model.TaskStatusFailed, model.TaskStatusCancelled:
		switch {
		case snapshot.Resources == cp.ResourcesFailed:
			if task.Status == model.TaskStatusFailed && snapshot.Billing.Status != cp.BillingRefunded {
				actions = append(actions, cp.ActionRecoverMedia)
			}
		case snapshot.Billing.Status == cp.BillingPendingReview,
			task.ProviderCancelStatus == model.ProviderCancelStatusRequested,
			isContentModerationFailure(task.Error):
		default:
			actions = append(actions, cp.ActionRetry)
		}
	}
	return actions
}

func (p *creationPlatform) Cancel(ctx context.Context, caller cp.Caller, taskID string) (cp.TaskSnapshot, error) {
	return p.runAction(caller, taskID, cp.ActionCancel, func(userID, id string) error {
		_, err := p.s.CancelTask(ctx, userID, id)
		return err
	})
}

func (p *creationPlatform) Retry(_ context.Context, caller cp.Caller, taskID string) (cp.TaskSnapshot, error) {
	return p.runAction(caller, taskID, cp.ActionRetry, func(userID, id string) error {
		_, err := p.s.RetryTask(userID, id)
		if err == nil {
			p.s.wakeTaskDispatcher()
		}
		return err
	})
}

func (p *creationPlatform) RecoverMedia(_ context.Context, caller cp.Caller, taskID string) (cp.TaskSnapshot, error) {
	return p.runAction(caller, taskID, cp.ActionRecoverMedia, func(userID, id string) error {
		_, err := p.s.RecoverTaskMedia(userID, id)
		if err == nil {
			p.s.wakeTaskDispatcher()
		}
		return err
	})
}

// runAction 先按快照的允许动作拒绝，再交给既有入口；既有入口仍做最终校验（状态可能在两次读取间变化）。
func (p *creationPlatform) runAction(caller cp.Caller, taskID string, action cp.Action, run func(userID, id string) error) (cp.TaskSnapshot, error) {
	if err := requireCaller(caller); err != nil {
		return cp.TaskSnapshot{}, err
	}
	taskID = strings.TrimSpace(taskID)
	snapshot, err := p.snapshot(caller.UserID, taskID)
	if err != nil {
		return cp.TaskSnapshot{}, err
	}
	if !snapshot.Allows(action) {
		code := cp.CodeActionNotAllowed
		if action == cp.ActionRetry && snapshot.Billing.Status == cp.BillingPendingReview {
			code = cp.CodeBillingPendingReview
		}
		return cp.TaskSnapshot{}, cp.NewError(code, "任务当前不允许该操作")
	}
	if err := run(caller.UserID, taskID); err != nil {
		var appErr *AppError
		if errors.As(err, &appErr) && appErr != nil && appErr.Status >= 400 && appErr.Status < 500 {
			return cp.TaskSnapshot{}, cp.NewError(cp.CodeActionNotAllowed, appErr.Message)
		}
		return cp.TaskSnapshot{}, platformError(err)
	}
	return p.snapshot(caller.UserID, taskID)
}
