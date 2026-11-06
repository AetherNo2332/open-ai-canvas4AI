package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	as "yingce/backend/internal/agentservice"
	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/model"
	"yingce/backend/internal/workbench"
)

// ecommerceAgentAdapter submits workbench generations through the existing
// workbench bridge, keyed by the Session's batch so a replayed call returns
// the same task. The batch record is durable, so the same business space keeps
// its product material, preset and tasks across restarts.
type ecommerceAgentAdapter struct{ s *Service }

func (ecommerceAgentAdapter) Application() string  { return as.ApplicationEcommerce }
func (ecommerceAgentAdapter) SpaceTypes() []string { return []string{as.SpaceWorkbenchBatch} }

func (a ecommerceAgentAdapter) ValidateSpace(_ context.Context, ownerID string, scope as.Scope) error {
	return a.s.validateBuiltinAgentSpace(ownerID, scope)
}

func (ecommerceAgentAdapter) Tools() []as.ToolSpec {
	return []as.ToolSpec{
		{Name: "ecommerce_generate_image", Description: "在当前电商批次里生成一张商品图", Charges: true,
			Parameters: json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string"},"channelId":{"type":"string"},"model":{"type":"string"},"parameters":{"type":"object"},"index":{"type":"integer"},"count":{"type":"integer"}},"required":["prompt","index","count"]}`)},
		{Name: "ecommerce_save_batch", Description: "保存当前批次的商品资料与选用预设，跨页面与服务重启可找回",
			Parameters: json.RawMessage(`{"type":"object","properties":{"product":{"type":"string"},"preset":{"type":"string"}},"required":["product"]}`)},
		{Name: "ecommerce_batch_status", Description: "读取当前批次的资料、任务与结果状态",
			Parameters: json.RawMessage(`{"type":"object","properties":{}}`)},
		{Name: "ecommerce_retry_failed", Description: "重试当前批次里失败的任务，只重试失败项", Charges: true,
			Parameters: json.RawMessage(`{"type":"object","properties":{}}`)},
	}
}

func (a ecommerceAgentAdapter) Execute(_ context.Context, call as.ToolInvocation) (as.ToolResult, error) {
	switch call.Name {
	case "ecommerce_generate_image":
		return a.generateImage(call)
	case "ecommerce_save_batch":
		return a.saveBatch(call)
	case "ecommerce_batch_status":
		return a.batchStatus(call)
	case "ecommerce_retry_failed":
		return a.retryFailed(call)
	}
	return as.ToolResult{}, as.NewError(as.CodeForbidden, "工具不存在")
}

func (a ecommerceAgentAdapter) generateImage(call as.ToolInvocation) (as.ToolResult, error) {
	var args struct {
		Prompt     string         `json:"prompt"`
		ChannelID  string         `json:"channelId"`
		Model      string         `json:"model"`
		Parameters map[string]any `json:"parameters"`
		Index      int            `json:"index"`
		Count      int            `json:"count"`
	}
	if err := json.Unmarshal(call.Arguments, &args); err != nil || strings.TrimSpace(args.Prompt) == "" {
		return as.ToolResult{}, as.NewError(as.CodeInvalidRequest, "参数无效")
	}
	req := WorkbenchTaskInput{Kind: WorkbenchKindImage, Prompt: args.Prompt, Model: args.Model, ChannelID: args.ChannelID, Parameters: args.Parameters,
		Batch: &workbench.TaskBatch{ID: call.Scope.SpaceID, Index: args.Index, Count: args.Count}}
	// 报价达到工作授权阈值时，提交前先等用户批准；批准消费绑定本操作，重试幂等。
	gate := func(quote int64) error {
		return a.s.gateAgentToolApproval(call, quote, workbenchTargetVersion(req))
	}
	task, err := a.s.createWorkbenchTaskUnderApproval(call.OwnerID, req, call.BudgetGrantID, gate)
	if err != nil {
		return as.ToolResult{}, err
	}
	if err := a.s.repo.AppendAgentEcommerceBatchTask(call.OwnerID, call.Scope.SpaceID, task.ID); err != nil {
		return as.ToolResult{}, err
	}
	return as.ToolResult{Text: "已提交生成任务，状态 " + string(task.Status), Operation: "task:" + task.ID}, nil
}

// workbenchBatchTasks returns the tasks recorded under one durable batch.
func (s *Service) workbenchBatchTasks(ownerID, batchID string) ([]model.Task, error) {
	record, err := s.repo.AgentEcommerceBatch(ownerID, batchID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return []model.Task{}, nil
	}
	if err != nil {
		return nil, err
	}
	refs := []string{}
	if record.TaskRefsJSON != "" {
		if err := json.Unmarshal([]byte(record.TaskRefsJSON), &refs); err != nil {
			return nil, err
		}
	}
	tasks := []model.Task{}
	for _, taskID := range refs {
		task, err := s.repo.TaskForUser(ownerID, taskID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, *task)
	}
	return tasks, nil
}

// workbenchTargetVersion names the exact batch slot a workbench submission
// targets; an approval for one slot never covers another.
func workbenchTargetVersion(req WorkbenchTaskInput) string {
	if req.Batch == nil {
		return ""
	}
	return fmt.Sprintf("batch:%s:%d", req.Batch.ID, req.Batch.Index)
}

func (a ecommerceAgentAdapter) saveBatch(call as.ToolInvocation) (as.ToolResult, error) {
	var args struct {
		Product string `json:"product"`
		Preset  string `json:"preset"`
	}
	if err := json.Unmarshal(call.Arguments, &args); err != nil || strings.TrimSpace(args.Product) == "" {
		return as.ToolResult{}, as.NewError(as.CodeInvalidRequest, "需要商品资料")
	}
	record := &model.AgentEcommerceBatch{ID: newID(), OwnerID: call.OwnerID, BatchID: call.Scope.SpaceID, ProductJSON: strings.TrimSpace(args.Product), PresetJSON: args.Preset}
	if err := a.s.repo.SaveAgentEcommerceBatch(record); err != nil {
		return as.ToolResult{}, err
	}
	return as.ToolResult{Text: fmt.Sprintf("批次 %s 的商品资料已保存，跨页面与服务重启可找回。", call.Scope.SpaceID), Operation: record.ID}, nil
}

func (a ecommerceAgentAdapter) batchStatus(call as.ToolInvocation) (as.ToolResult, error) {
	record, err := a.s.repo.AgentEcommerceBatch(call.OwnerID, call.Scope.SpaceID)
	found := "（未保存持久资料）"
	if err == nil {
		found = "商品资料：" + record.ProductJSON
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return as.ToolResult{}, err
	}
	tasks, err := a.s.workbenchBatchTasks(call.OwnerID, call.Scope.SpaceID)
	if err != nil {
		return as.ToolResult{}, err
	}
	lines := []string{fmt.Sprintf("批次 %s：", call.Scope.SpaceID), found}
	for _, task := range tasks {
		lines = append(lines, fmt.Sprintf("任务 %s（%s）", task.ID, task.Status))
	}
	return as.ToolResult{Text: strings.Join(lines, "\n")}, nil
}

func (a ecommerceAgentAdapter) retryFailed(call as.ToolInvocation) (as.ToolResult, error) {
	tasks, err := a.s.workbenchBatchTasks(call.OwnerID, call.Scope.SpaceID)
	if err != nil {
		return as.ToolResult{}, err
	}
	caller := cp.Caller{UserID: call.OwnerID}
	retried := []string{}
	for _, task := range tasks {
		if task.Status != model.TaskStatusFailed {
			continue
		}
		snapshot, retryErr := a.s.CreationPlatform().Retry(context.Background(), caller, task.ID)
		if retryErr != nil {
			return as.ToolResult{}, retryErr
		}
		retried = append(retried, fmt.Sprintf("%s→%s", task.ID, snapshot.TaskID))
	}
	if len(retried) == 0 {
		return as.ToolResult{Text: "当前批次没有失败的任务。"}, nil
	}
	return as.ToolResult{Text: "已重试失败项：" + strings.Join(retried, "、")}, nil
}
