package app

import (
	"fmt"
	"math"
	"strings"

	"yingce/backend/internal/model"
)

func isEcommerceLayoutRole(role string) bool {
	return role == "style" || role == "competitor"
}

// A nil group identifies an older request. Only an explicitly selected legacy
// source, or old style/competitor entries, can participate in that compatibility path.
func ecommerceLayoutSources(draft EcommerceDraft) []EcommerceReference {
	if draft.LayoutReferences != nil {
		return draft.LayoutReferences
	}
	if draft.LayoutReferenceID != "" {
		for _, reference := range draft.References {
			if reference.ResourceID == draft.LayoutReferenceID && reference.Role != "model" && reference.Role != "logo" {
				return []EcommerceReference{reference}
			}
		}
		return nil
	}
	var sources []EcommerceReference
	for _, reference := range draft.References {
		if isEcommerceLayoutRole(reference.Role) {
			sources = append(sources, reference)
		}
	}
	return sources
}

func (s *Service) validateEcommerceReferenceDraft(userID string, draft *EcommerceDraft, hasPlan bool) error {
	switch draft.ReferenceMode {
	case "", "product", "style", "detail":
	default:
		return BadAuthRequest("参考模式无效，请重新选择")
	}
	active := draft.ReferenceMode == "style" || draft.ReferenceMode == "detail"
	separated := draft.LayoutReferences != nil
	references, err := validateEcommerceReferenceGroups(*draft)
	if err != nil {
		return err
	}
	if draft.LayoutReferenceID != "" && !references.found {
		if active && !separated {
			return BadAuthRequest("版式参考图必须属于本项目已添加的参考图片")
		}
		resource, err := s.repo.ResourceForUser(userID, draft.LayoutReferenceID)
		if err != nil || resource.Kind != "image" || resource.Status != model.ResourceStatusReady {
			return BadAuthRequest("版式参考图不存在或不属于当前用户")
		}
		// Removing an inactive source should not leave a dangling deletion-protection reference.
		draft.LayoutReferenceID = ""
	}
	if active && !separated && (references.sourceRole == "model" || references.sourceRole == "logo") {
		return BadAuthRequest("模特图或品牌标识不能作为版式参考来源")
	}
	if hasPlan && active {
		if len(ecommerceLayoutSources(*draft)) == 0 {
			return BadAuthRequest("参考方案缺少独立借鉴图片，请添加后重新策划")
		}
		if separated && !references.hasPrimary {
			return BadAuthRequest("请先上传商品主图，借鉴图片不能代替真实商品素材")
		}
	}
	return nil
}

type ecommerceReferenceGroups struct {
	found, hasPrimary bool
	sourceRole        string
}

func validateEcommerceReferenceGroups(draft EcommerceDraft) (ecommerceReferenceGroups, error) {
	var result ecommerceReferenceGroups
	for _, reference := range draft.References {
		if reference.Width < 0 || reference.Height < 0 {
			return result, BadAuthRequest("参考图片尺寸无效")
		}
		if draft.LayoutReferences != nil && isEcommerceLayoutRole(reference.Role) {
			return result, BadAuthRequest("风格与竞品图片须放入独立借鉴图片列表")
		}
		result.hasPrimary = result.hasPrimary || reference.Role == "primary"
		if reference.ResourceID == draft.LayoutReferenceID {
			result.found = true
			result.sourceRole = reference.Role
		}
	}
	seen := make(map[string]bool, len(draft.LayoutReferences))
	for _, reference := range draft.LayoutReferences {
		if !isEcommerceLayoutRole(reference.Role) {
			return result, BadAuthRequest("借鉴图片只能使用风格或竞品用途，不能作为商品素材")
		}
		if reference.Width < 0 || reference.Height < 0 || reference.ResourceID == "" || seen[reference.ResourceID] {
			return result, BadAuthRequest("借鉴图片ID、尺寸无效或图片重复")
		}
		seen[reference.ResourceID] = true
		if reference.ResourceID == draft.LayoutReferenceID {
			result.found = true
			result.sourceRole = reference.Role
		}
	}
	return result, nil
}

