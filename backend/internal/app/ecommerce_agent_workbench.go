package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
	"yingce/backend/internal/ecommerceagent"
	"yingce/backend/internal/model"
)

const ecommerceProductAnchor = ecommerceagent.ProductAnchor

var ecommerceInternalCopyPattern = regexp.MustCompile(`(?i)(?:手持|正面|侧面|局部)?画面(?:呈现|展示|安排|采用|使用|保持)|(?:构图|镜头|机位|景别|排版|提示词|安全边距|字号|字重|文案位置|主体占比|相对关系|装饰仍清晰可见)|(?:主标题|副标题|说明文案|正文|标题)\s*[:：]|(?:放在|置于|位于).{0,12}(?:左侧|右侧|上方|下方|画幅)|(?:render|place|layout|composition|camera|prompt)\s*[:：]`)

func ecommerceConsumerCopyHasDirections(copy string) bool {
	return ecommerceInternalCopyPattern.MatchString(copy)
}

// ecommercePerformanceAnglePattern is the planning contract for a detail
// page's performanceAngle: one of five categories, then ｜ or a colon.
var ecommercePerformanceAnglePattern = regexp.MustCompile(`^(已证实性能|可见设计|实际操作|场景体验|选购决策)[｜:：]`)

var ecommercePerformanceCategories = []string{"已证实性能", "可见设计", "实际操作", "场景体验", "选购决策"}

// ecommercePerformanceAngleWords map the labels planners write instead of a
// category (整体识别, 使用情境, 操作位置, 信任 or 购买 questions …) to the
// closest one. web/src/pages/ecommerce/planner.ts keeps the same table.
var ecommercePerformanceAngleWords = []struct {
	category string
	words    []string
}{
	{"已证实性能", []string{"已证实", "已确认", "证实", "实证", "性能", "参数", "规格", "功能", "proven", "performance", "spec"}},
	{"实际操作", []string{"操作", "步骤", "用法", "使用方法", "上手", "演示", "operation", "how to", "step"}},
	{"场景体验", []string{"场景", "情境", "情景", "体验", "生活方式", "陪伴", "搭配", "陈列", "scene", "scenario", "experience", "lifestyle"}},
	{"选购决策", []string{"选购", "购买", "决策", "信任", "疑问", "答疑", "对比", "对照", "保障", "purchase", "buying", "decision", "trust"}},
	{"可见设计", []string{"可见", "设计", "结构", "外观", "细节", "特写", "识别", "造型", "轮廓", "材质", "做工", "工艺", "visible", "design", "detail", "appearance"}},
}

const (
	// ecommerceAngleOpeners may precede a label: 【可见设计】, “选购决策”…
	ecommerceAngleOpeners = "【[（(「『“\"' \t"
	// ecommerceAngleSeparators end a label.
	ecommerceAngleSeparators = "｜|:：】]）)」』”\"—–"
	// ecommerceAngleLabelRunes is the longest text read as a label.
	ecommerceAngleLabelRunes = 16
	// ecommerceDefaultPerformanceAngle fills an empty angle of a detail page
	// inside a set (the text agent-feed shows for a missing angle).
	ecommerceDefaultPerformanceAngle = "可见设计｜围绕可见结构与实际使用动作组织体验表达"
)

// ecommerceAngleCategory returns the category whose word appears first in a
// label, or "" when none does.
func ecommerceAngleCategory(label string) string {
	label = strings.ToLower(label)
	best, bestAt, bestLen := "", -1, 0
	for _, group := range ecommercePerformanceAngleWords {
		for _, word := range group.words {
			at := strings.Index(label, word)
			if at >= 0 && (bestAt < 0 || at < bestAt || (at == bestAt && len(word) > bestLen)) {
				best, bestAt, bestLen = group.category, at, len(word)
			}
		}
	}
	return best
}

// ecommerceAngleLabel splits "选购决策角度：…" into the label before the first
// separator and the text after it, when the label is short enough to be one.
func ecommerceAngleLabel(angle string) (label, rest string, ok bool) {
	body := strings.TrimLeft(angle, ecommerceAngleOpeners)
	runes := 0
	for at, r := range body {
		if runes > ecommerceAngleLabelRunes {
			break
		}
		if strings.ContainsRune(ecommerceAngleSeparators, r) {
			label = strings.TrimSpace(body[:at])
			return label, strings.TrimLeft(body[at+utf8.RuneLen(r):], ecommerceAngleSeparators+" \t"), label != ""
		}
		runes++
	}
	return "", body, false
}

