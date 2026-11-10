package app

import (
	"strings"
	"yingce/backend/internal/model"
)

func ecommerceExpressiveDirection(category string, req EcommerceCreativeDirectionsRequest, seed ecommerceDirectionSeed) ecommerceDirectionSeed {
	text := strings.ToLower(strings.Join([]string{req.ProductName, req.Description, req.Instruction}, " "))
	if category == "tech" && (strings.Contains(text, "耳机") || strings.Contains(text, "headphone")) {
		return ecommerceHeadphoneDirection(seed)
	}
	// Style names, camera language and copy tone stay tied to the product class.
	// A retrieved method can rank a direction, but must not supply product facts.
	styles := map[string]map[string][2]string{
		"home": {
			"hero":      {"静谧雕塑风", "让自然侧光描出真实轮廓，背景只留下安静的空间层次；文案方向：一处细节，自成风景。"},
			"lifestyle": {"晨光居室风", "把商品放进有生活痕迹的明亮居室，用远近景交代它与空间的关系；文案方向：让喜欢的日常，自然发生。"},
			"editorial": {"空间杂志风", "以建筑线条、留白和错位标题组织画面，商品作为视觉锚点；文案方向：把日常，布置成喜欢的样子。"},
			"feature":   {"细节档案风", "让可见结构与局部特写成为主角，文字只解释确认过的信息；文案方向：好看的细节，值得慢慢看。"},
			"macro":     {"触感纪实风", "靠近商品表面与接缝，以柔和掠光呈现可见纹理；文案方向：靠近一点，发现更多。"},
			"scale":     {"空间对话风", "用家具和日常物件建立真实比例，不标注未知尺寸；文案方向：每一处摆放，都有自己的理由。"},
		},
		"beauty": {
			"hero":      {"水光留白风", "以清透反射、克制水影和完整包装建立主视觉；文案方向：让日常，清透一点。"},
			"lifestyle": {"晨间梳妆风", "用窗边自然光与真实使用动作呈现产品，不推断功效；文案方向：从这一刻，慢慢开始。"},
			"lab":       {"纯净实验风", "以精确几何与冷暖光线呈现外观，不暗示未经确认的科研结论；文案方向：把目光留给真实细节。"},
			"feature":   {"质地笔记风", "用瓶身、开合与可见质感的微距切换叙事；文案方向：每一次靠近，都有新发现。"},
			"macro":     {"微光特写风", "近景追踪包装印字、曲线和表面高光，保留品牌原貌；文案方向：细节里，自有光芒。"},
			"gift":      {"礼序陈列风", "以高低错落的静物陈列营造礼赠气氛，不添加虚构赠品；文案方向：把心意，放在看得见的地方。"},
		},
		"apparel": {
			"editorial": {"都会编辑风", "用大胆留白与人物姿态展示真实版型，标题节奏简短有力；文案方向：今天，穿出自己的步调。"},
			"lifestyle": {"街头漫游风", "在真实街景中捕捉自然动作，让穿着关系成为画面焦点；文案方向：走到哪里，都是自己的日常。"},
			"hero":      {"极简棚拍风", "用干净色场和精准侧光还原衣物轮廓与颜色；文案方向：简单出场，也有态度。"},
			"feature":   {"穿搭手记风", "以整体搭配和局部纹理交替排版，不虚构面料成分；文案方向：喜欢的搭配，自有章法。"},
			"macro":     {"纹理观察风", "用近景表现可见织纹、接缝与真实配件；文案方向：把细节，穿在身上。"},
			"display":   {"精品橱窗风", "用克制道具与有序陈列托出商品外观；文案方向：这一眼，刚好心动。"},
		},
		"food": {
			"hero":      {"食光静物风", "以自然桌面光和真实包装建立清晰主视觉；文案方向：把这一刻，留给好味道。"},
			"lifestyle": {"餐桌日常风", "在可信的用餐情境里安排商品与人的距离，避免虚构配料；文案方向：日常一隅，刚好喜欢。"},
			"macro":     {"风味近景风", "用微距与侧光观察真实可见的形态和色泽；文案方向：靠近一点，遇见好味道。"},
			"table":     {"午后分享风", "用餐具、留白和自然动作建立轻松节奏，不制造评价或功效；文案方向：想分享的，是这一刻。"},
		},
		"maternal": {
			"hero":        {"柔光安心风", "以浅色棚拍和完整产品轮廓建立清晰首屏；文案方向：每一处，都想看仔细。"},
			"lifestyle":   {"亲子晨光风", "用自然家庭光与照护者的轻柔动作表达真实关系；文案方向：陪伴，就在这些小瞬间。"},
			"feature":     {"细节守护风", "用可见结构近景与留白说明已确认信息，不暗示认证或安全性能；文案方向：看见细节，也看见用心。"},
			"macro":       {"亲近观察风", "以真实可见的接触位置和局部结构组织镜头；文案方向：从近处，了解更多。"},
			"documentary": {"家庭纪实风", "抓取真实的照护动作与商品使用关系，人物自然不摆拍；文案方向：平常一天，也值得珍藏。"},
		},
		"appliance": {
			"hero":      {"明亮居家风", "浅木地板与灰白墙面衬托完整机身，侧光勾出真实轮廓；文案方向：让家里的日常，轻松一点。"},
			"lifestyle": {"家务日常风", "在客厅动线中呈现产品与家人的距离关系，只展示可见的工作状态；文案方向：把时间，留给更想做的事。"},
			"feature":   {"外观解析风", "用近景与引线说明已确认的外部结构，未知参数留白；文案方向：每个细节，都看得见。"},
			"creative":  {"静夜微光风", "低照度暖光与安静卧室构图营造夜间使用氛围，不写噪音或续航数值；文案方向：安静的夜里，家也被照顾着。"},
			"macro":     {"机身微距风", "以掠光表现外壳、按键和指示灯的真实质感；文案方向：靠近看，设计更清楚。"},
			"scale":     {"空间比例风", "借家具与地面建立真实尺度，不标注未经确认的尺寸；文案方向：放在哪里，都刚刚好。"},
		},
		"tech": {
			"hero":      {"精密光影风", "用低调侧光突出商品外形，线条只服务于轮廓；文案方向：让设计，清晰可见。"},
			"lifestyle": {"都市随行风", "在通勤或工作场景中展示真实使用关系；文案方向：跟上自己的节奏。"},
			"creative":  {"霓虹节奏风", "以克制霓虹和动势网格建立视觉速度，保留商品真实细节；文案方向：这一刻，自有频率。"},
			"feature":   {"结构解析风", "用可见接口与外观细节构成信息层级，不绘制未知内部零件；文案方向：从细节，看见设计。"},
			"macro":     {"微距工艺风", "局部掠光呈现外壳、按键与真实接缝；文案方向：靠近，才更清楚。"},
			"desk":      {"桌面秩序风", "以自然窗光和整洁桌面呈现产品与日常装备的关系；文案方向：桌边的每一刻，都有条理。"},
		},
	}
	if category == "home" && strings.Contains(text, "沙发") {
		styles["home"]["lifestyle"] = [2]string{"松弛客厅风", "用有呼吸感的客厅构图与自然坐姿展现沙发的真实比例；文案方向：把放松，留给回家的时刻。"}
	} else if category == "home" && (strings.Contains(text, "台灯") || strings.Contains(text, "吊灯")) {
		styles["home"]["hero"] = [2]string{"光影建筑风", "让灯具轮廓与明暗边界构成画面，灯光效果只作概念呈现；文案方向：让一束光，找到自己的位置。"}
	}
	if byMode, ok := styles[category]; ok {
		if style, ok := byMode[seed.Mode]; ok {
			seed.Name, seed.Summary = style[0], style[1]
			return seed
		}
	}
	if category != "bathroom" {
		if !strings.HasSuffix(seed.Name, "风") {
			seed.Name += "风"
		}
		if !strings.Contains(seed.Summary, "文案方向") {
			copy := map[string]string{"hero": "让喜欢的日常，更有画面", "lifestyle": "把这一刻，留给生活", "creative": "换一种视角，看见不同", "feature": "细节之中，自有答案", "macro": "靠近一点，看见更多", "editorial": "日常，也有自己的章节"}[seed.Mode]
			if copy == "" {
				copy = "让每一处细节，有自己的位置"
			}
			seed.Summary = strings.TrimRight(seed.Summary, "。") + "；文案方向：" + copy + "。"
		}
	}
	return seed
}