func (s *Service) validateEcommerceReferencePlan(userID string, draft EcommerceDraft, plan *model.EcommercePlan) error {
	active := draft.ReferenceMode == "style" || draft.ReferenceMode == "detail"
	if len(plan.ReferenceSummary) > 32000 {
		return BadAuthRequest("参考图分析摘要过长")
	}
	if active && strings.TrimSpace(plan.ReferenceSummary) == "" {
		return BadAuthRequest("参考方案缺少借鉴图片分析摘要，请重新策划")
	}
	sources := ecommerceLayoutSources(draft)
	references := make(map[string]int, len(sources))
	for index, reference := range sources {
		if _, duplicate := references[reference.ResourceID]; duplicate {
			return BadAuthRequest("借鉴图片不能重复")
		}
		references[reference.ResourceID] = index
	}
	detail := draft.ReferenceMode == "detail"
	coverage := ecommerceReferenceCoverage{groupIndex: -1}
	for index, image := range plan.Images {
		screen := image.ReferenceScreen
		if screen == nil {
			if detail {
				return BadAuthRequest(fmt.Sprintf("第%d屏缺少详情参考分析，请重新策划", index+1))
			}
			continue
		}
		position, found := references[screen.SourceResourceID]
		if screen.SourceResourceID == "" || !found {
			return BadAuthRequest(fmt.Sprintf("第%d屏的来源与所选参考图不一致", index+1))
		}
		if err := validateEcommerceReferenceScreen(screen, index, detail); err != nil {
			return err
		}
		if detail {
			if err := coverage.include(position, screen); err != nil {
				return err
			}
		}
		if err := s.validateEcommerceScreenResource(userID, screen.ResourceID, index); err != nil {
			return err
		}
	}
	if detail && (coverage.groupIndex != len(sources)-1 || coverage.groupIndex < 0 || math.Abs(coverage.previousBottom-1) > 0.010000001) {
		return BadAuthRequest("详情参考须完整覆盖每张借鉴图片及其底部，请重新策划")
	}
	return nil
}

type ecommerceReferenceCoverage struct {
	previousBottom float64
	groupIndex     int
}

func (coverage *ecommerceReferenceCoverage) include(position int, screen *model.EcommerceReferenceScreen) error {
	if position != coverage.groupIndex {
		if position != coverage.groupIndex+1 || (coverage.groupIndex >= 0 && math.Abs(coverage.previousBottom-1) > 0.010000001) {
			return BadAuthRequest("详情屏须按借鉴图片顺序完整分组，不能遗漏、交错或倒序")
		}
		coverage.groupIndex = position
		coverage.previousBottom = 0
	}
	// Small model rounding differences are acceptable; omitted or reordered sections are not.
	if math.Abs(screen.Top-coverage.previousBottom) > 0.010000001 || screen.Bottom <= coverage.previousBottom {
		return BadAuthRequest("详情参考范围须按原图从上到下连续覆盖，不能缺屏、重叠或倒序")
	}
	coverage.previousBottom = screen.Bottom
	return nil
}

func validateEcommerceReferenceScreen(screen *model.EcommerceReferenceScreen, index int, detail bool) error {
	if screen.ScreenIndex < 1 || screen.ScreenIndex > 30 || (detail && screen.ScreenIndex != index+1) {
		return BadAuthRequest("详情参考屏次必须从1开始按顺序排列")
	}
	if math.IsNaN(screen.Top) || math.IsNaN(screen.Bottom) || math.IsInf(screen.Top, 0) || math.IsInf(screen.Bottom, 0) || screen.Top < 0 || screen.Bottom > 1 || screen.Top >= screen.Bottom {
		return BadAuthRequest(fmt.Sprintf("第%d屏的参考范围无效，应满足0≤top<bottom≤1", index+1))
	}
	if strings.TrimSpace(screen.Content) == "" || strings.TrimSpace(screen.Layout) == "" || strings.TrimSpace(screen.Visual) == "" {
		return BadAuthRequest(fmt.Sprintf("第%d屏缺少内容、排版或画面分析", index+1))
	}
	if len(screen.Content)+len(screen.Layout)+len(screen.Visual)+len(screen.SourceCopy) > 32000 {
		return BadAuthRequest(fmt.Sprintf("第%d屏的参考分析过长", index+1))
	}
	return nil
}

func (s *Service) validateEcommerceScreenResource(userID, resourceID string, index int) error {
	if resourceID == "" {
		return nil
	}
	resource, err := s.repo.ResourceForUser(userID, resourceID)
	if err != nil || resource.Kind != "image" || resource.Status != model.ResourceStatusReady {
		return BadAuthRequest(fmt.Sprintf("第%d屏的裁剪图片不存在或不属于当前用户", index+1))
	}
	return nil
}