// ecommerceBareAngleLabel drops a generic suffix, so "选购决策角度" reads as
// the category itself.
func ecommerceBareAngleLabel(label string) string {
	for _, suffix := range []string{"切入点", "角度", "分类", "类别", "类型", "维度", "方向", "切入", "类"} {
		label = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(label), suffix))
	}
	return label
}

// repairEcommercePerformanceAngle files a near-miss label (选购决策角度：…,
// 已证实性能/可见设计角度：…, 使用情境：…) under the closest of the five
// categories. With fallback, an angle without a recognisable label is filed
// under 可见设计 and an empty one gets a neutral angle; without it such an
// angle is returned unchanged for the caller to reject.
func repairEcommercePerformanceAngle(angle string, fallback bool) string {
	angle = strings.TrimSpace(angle)
	if ecommercePerformanceAnglePattern.MatchString(angle) {
		return angle
	}
	if label, rest, ok := ecommerceAngleLabel(angle); ok {
		if category := ecommerceAngleCategory(label); category != "" {
			if ecommerceBareAngleLabel(label) == category {
				return category + "｜" + rest
			}
			return category + "｜" + angle
		}
	}
	body := strings.TrimLeft(angle, ecommerceAngleOpeners)
	for _, category := range ecommercePerformanceCategories {
		if rest, ok := strings.CutPrefix(body, category); ok {
			return category + "｜" + strings.TrimLeft(rest, ecommerceAngleSeparators+" \t")
		}
	}
	switch {
	case !fallback:
		return angle
	case angle == "":
		return ecommerceDefaultPerformanceAngle
	}
	return "可见设计｜" + angle
}

// ecommercePerformanceAngleCategory is the category of a (repaired) angle, or
// "" when it has none.
func ecommercePerformanceAngleCategory(angle string) string {
	if match := ecommercePerformanceAnglePattern.FindStringSubmatch(repairEcommercePerformanceAngle(angle, false)); len(match) == 2 {
		return match[1]
	}
	return ""
}

func validateEcommerceDetailRhythm(draft EcommerceDraft, plan model.EcommercePlan) error {
	if draft.ImageType != "detail" || draft.ReferenceMode == "detail" || len(plan.Images) != 9 {
		return nil
	}
	angles := map[string]struct{}{}
	goals := map[string]struct{}{}
	compositions := map[string]struct{}{}
	closeups := 0
	closeupPattern := regexp.MustCompile(`(?i)微距|近景|特写|局部|细节放大|macro|close[- ]?up|detail shot|detail view`)
	for _, image := range plan.Images {
		if category := ecommercePerformanceAngleCategory(image.PerformanceAngle); category != "" {
			angles[category] = struct{}{}
		}
		if goal := strings.TrimSpace(image.Goal); goal != "" {
			goals[strings.Join(strings.Fields(goal), " ")] = struct{}{}
		}
		if composition := strings.TrimSpace(image.Composition); composition != "" {
			compositions[strings.Join(strings.Fields(composition), " ")] = struct{}{}
		}
		if closeupPattern.MatchString(strings.Join([]string{image.Goal, image.Theme, image.Composition, image.MainVisual, image.Prompt}, " ")) {
			closeups++
		}
	}
	if len(angles) < 2 {
		return BadAuthRequest("九张详情图至少需要两种不同的性能/体验说服角度")
	}
	if closeups < 2 {
		return BadAuthRequest("九张详情图至少需要两张局部近景或细节特写")
	}
	if len(goals) < 3 {
		return BadAuthRequest("九张详情图至少需要三种不同的购买目标或使用场景")
	}
	if len(compositions) < 3 {
		return BadAuthRequest("九张详情图至少需要三种不同的机位与构图")
	}
	return nil
}

// Vision and art direction share one paid task and one durable result.
func ecommerceWorkbenchDirectionsPrompt(draft EcommerceDraft) (string, string) {
	p := ecommerceWorkbenchPlatform(draft)
	ratio := draft.Ratio
	if ratio == "" {
		ratio = p.DetailRatio
	}
	return ecommerceagent.WorkbenchDirectionsSystem,
		ecommerceagent.BuildStyleRecommendRequest(p, draft.Language, draft.ImageType, draft.Count, ratio, draft.ReferenceMode, draft)
}

