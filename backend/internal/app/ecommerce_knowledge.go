package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"yingce/backend/internal/ecommercecatalog"
	"yingce/backend/internal/model"
)

type EcommerceCreativeDirectionsRequest struct {
	// AgentModel is no longer accepted because the administrator fixes the
	// analysis model. It stays decodable so an outdated client gets a clear
	// error instead of having its choice silently replaced.
	AgentModel     *EcommerceAgentModelSelection `json:"agentModel,omitempty"`
	ProjectID      string                        `json:"projectId"`
	ProductName    string                        `json:"productName"`
	Category       string                        `json:"category"`
	Description    string                        `json:"description"`
	Claims         string                        `json:"claims"`
	Materials      string                        `json:"materials"`
	Colors         string                        `json:"colors"`
	Specifications string                        `json:"specifications"`
	Platform       string                        `json:"platform"`
	Region         string                        `json:"region"`
	Language       string                        `json:"language"`
	ImageType      string                        `json:"imageType"`
	Count          int                           `json:"count"`
	Instruction    string                        `json:"instruction"`
	Style          string                        `json:"style"`
	StylePrompt    string                        `json:"stylePrompt"`
	ReferenceMode  string                        `json:"referenceMode"`
	MaterialPlan   string                        `json:"materialPlan"`
	ReferenceCount int                           `json:"referenceCount"`
	// Distribution is the draft's typeOptions.distribution, filled in by the
	// server only (never decoded or signed): a two-image set split into 主图
	// and 副图 reads the main-image knowledge (ecommerceKnowledgeContentType).
	Distribution string `json:"-"`
}

// ecommerceKnowledgeContentType is the content type whose knowledge applies:
// the 主副图 quick type (a two-image set of 主图 + 副图) reads the main-image
// guidance instead of the product-set one.
func ecommerceKnowledgeContentType(req EcommerceCreativeDirectionsRequest) string {
	if ecommerceMainSecondary(req.ImageType, req.Count, req.Distribution) {
		return "main"
	}
	return req.ImageType
}

type EcommerceAgentModelSelection struct {
	LogicalModelID string `json:"logicalModelId,omitempty"`
	ChannelID      string `json:"channelId,omitempty"`
	ModelKey       string `json:"modelKey,omitempty"`
}

type EcommerceCreativeDirection struct {
	ID          string                    `json:"id"`
	Name        string                    `json:"name"`
	Summary     string                    `json:"summary"`
	Tags        []string                  `json:"tags"`
	CategoryFit string                    `json:"categoryFit,omitempty"`
	Visual      *ecommerceDirectionVisual `json:"visual,omitempty"`
	// Group is the style library category; recommended directions have none.
	Group    string `json:"group,omitempty"`
	strategy map[string]any
}

type ecommerceStoredDirection struct {
	ID          string                    `json:"id"`
	Name        string                    `json:"name"`
	Summary     string                    `json:"summary"`
	Tags        []string                  `json:"tags"`
	CategoryFit string                    `json:"categoryFit,omitempty"`
	Visual      *ecommerceDirectionVisual `json:"visual,omitempty"`
	Strategy    map[string]any            `json:"strategy"`
}

type EcommerceKnowledgeEvidence struct {
	ID              string `json:"id"`
	Title           string `json:"title"`
	KnowledgeDomain string `json:"knowledgeDomain,omitempty"`
	KnowledgeType   string `json:"knowledgeType"`
	SourceURL       string `json:"sourceUrl"`
	License         string `json:"license"`
	TrustLevel      string `json:"trustLevel"`
}

type EcommerceCreativeDirectionsResponse struct {
	RecommendationID string                       `json:"recommendationId"`
	Directions       []EcommerceCreativeDirection `json:"directions"`
	Evidence         []EcommerceKnowledgeEvidence `json:"evidence"`
	RetrievalStatus  string                       `json:"retrievalStatus"`
	ProductCategory  string                       `json:"productCategory"`
	// StyleLibrary lists the ready-made styles ("preset:<key>") the user can
	// pick instead of the four recommended directions.
	StyleLibrary []EcommerceCreativeDirection `json:"styleLibrary,omitempty"`
}

