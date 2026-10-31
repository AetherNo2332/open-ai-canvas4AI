package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"yingce/backend/internal/kernel"

	"yingce/backend/internal/canvas/commands"
	"yingce/backend/internal/canvas/tasks"
	"yingce/backend/internal/identity"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
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
	QuoteHash   string `json:"quoteHash,omitempty"`
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

func (s *Service) SplitCanvasStoryboard(userID, canvasID string, req CanvasStoryboardSplitRequest) (result *CanvasStoryboardSplitResult, err error) {
	defer func() {
		var command *commands.Error
		if errors.As(err, &command) {
			status := 409
			if command.Reason == "invalid_operation" {
				status = 400
			}
			if command.Reason == "permission_denied" {
				status = 403
			}
			if command.Reason == "entity_deleted" {
				status = 404
			}
			err = &AppError{Status: status, Code: status, Reason: kernel.ErrorReason(command.Reason), Message: command.Message}
		}
	}()
	if !req.QuoteOnly && req.OperationID != "" {
		if replay, found, err := s.storyboardSplitReplay(userID, canvasID, req); found || err != nil {
			return replay, err
		}
	}
	plan, err := s.prepareCanvasStoryboardSplit(userID, canvasID, req, nil)
	if err != nil {
		return nil, err
	}
	if req.QuoteOnly {
		return &plan.Result, nil
	}
	if req.QuoteHash == "" {
		return nil, creationConflict("请先确认拆镜报价")
	}
	if err := s.verifyStoryboardQuote(req.QuoteHash, plan.quoteKey); err != nil {
		return nil, err
	}
	handle, err := plan.submit(s.repo, s)
	if err != nil {
		return nil, creationError(err)
	}
	plan.Result.TaskID = handle.TaskID
	s.wakeTaskDispatcher()
	return &plan.Result, nil
}

type canvasStoryboardSplitPlan struct {
	Principal  identity.Principal
	Submission tasks.SubmitRequest
	Prepared   tasks.PreparedTask
	Result     CanvasStoryboardSplitResult
	Signature  string
	quoteKey   string
}