func ecommerceFastDirectionsRequest(draft EcommerceDraft) string {
	p := ecommerceWorkbenchPlatform(draft)
	ratio := draft.Ratio
	if ratio == "" {
		ratio = p.DetailRatio
	}
	facts := map[string]string{"商品名": draft.ProductName, "品类": draft.Category, "品牌": draft.Brand, "描述": draft.Description, "已确认卖点": draft.Claims, "材质": draft.Materials, "颜色": draft.Colors, "规格": draft.Specifications}
	noText := draft.NoText || (draft.CreativeOptions != nil && draft.CreativeOptions.TextMode == "none")
	return ecommerceagent.FastDirectionsRequest(p, draft.Language, ecommerceDirectionsContentType(draft), draft.Count, ratio, facts, draft.Instruction, noText, draft.StylePrompt)
}

// ecommerceCompactStrategy keeps the parts of a confirmed direction that shape
// the plan (product brief, the direction's visual system and lock, request
// facts) and drops stored knowledge copies, skill outputs and traces, which
// otherwise repeat the knowledge block and the lock sentence many times.
func ecommerceCompactStrategy(strategy map[string]any) map[string]any {
	if strategy == nil {
		return nil
	}
	compact := map[string]any{}
	for _, key := range []string{"brief", "contentType", "imageCount", "platform", "instruction", "verifiedFacts", "visualLock"} {
		if value, ok := strategy[key]; ok {
			compact[key] = value
		}
	}
	if direction, ok := strategy["direction"].(map[string]any); ok {
		kept := map[string]any{}
		for _, key := range []string{"label", "style", "why", "visualSystem"} {
			if value, ok := direction[key]; ok {
				kept[key] = value
			}
		}
		compact["direction"] = kept
	} else if direction, ok := strategy["direction"]; ok {
		compact["direction"] = direction
	}
	return compact
}

func ecommerceTruncate(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "…"
}

// ecommerceDirectionVisual exposes the reviewable parts of a direction so the
// user can compare palettes, sets and light before paying for a plan.
type ecommerceDirectionVisual struct {
	Style      string   `json:"style"`
	Why        string   `json:"why"`
	Palette    []string `json:"palette"`
	Typography string   `json:"typography,omitempty"`
	Layout     string   `json:"layout,omitempty"`
	Scene      string   `json:"scene,omitempty"`
	Lighting   string   `json:"lighting,omitempty"`
	Camera     string   `json:"camera,omitempty"`
	Props      string   `json:"props,omitempty"`
	ColorGrade string   `json:"colorGrade,omitempty"`
	Benchmark  string   `json:"benchmark,omitempty"`
}

func ecommerceDirectionVisualFrom(rec ecommerceagent.Recommendation) *ecommerceDirectionVisual {
	v := rec.VisualSystem
	return &ecommerceDirectionVisual{Style: rec.Style, Why: rec.Why, Palette: v.Palette, Typography: v.Typography, Layout: v.Layout, Scene: v.Scene, Lighting: v.Lighting, Camera: v.Camera, Props: v.Props, ColorGrade: v.ColorGrade, Benchmark: v.Benchmark}
}

// prepareEcommerceDirections repairs the minor gaps fast models leave (an
// empty sub-field, a paraphrased product anchor, a claim word) and rejects only
// directions without a name, description or palette, naming the gap.
func prepareEcommerceDirections(recs []ecommerceagent.Recommendation, draft EcommerceDraft, ratio string) error {
	for i := range recs {
		rec := &recs[i]
		if filled := ecommerceagent.RepairStyleRecommendation(rec); len(filled) > 0 {
			slog.Info("ecommerce direction repaired", "index", i+1, "fields", strings.Join(filled, ","))
		}
		if !ecommerceagent.CompleteStyleRecommendation(*rec) || !safeRecommendationPrompt(rec.PreviewPrompt) {
			missing := "风格名称或描述"
			if rec.Label != "" && rec.Style != "" {
				missing = "至少两个配色"
			}
			return BadAuthRequest(fmt.Sprintf("第 %d 套创意方向缺少%s，模型输出不完整，请重新获取方向", i+1, missing))
		}
		rec.Pages, rec.AspectRatio = draft.Count, ratio
		if draft.NoText || draft.ImageType == "white" || draft.ImageType == "three-view" || (draft.CreativeOptions != nil && draft.CreativeOptions.TextMode == "none") {
			rec.NeedText = false
		}
	}
	return nil
}

