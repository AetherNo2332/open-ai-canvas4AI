package creationplatform

import "context"

// Caller 是受信任调用上下文注入的身份。HTTP 适配层从登录会话得到 UserID；
// Go 业务编排（短剧调度器等）使用业务对象的所有者。身份从不来自请求体或 BusinessRef。
type Caller struct {
	UserID string
}

// Platform 是业务共用的能力入口。HTTP 处理器与 Go 编排调用同一实现，不存在第二条执行路径。
type Platform interface {
	Models(ctx context.Context, caller Caller, capability Capability) (ModelCatalog, error)
	Quote(ctx context.Context, caller Caller, req GenerationRequest) (Quote, error)
	Submit(ctx context.Context, caller Caller, req GenerationRequest) (SubmitReceipt, error)
	Task(ctx context.Context, caller Caller, taskID string) (TaskSnapshot, error)
	Events(ctx context.Context, caller Caller, query EventQuery) (EventPage, error)
	Cancel(ctx context.Context, caller Caller, taskID string) (TaskSnapshot, error)
	Retry(ctx context.Context, caller Caller, taskID string) (TaskSnapshot, error)
	RecoverMedia(ctx context.Context, caller Caller, taskID string) (TaskSnapshot, error)
	Balance(ctx context.Context, caller Caller) (CreditBalance, error)
	BillingAggregate(ctx context.Context, caller Caller, business BusinessRef) (BillingAggregate, error)
}