func (s *Service) prepareCanvasStoryboardSplit(userID, canvasID string, req CanvasStoryboardSplitRequest, admission *taskAdmission) (*canvasStoryboardSplitPlan, error) {
	if strings.TrimSpace(req.NodeID) == "" {
		return nil, BadAuthRequest("缺少分镜节点")
	}
	if req.ShotCount < 0 || req.ShotCount > 100 || req.ShotDurationSeconds < 0 || req.ShotDurationSeconds > 60 || len(req.RowIDs) > 100 || len([]rune(req.SkillContext)) > 20000 {
		return nil, BadAuthRequest("拆镜参数超出范围")
	}
	clientID := strings.TrimSpace(req.ClientID)
	if clientID == "" {
		clientID = "storyboard-split"
	}
	principal := identity.Principal{ID: userID, OwnerID: userID, Role: "owner", ClientID: clientID, Scopes: identity.OwnerScopes()}
	cs, _ := s.CanvasCommandServices()
	snapshot, err := cs.ReadSnapshot(context.Background(), principal, canvasID)
	if err != nil {
		return nil, err
	}
	scope, err := commands.PlanStoryboardSplitScope(snapshot.Document, req.NodeID, req.RowIDs)
	if err != nil {
		return nil, err
	}
	prompt, err := commands.BuildStoryboardSplitPrompt(snapshot.Document, req.NodeID, scope, commands.StoryboardSplitPromptInput{Request: req.Request, ShotCount: req.ShotCount, ShotDurationSeconds: req.ShotDurationSeconds, SkillContext: req.SkillContext})
	if err != nil {
		return nil, err
	}
	config := creationSafeConfig(req.Config)
	input := map[string]any{"mode": "text", "prompt": prompt, "config": config, "metadata": map[string]any{"nodeId": req.NodeID, "storyboardSplit": map[string]any{"request": req.Request, "shotCount": float64(req.ShotCount), "shotDurationSeconds": float64(req.ShotDurationSeconds), "skillContext": req.SkillContext}}}
	operationID := strings.TrimSpace(req.OperationID)
	if operationID == "" {
		operationID = "storyboard-split:" + newID()
	}
	plan := &canvasStoryboardSplitPlan{Principal: principal, Submission: tasks.SubmitRequest{
		Command:      commands.Command{Protocol: commands.ProtocolVersion, CanvasID: canvasID, DocumentEpoch: snapshot.DocumentEpoch, ClientID: clientID, OperationID: operationID, BaseRevision: snapshot.Revision},
		TargetNodeID: req.NodeID, Task: tasks.TaskRequest{Mode: "text", Type: "canvas_text", Operation: "storyboard", Prompt: prompt, LogicalModelID: req.LogicalModelID, Input: input},
		Consumer: tasks.Consumer{Kind: "storyboard_document", RowIDs: scope.RowIDs},
	}, Result: CanvasStoryboardSplitResult{RowIDs: scope.RowIDs, Prompt: prompt, Partial: len(scope.RowIDs) > 0}}
	// Admission can initialize the default user group. Freeze it outside the
	// canvas transaction so SQLite never opens a second write under ExecuteHost.
	frozenInput, err := normalizeTaskInput(input)
	if err != nil {
		return nil, err
	}
	meta := frozenInput["metadata"].(map[string]any)
	delete(meta, "storyboardSplit")
	meta["canvasConsumer"], meta["canvasId"], meta["documentEpoch"] = "storyboard_document", canvasID, snapshot.DocumentEpoch
	meta["storyboardRequestHash"] = storyboardSplitRequestHash(req)
	frozenRequest := plan.Submission.Task
	frozenRequest.Input = frozenInput
	plan.Prepared, err = (&canvasTaskSubmitter{s: s, admission: admission}).Prepare(context.Background(), principal, frozenRequest)
	if err != nil {
		return nil, err
	}
	plan.Prepared.Task.ProjectID = canvasID
	var normalized map[string]any
	if err := json.Unmarshal([]byte(plan.Prepared.Task.InputJSON), &normalized); err != nil {
		return nil, err
	}
	resolved, _ := normalized["config"].(map[string]any)
	plan.Signature, err = s.repo.CreationPriceSignature(&plan.Prepared.Task, stringValue(resolved["channelId"]), stringValue(resolved["model"]))
	if err != nil {
		return nil, err
	}
	plan.Result.Quote = creationQuoteFor(&plan.Prepared.Task, plan.Prepared.BillingOrder, plan.Signature, time.Now().Add(5*time.Minute))
	_, quotedBoard, _, err := storyboardNodeFromDocument(map[string]any(snapshot.Document), req.NodeID)
	if err != nil {
		return nil, err
	}
	plan.quoteKey = creationHash([]any{quotedBoard, userID, canvasID, snapshot.DocumentEpoch, req.NodeID, scope.RowIDs, prompt, config, req.LogicalModelID, plan.Result.Quote.QuoteHash})
	plan.Result.Quote.QuoteHash, err = s.signStoryboardQuote(plan.quoteKey, plan.Result.Quote.ExpiresAt)
	return plan, err
}

type frozenStoryboardSubmitter struct {
	service *Service
	plan    *canvasStoryboardSplitPlan
}