func (s *Service) storeEcommerceWorkbenchDirections(userID, projectID string, draft EcommerceDraft, analysis ecommerceagent.Analysis) (*EcommerceCreativeDirectionsResponse, error) {
	if _, err := s.EcommerceProject(userID, projectID); err != nil {
		return nil, err
	}
	if len(analysis.Recommendations) > 4 {
		analysis.Recommendations = analysis.Recommendations[:4]
	}
	if strings.TrimSpace(analysis.Brief.Category) == "" {
		return nil, BadAuthRequest("分析模型没有识别出商品品类，请换一张更清晰的商品图后重试")
	}
	if len(analysis.Recommendations) != 4 {
		return nil, BadAuthRequest(fmt.Sprintf("分析模型只返回了 %d 套创意方向（需要 4 套），请重试或更换模型", len(analysis.Recommendations)))
	}
	if err := prepareEcommerceDirections(analysis.Recommendations, draft, draft.Ratio); err != nil {
		return nil, err
	}
	req := ecommerceWorkbenchDirectionsRequest(projectID, draft)
	knowledgeRequest := req
	knowledgeRequest.Category = analysis.Brief.Category
	_, knowledge, err := s.ecommerceCreativeContext(knowledgeRequest)
	if err != nil {
		return nil, err
	}
	item := &model.EcommerceCreativeRecommendation{ID: "ecr-" + newID(), UserID: userID, ProjectID: projectID, InputSignature: ecommerceRecommendationSignature(req), ProductCategory: analysis.Brief.Category, Platform: draft.Platform, Region: draft.Region, Language: draft.Language, Status: "ready"}
	directions := make([]EcommerceCreativeDirection, 0, 4)
	stored := make([]ecommerceStoredDirection, 0, 4)
	productAnalysis := runEcommerceProductAnalysis(ecommerceProductCategory(knowledgeRequest), req, knowledge)
	plans := planEcommerceDirectionInputs(ecommerceWorkbenchDirectionInputs(analysis.Recommendations), knowledge, productAnalysis, "creative-directions")
	for i, rec := range analysis.Recommendations {
		id := fmt.Sprintf("%s-direction-%d", item.ID, i+1)
		strategy := map[string]any{"engine": "ecommerce-agent", "brief": analysis.Brief, "direction": rec, "contentType": draft.ImageType, "imageCount": draft.Count, "verifiedFacts": nonEmptyFacts(req), "platform": draft.Platform, "instruction": draft.Instruction, "knowledgeEntryIds": knowledgeIDs(knowledge), "knowledgeContext": knowledgeContext(knowledge), "knowledgeVersion": ecommerceagent.CreativeKnowledgeVersion}
		plans[i].attach(strategy)
		tags := append(append([]string{}, rec.VisualSystem.Palette...), rec.AspectRatio)
		summary := rec.Style + "；" + rec.Why
		visual := ecommerceDirectionVisualFrom(rec)
		directions = append(directions, EcommerceCreativeDirection{ID: id, Name: rec.Label, Summary: summary, Tags: tags, CategoryFit: analysis.Brief.Category, Visual: visual})
		stored = append(stored, ecommerceStoredDirection{ID: id, Name: rec.Label, Summary: summary, Tags: tags, CategoryFit: analysis.Brief.Category, Visual: visual, Strategy: strategy})
	}
	raw, _ := json.Marshal(stored)
	evidence := []EcommerceKnowledgeEvidence{}
	for _, entry := range knowledge {
		evidence = append(evidence, EcommerceKnowledgeEvidence{ID: entry.ID, Title: entry.Title, KnowledgeDomain: entry.KnowledgeDomain, KnowledgeType: entry.KnowledgeType, SourceURL: entry.SourceURL, License: entry.License, TrustLevel: entry.TrustLevel})
	}
	evidenceJSON, _ := json.Marshal(evidence)
	item.DirectionsJSON, item.EvidenceJSON = string(raw), string(evidenceJSON)
	if err := s.repo.CreateEcommerceRecommendation(item); err != nil {
		return nil, err
	}
	draft.Category = analysis.Brief.Category
	return &EcommerceCreativeDirectionsResponse{RecommendationID: item.ID, Directions: directions, Evidence: evidence, RetrievalStatus: "ready", ProductCategory: item.ProductCategory, StyleLibrary: ecommerceStyleLibraryDirections(draft)}, nil
}

