package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"yingce/backend/internal/ecommerceagent"
)

func (s *Service) cachedEcommerceAnalysis(userID, signature string) (*ecommerceagent.Analysis, error) {
	cached, err := s.repo.EcommerceAgentAnalysis(userID, signature)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if time.Since(cached.CreatedAt) >= time.Hour {
		return nil, nil
	}
	var result ecommerceagent.Analysis
	if json.Unmarshal([]byte(cached.PayloadJSON), &result) != nil || len(result.Recommendations) < 4 {
		return nil, nil
	}
	return &result, nil
}

func (s *Service) analyzeEcommerceProduct(ctx context.Context, userID string, cfg ecommerceProviderConfig, req ecommerceagent.Request) (ecommerceagent.Brief, bool, error) {
	brief := ecommerceagent.MockBrief(req.Query)
	if cfg.Mode != "live" {
		return brief, false, nil
	}
	images := make([]string, 0, len(req.FileInfoList))
	for _, file := range req.FileInfoList {
		uri, _, _, err := s.ecommerceReference(userID, file.URL)
		if err != nil {
			return brief, false, err
		}
		images = append(images, uri)
	}
	verified := true
	prompt := ecommerceagent.BuildVisionPrompt(req)
	if err := s.ecommerceChatJSON(ctx, cfg, "analyze", ecommerceagent.SystemVision, prompt, images, &brief); err != nil {
		if ctx.Err() != nil {
			return brief, false, ctx.Err()
		}
		verified = false
		brief = ecommerceagent.MockBrief(req.Query)
		brief.VisualRisks = []string{"视觉理解失败，未核验商品图；材质、结构、性能和商业卖点均待卖家确认"}
	}
	if strings.TrimSpace(brief.Category) == "" {
		verified = false
		brief = ecommerceagent.MockBrief(req.Query)
		brief.VisualRisks = []string{"视觉理解未返回商品品类，未核验商品图；材质、结构、性能和商业卖点均待卖家确认"}
	}
	return brief, verified, nil
}

func normalizeEcommerceRecommendations(proposed []ecommerceagent.Recommendation) bool {
	if len(proposed) < 4 || len(proposed) > 6 {
		return false
	}
	for i := range proposed {
		recommendation := &proposed[i]
		recommendation.ID = fmt.Sprintf("r%d", i+1)
		if recommendation.Label == "" || recommendation.Style == "" || recommendation.Why == "" || !safeRecommendationPrompt(recommendation.PreviewPrompt) || recommendation.AspectRatio == "" {
			return false
		}
		if recommendation.Pages < 1 || recommendation.Pages > 15 {
			recommendation.Pages = 1
		}
	}
	return true
}