type EcommerceCreativeDirectionSelection struct {
	RecommendationID string         `json:"recommendationId"`
	DirectionID      string         `json:"directionId"`
	Status           string         `json:"status"`
	PlanningContext  map[string]any `json:"planningContext"`
	// Direction is the confirmed style as stored, including a library preset
	// or the user's own style expanded into a visual system.
	Direction *EcommerceCreativeDirection `json:"direction,omitempty"`
}

type ecommerceDirectionSeed struct {
	Name    string
	Summary string
	Tags    []string
	Mode    string
}

func ecommerceProductCategory(req EcommerceCreativeDirectionsRequest) string {
	if match := ecommercecatalog.ResolveProduct(req.Category, req.ProductName); match.Node.ID != "na" && match.Node.ID != "" {
		return match.Group
	}
	groups := []struct {
		name  string
		words []string
	}{
		{"appliance", []string{"榨汁", "搅拌机", "果汁机", "juicer", "blender"}},
		{"beauty", []string{"护肤", "洗面奶", "精华", "面霜", "口红", "彩妆", "美妆", "香水", "skincare", "cosmetic"}},
		{"maternal", []string{"奶瓶", "婴儿", "母婴", "宝宝", "童装", "婴幼儿", "baby", "maternity"}},
		{"apparel", []string{"女装", "男装", "服装", "裙", "外套", "鞋", "箱包", "珠宝", "首饰", "jewelry", "fashion"}},
		{"bathroom", []string{"马桶", "坐便器", "座便器", "toilet"}},
		{"appliance", []string{"家电", "扫地机", "洗地机", "吸尘器", "空气净化器", "净化器", "加湿器", "除湿机", "空气炸锅", "电饭煲", "破壁机", "微波炉", "烤箱", "冰箱", "洗衣机", "电风扇", "净水器", "appliance", "vacuum"}},
		{"tech", []string{"耳机", "手机", "电脑", "键盘", "手表", "数码", "3c", "tech", "headphone"}},
		{"home", []string{"家具", "家居", "台灯", "吊灯", "沙发", "厨房", "收纳", "家纺", "home", "furniture"}},
		{"food", []string{"食品", "咖啡", "茶", "饮料", "零食", "food", "coffee"}},
		{"sport", []string{"瑜伽", "健身", "哑铃", "跑步", "运动", "sport", "fitness"}},
		{"pet", []string{"宠物", "猫", "狗", "犬", "pet"}},
	}
	// The product identity takes priority over lifestyle words in the prompt.
	for _, field := range []string{req.Category, req.ProductName, req.Description, req.Claims, req.Instruction} {
		text := strings.ToLower(field)
		for _, group := range groups {
			if text == group.name {
				return group.name
			}
			for _, word := range group.words {
				if strings.Contains(text, word) {
					return group.name
				}
			}
		}
	}
	return "general"
}

