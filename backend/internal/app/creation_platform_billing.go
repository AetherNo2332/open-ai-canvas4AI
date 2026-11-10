package app

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

const platformQuoteTTL = 10 * time.Minute

// platformSubmission 是一次合同提交的准备结果：幂等记录、翻译后的任务请求与可选费用上限。
type platformSubmission struct {
	record  *model.PlatformSubmission
	taskReq CreateTaskRequest
}

// existingPlatformSubmission 处理同一幂等作用域的已有记录：同内容返回原任务，异内容返回冲突。
func (p *creationPlatform) existingPlatformSubmission(userID, application, key, fingerprint string) (*cp.SubmitReceipt, error) {
	existing, err := p.s.repo.PlatformSubmissionByKey(userID, application, strings.TrimSpace(key))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if existing.Fingerprint != fingerprint {
		return nil, cp.NewError(cp.CodeIdempotencyConflict, "同一幂等键已用于不同内容的请求")
	}
	snapshot, err := p.snapshot(userID, existing.TaskID)
	if err != nil {
		return nil, err
	}
	return &cp.SubmitReceipt{Snapshot: snapshot, Replayed: true}, nil
}

// applyCostAuthorization 把单次费用上限写入费用订单（由冻结与结算校验执行），并核对报价签名。
// 报价签名覆盖渠道、价格档、分组与计费设置；价格或规格变化后签名不同，返回 quote_expired。
func (p *creationPlatform) applyCostAuthorization(userID string, req cp.GenerationRequest, task *model.Task, order *model.BillingOrder) error {
	if req.Cost.MaxMicrocredits != nil {
		limit := *req.Cost.MaxMicrocredits
		amount := int64(0)
		if order != nil {
			amount = order.AmountMicrocredits
		}
		if amount > limit {
			return cp.NewError(cp.CodeCostLimitExceeded, "报价超过授权的费用上限，尚未创建任务或冻结费用")
		}
		if order != nil {
			order.ChargeLimitSet, order.ChargeLimitMicrocredits = true, limit
		}
		task.AuthorizedChargeMicrocredits = limit
	}
	if strings.TrimSpace(req.Cost.QuoteHash) == "" {
		return nil
	}
	quote, err := p.quoteFor(userID, req, task, order)
	if err != nil {
		return err
	}
	if quote.QuoteHash != strings.TrimSpace(req.Cost.QuoteHash) {
		return cp.NewError(cp.CodeQuoteExpired, "报价已失效，请重新报价确认")
	}
	return nil
}

// quoteFor 计算与提交同口径的报价：同一次模型解析、同一份费用订单与价格签名。
func (p *creationPlatform) quoteFor(_ string, req cp.GenerationRequest, task *model.Task, order *model.BillingOrder) (cp.Quote, error) {
	signature, err := p.s.repo.PlatformPriceSignature(task, strings.TrimSpace(req.Model.ChannelID), strings.TrimSpace(req.Model.ModelKey))
	if err != nil {
		return cp.Quote{}, platformError(err)
	}
	quote := cp.Quote{Capability: req.Capability, ModelLabel: task.Model, BillingMode: "free", Quantity: 1, ExpiresAt: time.Now().Add(platformQuoteTTL)}
	if order != nil {
		quote.BillingMode, quote.Quantity, quote.AmountMicrocredits = order.BillingMode, order.Quantity, order.AmountMicrocredits
		quote.Estimated = order.BillingMode == "token"
	}
	// 签名已覆盖全部线路的计费配置。随机选线的金额、模式与数量不能成为报价身份，
	// 单次扣费仍由 MaxMicrocredits 独立约束。
	quote.QuoteHash = creationHash([]any{signature, platformQuoteSpec(req)})
	return quote, nil
}

// platformQuoteSpec 是报价绑定的生成规格：不含幂等键、费用授权、业务标签与追踪 ID，
// 使“先报价、再带 quoteHash 提交”两次计算得到同一签名。
func platformQuoteSpec(req cp.GenerationRequest) string {
	spec := req
	spec.IdempotencyKey, spec.TraceID, spec.Cost, spec.Business = "quote", "", cp.CostAuthorization{}, cp.BusinessRef{}
	return spec.Fingerprint()
}

// preparePlatformTask 用既有 CreateTask 的准备模式完成模型解析、能力校验与计价，不写库、不冻结。
func (p *creationPlatform) preparePlatformTask(userID string, req cp.GenerationRequest) (*model.Task, *model.BillingOrder, error) {
	taskReq, err := p.platformTaskRequest(userID, req)
	if err != nil {
		return nil, nil, platformError(err)
	}
	return p.prepareResolvedTask(userID, taskReq)
}

