package app

import (
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"
	"yingce/backend/internal/ecommerceagent"
	"yingce/backend/internal/model"
)

func (s *Service) validateEcommerceWorkbenchResult(userID string, input canvasGenerationInput, result map[string]any) error {
	if stringValue(input.Metadata["ecommerceAgentAdapter"]) != "workbench-v1" {
		return nil
	}
	var draft EcommerceDraft
	raw, _ := json.Marshal(input.Metadata["ecommerceDraft"])
	if err := json.Unmarshal(raw, &draft); err != nil {
		return err
	}
	if stringValue(input.Metadata["stage"]) == "creative-directions" {
		return s.validateEcommerceDirectionsResult(userID, input, result, draft)
	}
	if stringValue(input.Metadata["stage"]) != "planning" {
		return nil
	}
	var plan model.EcommercePlan
	if err := ecommerceParseJSON(stringValue(result["text"]), &plan); err != nil {
		return err
	}
	if draft.ReferenceMode != "detail" && len(plan.Images) != draft.Count {
		return BadAuthRequest("策划图片数量与本轮请求不一致")
	}
	if plan.ConfirmedFacts == nil || plan.UncertainFacts == nil || plan.QualityChecks == nil {
		return BadAuthRequest("策划结果缺少事实或质检列表")
	}
	inferred := []string{}
	for i := range plan.Images {
		claims, err := normalizeEcommerceWorkbenchImage(draft, input, &plan, i)
		if err != nil {
			return err
		}
		inferred = append(inferred, claims...)
	}
	if err := finalizeEcommercePlanFacts(draft, &plan, inferred); err != nil {
		return err
	}
	if err := applyEcommerceShotAssignments(draft, input, &plan); err != nil {
		return err
	}
	if err := s.validateEcommercePlan(userID, &plan); err != nil {
		return err
	}
	if err := s.validateEcommerceReferencePlan(userID, draft, &plan); err != nil {
		return err
	}
	if draft.ImageType == "detail" {
		if err := validateEcommerceDetailRhythm(draft, plan); err != nil {
			return err
		}
		addEcommerceDetailReview(&plan)
	}
	raw, err := json.Marshal(plan)
	if err == nil {
		result["text"] = string(raw)
	}
	return err
}

func (s *Service) validateEcommerceDirectionsResult(userID string, input canvasGenerationInput, result map[string]any, draft EcommerceDraft) error {
	var analysis ecommerceagent.Analysis
	text := stringValue(result["text"])
	recovered := false
	parseErr := ecommerceParseJSON(text, &analysis)
	if parseErr != nil {
		if repaired, ok := ecommerceRepairDirectionsTerminalObject(text); ok {
			parseErr = ecommerceParseJSON(repaired, &analysis)
			if parseErr == nil {
				slog.Info("ecommerce directions terminal object repaired", "model", input.Config.Model)
			}
		}
	}
	if parseErr != nil {
		if fallback, ok := ecommerceRecoverDirections(text, draft); ok {
			analysis, parseErr, recovered = fallback, nil, true
			slog.Info("ecommerce directions recovered from complete observations", "model", input.Config.Model)
		}
	}
	if parseErr == nil && len(analysis.Recommendations) < 4 {
		if completed, ok := ecommerceCompleteDirections(analysis, draft); ok {
			analysis, recovered = completed, true
		}
	}
	if parseErr != nil {
		slog.Warn("ecommerce directions unparsable", "model", input.Config.Model, "chars", len(text), "head", ecommerceLogExcerpt(text, 600), "tail", ecommerceLogExcerpt(text[max(0, len(text)-300):], 300))
		if strings.TrimSpace(text) == "" {
			return BadAuthRequest("分析模型没有返回内容，请重试或更换模型")
		}
		return BadAuthRequest("分析模型返回的内容不是有效 JSON（可能被截断），请重试或更换模型")
	}
	response, err := s.storeEcommerceWorkbenchDirections(userID, stringValue(input.Metadata["ecommerceProjectId"]), draft, analysis)
	if err != nil {
		slog.Warn("ecommerce directions rejected", "model", input.Config.Model, "reason", err.Error(), "category", analysis.Brief.Category, "directions", len(analysis.Recommendations), "head", ecommerceLogExcerpt(text, 600))
		return err
	}
	if recovered {
		response.RetrievalStatus = "degraded"
		result["ecommerceDirectionsRecovered"] = true
	}
	raw, err := json.Marshal(response)
	if err == nil {
		result["text"] = string(raw)
	}
	return err
}