func ecommerceDirectionSeeds(category string) []ecommerceDirectionSeed {
	sets := map[string][]ecommerceDirectionSeed{
		"bathroom":  {{"清澈卫浴风", "晨间自然光与浅灰石材留白，让真实商品成为空间中心；文案方向：让清爽，成为每天的开始。", []string{"卫浴空间", "柔光", "生活场景"}, "hero"}, {"开合叙事风", "从闭合全景切换到盖板打开的使用视角；开启姿态仅作视觉提案，文案方向：打开一天的清爽节奏。", []string{"状态切换", "使用视角", "概念提案"}, "lifestyle"}, {"流线解构风", "将外观可见的盖板与主体做分层悬浮的概念构图，以弧线串联画面；不绘制未知内部零件。文案方向：让空间，回到有序。", []string{"外观解构", "概念构图", "留白"}, "creative"}, {"轻奢秩序风", "用侧向光线、局部轮廓和分栏文字呈现可见细节；文案方向：简洁之中，自有秩序。", []string{"细节特写", "分栏", "克制文案"}, "feature"}},
		"tech":      {{"沉浸式科技主视觉", "用产品特写与实验室光影建立清晰的科技质感。", []string{"产品摄影", "高保真", "科技质感"}, "hero"}, {"都市真实使用", "把商品放入通勤或桌面场景，展示自然的使用关系。", []string{"生活方式", "真实场景", "人物关系"}, "lifestyle"}, {"年轻色彩实验", "用有节奏的色彩和动态构图吸引年轻用户注意。", []string{"创意构图", "年轻审美", "高对比"}, "creative"}, {"功能信息拆解", "用细节特写和克制的信息层级解释已确认的商品卖点。", []string{"信息图", "细节特写", "真实卖点"}, "feature"}},
		"beauty":    {{"清透水感静物", "以干净材质、柔光和留白突出包装与质感。", []string{"柔光", "清透", "静物摄影"}, "hero"}, {"护肤生活方式", "在真实梳妆或洗护场景中呈现使用氛围，不添加功效承诺。", []string{"生活方式", "自然光", "使用场景"}, "lifestyle"}, {"高端实验室", "用精确的光影与结构化背景建立专业产品形象。", []string{"实验室", "高级感", "精密布光"}, "lab"}, {"成分与触感细节", "围绕已确认信息安排局部特写和清晰的卖点层级。", []string{"细节图", "卖点表达", "克制排版"}, "feature"}},
		"maternal":  {{"温柔亲子日常", "以安全、自然的家庭场景表达商品与照护关系。", []string{"母婴", "家庭场景", "温柔"}, "lifestyle"}, {"安心产品棚拍", "用柔和棚拍保持商品外观清晰，突出材质和结构。", []string{"棚拍", "高保真", "安全感"}, "hero"}, {"材质细节展示", "通过近距离细节解释可见结构，不虚构认证或容量。", []string{"材质", "细节特写", "真实信息"}, "feature"}, {"清晰使用步骤", "用简洁分镜说明实际操作关系，避免夸张功能演示。", []string{"步骤图", "使用方式", "清晰"}, "instruction"}},
		"apparel":   {{"杂志编辑大片", "用明确的版型、面料和构图建立时尚视觉。", []string{"编辑视觉", "版型保真", "大片"}, "editorial"}, {"城市街拍穿搭", "在真实街景中展示穿着关系和整体搭配。", []string{"街拍", "穿搭", "生活方式"}, "lifestyle"}, {"高级棚拍细节", "用干净背景和局部特写锁定颜色、纹理与配件。", []string{"棚拍", "细节", "材质表现"}, "hero"}, {"场景搭配指南", "以有层级的搭配信息帮助用户理解实际穿着方式。", []string{"搭配", "信息图", "购买决策"}, "feature"}},
		"home":      {{"空间生活方式", "把商品放进匹配的居住空间，展示尺度与使用氛围。", []string{"空间摄影", "生活方式", "真实尺度"}, "lifestyle"}, {"材质光影静物", "以侧光和近景表现表面纹理与轮廓。", []string{"材质", "光影", "静物"}, "hero"}, {"现代杂志家居", "用克制的编辑构图建立完整的空间品牌感。", []string{"杂志感", "空间构图", "品牌感"}, "editorial"}, {"尺寸与功能说明", "使用清楚的比例和信息布局解释已确认的规格。", []string{"尺寸图", "信息层级", "真实规格"}, "feature"}},
		"appliance": {{"明亮家居主视觉", "把完整机身放在明亮现代的居家环境中，轮廓与可见按键清晰。", []string{"家居", "轮廓", "高保真"}, "hero"}, {"日常清洁场景", "在客厅或厨房的真实动线里呈现产品工作状态，不演示未确认性能。", []string{"生活场景", "真实尺度", "家庭"}, "lifestyle"}, {"外观结构说明", "用可见结构近景和克制标注解释已确认的外部设计。", []string{"结构", "细节", "真实信息"}, "feature"}, {"静音夜间氛围", "以低照度暖光和安静卧室构图表达使用时刻，不写噪音数值。", []string{"夜间", "暖光", "氛围"}, "creative"}},
		"general":   {{"品牌质感主视觉", "用清晰商品摄影和统一色彩建立第一眼记忆点。", []string{"主视觉", "品牌感", "高保真"}, "hero"}, {"真实生活场景", "选择与商品用途相符的场景，呈现自然的使用方式。", []string{"生活方式", "场景代入", "自然"}, "lifestyle"}, {"创意广告构图", "通过有区分度的构图与色彩表达商品气质。", []string{"创意广告", "构图", "视觉记忆"}, "creative"}, {"卖点细节信息图", "用细节、规格和已确认卖点帮助用户做购买判断。", []string{"细节图", "信息图", "购买决策"}, "feature"}},
	}
	if value, ok := sets[category]; ok {
		return value
	}
	return sets["general"]
}

