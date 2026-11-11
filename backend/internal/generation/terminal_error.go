package generation

import (
	"fmt"

	"yingce/backend/internal/protocol"
)

// TerminalError 表示协议适配器已从上游响应解析出明确的失败或取消终态。
// 它和超时、断线不同：上游已确认任务结束，调用日志与账单都按失败收尾，不进入待核对。
// 服务层用别名保持既有调用点，稳定错误事实归 generation 所有。
type TerminalError struct {
	TaskID  string
	Status  protocol.Status
	Message string
}

func (e TerminalError) Error() string {
	if e.TaskID == "" {
		return e.Message
	}
	return fmt.Sprintf("声明式协议任务失败（任务 %s）：%s", e.TaskID, e.Message)
}