func validateEcommerceDetailFields(draft EcommerceDraft, image *model.EcommercePlanTask) error {
	// A detail draft keeps the strict planning contract after near-miss
	// labels are repaired. A detail page inside a set or other mixed
	// plan is filed under the closest category instead of failing the
	// whole plan, as the web planner does.
	strict := draft.ImageType == "detail"
	if strict && strings.TrimSpace(image.PerformanceAngle) == "" {
		return BadAuthRequest("详情策划缺少本页性能/体验角度，请由 Agent 补全后再试")
	}
	image.PerformanceAngle = repairEcommercePerformanceAngle(image.PerformanceAngle, !strict)
	if !ecommercePerformanceAnglePattern.MatchString(image.PerformanceAngle) {
		return BadAuthRequest("详情页性能/体验角度缺少策划分类，请由 Agent 补全后再试")
	}
	component := strings.TrimSpace(image.InformationComponent)
	if strict && !regexp.MustCompile(`^(卖点总览|标题导语|细节放大窗|场景注释|侧栏目录|规格卡|步骤编号|双栏对照|图标组|留白收束|无文字陈列)[｜:：].{12,}`).MatchString(component) {
		return BadAuthRequest("详情页缺少具体的信息组件与版式设计，请由 Agent 补全后再试")
	}
	return nil
}

func validateEcommercePageCopy(draft EcommerceDraft, image *model.EcommercePlanTask) (bool, error) {
	purpose := draft.TypeOptions["purpose"]
	listingMain := ecommerceagent.ListingMainImage(draft.Platform, image.ImageType, purpose) || ecommerceagent.ListingMainImage(draft.Platform, draft.ImageType, purpose)
	noText := draft.NoText || listingMain || ecommerceagent.PlatformNoText(draft.Platform, image.ImageType, purpose) || image.ImageType == "white" || image.ImageType == "three-view" || draft.CloneOptions["copyMode"] == "none" || (draft.CreativeOptions != nil && draft.CreativeOptions.TextMode == "none")
	if noText {
		image.Copy = ""
	} else if strings.TrimSpace(image.Copy) == "" {
		return false, BadAuthRequest("策划结果缺少可直接上图的文案")
	} else if ecommerceConsumerCopyHasDirections(image.Copy) {
		return false, BadAuthRequest("策划结果把镜头或排版说明写进了消费者文案，请重新策划")
	}
	return noText, nil
}

func normalizeEcommerceWorkbenchImage(draft EcommerceDraft, input canvasGenerationInput, plan *model.EcommercePlan, i int) ([]string, error) {
	image := &plan.Images[i]
	validType := false
	for _, kind := range []string{"main", "secondary", "sku", "white", "detail", "scene", "selling", "feature", "size", "three-view", "material", "package", "poster", "amazon-a-plus", "social", "single", "marketing"} {
		if image.ImageType == kind {
			validType = true
		}
	}
	if !validType || strings.TrimSpace(image.Prompt) == "" {
		return nil, BadAuthRequest("策划图片类型或提示词无效")
	}
	image.TaskID, image.ManualPrompt = "", false
	if image.ReferenceScreen != nil {
		image.ReferenceScreen.ResourceID = ""
	}
	if draft.ReferenceMode != "detail" {
		image.ReferenceScreen = nil
	}
	if draft.ImageType != "set" && image.ImageType != draft.ImageType {
		return nil, BadAuthRequest("策划图片类型与本轮请求不一致")
	}
	image.Platform, image.Language, image.ImageModel = draft.Platform, draft.Language, draft.ImageModel
	image.Ratio, image.Resolution = draft.Ratio, draft.Resolution
	applyEcommerceImagePromptRules(image, input)
	if image.ImageType == "detail" {
		if err := validateEcommerceDetailFields(draft, image); err != nil {
			return nil, err
		}
	}
	// Listing main images (Amazon …) are pure white without text, so their
	// copy stays empty; this mirrors the web's commerceAllowsText.
	noText, err := validateEcommercePageCopy(draft, image)
	if err != nil {
		return nil, err
	}
	if draft.ImageType == "sku" {
		found := false
		for _, sku := range draft.Skus {
			if sku.ID == image.SkuID {
				found = true
			}
		}
		if !found {
			return nil, BadAuthRequest("策划结果引用了未知 SKU")
		}
	}
	// Advertising red lines (certification, health/medical/absolute safety)
	// are removed from this page; the page keeps its layout and copy.
	if removed := groundEcommercePageClaims(draft, *plan, image, i); len(removed) > 0 {
		slog.Info("ecommerce plan claims removed", "page", i+1, "claims", strings.Join(removed, ","))
		if !noText && strings.TrimSpace(image.Copy) == "" {
			return nil, BadAuthRequest("策划结果去掉违规宣称后没有可上图的文案，请重新策划")
		}
	}
	// Inferred product information (capacity, function, material, specs)
	// stays on the page and is listed for the seller to verify.
	return ecommerceInferredClaims(draft, *image), nil
}