func ecommerceWorkbenchDirectionsRequest(projectID string, draft EcommerceDraft) EcommerceCreativeDirectionsRequest {
	return EcommerceCreativeDirectionsRequest{ProjectID: projectID, ProductName: draft.ProductName, Category: draft.Category, Description: draft.Description, Claims: draft.Claims, Materials: draft.Materials, Colors: draft.Colors, Specifications: draft.Specifications, Platform: draft.Platform, Region: draft.Region, Language: draft.Language, ImageType: draft.ImageType, Count: draft.Count, Instruction: draft.Instruction, Style: draft.Style, StylePrompt: draft.StylePrompt, ReferenceMode: draft.ReferenceMode, Distribution: draft.TypeOptions["distribution"]}
}

// ecommerceStyleLibraryDirections lists the style library for the picker.
// White-background and three-view sets keep their fixed look, so they get none.
func ecommerceStyleLibraryDirections(draft EcommerceDraft) []EcommerceCreativeDirection {
	if draft.ImageType == "white" || draft.ImageType == "three-view" {
		return nil
	}
	library := make([]EcommerceCreativeDirection, 0, len(ecommerceagent.StyleLibrary))
	for _, preset := range ecommerceagent.ProductStyleLibrary(draft.Category) {
		rec := preset.Recommendation()
		library = append(library, EcommerceCreativeDirection{ID: "preset:" + preset.Key, Group: preset.Group, Name: rec.Label, Summary: rec.Style + "；" + rec.Why, Tags: append([]string{}, rec.VisualSystem.Palette...), Visual: ecommerceDirectionVisualFrom(rec)})
	}
	return library
}

func ecommerceWorkbenchEnabled() bool {
	mode := ecommerceConfig().Mode
	return mode == "live" || mode == "mock"
}

// Preserve the workbench's platform identity, including platforms outside the
// standalone Agent catalogue. Never apply another marketplace's rules by default.
func ecommerceWorkbenchPlatform(draft EcommerceDraft) ecommerceagent.Platform {
	aliases := map[string]string{"taobao": "淘宝", "tmall": "淘宝", "1688": "1688", "pdd": "拼多多", "pinduoduo": "拼多多", "amazon": "Amazon", "temu": "Temu", "shopee": "Shopee", "tiktok": "TikTok Shop", "tiktok-shop": "TikTok Shop", "aliexpress": "AliExpress", "ozon": "OZON"}
	name := draft.Platform
	if p, ok := ecommerceagent.PlatformFor(name); ok {
		return p
	}
	if alias := aliases[strings.ToLower(name)]; alias != "" {
		name = alias
	}
	if p, ok := ecommerceagent.PlatformFor(name); ok {
		return p
	}
	ratio := draft.Ratio
	if ratio == "" {
		ratio = "1:1"
	}
	return ecommerceagent.Platform{Label: draft.Platform, Ratio: ratio, DetailRatio: ratio, TitleMax: 120, BulletMax: 5, Tone: "忠于商品资料与目标语言", Rules: []string{"平台最新规则以官方要求为准，不推测未核实的平台强制要求", "不编造价格、认证、参数、功效或内部结构"}}
}