func ecommerceDirectionCandidates(category string, req EcommerceCreativeDirectionsRequest) []ecommerceDirectionSeed {
	candidates := append([]ecommerceDirectionSeed{}, ecommerceDirectionSeeds(category)...)
	if category != "general" {
		candidates = append(candidates, ecommerceDirectionSeeds("general")...)
	}
	var extras []ecommerceDirectionSeed
	switch category {
	case "tech":
		extras = []ecommerceDirectionSeed{{"结构微距与材质", "通过可见结构与局部布光展示商品工艺，不推断内部性能。", []string{"微距摄影", "材质", "细节"}, "macro"}, {"桌面与装备搭配", "在真实的数码桌面里建立商品与日常装备的关系。", []string{"桌搭", "场景", "年轻"}, "desk"}}
	case "beauty":
		extras = []ecommerceDirectionSeed{{"包装材质微距", "保持包装与文字外观，聚焦瓶身、表面和真实可见细节。", []string{"包装", "材质", "微距"}, "macro"}, {"礼赠陈列视觉", "用克制的陈列与留白呈现商品，不虚构套装或赠品。", []string{"陈列", "礼赠", "品牌"}, "gift"}}
	case "maternal":
		extras = []ecommerceDirectionSeed{{"喂养结构近景", "以可见部件和安全的操作关系说明产品结构。", []string{"结构", "安全", "近景"}, "macro"}, {"亲子手部纪实", "用照护者的自然手部动作展示真实的使用情境。", []string{"亲子", "手部", "纪实"}, "documentary"}}
	case "apparel":
		extras = []ecommerceDirectionSeed{{"版型与纹理观察", "用局部光线和合适机位保留版型、纹理及图案。", []string{"版型", "面料", "微距"}, "macro"}, {"精品陈列与搭配", "以整洁陈列和实穿搭配建立购买想象。", []string{"陈列", "穿搭", "搭配"}, "display"}}
	case "home":
		extras = []ecommerceDirectionSeed{{"家居细节纪实", "聚焦可见结构、表面纹理与自然接触关系。", []string{"细节", "纪实", "材质"}, "macro"}, {"空间尺度对话", "用实际家居参照物表现空间关系，不标注未经确认的尺寸。", []string{"空间", "尺度", "场景"}, "scale"}}
	case "food":
		extras = []ecommerceDirectionSeed{{"食物质感特写", "保持包装和可见食材真实，以光影表达食物质感。", []string{"食物摄影", "近景", "质感"}, "macro"}, {"日常品尝场景", "选择合理餐桌或饮用情境，避免虚构成分与功效。", []string{"餐桌", "生活方式", "场景"}, "table"}}
	case "appliance":
		extras = []ecommerceDirectionSeed{{"机身细节微距", "聚焦可见的按键、指示灯、出风口或滚刷外观，不绘制内部模组。", []string{"微距", "外观", "细节"}, "macro"}, {"家居尺度对话", "用沙发、地板和家具建立真实比例，不标注未确认尺寸。", []string{"空间", "尺度", "家居"}, "scale"}}
	case "bathroom":
		extras = []ecommerceDirectionSeed{{"柔光细节风", "以可见盖板曲线、外观接缝与接触阴影做近景，文案方向：细节，藏在每一次目光里。", []string{"可见细节", "近景", "柔光"}, "macro"}, {"空间编辑风", "以建筑感分栏、自然侧光和完整商品比例组织长图，文案方向：给日常留一份从容。", []string{"长图", "编辑排版", "空间"}, "editorial"}}
	}
	candidates = append(candidates, extras...)
	return candidates
}

