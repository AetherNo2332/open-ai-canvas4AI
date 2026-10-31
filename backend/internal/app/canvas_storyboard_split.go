package app

import (
	"context"
	"strings"
	"time"

	"yingce/backend/internal/canvas/commands"
	"yingce/backend/internal/canvas/tasks"
	"yingce/backend/internal/identity"
	"yingce/backend/internal/model"
)

// 分镜拆镜脚本的统一入口：画布按钮和 Agent 都从这里提交。服务端从画布组装提示词、冻结重拆范围，
// 任务结束后由画布任务写回（commands.ApplyStoryboardSplit），调用方不直接写分镜行。

// CanvasStoryboardSplitRequest 是调用方能给的全部输入；剧情、风格、角色与衔接镜头都由服务端读取。
type CanvasStoryboardSplitRequest struct {
	NodeID              string         `json:"nodeId"`
	RowIDs              []string       `json:"rowIds,omitempty"`
	Request             string         `json:"request,omitempty"`
	ShotCount           int            `json:"shotCount,omitempty"`
	ShotDurationSeconds int            `json:"shotDurationSeconds,omitempty"`
	SkillContext        string         `json:"skillContext,omitempty"`
	LogicalModelID      string         `json:"logicalModelId,omitempty"`
	Config              map[string]any `json:"config,omitempty"`
	// QuoteOnly 只组装提示词并报价，不提交任务、不改画布；确认费用后再以同样参数正式提交。
	QuoteOnly   bool   `json:"quoteOnly,omitempty"`
	OperationID string `json:"operationId,omitempty"`
	ClientID    string `json:"clientId,omitempty"`
}

// CanvasStoryboardSplitResult 是提交（或报价）的回执。
type CanvasStoryboardSplitResult struct {
	TaskID  string        `json:"taskId,omitempty"`
	Quote   CreationQuote `json:"quote"`
	RowIDs  []string      `json:"rowIds,omitempty"`
	Prompt  string        `json:"-"`
	Partial bool          `json:"partial"`
}

func (s *Service) SplitCanvasStoryboard(userID, canvasID string, req CanvasStoryboardSplitRequest) (*CanvasStoryboardSplitResult, error) {
	if strings.TrimSpace(req.NodeID) == "" {
		return nil, BadAuthRequest("缺少分镜节点")
	}
	if req.ShotCount < 0 || req.ShotCount > 100 || req.ShotDurationSeconds < 0 || req.ShotDurationSeconds > 60 {
		return nil, BadAuthRequest("镜头数须在 0-100、单镜时长须在 0-60 秒之间")
	}
	if len(req.RowIDs) > 100 {
		return nil, BadAuthRequest("重拆范围最多 100 个镜头")
	}
	cs, ts := s.CanvasCommandServices()
	clientID := strings.TrimSpace(req.ClientID)
	if clientID == "" {
		clientID = "storyboard-split"
	}
	principal := identity.Principal{ID: userID, OwnerID: userID, Role: "owner", ClientID: clientID, Scopes: identity.OwnerScopes()}
	ctx := context.Background()
	snapshot, err := cs.ReadSnapshot(ctx, principal, canvasID)
	if err != nil {
		return nil, err
	}
	scope, err := commands.PlanStoryboardSplitScope(snapshot.Document, req.NodeID, req.RowIDs)
	if err != nil {
		return nil, err
	}
	prompt, err := commands.BuildStoryboardSplitPrompt(snapshot.Document, req.NodeID, scope, commands.StoryboardSplitPromptInput{
		Request: req.Request, ShotCount: req.ShotCount, ShotDurationSeconds: req.ShotDurationSeconds, SkillContext: req.SkillContext,
	})
	if err != nil {
		return nil, err
	}
	config := map[string]any{}
	for key, value := range req.Config {
		config[key] = value
	}
	input := map[string]any{
		"mode": "text", "prompt": prompt, "config": config,
		"metadata": map[string]any{"nodeId": req.NodeID, "storyboardSplit": map[string]any{
			"request": req.Request, "shotCount": float64(req.ShotCount), "shotDurationSeconds": float64(req.ShotDurationSeconds), "skillContext": req.SkillContext,
		}},
	}
	result := &CanvasStoryboardSplitResult{RowIDs: scope.RowIDs, Prompt: prompt, Partial: len(scope.RowIDs) > 0}
	if req.QuoteOnly {
		task, order, signature, err := s.prepareCreationTask(userID, CreateTaskRequest{ProjectID: canvasID, Type: "canvas_text", Operation: "storyboard", Prompt: prompt, LogicalModelID: req.LogicalModelID, Input: input})
		if err != nil {
			return nil, err
		}
		result.Quote = creationQuoteFor(task, order, signature, time.Now().Add(5*time.Minute))
		return result, nil
	}
	operationID := strings.TrimSpace(req.OperationID)
	if operationID == "" {
		operationID = "storyboard-split:" + newID()
	}
	handle, err := ts.Submit(ctx, principal, tasks.SubmitRequest{
		Command:      commands.Command{Protocol: commands.ProtocolVersion, CanvasID: canvasID, DocumentEpoch: snapshot.DocumentEpoch, ClientID: clientID, OperationID: operationID, BaseRevision: snapshot.Revision},
		TargetNodeID: req.NodeID,
		Task:         tasks.TaskRequest{Mode: "text", Type: "canvas_text", Operation: "storyboard", Prompt: prompt, LogicalModelID: req.LogicalModelID, Input: input},
		Consumer:     tasks.Consumer{Kind: "storyboard_document", RowIDs: scope.RowIDs},
	})
	if err != nil {
		return nil, err
	}
	result.TaskID = handle.TaskID
	if task, readErr := s.repo.TaskForUser(userID, handle.TaskID); readErr == nil {
		var order *model.BillingOrder
		if task.BillingOrderID != "" {
			order, _ = s.repo.BillingOrder(task.BillingOrderID)
		}
		result.Quote = creationQuoteFor(task, order, "", time.Now())
	}
	return result, nil
}
