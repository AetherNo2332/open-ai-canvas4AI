package app

import (
	"context"
	"errors"
	"time"
	"yingce/backend/internal/ecommerceagent"
	"yingce/backend/internal/model"
)

type ecommercePageResult struct {
	page    ecommerceagent.Page
	latency int64
}

type ecommercePageGenerator struct {
	svc           *Service
	config        ecommerceProviderConfig
	userID, runID string
	platform      ecommerceagent.Platform
	brand         ecommerceagent.Brand
	brief         ecommerceagent.Brief
	selected      *ecommerceagent.Recommendation
	unit          int64
	reference     []byte
	mime          string
	emit          func(ecommerceagent.Event) error
}

func (g *ecommercePageGenerator) run(ctx context.Context, pages []ecommerceagent.Page) error {
	results := make(chan ecommercePageResult, g.config.MaxConcurrent)
	next, active, done := 0, 0, 0
	persisted := make([]bool, len(pages))

	for done < len(pages) {
		for next < len(pages) && active < g.config.MaxConcurrent && ctx.Err() == nil {
			g.launch(ctx, next, pages, results)
			next++
			active++
		}
		if active == 0 {
			break
		}
		select {
		case <-ctx.Done(): // The workers share this context and will stop before further upstream requests.
			for active > 0 {
				<-results
				active--
			}
			done = len(pages)
		case item := <-results:
			active--
			done++
			page := item.page
			status := "failed"
			if page.URL != "" {
				status = "completed"
			}
			row := &model.EcommerceAgentPage{ID: newID(), RunID: g.runID, Index: page.Index, Role: page.Role, Title: page.Title, Headline: page.Headline, Prompt: page.Prompt, URL: page.URL, Status: status, Error: page.Error, Ratio: page.AspectRatio, NeedText: page.NeedText, LatencyMs: item.latency}
			if saveErr := g.svc.repo.SaveEcommerceAgentPage(g.userID, row); saveErr != nil {
				return saveErr
			}
			persisted[page.Index-1] = true
			pages[page.Index-1] = page
			if emitErr := g.emit(ecommerceagent.Event{Event: "iteration_completed", Data: map[string]any{"page": page, "progress": float64(done) / float64(len(pages))}}); emitErr != nil {
				return emitErr
			}
		}
	}
	if ctx.Err() != nil {
		return g.finishCancelledPages(ctx, pages, persisted)
	}
	return nil

}

func (g *ecommercePageGenerator) launch(ctx context.Context, index int, pages []ecommerceagent.Page, results chan<- ecommercePageResult) {
	page := pages[index]
	go func() {
		start := time.Now()
		page.Prompt = ecommerceImagePrompt(page, g.platform, g.brand, g.brief, g.selected)
		if g.config.Mode == "mock" {
			page.URL = ecommerceagent.MockImage(page, g.brief.Category)
		} else {
			data, upstreamURL, e := ecommerceImage(ctx, g.config, g.runID, page.Prompt, page.AspectRatio, g.mime, g.reference, g.unit)
			if e == nil {
				page.URL, e = g.svc.ecommerceAgentGeneratedURL(g.userID, data, upstreamURL, page.AspectRatio)
			}
			if e != nil {
				page.Error = "出图失败，请重试"
			}
		}
		results <- ecommercePageResult{page: page, latency: time.Since(start).Milliseconds()}
	}()
}

func (g *ecommercePageGenerator) finishCancelledPages(ctx context.Context, pages []ecommerceagent.Page, persisted []bool) error {
	code := "CANCELLED"
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		code = "MODEL_TIMEOUT"
	}
	for i := range pages {
		if persisted[i] {
			continue
		}
		page := &pages[i]
		page.Error = code
		row := &model.EcommerceAgentPage{ID: newID(), RunID: g.runID, Index: page.Index, Role: page.Role, Title: page.Title, Headline: page.Headline, Status: "failed", Error: code, Ratio: page.AspectRatio, NeedText: page.NeedText}
		if saveErr := g.svc.repo.SaveEcommerceAgentPage(g.userID, row); saveErr != nil {
			return saveErr
		}
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ecommerceErr("MODEL_TIMEOUT", "出图超时")
	}
	return ecommerceErr("CANCELLED", "任务已取消")
}