func ecommercePickDirections(category string, req EcommerceCreativeDirectionsRequest, knowledge []model.EcommerceKnowledgeEntry) []ecommerceDirectionSeed {
	candidates := ecommerceDirectionCandidates(category, req)
	query := strings.ToLower(strings.Join([]string{req.ProductName, req.Description, req.Instruction, req.ImageType}, " "))
	knowledgeSignal := make(map[string]int)
	for _, entry := range knowledge {
		switch entry.KnowledgeType {
		case "photography", "product_knowledge":
			knowledgeSignal["hero"]++
			knowledgeSignal["macro"]++
		case "detail_page_pattern", "layout_pattern", "copywriting_pattern":
			knowledgeSignal["feature"]++
		case "scene_pattern", "model_casting_pattern":
			knowledgeSignal["lifestyle"]++
		case "visual_style", "prompt_pattern":
			knowledgeSignal["creative"]++
		}
	}
	type scored struct {
		direction ecommerceDirectionSeed
		score     int
		order     int
	}
	ranked := make([]scored, 0, len(candidates))
	for i, candidate := range candidates {
		score := ecommerceDirectionScore(candidate, i, len(candidates), category, query, req, knowledgeSignal)
		ranked = append(ranked, scored{candidate, score, i})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score == ranked[j].score {
			return ranked[i].order < ranked[j].order
		}
		return ranked[i].score > ranked[j].score
	})
	selected, modes := make([]ecommerceDirectionSeed, 0, 4), map[string]bool{}
	for _, item := range ranked {
		if modes[item.direction.Mode] {
			continue
		}
		selected = append(selected, item.direction)
		modes[item.direction.Mode] = true
		if len(selected) == 4 {
			break
		}
	}
	for index := range selected {
		selected[index] = ecommerceExpressiveDirection(category, req, selected[index])
	}
	return selected
}

func jsonArrayContains(raw, value string) bool {
	var values []string
	if json.Unmarshal([]byte(raw), &values) != nil || len(values) == 0 {
		return true
	}
	for _, item := range values {
		if strings.EqualFold(item, value) {
			return true
		}
	}
	return false
}