func (s *Service) RecommendEcommerceCreativeDirectionsContext(ctx context.Context, userID string, req EcommerceCreativeDirectionsRequest) (*EcommerceCreativeDirectionsResponse, error) {
	if err := s.RequireFeature(FeatureEcommerce); err != nil {
		return nil, err
	}
	if req.AgentModel != nil {
		return nil, InvalidModelSelection("电商 Agent 模型已改为后台统一指定，请刷新页面后重试")
	}
	if !ecommerceWorkbenchEnabled() {
		return s.RecommendEcommerceCreativeDirections(userID, req)
	}
	project, err := s.EcommerceProject(userID, req.ProjectID)
	if err != nil {
		return nil, err
	}
	var draft EcommerceDraft
	if err := json.Unmarshal([]byte(project.DraftJSON), &draft); err != nil {
		return nil, BadAuthRequest("商品草稿数据损坏")
	}
	// The endpoint carries the current text fields; only the owned saved project
	// supplies resource identities. Layout/model references never become product facts.
	draft.ProductName, draft.Category, draft.Description = req.ProductName, req.Category, req.Description
	draft.Claims, draft.Materials, draft.Colors, draft.Specifications = req.Claims, req.Materials, req.Colors, req.Specifications
	draft.Platform, draft.Language, draft.Instruction = req.Platform, req.Language, req.Instruction
	draft.Style, draft.StylePrompt = req.Style, req.StylePrompt
	draft.ImageType, draft.Count, draft.ReferenceMode = req.ImageType, req.Count, req.ReferenceMode
	if draft.Count < 1 || draft.Count > 30 {
		draft.Count = 3
		if draft.ImageType == "detail" {
			draft.Count = 9
		}
	}
	requirements := map[string]any{"request": req, "typeOptions": draft.TypeOptions, "creativeOptions": draft.CreativeOptions, "cloneOptions": draft.CloneOptions, "noText": draft.NoText, "modelEnabled": draft.ModelEnabled, "brand": draft.Brand, "audience": draft.Audience, "scene": draft.Scene, "lockedDetails": draft.LockedDetails, "ratio": draft.Ratio}
	req.Distribution = draft.TypeOptions["distribution"]
	knowledge, _, err := s.ecommerceCreativeContext(req)
	if err != nil {
		return nil, err
	}
	p := ecommerceWorkbenchPlatform(draft)
	cfg, err := s.ecommerceVisionText(userID, ecommerceConfig())
	if err != nil {
		return nil, err
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	signatureInput, _ := json.Marshal([]any{ecommerceagent.CreativeKnowledgeVersion, knowledge, requirements, draft.References, cfg.Mode, cfg.TextModel})
	signature := fmt.Sprintf("%x", sha256.Sum256(signatureInput))
	var analysis ecommerceagent.Analysis
	cached := false
	if cfg.Mode == "live" {
		if item, err := s.repo.EcommerceAgentAnalysis(userID, signature); err == nil && time.Since(item.CreatedAt) < time.Hour {
			cached = json.Unmarshal([]byte(item.PayloadJSON), &analysis) == nil && len(analysis.Recommendations) >= 4
		}
	}
	if !cached {
		analysis, err = s.loadEcommerceWorkbenchAnalysis(ctx, userID, req, draft, p, cfg, knowledge, signature)
		if err != nil {
			return nil, err
		}
	}
	return s.storeEcommerceWorkbenchDirections(userID, project.ID, draft, analysis)
}

// Prepare before billing/admission so the persisted task, model routing and
// worker use one input snapshot and one normal task charge. No nested Agent run.

func (s *Service) loadEcommerceWorkbenchAnalysis(ctx context.Context, userID string, req EcommerceCreativeDirectionsRequest, draft EcommerceDraft, p ecommerceagent.Platform, cfg ecommerceProviderConfig, knowledge, signature string) (ecommerceagent.Analysis, error) {
	var analysis ecommerceagent.Analysis
	// Resolve binary references only for a cache miss. A repeated analysis
	// should return the stored, already-reviewed directions without reading
	// every product file again.
	images := []string{}
	for _, ref := range draft.References {
		if ref.Role == "model" || ref.Role == "style" || ref.Role == "competitor" {
			continue
		}
		uri, _, _, err := s.ecommerceReference(userID, "/api/resources/"+ref.ResourceID+"/file")
		if err != nil {
			return ecommerceagent.Analysis{}, err
		}
		images = append(images, uri)
	}
	ratio := draft.Ratio
	if ratio == "" {
		ratio = p.DetailRatio
	}
	verified := false
	if cfg.Mode == "live" {
		system, prompt := ecommerceWorkbenchDirectionsPrompt(draft)
		prompt += "\n" + knowledge
		if err := s.ecommerceChatJSON(ctx, cfg, "workbench-analysis", system, prompt, images, &analysis); err != nil {
			return ecommerceagent.Analysis{}, err
		}
		if strings.TrimSpace(analysis.Brief.Category) == "" {
			return ecommerceagent.Analysis{}, BadAuthRequest("商品理解未返回品类，请重试")
		}
		if len(analysis.Recommendations) != 4 {
			return ecommerceagent.Analysis{}, BadAuthRequest("创意方向数量不符合要求，请重试")
		}
		if err := prepareEcommerceDirections(analysis.Recommendations, draft, ratio); err != nil {
			return ecommerceagent.Analysis{}, err
		}
		verified = len(images) > 0
	} else {
		query, _ := json.Marshal(req)
		brief := ecommerceagent.MockBrief(string(query))
		recs := ecommerceagent.MockStyleRecommendations(brief, p, draft.ImageType, draft.Count, ratio)
		analysis = ecommerceagent.Analysis{Brief: brief, Recommendations: recs[:4]}
	}
	if verified {
		raw, _ := json.Marshal(analysis)
		if err := s.repo.SaveEcommerceAgentAnalysis(&model.EcommerceAgentAnalysis{ID: newID(), UserID: userID, Signature: signature, PayloadJSON: string(raw)}); err != nil {
			return ecommerceagent.Analysis{}, err
		}
	}
	return analysis, nil
}