func commerceRelevance(entry model.EcommerceKnowledgeEntry, category, query string) int {
	score := 1
	if entry.Category == category {
		score += 4
	}
	if entry.TrustLevel == "official_taxonomy" {
		score += 2
	}
	if entry.TrustLevel == "first_party" {
		score += 5
	}
	if entry.TrustLevel == "reviewed_community" {
		score += 6
	}
	if entry.KnowledgeType == "creation_type_rule" {
		score += 30
	}
	if entry.KnowledgeType == "reference_method" {
		score += 12
	}
	if entry.SourceID == "image-style-craft" && entry.KnowledgeType == "visual_style" {
		score += 18
	}

	text := strings.ToLower(entry.Title + " " + entry.TagsJSON + " " + entry.ContentJSON)
	for _, token := range strings.Fields(query) {
		if len([]rune(token)) < 2 {
			continue
		}
		if strings.Contains(text, token) {
			score += 2
		}
	}
	// Chinese product names are not space separated, so "婴儿奶瓶" alone would
	// never match a method that mentions "奶瓶". Score known product and
	// category words individually for the KB01-KB10 library only; the ranking
	// of request-level methods and reviewed sources stays unchanged.
	if !strings.HasPrefix(entry.SourceID, ecommerceBuiltinKnowledgePrefix) {
		return score
	}
	bonus := 0
	bonus += ecommerceProductRelevance(entry.Title, text, category, query)
	// Platform rules titled for one marketplace only apply to that platform.
	bonus += ecommercePlatformRelevance(entry.Title, query)
	// General methods titled for another category (e.g. "母婴家庭温馨场景")
	// should not be chosen for a sofa.
	for other, labels := range ecommerceCategoryTitleLabels {
		if other == category {
			continue
		}
		for _, label := range labels {
			if strings.Contains(entry.Title, label) && !strings.Contains(query, label) {
				bonus -= 8
			}
		}
	}
	return score + min(bonus, 20)
}

