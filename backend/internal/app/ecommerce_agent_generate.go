package app

import (
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"strings"
	"time"
	"unicode"
	"yingce/backend/internal/ecommerceagent"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func (s *Service) GenerateEcommerceAgent(ctx context.Context, userID string, req ecommerceagent.Request, emit func(ecommerceagent.Event) error) (view *EcommerceAgentTaskView, err error) {
	key := strings.TrimSpace(req.IdempotencyKey)
	if replay, found, replayErr := s.replayExistingEcommerceGeneration(userID, key, emit); found {
		return replay, replayErr
	}
	if key == "" {
		key = newID()
	}
	p, cfg, err := s.prepareEcommerceGeneration(userID, req)
	if err != nil {
		return nil, err
	}
	analysis, err := s.AnalyzeEcommerceAgent(ctx, userID, req)
	if err != nil {
		return nil, err
	}
	selected, err := selectEcommerceRecommendation(analysis, req.SelectedRecommendationID)
	if err != nil {
		return nil, err
	}
	pages := ecommerceagent.Plan(req.ImageCount, req.GenerateType, p, selected, analysis.Brief)
	ecommerceagent.ApplyTextPreference(pages, req.NeedText, req.Language)
	runID := newID()
	projectID := req.ProjectID
	var project *model.EcommerceProject
	if projectID == "" {
		projectID = newID()
		project = &model.EcommerceProject{ID: projectID, UserID: userID, Name: analysis.Brief.Category, Status: "draft", DraftJSON: "{}", PlanJSON: "{}", Revision: 1}
	}
	order, unit, err := s.ecommerceAgentOrder(userID, runID, key, req, cfg, len(pages))
	if err != nil {
		return nil, err
	}
	if err := s.applyEcommercePricedModel(&cfg, order); err != nil {
		return nil, err
	}
	run := &model.EcommerceAgentRun{ID: runID, UserID: userID, IdempotencyKey: key, ProjectID: projectID, BillingOrderID: order.ID, Status: "running", Mode: cfg.Mode, Platform: req.Platform, Language: req.Language, GenerateType: req.GenerateType, PageCount: len(pages), UnitMicrocredits: unit}
	if err = s.repo.CreateEcommerceAgentRun(run, project, order); err != nil {
		if prior, e := s.repo.EcommerceAgentRunByKey(userID, key); e == nil {
			return s.replayEcommerceAgent(userID, prior, emit)
		}
		if errors.Is(err, repository.ErrInsufficientCredits) {
			return nil, ecommerceErr("INSUFFICIENT_CREDITS", "积分不足")
		}
		return nil, err
	}
	if err = s.MarkBillingRunning(order.ID); err != nil {
		_, _ = s.repo.FinishEcommerceAgentRun(userID, runID, "failed", "UPSTREAM_ERROR")
		return nil, err
	}
	workCtx, cancel := context.WithTimeout(ctx, cfg.GenerateTimeout)
	s.registerActiveTask(runID, cancel)
	defer func() { cancel(); s.unregisterActiveTask(runID) }()
	go s.pollEcommerceCancellation(workCtx, userID, runID, cancel)
	finished := false
	defer func() {
		if !finished {
			s.finishAbortedEcommerceGeneration(workCtx, userID, runID)
		}
	}()
	if err := emitEcommerceGenerationStarted(emit, run, order, analysis.Brief); err != nil {
		return nil, err
	}
	copyResult := map[string]any{"oneLinePitch": analysis.Brief.Category, "sellingPoints": []any{}, "complianceNotes": []string{"仅使用已确认的商品事实；生成文案需人工核对"}}
	s.improveEcommerceGenerationPlan(workCtx, cfg, runID, p, req, analysis, copyResult, pages)
	answer := ecommerceRecommendationMarkup(analysis.Recommendations)
	if err = emit(ecommerceagent.Event{Event: "node_finished", Data: map[string]any{"step": map[string]string{"name": "plan_done", "label": "分镜完成"}, "outputs": map[string]any{"answer": answer, "options": analysis.Recommendations}}}); err != nil {
		return nil, err
	}
	reference, mime, err := s.ecommerceGenerationReference(userID, cfg, req)
	if err != nil {
		return nil, err
	}
	generator := ecommercePageGenerator{svc: s, config: cfg, userID: userID, runID: runID, platform: p, brand: req.Brand, brief: analysis.Brief, selected: selected, unit: unit, reference: reference, mime: mime, emit: emit}
	if err = generator.run(workCtx, pages); err != nil {
		return nil, err
	}
	canvas := ecommerceagent.BuildCanvas(pages, p)
	run, err = s.persistEcommerceGeneration(run, analysis.Brief, copyResult, canvas)
	if err != nil {
		return nil, err
	}
	finished = true
	view, err = s.EcommerceAgentTask(userID, runID)
	if err != nil {
		return nil, err
	}
	if err = emit(ecommerceagent.Event{Event: "node_finished", Data: map[string]any{"outputs": map[string]any{"answer": "出图完成", "files": pages}, "posterCanvas": canvas, "brief": analysis.Brief, "copy": copyResult, "pages": pages, "credit": map[string]any{"charged": float64(run.UnitMicrocredits*int64(run.SuccessPages)) / float64(CreditScale)}, "projectId": projectID}}); err != nil {
		return view, err
	}
	err = emit(ecommerceagent.Event{Event: "message", Data: map[string]any{"done": true}})
	return view, err
}

func (s *Service) ecommerceGenerationReference(userID string, cfg ecommerceProviderConfig, req ecommerceagent.Request) ([]byte, string, error) {
	if cfg.Mode != "live" {
		return nil, "", nil
	}
	_, reference, mime, err := s.ecommerceReference(userID, req.FileInfoList[0].URL)
	return reference, mime, err
}

func safeEcommerceImagePrompt(candidate string, req ecommerceagent.Request) bool {
	if len(candidate) < 40 || len(candidate) > 4000 || !strings.Contains(candidate, "same product as reference image, identical shape/color/logo placement") {
		return false
	}
	lower := strings.ToLower(candidate)
	for _, forbidden := range []string{"best seller", "no.1", "fda approved", "certified", "guaranteed", "price", "discount", "free shipping", "第一", "最好", "认证", "销量"} {
		if strings.Contains(lower, forbidden) {
			return false
		}
	}
	if strings.IndexFunc(candidate, unicode.IsDigit) >= 0 && strings.IndexFunc(req.Query+req.UserSellingPoints, unicode.IsDigit) < 0 {
		return false
	}
	return true
}

func (s *Service) replayEcommerceAgent(userID string, run *model.EcommerceAgentRun, emit func(ecommerceagent.Event) error) (*EcommerceAgentTaskView, error) {
	view, err := s.EcommerceAgentTask(userID, run.ID)
	if err != nil {
		return nil, err
	}
	if err = emit(ecommerceagent.Event{Event: "workflow_started", TaskID: run.ID, ConversationID: run.ProjectID, Data: map[string]any{"pageCount": run.PageCount, "charged": float64(run.UnitMicrocredits*int64(run.PageCount)) / float64(CreditScale), "mode": run.Mode, "replayed": true}}); err != nil {
		return nil, err
	}
	for _, page := range view.Pages {
		if err = emit(ecommerceagent.Event{Event: "iteration_completed", Data: map[string]any{"page": page, "progress": float64(page.Index) / float64(run.PageCount)}}); err != nil {
			return nil, err
		}
	}
	_ = emit(ecommerceagent.Event{Event: "message", Data: map[string]any{"done": run.Status != "running", "replayed": true, "status": run.Status}})
	return view, nil
}

func (s *Service) prepareEcommerceGeneration(userID string, req ecommerceagent.Request) (ecommerceagent.Platform, ecommerceProviderConfig, error) {
	if err := s.RequireFeature(FeatureEcommerce); err != nil {
		return ecommerceagent.Platform{}, ecommerceProviderConfig{}, err
	}
	if err := s.RequireFeature(FeatureImageWorkbench); err != nil {
		return ecommerceagent.Platform{}, ecommerceProviderConfig{}, err
	}
	p, err := validateEcommerceAgentRequest(req, true)
	if err != nil {
		return ecommerceagent.Platform{}, ecommerceProviderConfig{}, err
	}
	cfg := ecommerceConfig()
	cfg, err = s.ecommerceConfiguredText(userID, cfg)
	if err != nil {
		return ecommerceagent.Platform{}, ecommerceProviderConfig{}, err
	}
	if err = cfg.validate(); err != nil {
		return ecommerceagent.Platform{}, ecommerceProviderConfig{}, err
	}
	if cfg.Mode == "live" {
		cfg, err = s.ecommerceLiveImageConfig(req, cfg)
		if err != nil {
			return ecommerceagent.Platform{}, ecommerceProviderConfig{}, err
		}
	}
	return p, cfg, nil
}

func selectEcommerceRecommendation(analysis *ecommerceagent.Analysis, id string) (*ecommerceagent.Recommendation, error) {
	var selected *ecommerceagent.Recommendation
	if id != "" {
		for i := range analysis.Recommendations {
			if analysis.Recommendations[i].ID == id {
				selected = &analysis.Recommendations[i]
				break
			}
		}
		if selected == nil {
			return nil, ecommerceErr("INVALID_INPUT", "设计方向已失效，请重新选择")
		}
	}
	return selected, nil
}

func (s *Service) improveEcommerceGenerationPlan(ctx context.Context, cfg ecommerceProviderConfig, runID string, p ecommerceagent.Platform, req ecommerceagent.Request, analysis *ecommerceagent.Analysis, copyResult map[string]any, pages []ecommerceagent.Page) {
	if cfg.Mode == "live" {
		var proposedCopy map[string]any
		copyPrompt := ecommerceagent.BuildCopyPrompt(analysis.Brief, p, req.Language, req.UserSellingPoints)
		if e := s.ecommerceChatJSON(ctx, cfg, runID, ecommerceagent.SystemCopy, copyPrompt, nil, &proposedCopy); e == nil {
			// Keep the model's proposed copy outside the published canvas until a
			// seller verifies claims. The canvas only receives neutral headlines.
			copyResult["proposedDraft"] = proposedCopy
			copyResult["requiresReview"] = true
		}
		var proposed []ecommerceagent.Page
		planPrompt := ecommerceagent.BuildPlannerPrompt(copyResult, pages, p, req.Language, analysis.Brief)
		if e := s.ecommerceChatJSON(ctx, cfg, runID, ecommerceagent.SystemPlanner, planPrompt, nil, &proposed); e == nil && len(proposed) == len(pages) {
			for i := range pages {
				candidate := proposed[i].ImagePrompt
				if safeEcommerceImagePrompt(candidate, req) {
					pages[i].ImagePrompt = candidate
					pages[i].NegativePrompt = proposed[i].NegativePrompt
				}
			}
		}
	}
}

func (s *Service) applyEcommercePricedModel(cfg *ecommerceProviderConfig, order *model.BillingOrder) error {
	if cfg.Mode == "live" {
		pricedModel, loadErr := s.repo.ChannelModel(order.ChannelModelID)
		if loadErr != nil {
			return loadErr
		}
		for _, tier := range pricedModel.PriceTiers {
			if tier.ID == order.PriceTierID && tier.ProviderModelKey != "" {
				cfg.ImageModel = tier.ProviderModelKey
				break
			}
		}
		if cfg.ImageProvider != nil {
			cfg.ImageProvider.Model = cfg.ImageModel
		}
	}
	return nil
}

func (s *Service) pollEcommerceCancellation(workCtx context.Context, userID, runID string, cancel context.CancelFunc) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-workCtx.Done():
			return
		case <-ticker.C:
			run, pollErr := s.repo.EcommerceAgentRun(userID, runID)
			if pollErr == nil && run.CancelRequested {
				cancel()
				return
			}
		}
	}
}

