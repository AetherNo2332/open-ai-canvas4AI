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

// TaskStatusActive 判断任务是否仍占用执行：queued、running 与 submission_uncertain。
// 待核对的提交可能已被上游接单并计费，在核对前不能当作已结束去接新轮、判卡死或回收 worker。
func TaskStatusActive(s TaskStatus) bool {
	return s == TaskStatusQueued || s == TaskStatusRunning || s == TaskStatusSubmissionUncertain
}
