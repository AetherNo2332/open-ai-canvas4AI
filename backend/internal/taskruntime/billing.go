package taskruntime

import (
	"errors"
	"fmt"
	"strings"

	"yingce/backend/internal/generation"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
)

// BillingRepository 是计费协调器所需的窄持久化合同。
type BillingRepository interface {
	MarkBillingRunning(orderID string) error
	SettleBillingOrder(orderID string, providerRequestID string) error
	RestoreRefundedBillingOrder(orderID string, providerRequestID string) error
	RefundBillingOrder(orderID string, errorText string) error
	MarkBillingUncertain(orderID string, errorText string) error
	BillingOrder(orderID string) (*model.BillingOrder, error)
	TaskHasSuccessfulBillableCall(taskID string) (bool, error)
}

// ErrBillingReview 表示订单状态已经不能由重试流程安全推断，必须先完成费用核对。
var ErrBillingReview = errors.New("task billing order requires review")

// BillingCoordinator 只负责任务生命周期中的计费状态迁移和核对策略。
type BillingCoordinator struct {
	repo BillingRepository
}

// NewBillingCoordinator 组合计费协调器。
func NewBillingCoordinator(repo BillingRepository) *BillingCoordinator {
	return &BillingCoordinator{repo: repo}
}

// MarkBillingRunning 把订单标记为执行中；空订单是显式无操作。
func (c *BillingCoordinator) MarkBillingRunning(orderID string) error {
	if orderID == "" {
		return nil
	}
	return c.repo.MarkBillingRunning(orderID)
}

// SettleBilling 按上游请求 ID 结算订单。
func (c *BillingCoordinator) SettleBilling(orderID string, providerRequestID string) error {
	if orderID == "" {
		return nil
	}
	return c.repo.SettleBillingOrder(orderID, providerRequestID)
}

// BillingOrder 读取订单；空订单 ID 返回 nil。
func (c *BillingCoordinator) BillingOrder(orderID string) (*model.BillingOrder, error) {
	if orderID == "" {
		return nil, nil
	}
	return c.repo.BillingOrder(orderID)
}

// SettleBillingWithAudioDuration 在按秒计费的音频订单上按实际时长结算；
// repository 未实现时长接口时回退到常规结算。
func (c *BillingCoordinator) SettleBillingWithAudioDuration(orderID string, providerRequestID string, durationMs int64) error {
	if orderID == "" {
		return nil
	}
	if repo, ok := c.repo.(interface {
		SettleBillingOrderWithAudioDuration(string, string, int64) error
	}); ok {
		return repo.SettleBillingOrderWithAudioDuration(orderID, providerRequestID, durationMs)
	}
	return c.repo.SettleBillingOrder(orderID, providerRequestID)
}

// RestoreRefundedBilling 把已退款订单恢复为待结算。
func (c *BillingCoordinator) RestoreRefundedBilling(orderID string, providerRequestID string) error {
	if orderID == "" {
		return nil
	}
	return c.repo.RestoreRefundedBillingOrder(orderID, providerRequestID)
}

// RefundBilling 退款并把原因脱敏、截断后入库。
func (c *BillingCoordinator) RefundBilling(orderID string, errorText string) error {
	if orderID == "" {
		return nil
	}
	return c.repo.RefundBillingOrder(orderID, kernel.TruncateRunes(RedactUpstreamAddress(errorText), 1000))
}

// MarkBillingUncertain 把订单标记为待核对；原因同样脱敏、截断。
func (c *BillingCoordinator) MarkBillingUncertain(orderID string, errorText string) error {
	if orderID == "" {
		return nil
	}
	return c.repo.MarkBillingUncertain(orderID, kernel.TruncateRunes(RedactUpstreamAddress(errorText), 1000))
}

// CheckRetryEligibility 收敛失败/取消任务重试前的计费状态校验。
//
// 计费订单读取失败不能被当作“没有待核对订单”继续放行，否则重试可能重复扣费。
// 只有明确读到订单且订单不是 uncertain 时，才允许进入后续的重新计费流程。
func (c *BillingCoordinator) CheckRetryEligibility(orderID string) error {
	if strings.TrimSpace(orderID) == "" {
		return nil
	}
	order, err := c.repo.BillingOrder(orderID)
	if err != nil {
		return fmt.Errorf("读取任务计费订单失败：%w", err)
	}
	if order == nil {
		return fmt.Errorf("任务计费订单不存在：%s", orderID)
	}
	if order.Status == model.BillingStatusUncertain {
		return ErrBillingReview
	}
	return nil
}

// BillingFailureRequiresReview 判断失败收尾是否必须进入待核对：
// 上游未明确给出终态且错误像连接中断、订单状态不可读或已经是 uncertain、
// 或本次任务存在成功的计费调用时，都不能直接退款。
func (c *BillingCoordinator) BillingFailureRequiresReview(orderID string, taskID string, err error) bool {
	if orderID == "" {
		return false
	}
	// 上游已明确返回失败终态时，原因里的 timeout、524 等字样是上游自述，不代表本地连接中断；
	// 是否仍有成功的计费调用照常由下面的订单和调用日志检查决定。
	var terminal generation.TerminalError
	if !errors.As(err, &terminal) && BillingFailureUncertain(err) {
		return true
	}
	order, orderErr := c.repo.BillingOrder(orderID)
	if orderErr != nil || order == nil || order.Status == model.BillingStatusUncertain {
		return true
	}
	hasSuccessfulCall, logErr := c.repo.TaskHasSuccessfulBillableCall(taskID)
	return logErr != nil || hasSuccessfulCall
}

// BillingFailureUncertain 判断错误是否像本地连接中断类失败，需要进入待核对。
func BillingFailureUncertain(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{"524", "timeout", "超时", "deadline exceeded", "context canceled", "connection reset", "unexpected eof", "broken pipe"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}