func (s *Service) RecommendEcommerceCreativeDirections(userID string, req EcommerceCreativeDirectionsRequest) (*EcommerceCreativeDirectionsResponse, error) {
	if err := s.RequireFeature(FeatureEcommerce); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.ProjectID) == "" {
		return nil, BadAuthRequest("推荐任务缺少项目 ID")
	}
	if _, err := s.EcommerceProject(userID, req.ProjectID); err != nil {
		return nil, err
	}
	if req.Count < 1 || req.Count > 30 {
		req.Count = 3
	}
	retrieval, err := s.retrieveEcommerceKnowledge(req)
	if err != nil {
		return nil, err
	}
	// An explicitly disabled source set blocks a source-only request, preserving
	// the admin's disable switch and avoiding silent use of stale imported data.
	if retrieval.SourcesBlocked {
		return nil, fmt.Errorf("当前没有已审核且适用的电商知识，请管理员先同步知识源")
	}
	category, matched := retrieval.Category, retrieval.Selected
	platform := strings.TrimSpace(req.Platform)
	if platform == "" {
		platform = "general"
	}
	recommendation := &model.EcommerceCreativeRecommendation{ID: "ecr-" + newID(), UserID: userID, ProjectID: req.ProjectID, InputSignature: ecommerceRecommendationSignature(req), ProductCategory: category, Platform: platform, Region: req.Region, Language: req.Language, Status: "ready"}
	analysis := runEcommerceProductAnalysis(category, req, matched)
	seeds := ecommercePickDirections(category, req, matched)
	plans := planEcommerceDirectionsWithKnowledge(seeds, matched, analysis)
	directions := make([]EcommerceCreativeDirection, 0, 4)
	for index, plan := range plans {
		id := fmt.Sprintf("%s-direction-%d", recommendation.ID, index+1)
		strategy := map[string]any{"mode": plan.Seed.Mode, "category": category, "platform": platform, "knowledgeEntryIds": knowledgeIDs(matched), "knowledgeContext": knowledgeContext(matched), "verifiedFacts": nonEmptyFacts(req), "instruction": strings.TrimSpace(req.Instruction)}
		plan.attach(strategy)
		directions = append(directions, EcommerceCreativeDirection{ID: id, Name: plan.Seed.Name, Summary: plan.Seed.Summary, Tags: plan.Seed.Tags, CategoryFit: category, strategy: strategy})
	}
	storedDirections := make([]ecommerceStoredDirection, 0, len(directions))
	for _, direction := range directions {
		storedDirections = append(storedDirections, ecommerceStoredDirection{ID: direction.ID, Name: direction.Name, Summary: direction.Summary, Tags: direction.Tags, CategoryFit: direction.CategoryFit, Strategy: direction.strategy})
	}
	directionsJSON, _ := json.Marshal(storedDirections)
	evidence := make([]EcommerceKnowledgeEvidence, 0, len(matched))
	for _, entry := range matched {
		evidence = append(evidence, EcommerceKnowledgeEvidence{ID: entry.ID, Title: entry.Title, KnowledgeDomain: entry.KnowledgeDomain, KnowledgeType: entry.KnowledgeType, SourceURL: entry.SourceURL, License: entry.License, TrustLevel: entry.TrustLevel})
	}
	evidenceJSON, _ := json.Marshal(evidence)
	recommendation.DirectionsJSON, recommendation.EvidenceJSON = string(directionsJSON), string(evidenceJSON)
	if err := s.repo.CreateEcommerceRecommendation(recommendation); err != nil {
		return nil, err
	}
	return &EcommerceCreativeDirectionsResponse{RecommendationID: recommendation.ID, Directions: directions, Evidence: evidence, RetrievalStatus: "ready", ProductCategory: category}, nil
}

func ecommerceKnowledgeDomain(kind, category string) string {
	switch kind {
	case "copywriting_pattern":
		return "KB05"
	case "visual_style", "prompt_pattern", "reference_method", "custom_style_method":
		return "KB03"
	case "layout_pattern", "detail_page_pattern", "creation_type_rule":
		return "KB04"
	case "photography":
		return "KB06"
	case "scene_pattern", "model_casting_pattern":
		return "KB07"
	case "platform_rule":
		return "KB08"
	case "quality_rule":
		return "KB10"
	case "trend":
		return "KB09"
	case "marketing_strategy":
		return "KB02"
	case "product_knowledge", "knowledge_index":
		return "KB01"
	default:
		if category == "taxonomy" {
			return "KB01"
		}
		return "KB01"
	}
}

func ecommerceRecommendationSignature(req EcommerceCreativeDirectionsRequest) string {
	b, _ := json.Marshal(req)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func knowledgeIDs(entries []model.EcommerceKnowledgeEntry) []string {
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.ID)
	}
	return ids
}