func applyEcommerceShotAssignments(draft EcommerceDraft, input canvasGenerationInput, plan *model.EcommercePlan) error {
	if raw, err := json.Marshal(input.Metadata["ecommerceShotAssignments"]); err == nil {
		var assignments []string
		if json.Unmarshal(raw, &assignments) == nil && len(assignments) == len(plan.Images) {
			copies := map[string]bool{}
			titles := map[string]bool{}
			for i := range plan.Images {
				image := &plan.Images[i]
				copyKey := strings.Join(strings.Fields(image.Copy), "")
				if len([]rune(copyKey)) >= 8 && copies[copyKey] {
					return BadAuthRequest("逐屏文案出现重复，请重新策划；未提交重复图片生成")
				}
				copies[copyKey] = true
				title := strings.TrimSpace(strings.Split(strings.TrimSpace(image.Copy), "\n")[0])
				brandLine := title == strings.TrimSpace(draft.Brand) || title == strings.TrimSpace(draft.ProductName)
				if !brandLine && len([]rune(title)) >= 4 && titles[title] {
					return BadAuthRequest("逐屏标题出现重复，请重新策划；未提交重复图片生成")
				}
				titles[title] = true
				image.DisplayAssignment = assignments[i]
				image.Prompt = "【本屏唯一拍摄要求】" + assignments[i] + "\n【本屏具体内容】" + image.Prompt
			}
		}
	}
	return nil
}

func applyEcommerceImagePromptRules(image *model.EcommercePlanTask, input canvasGenerationInput) {
	if !strings.Contains(image.Prompt, ecommerceProductAnchor) {
		image.Prompt = ecommerceProductAnchor + ", " + image.Prompt
	}
	if !strings.Contains(image.Prompt, ecommerceagent.HumanPhotographyKnowledge) {
		image.Prompt += "\n" + ecommerceagent.HumanPhotographyKnowledge
	}
	if !strings.Contains(image.Prompt, ecommerceagent.LayoutSafetyRule) {
		image.Prompt += "\n" + ecommerceagent.LayoutSafetyRule
	}
	if rules := ecommerceManagedImageRules(input.Metadata); rules != "" && !strings.Contains(image.Prompt, rules) {
		image.Prompt += "\n" + rules
	}
	// The confirmed direction's visual lock is enforced server-side, so the
	// image task always carries the selected palette, light and layout even
	// when the model paraphrased or dropped it.
	if lock := stringValue(input.Metadata["ecommerceDirectionLock"]); lock != "" && !strings.Contains(image.Prompt, lock) {
		image.Prompt = strings.TrimSpace(image.Prompt) + "\n" + lock
	}
}

func finalizeEcommercePlanFacts(draft EcommerceDraft, plan *model.EcommercePlan, inferred []string) error {
	if draft.ImageType == "sku" {
		for _, sku := range draft.Skus {
			count := 0
			for _, image := range plan.Images {
				if image.SkuID == sku.ID {
					count++
				}
			}
			if count != sku.Count {
				return BadAuthRequest("策划结果中的 SKU 张数不匹配")
			}
		}
	}
	if draft.ImageType == "detail" && len(plan.UncertainFacts) > 0 {
		// Detail pages keep the Agent's inferred product information; the
		// open questions become items for the seller to verify instead of
		// being stripped from the copy before generation.
		inferred = append(inferred, plan.UncertainFacts...)
		plan.UncertainFacts = []string{}
	}
	if inferred = ecommerceUniqueStrings(inferred); len(inferred) > 0 {
		plan.QualityChecks = append(plan.QualityChecks, "发布前请商家核对 Agent 推断的商品信息（与实物不符请修改该页文案后重新生成）："+strings.Join(inferred, "、"))
	}
	if policy, ok := ecommerceagent.PlatformKnowledgeFor(draft.Platform); ok {
		plan.QualityChecks = append(plan.QualityChecks, "发布前核对站点、类目、成片文字与实物、图片文件规格及商品资质；平台资料适用范围："+policy.Scope)
		if policy.ID == "etsy" && len(policy.OfficialRules) > 0 {
			plan.QualityChecks = append(plan.QualityChecks, policy.OfficialRules[0])
		}
	}
	return nil
}
