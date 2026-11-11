package app

import (
	"encoding/json"
	"strings"
	"yingce/backend/internal/model"
)

func (s *Service) validateEcommerceDraftLimits(userID string, req *SaveEcommerceProjectRequest) error {
	if req.Draft.Count < 1 || req.Draft.Count > 30 {
		return BadAuthRequest("生成数量须为1到30张")
	}
	if len(req.Draft.References)+len(req.Draft.LayoutReferences) > 24 || len(req.Draft.Skus) > 30 {
		return BadAuthRequest("参考图片或SKU数量超出限制")
	}
	if len(req.Draft.SkillIDs) > 8 {
		return BadAuthRequest("每轮最多选择 8 个 Skills")
	}
	if err := validateEcommerceCreativeOptions(req.Draft.CreativeOptions); err != nil {
		return err
	}
	if err := s.validateEcommerceReferenceDraft(userID, &req.Draft, req.Plan != nil); err != nil {
		return err
	}

	return nil
}

func (s *Service) validateEcommerceDraftText(req *SaveEcommerceProjectRequest) error {
	if len(req.Draft.TypeOptions) > 16 {
		return BadAuthRequest("创作类型参数超出限制")
	}
	for key, value := range req.Draft.TypeOptions {
		if len(key) > 80 || len(value) > 4000 {
			return BadAuthRequest("创作类型参数过长")
		}
	}
	if len(strings.TrimSpace(req.Draft.ProductName)) > 160 || len(req.Draft.Instruction) > 8000 {
		return BadAuthRequest("商品名称或策划要求过长")
	}

	return nil
}

func (s *Service) validateEcommerceDraftDirection(userID string, req *SaveEcommerceProjectRequest) error {
	if req.Draft.CreativeRecommendationID != "" || req.Draft.CreativeDirectionID != "" {
		selection, err := s.repo.EcommerceRecommendation(userID, req.Draft.CreativeRecommendationID)
		if err != nil || selection.SelectedDirectionID == "" || selection.SelectedDirectionID != req.Draft.CreativeDirectionID {
			return BadAuthRequest("所选创意方向不存在或不属于当前用户")
		}
	}
	if req.Draft.Platform == "" || req.Draft.ImageType == "" {
		return BadAuthRequest("请选择平台和图片类型")
	}

	return nil
}

func (s *Service) validateEcommerceDraftImages(userID string, req *SaveEcommerceProjectRequest) error {
	for _, ref := range append(append([]EcommerceReference{}, req.Draft.References...), req.Draft.LayoutReferences...) {
		if !validEcommerceReferenceRole(ref.Role) {
			return BadAuthRequest("参考图片用途无效")
		}
		resource, e := s.repo.ResourceForUser(userID, ref.ResourceID)
		if e != nil || resource.Kind != "image" || resource.Status != model.ResourceStatusReady {
			return BadAuthRequest("参考图片不存在或不可用")
		}
	}
	if req.Draft.ModelEnabled && req.Draft.PrimaryModelResourceID != "" {
		found := false
		for _, ref := range req.Draft.References {
			if ref.Role == "model" && ref.ResourceID == req.Draft.PrimaryModelResourceID {
				found = true
				break
			}
		}
		if !found {
			return BadAuthRequest("主模特必须属于已添加的模特参考图")
		}
	}

	return nil
}

func (s *Service) validateEcommerceDraftSkus(userID string, req *SaveEcommerceProjectRequest) error {
	for _, sku := range req.Draft.Skus {
		if sku.Count < 0 || sku.Count > 30 {
			return BadAuthRequest("SKU生成数量无效")
		}
		if sku.ResourceID != "" {
			resource, e := s.repo.ResourceForUser(userID, sku.ResourceID)
			if e != nil || resource.Kind != "image" || resource.Status != model.ResourceStatusReady {
				return BadAuthRequest("SKU参考图不存在或不可用")
			}
			linked := false
			for _, ref := range req.Draft.References {
				if ref.Role == "sku" && ref.ResourceID == sku.ResourceID {
					linked = true
					break
				}
			}
			if !linked {
				return BadAuthRequest("SKU参考图须加入商品参考图列表")
			}
		}
	}

	return nil
}