func (s *Service) finishAbortedEcommerceGeneration(workCtx context.Context, userID, runID string) {
	status, code := "failed", "UPSTREAM_ERROR"
	if workCtx.Err() != nil {
		if errors.Is(workCtx.Err(), context.DeadlineExceeded) {
			status, code = "failed", "MODEL_TIMEOUT"
		} else {
			status, code = "cancelled", "CANCELLED"
		}
	}
	_, _ = s.repo.FinishEcommerceAgentRun(userID, runID, status, code)
}

func (s *Service) replayExistingEcommerceGeneration(userID, key string, emit func(ecommerceagent.Event) error) (*EcommerceAgentTaskView, bool, error) {
	if key == "" {
		return nil, false, nil
	}
	prior, err := s.repo.EcommerceAgentRunByKey(userID, key)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	view, err := s.replayEcommerceAgent(userID, prior, emit)
	return view, true, err
}

func emitEcommerceGenerationStarted(emit func(ecommerceagent.Event) error, run *model.EcommerceAgentRun, order *model.BillingOrder, brief ecommerceagent.Brief) error {
	var err error
	if err = emit(ecommerceagent.Event{Event: "workflow_started", TaskID: run.ID, ConversationID: run.ProjectID, Data: map[string]any{"pageCount": run.PageCount, "charged": float64(order.AmountMicrocredits) / float64(CreditScale), "mode": run.Mode}}); err != nil {
		return err
	}
	if err = emit(ecommerceagent.Event{Event: "node_finished", Data: map[string]any{"step": map[string]string{"name": "vision", "label": "分析商品图"}, "outputs": map[string]any{"answer": brief}}}); err != nil {
		return err
	}
	return nil
}

func ecommerceRecommendationMarkup(recommendations []ecommerceagent.Recommendation) string {
	answerParts := make([]string, 0, len(recommendations))
	for _, r := range recommendations {
		answerParts = append(answerParts, "<agent-option>"+r.Label+"</agent-option>")
	}
	return strings.Join(answerParts, " ")
}

func (s *Service) persistEcommerceGeneration(run *model.EcommerceAgentRun, brief ecommerceagent.Brief, copyResult map[string]any, canvas ecommerceagent.PosterCanvas) (*model.EcommerceAgentRun, error) {
	var err error
	briefJSON, _ := json.Marshal(brief)
	copyJSON, _ := json.Marshal(copyResult)
	canvasJSON, _ := json.Marshal(canvas)
	if err = s.repo.SaveEcommerceAgentResult(run.UserID, run.ID, string(briefJSON), string(copyJSON), string(canvasJSON)); err != nil {
		return nil, err
	}
	if _, err = s.repo.SaveEcommerceAgentCanvas(run.UserID, run.ProjectID, string(canvasJSON)); err != nil {
		return nil, err
	}
	if run, err = s.repo.FinishEcommerceAgentRun(run.UserID, run.ID, "completed", ""); err != nil {
		return nil, err
	}
	return run, nil
}