func knowledgeContext(entries []model.EcommerceKnowledgeEntry) []map[string]string {
	context := make([]map[string]string, 0, ecommerceKnowledgeSelectionLimit)
	for _, entry := range entries {
		if len(context) == ecommerceKnowledgeSelectionLimit {
			break
		}
		var content struct {
			Guidance string `json:"guidance"`
		}
		_ = json.Unmarshal([]byte(entry.ContentJSON), &content)
		guidance := []rune(content.Guidance)
		if len(guidance) > 600 {
			guidance = guidance[:600]
		}
		context = append(context, map[string]string{"id": entry.ID, "domain": ecommerceEntryDomain(entry), "title": entry.Title, "type": entry.KnowledgeType, "content": string(guidance), "source": entry.SourceURL})
	}
	return context
}

// ecommercePromptKnowledgeRunes caps each entry sent to the model. The stored
// strategy keeps the full knowledgeContext; the prompt only needs the method.
const ecommercePromptKnowledgeRunes = 320

// promptKnowledgeContext is the model-facing form of knowledgeContext: the same
// entries and order, without source URLs and with shorter guidance, so the
// direction and planning prompts stay small and fast to answer.
func promptKnowledgeContext(entries []model.EcommerceKnowledgeEntry) []map[string]string {
	context := knowledgeContext(entries)
	for _, item := range context {
		delete(item, "source")
		if guidance := []rune(item["content"]); len(guidance) > ecommercePromptKnowledgeRunes {
			item["content"] = string(guidance[:ecommercePromptKnowledgeRunes]) + "…"
		}
	}
	return context
}

func (s *Service) SelectEcommerceCreativeDirection(userID, recommendationID, directionID string) (*EcommerceCreativeDirectionSelection, error) {
	if err := s.RequireFeature(FeatureEcommerce); err != nil {
		return nil, err
	}
	item, err := s.repo.EcommerceRecommendation(userID, recommendationID)
	if err != nil {
		return nil, NotFound("推荐任务不存在")
	}
	if item.SelectedDirectionID != "" && item.SelectedDirectionID != directionID {
		return nil, BadAuthRequest("该推荐任务已经选择了其他方向，请重新获取推荐")
	}
	var directions []ecommerceStoredDirection
	if err := json.Unmarshal([]byte(item.DirectionsJSON), &directions); err != nil {
		return nil, BadAuthRequest("推荐任务数据损坏")
	}
	var selected *ecommerceStoredDirection
	for _, direction := range directions {
		if direction.ID == directionID {
			copy := direction
			selected = &copy
			break
		}
	}
	if selected == nil {
		return nil, BadAuthRequest("创意方向不存在")
	}
	if err := s.repo.SelectEcommerceRecommendationDirection(userID, recommendationID, directionID); err != nil {
		return nil, err
	}
	return ecommerceDirectionSelection(recommendationID, *selected), nil
}

func ecommerceDirectionSelection(recommendationID string, stored ecommerceStoredDirection) *EcommerceCreativeDirectionSelection {
	direction := &EcommerceCreativeDirection{ID: stored.ID, Name: stored.Name, Summary: stored.Summary, Tags: stored.Tags, CategoryFit: stored.CategoryFit, Visual: stored.Visual}
	return &EcommerceCreativeDirectionSelection{RecommendationID: recommendationID, DirectionID: stored.ID, Status: "selected", PlanningContext: stored.Strategy, Direction: direction}
}

func (s *Service) EcommerceKnowledgeStats() (map[string]any, error) {
	sources, err := s.repo.EcommerceKnowledgeSources()
	if err != nil {
		return nil, err
	}
	total, approved, err := s.repo.EcommerceKnowledgeCounts()
	if err != nil {
		return nil, err
	}
	statuses, err := s.repo.EcommerceKnowledgeStatusCounts()
	if err != nil {
		return nil, err
	}
	coverage, expired, err := s.repo.EcommerceKnowledgeCoverage()
	if err != nil {
		return nil, err
	}
	domainCoverage, err := s.repo.EcommerceKnowledgeDomainCoverage()
	if err != nil {
		return nil, err
	}
	var lastSyncDuplicates int64
	for _, source := range sources {
		lastSyncDuplicates += source.LastDuplicateCount
	}
	return map[string]any{"sourceCount": len(sources), "entryCount": total, "approvedCount": approved, "pendingCount": statuses["pending"], "rejectedCount": statuses["rejected"], "expiredCount": expired, "categoryCoverage": coverage, "domainCoverage": domainCoverage, "lastSyncDuplicateCount": lastSyncDuplicates, "sources": sources}, nil
}