var ecommerceCategoryTitleLabels = map[string][]string{
	"maternal": {"母婴", "婴儿"}, "tech": {"数码", "3C"}, "apparel": {"服装", "服饰", "女装"}, "beauty": {"美妆", "护肤"},
	"food": {"食品", "餐饮", "美食"}, "home": {"家居", "家具"}, "appliance": {"家电"}, "pet": {"宠物"}, "sport": {"运动", "健身", "户外"},
}

var ecommercePlatformTitleAliases = [][]string{
	{"taobao", "tmall", "淘宝", "天猫"}, {"jd", "京东"}, {"douyin", "tiktok", "抖音"}, {"amazon"}, {"shopify"},
	{"xiaohongshu", "小红书"}, {"pinduoduo", "拼多多"}, {"etsy"},
}

var ecommerceCategoryTerms = map[string][]string{
	"maternal":  {"母婴", "婴儿", "宝宝", "喂养"},
	"tech":      {"数码", "3c", "科技"},
	"apparel":   {"服饰", "服装", "穿搭", "女装", "面料"},
	"home":      {"家居", "家具", "客厅", "居室"},
	"appliance": {"家电", "智能家居", "客厅", "家庭"},
	"beauty":    {"美妆", "护肤"},
	"food":      {"食品", "食物"},
	"bathroom":  {"卫浴"},
	"sport":     {"运动", "健身"},
	"pet":       {"宠物"},
}

var ecommerceProductTerms = []string{"榨汁机", "搅拌机", "破壁机", "奶瓶", "奶嘴", "耳机", "手机", "键盘", "手表", "连衣裙", "女装", "外套", "鞋", "沙发", "家具", "台灯", "扫地", "吸尘器", "净化器", "加湿器", "洗面奶", "口红", "香水", "咖啡", "零食", "马桶"}

func ecommerceKnowledgeTerms(category, query string) []string {
	terms := append([]string{}, ecommerceCategoryTerms[category]...)
	for _, term := range ecommerceProductTerms {
		if strings.Contains(query, term) {
			terms = append(terms, term)
		}
	}
	return terms
}

func nonEmptyFacts(req EcommerceCreativeDirectionsRequest) []string {
	values := []string{strings.TrimSpace(req.ProductName), strings.TrimSpace(req.Claims), strings.TrimSpace(req.Materials), strings.TrimSpace(req.Colors), strings.TrimSpace(req.Specifications)}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}

func ecommercePlatformRelevance(title, query string) int {
	bonus := 0
	for _, aliases := range ecommercePlatformTitleAliases {
		titled, requested := false, false
		for _, alias := range aliases {
			titled = titled || strings.Contains(strings.ToLower(title), strings.ToLower(alias))
			requested = requested || strings.Contains(query, strings.ToLower(alias))
		}
		if titled && requested {
			bonus += 14
		} else if titled {
			bonus -= 10
		}
	}
	return bonus
}

func ecommerceHeadphoneDirection(seed ecommerceDirectionSeed) ecommerceDirectionSeed {
	switch seed.Mode {
	case "hero":
		seed.Name, seed.Summary = "声学科技风", "深色光影与清晰商品特写组成主视觉，声波线条只作氛围；文案方向：把喜欢的旋律，带进日常。"
	case "lifestyle":
		seed.Name, seed.Summary = "潮流穿搭风", "结合街头出行与自然佩戴动作，用偏轴大标题强化时尚感；文案方向：让音乐跟着脚步走。"
	case "creative":
		seed.Name, seed.Summary = "潮酷电音风", "高饱和色块与节奏线形成动感排版，商品轮廓保持清晰；文案方向：让这一刻，有自己的节拍。"
	case "feature":
		seed.Name, seed.Summary = "清新治愈风", "浅色柔光、干净留白与可见细节近景组成轻盈长图；文案方向：把片刻留给喜欢的声音。"
	case "macro":
		seed.Name, seed.Summary = "声学微距风", "以可见耳罩曲线、按键和外壳细节组成节奏近景，不虚构内部构造；文案方向：靠近一点，听见设计。"
	case "desk":
		seed.Name, seed.Summary = "桌边日常风", "桌边自然窗光与耳机静物建立安静的生活场景；文案方向：在日常里，听见自己的节奏。"
	}
	return seed
}

func ecommerceProductRelevance(title, text, category, query string) int {
	bonus := 0
	for _, term := range ecommerceCategoryTerms[category] {
		if strings.Contains(text, term) {
			bonus++
		}
	}
	// A product word in the title marks product-specific knowledge, which
	// must outrank a category overview that only lists the product.
	for _, term := range ecommerceProductTerms {
		if strings.Contains(query, term) {
			if strings.Contains(strings.ToLower(title), term) {
				bonus += 14
			} else if strings.Contains(text, term) {
				bonus += 3
			}
		}
	}
	return bonus
}
