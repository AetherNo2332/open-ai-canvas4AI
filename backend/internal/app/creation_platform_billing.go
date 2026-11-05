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
func (p *creationPlatform) existingPlatformSubmission(userID, application string, req cp.GenerationRequest) (*cp.SubmitReceipt, error) {
	existing, err := p.s.repo.PlatformSubmissionByKey(userID, application, strings.TrimSpace(req.IdempotencyKey))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if existing.Fingerprint != req.Fingerprint() {
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
	// 签名按 task.ChannelModelID 定位渠道模型（逻辑模型同样记录了选中的路由），不依赖请求里的渠道字段。
	signature, err := p.s.repo.CreationPriceSignature(task, strings.TrimSpace(req.Model.ChannelID), strings.TrimSpace(req.Model.ModelKey))
	if err != nil {
		return cp.Quote{}, platformError(err)
	}
	quote := cp.Quote{Capability: req.Capability, ModelLabel: task.Model, BillingMode: "free", Quantity: 1, ExpiresAt: time.Now().Add(platformQuoteTTL)}
	if order != nil {
		quote.BillingMode, quote.Quantity, quote.AmountMicrocredits = order.BillingMode, order.Quantity, order.AmountMicrocredits
		quote.Estimated = order.BillingMode == "token"
	}
	quote.QuoteHash = creationHash([]any{signature, quote.BillingMode, quote.Quantity, quote.AmountMicrocredits, platformQuoteSpec(req)})
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
	prepared := &creationTaskPreparation{}
	taskReq.creationPrepare = prepared
	task, err := p.s.CreateTask(userID, taskReq)
	if err != nil {
		return nil, nil, platformError(err)
	}
	return task, prepared.Order, nil
}

// commitPlatformTask 在一个事务里冻结费用、写任务与提交记录；提交成功后才唤醒 worker。
func (p *creationPlatform) commitPlatformTask(task *model.Task, order *model.BillingOrder, record *model.PlatformSubmission) error {
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

func derefReceipt(receipt *cp.SubmitReceipt) cp.SubmitReceipt {
	if receipt == nil {
		return cp.SubmitReceipt{}
	}
	return *receipt
}
