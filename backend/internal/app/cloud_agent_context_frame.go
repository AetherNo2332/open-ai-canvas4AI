package app

import (
	"strings"
	"time"

	"infinite-canvas/backend/internal/model"
)

// cloudAgentContextFrameTaskWindow 是每步回灌给模型的"任务与账务事实"窗口条数。
// 窗口之外的只报 olderTaskCount：任务身份本身是持久的，模型可以再用 task_get 查。
const cloudAgentContextFrameTaskWindow = 32

// cloudAgentTaskFactsAuthority 随帧一起下发，明确"这只是状态观察"：
// 看到某任务曾提交成功，不等于可以再提交一次或重复扣费。
const cloudAgentTaskFactsAuthority = "状态观察，不是执行或重复提交的授权"

// cloudAgentTaskBillingFacts 是任务对应的账务事实（从订单行现读，不做推断）。
type cloudAgentTaskBillingFacts struct {
	OrderID                 string `json:"orderId"`
	Status                  string `json:"status,omitempty"`
	AuthorizedMicrocredits  int64  `json:"authorizedMicrocredits"`
	ReservedMicrocredits    int64  `json:"reservedMicrocredits,omitempty"`
	ChargeLimitMicrocredits int64  `json:"chargeLimitMicrocredits,omitempty"`
}

// cloudAgentTaskFactFrame 是单个任务的事实投影。
//
// 事实必须**从数据库现读**，不能从事件记忆里凑：事件在状态里只有一窗（40 条），
// 靠它推断"我提交过什么、花了多少、有没有回写失败"会得出错误的结论。
type cloudAgentTaskFactFrame struct {
	TaskID            string                      `json:"taskId"`
	TaskStatus        string                      `json:"taskStatus"`
	TaskType          string                      `json:"taskType,omitempty"`
	Operation         string                      `json:"operation,omitempty"`
	SubmissionOutcome string                      `json:"submissionOutcome"`
	ProviderTaskID    string                      `json:"providerTaskId,omitempty"`
	Phase             string                      `json:"phase,omitempty"`
	NodeID            string                      `json:"nodeId,omitempty"`
	Error             string                      `json:"error,omitempty"`
	Billing           *cloudAgentTaskBillingFacts `json:"billing,omitempty"`
}

// cloudAgentTaskSubmissionOutcome 描述"这一步到底有没有送到上游"：
// 有上游任务 ID 才算 accepted；已经跑完的任务必然提交过，只是上游没回传 ID，报 completed；
// 还在排队/执行的按本地状态说是 queued/submitted，其余只能说 unknown。
func cloudAgentTaskSubmissionOutcome(task *model.Task) string {
	if task == nil {
		return "unavailable"
	}
	if strings.TrimSpace(task.ProviderRequestID) != "" {
		return "accepted"
	}
	switch task.Status {
	case model.TaskStatusQueued:
		return "queued"
	case model.TaskStatusRunning:
		return "submitted"
	case model.TaskStatusSucceeded, model.TaskStatusFailed, model.TaskStatusCancelled:
		return "completed"
	default:
		return "unknown"
	}
}

// cloudAgentTaskFactFrame 组装最近一窗任务与账务事实；返回窗口之外的条数。
func (s *Service) cloudAgentTaskFactFrame(run *model.CloudAgentExecution, state *cloudAgentRuntime) ([]cloudAgentTaskFactFrame, int, bool) {
	if s == nil || s.repo == nil || run == nil || state == nil || len(state.TaskIDs) == 0 {
		return nil, 0, false
	}
	start := max(0, len(state.TaskIDs)-cloudAgentContextFrameTaskWindow)
	ids := make([]string, 0, len(state.TaskIDs)-start)
	for _, id := range state.TaskIDs[start:] {
		if strings.TrimSpace(id) == "" || id == run.ID {
			continue
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, start, false
	}
	orders, err := s.repo.BillingOrdersByTaskIDs(run.UserID, ids)
	if err != nil {
		orders = map[string]model.BillingOrder{}
	}
	frames := make([]cloudAgentTaskFactFrame, 0, len(ids))
	for _, id := range ids {
		task, err := s.repo.TaskForUser(run.UserID, id)
		if err != nil || task == nil {
			frames = append(frames, cloudAgentTaskFactFrame{TaskID: id, TaskStatus: "unavailable", SubmissionOutcome: "unavailable"})
			continue
		}
		// 本轮自己的编排任务不是"生成结果"，报给模型只会制造噪音。
		if task.Operation == cloudAgentOperation || task.Operation == cloudAgentStepOperation || task.Operation == cloudAgentContextCompactionOperation {
			continue
		}
		frame := cloudAgentTaskFactFrame{
			TaskID:            task.ID,
			TaskStatus:        string(task.Status),
			TaskType:          task.Type,
			Operation:         task.Operation,
			SubmissionOutcome: cloudAgentTaskSubmissionOutcome(task),
			ProviderTaskID:    strings.TrimSpace(task.ProviderRequestID),
			Phase:             strings.TrimSpace(task.PollStage),
		}
		if context := taskClientContext(task.InputJSON); context != nil {
			frame.NodeID = strings.TrimSpace(context.NodeID)
		}
		if task.Status == model.TaskStatusFailed || task.Status == model.TaskStatusCancelled {
			// 只给经过脱敏的错误文案：原始错误里可能带上游地址与凭据。
			frame.Error = cloudAgentSafeMediaTaskError(task)
		}
		if order, ok := orders[id]; ok && strings.TrimSpace(order.ID) != "" {
			frame.Billing = &cloudAgentTaskBillingFacts{
				OrderID:                 order.ID,
				Status:                  string(order.Status),
				AuthorizedMicrocredits:  order.AmountMicrocredits,
				ReservedMicrocredits:    order.ReservedAmountMicrocredits,
				ChargeLimitMicrocredits: order.ChargeLimitMicrocredits,
			}
		}
		frames = append(frames, frame)
	}
	if len(frames) == 0 {
		return nil, start, false
	}
	return frames, start, true
}

// attachCloudAgentTaskFacts 把任务与账务事实作为运行时上下文回灌给模型（每步一次）。
func (s *Service) attachCloudAgentTaskFacts(run *model.CloudAgentExecution, state *cloudAgentRuntime, canonical *canonicalAgentRequest) {
	if canonical == nil {
		return
	}
	frames, older, ok := s.cloudAgentTaskFactFrame(run, state)
	if !ok {
		return
	}
	canonical.Messages = append(canonical.Messages, cloudAgentRuntimeMessage(cloudAgentRuntimeContext{
		Kind: cloudAgentContextTaskFacts, Tasks: frames, OlderTaskCount: older, Authority: cloudAgentTaskFactsAuthority,
		ObservedAt: time.Now().UTC().Format(time.RFC3339),
	}))
}