// prepareResolvedTask 对已解析的任务请求做准入与计价，不写库、不冻结；业务适配器（如工作台）直接调用它。
func (p *creationPlatform) prepareResolvedTask(userID string, taskReq CreateTaskRequest) (*model.Task, *model.BillingOrder, error) {
	prepared := &creationTaskPreparation{}
	taskReq.creationPrepare = prepared
	task, err := p.s.CreateTask(userID, taskReq)
	if err != nil {
		return nil, nil, platformError(err)
	}
	return task, prepared.Order, nil
}

// commitPlatformTask 在一个事务里冻结费用、写任务与提交记录；提交成功后才唤醒 worker。
func (p *creationPlatform) commitPlatformTask(task *model.Task, order *model.BillingOrder, record *model.PlatformSubmission, dramaJob ...*model.DramaJob) error {
	var input map[string]any
	if err := json.Unmarshal([]byte(task.InputJSON), &input); err != nil {
		return err
	}
	if err := p.s.protectTaskSecrets(input); err != nil {
		return err
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return err
	}
	task.InputJSON = string(encoded)
	if len(dramaJob) == 1 && dramaJob[0] != nil {
		_, err := p.s.persistDramaTask(dramaJob[0], task, order, record)
		return err
	}
	if order != nil {
		task.BillingOrderID = order.ID
	}
	policy, err := p.s.RuntimePolicy()
	if err != nil {
		return err
	}
	if err := p.s.createTaskWithinStorageQuota(task, order, policy, record); err != nil {
		return err
	}
	p.s.recordActivity(task.UserID, "task", 1)
	_ = p.s.log(task.UserID, task.ID, "info", "任务已进入队列", "")
	return nil
}

func platformSubmissionRecord(userID, application string, req cp.GenerationRequest) *model.PlatformSubmission {
	business, _ := json.Marshal(req.Business)
	record := &model.PlatformSubmission{
		UserID: userID, Application: application, IdempotencyKey: strings.TrimSpace(req.IdempotencyKey),
		Fingerprint: req.Fingerprint(), Capability: string(req.Capability), BusinessJSON: string(business),
		ProjectRef: req.Business.ProjectID, ObjectType: req.Business.ObjectType, ObjectID: req.Business.ObjectID, OperationID: req.Business.OperationID,
	}
	if req.Cost.MaxMicrocredits != nil {
		record.MaxChargeSet, record.MaxChargeMicrocredits = true, *req.Cost.MaxMicrocredits
	}
	return record
}

// replayConcurrentWinner 处理查过幂等记录之后的任何失败：同键的并发请求可能已经成功提交，
// 它占用的活动名额或额度会让本请求在准备、授权或提交阶段被拒。此时按同一指纹回放赢家，
// 内容不同则返回冲突；没有赢家才把原错误映射为合同错误返回。
func replayConcurrentWinner(lookup func() (*cp.SubmitReceipt, error), cause error) (*cp.SubmitReceipt, error) {
	if replay, err := lookup(); replay != nil || err != nil {
		return replay, err
	}
	return nil, platformError(cause)
}

func derefReceipt(receipt *cp.SubmitReceipt) cp.SubmitReceipt {
	if receipt == nil {
		return cp.SubmitReceipt{}
	}
	return *receipt
}

// applyPlatformRetryCap 让平台任务的重试沿用提交时授权的单次费用上限：新订单写入同样的上限，
// 报价超过上限时拒绝。它挂在既有 retryTask 上，平台重试与 /api/tasks/:id/retry 都会经过。
func (s *Service) applyPlatformRetryCap(userID string, task *model.Task, order *model.BillingOrder) error {
	submission, err := s.repo.PlatformSubmissionForTask(userID, task.ID)
	if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && !submission.MaxChargeSet) {
		return nil
	}
	if err != nil {
		return err
	}
	if order == nil {
		return nil
	}
	if order.AmountMicrocredits > submission.MaxChargeMicrocredits {
		return cp.NewError(cp.CodeCostLimitExceeded, "当前价格超过提交时授权的费用上限，未重试、未冻结")
	}
	order.ChargeLimitSet, order.ChargeLimitMicrocredits = true, submission.MaxChargeMicrocredits
	task.AuthorizedChargeMicrocredits = submission.MaxChargeMicrocredits
	return nil
}

// cappedRetryBillingOrder 为重试重新计价，并对平台任务套用提交时的费用上限。
func (s *Service) cappedRetryBillingOrder(userID string, task *model.Task, input map[string]any) (*model.BillingOrder, error) {
	order, err := s.taskBillingOrder(userID, task, input)
	if err != nil {
		return nil, err
	}
	return order, s.applyPlatformRetryCap(userID, task, order)
}