func (s *Service) EcommerceKnowledgeEntryPage(actor *model.User, keyword, status string, page, limit int) (map[string]any, error) {
	if err := s.RequireAdmin(actor); err != nil {
		return nil, err
	}
	if page < 1 || limit < 1 || limit > 100 {
		return nil, BadAuthRequest("知识分页参数无效")
	}
	entries, total, err := s.repo.EcommerceKnowledgeEntryPage(strings.TrimSpace(keyword), strings.TrimSpace(status), page, limit)
	if err != nil {
		return nil, err
	}
	return map[string]any{"entries": entries, "total": total, "page": page, "limit": limit}, nil
}

func (s *Service) ReviewEcommerceKnowledgeEntry(actor *model.User, id, status string) error {
	if err := s.RequireAdmin(actor); err != nil {
		return err
	}
	if status != "approved" && status != "rejected" && status != "pending" {
		return BadAuthRequest("审核状态无效")
	}
	return s.repo.ReviewEcommerceKnowledgeEntry(id, status)
}

func (s *Service) SetEcommerceKnowledgeSourceEnabled(actor *model.User, id string, enabled bool) error {
	if err := s.RequireAdmin(actor); err != nil {
		return err
	}
	source, err := s.repo.EcommerceKnowledgeSource(id)
	if err != nil {
		return NotFound("知识源不存在")
	}
	status := "disabled"
	if enabled {
		status = "stale"
		if source.LastSyncedAt != nil {
			status = "ready"
		}
	}
	return s.repo.SetEcommerceKnowledgeSourceStatus(id, status)
}

func ecommerceDirectionScore(candidate ecommerceDirectionSeed, i, candidateCount int, category, query string, req EcommerceCreativeDirectionsRequest, knowledgeSignal map[string]int) int {
	score := min(knowledgeSignal[candidate.Mode], 4)
	if i < 4 && category != "general" {
		score += 5
	}
	if i >= candidateCount-2 && category != "general" {
		score += 4
	}
	text := strings.ToLower(candidate.Name + " " + candidate.Summary + " " + strings.Join(candidate.Tags, " "))
	for _, word := range []string{"场景", "生活", "模特", "真人", "城市", "通勤", "材质", "细节", "结构", "包装", "创意", "广告", "海报", "卖点", "功能", "摄影", "搭配", "科技", "清透", "近景", "耳机", "奶瓶", "洗面奶", "女装"} {
		if strings.Contains(query, word) && strings.Contains(text, word) {
			score += 3
		}
	}
	if req.ImageType == "detail" && (candidate.Mode == "feature" || candidate.Mode == "macro") {
		score += 2
	}
	if req.ImageType == "scene" && (candidate.Mode == "lifestyle" || candidate.Mode == "documentary") {
		score += 2
	}
	for _, preference := range []struct{ keyword, mode string }{
		{"耳机", "macro"}, {"手机", "desk"}, {"键盘", "desk"}, {"手表", "lifestyle"},
		{"奶瓶", "macro"}, {"童装", "documentary"}, {"婴儿", "documentary"},
		{"洗面奶", "macro"}, {"口红", "gift"}, {"香水", "gift"},
		{"连衣裙", "macro"}, {"女装", "display"}, {"鞋", "display"},
		{"扫地", "scale"}, {"净化器", "macro"},
		{"沙发", "scale"}, {"沙发", "lifestyle"}, {"家具", "scale"}, {"灯", "macro"},
		{"咖啡", "table"}, {"茶", "table"}, {"食品", "macro"},
	} {
		if strings.Contains(query, preference.keyword) && candidate.Mode == preference.mode {
			score += 7
		}
	}
	return score
}