func (s *Service) validateEcommerceDraftTasks(id string, userID string, req *SaveEcommerceProjectRequest) error {
	if req.Draft.PlanningTaskID != "" {
		task, e := s.repo.TaskForUser(userID, req.Draft.PlanningTaskID)
		if e != nil || task.Type != "canvas_text" {
			return BadAuthRequest("策划任务不存在或不属于当前用户")
		}
	}
	if req.Draft.DirectionsTaskID != "" {
		task, e := s.repo.TaskForUser(userID, req.Draft.DirectionsTaskID)
		var input struct {
			Metadata struct {
				ProjectID string `json:"ecommerceProjectId"`
				Stage     string `json:"stage"`
			} `json:"metadata"`
		}
		if e != nil || task.Type != "canvas_text" || json.Unmarshal([]byte(task.InputJSON), &input) != nil || input.Metadata.ProjectID != id || input.Metadata.Stage != "creative-directions" {
			return BadAuthRequest("商品分析任务不存在或不属于当前项目")
		}
	}

	return nil
}

func (s *Service) validateEcommercePlanHistory(userID string, req *SaveEcommerceProjectRequest) error {
	if req.HistoryMode != "" && req.HistoryMode != "rounds" {
		return BadAuthRequest("不支持的保存历史模式")
	}
	if req.HistoryMode == "rounds" {
		return validateEcommerceActivePlan(req.Plan)
	}
	if req.Plan != nil {
		if len(req.Plan.Images) == 0 && len(req.Plan.PreviousImages) == 0 {
			return BadAuthRequest("策划图片不能为空")
		}
		if len(req.Plan.PreviousImages)+len(req.Plan.Images) > 500 {
			return BadAuthRequest("当前画布已达500张历史图片，请新建项目继续")
		}
		seen := map[string]bool{}
		for _, image := range append(append([]model.EcommercePlanTask{}, req.Plan.PreviousImages...), req.Plan.Images...) {
			if image.ID == "" || seen[image.ID] {
				return BadAuthRequest("画布图片ID重复或为空")
			}
			seen[image.ID] = true
		}
		for _, image := range req.Plan.PreviousImages {
			if len(image.SourceDraft) > 128<<10 {
				return BadAuthRequest("历史图片参数快照过长")
			}
			task, err := s.repo.TaskForUser(userID, image.TaskID)
			if image.TaskID == "" || err != nil || task.Type != "canvas_image" {
				return BadAuthRequest("历史生成任务不存在或不属于当前用户")
			}
		}
	}

	return nil
}

func validateEcommerceActivePlan(plan *model.EcommercePlan) error {
	if plan == nil {
		return nil
	}
	if len(plan.PreviousImages) > 0 {
		return BadAuthRequest("历史轮次由服务端单独保存，请刷新页面后重试")
	}
	seen := map[string]bool{}
	for _, image := range plan.Images {
		if image.ID == "" || seen[image.ID] {
			return BadAuthRequest("画布图片ID重复或为空")
		}
		seen[image.ID] = true
	}
	return nil
}

func (s *Service) validateEcommercePlannedDraft(userID string, req *SaveEcommerceProjectRequest) error {
	if req.Plan != nil && len(req.Plan.Images) > 0 {
		if err := s.validateEcommercePlan(userID, req.Plan); err != nil {
			return err
		}
		if err := s.validateEcommerceReferencePlan(userID, req.Draft, req.Plan); err != nil {
			return err
		}
		if len(req.Plan.Images) != req.Draft.Count {
			return BadAuthRequest("策划图片数量与当前设置不一致，请重新策划")
		}
		if req.Draft.ImageType == "sku" {
			counts := make(map[string]int, len(req.Draft.Skus))
			for _, sku := range req.Draft.Skus {
				if sku.ID == "" || strings.TrimSpace(sku.Name) == "" {
					return BadAuthRequest("SKU名称或ID不能为空")
				}
				if _, exists := counts[sku.ID]; exists {
					return BadAuthRequest("SKU ID不能重复")
				}
				counts[sku.ID] = 0
			}
			for _, image := range req.Plan.Images {
				if _, exists := counts[image.SkuID]; !exists {
					return BadAuthRequest("SKU策划图未关联有效SKU")
				}
				counts[image.SkuID]++
			}
			for _, sku := range req.Draft.Skus {
				if counts[sku.ID] != sku.Count {
					return BadAuthRequest("SKU策划张数与商品设置不一致")
				}
			}
		}
	}
	return nil
}
