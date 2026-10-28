package model

const (
	// TaskStatusSubmissionUncertain 表示上游提交结果未知：在核对原请求之前不自动领取或重投。
	// 超时不等于供应商拒绝了请求。仅由画布任务绑定与媒体版本链路写入。
	TaskStatusSubmissionUncertain TaskStatus = "submission_uncertain"
	// TaskStatusHolding 是 Agent 建 run 时的占位任务：只承载这一轮的报价预留，从不进入队列。
	// worker 只领取 queued/running；首个模型步骤会在同一事务里换成真实首步任务，
	// 或由终态清理退还预留。
	TaskStatusHolding TaskStatus = "holding"
)