func (d *frozenStoryboardSubmitter) Prepare(_ context.Context, _ identity.Principal, req tasks.TaskRequest) (tasks.PreparedTask, error) {
	if req.Prompt != d.plan.Prepared.Task.Prompt {
		return tasks.PreparedTask{}, creationConflict("拆镜上下文已变化，请重新确认费用")
	}
	return d.plan.Prepared, nil
}
func (d *frozenStoryboardSubmitter) Commit(ctx context.Context, tx *repository.Repository, prepared tasks.PreparedTask) error {
	if err := checkCreationPriceSignature(tx, &prepared.Task, d.plan.Signature); err != nil {
		return err
	}
	return (&canvasTaskSubmitter{s: d.service}).Commit(ctx, tx, prepared)
}
func (d *frozenStoryboardSubmitter) Wake() { /* The enclosing transaction owns the wake. */ }
func (p *canvasStoryboardSplitPlan) submit(repo *repository.Repository, s *Service) (tasks.TaskHandle, error) {
	cs := commands.NewService(repo, commands.ServiceOptions{Validation: s.CanvasCommandValidation})
	ts := tasks.NewService(repo, cs, &frozenStoryboardSubmitter{service: s, plan: p}, s.CanvasTaskOptions())
	return ts.Submit(context.Background(), p.Principal, p.Submission)
}

type storyboardQuoteClaims struct {
	Key     string `json:"key"`
	Expires int64  `json:"expires"`
}

func (s *Service) storyboardQuoteMAC(payload string) (string, error) {
	key, err := s.settingsEncryptionKey()
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("storyboard-quote\n" + payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}
func (s *Service) signStoryboardQuote(key string, expires time.Time) (string, error) {
	raw, err := json.Marshal(storyboardQuoteClaims{Key: key, Expires: expires.Unix()})
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	signature, err := s.storyboardQuoteMAC(payload)
	return payload + "." + signature, err
}
func (s *Service) verifyStoryboardQuote(token, key string) error {
	invalid := creationConflict("拆镜报价已变化或过期，请重新确认费用")
	payload, signature, ok := strings.Cut(token, ".")
	if !ok {
		return invalid
	}
	expected, err := s.storyboardQuoteMAC(payload)
	if err != nil {
		return err
	}
	if !hmac.Equal([]byte(signature), []byte(expected)) {
		return invalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return invalid
	}
	var claims storyboardQuoteClaims
	if json.Unmarshal(raw, &claims) != nil || claims.Key != key || time.Now().Unix() >= claims.Expires {
		return invalid
	}
	return nil
}

func storyboardSplitRequestHash(req CanvasStoryboardSplitRequest) string {
	return creationHash([]any{req.NodeID, req.RowIDs, req.Request, req.ShotCount, req.ShotDurationSeconds, req.SkillContext, req.LogicalModelID, creationSafeConfig(req.Config)})
}
func (s *Service) storyboardSplitReplay(userID, canvasID string, req CanvasStoryboardSplitRequest) (*CanvasStoryboardSplitResult, bool, error) {
	client := strings.TrimSpace(req.ClientID)
	if client == "" {
		client = "storyboard-split"
	}
	principal := identity.Principal{ID: userID, OwnerID: userID, Role: "owner", ClientID: client, Scopes: identity.OwnerScopes()}
	cs, _ := s.CanvasCommandServices()
	snapshot, err := cs.ReadSnapshot(context.Background(), principal, canvasID)
	if err != nil {
		return nil, false, err
	}
	binding, err := s.repo.CanvasTaskBindingForOperation(userID, canvasID, snapshot.DocumentEpoch, userID, client, req.OperationID)
	if s.repo.IsMissing(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	task, err := s.repo.TaskForUser(userID, binding.TaskID)
	if err != nil {
		return nil, false, err
	}
	var input map[string]any
	if err := json.Unmarshal([]byte(task.InputJSON), &input); err != nil {
		return nil, false, err
	}
	meta, _ := input["metadata"].(map[string]any)
	if meta["storyboardRequestHash"] != storyboardSplitRequestHash(req) {
		return nil, false, creationConflict("同一拆镜操作标识不能用于不同请求")
	}
	var order *model.BillingOrder
	if task.BillingOrderID != "" {
		order, err = s.repo.BillingOrder(task.BillingOrderID)
		if err != nil {
			return nil, false, err
		}
	}
	return &CanvasStoryboardSplitResult{TaskID: task.ID, RowIDs: req.RowIDs, Partial: len(req.RowIDs) > 0, Quote: creationQuoteFor(task, order, "", time.Now())}, true, nil
}
