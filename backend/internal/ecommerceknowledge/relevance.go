package ecommerceknowledge

import (
	"strings"

	"yingce/backend/internal/model"
)

const BuiltinSourcePrefix = "meiye-kb"

func Relevance(entry model.EcommerceKnowledgeEntry, category, loweredQuery string) int {
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
	for _, token := range strings.Fields(loweredQuery) {
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
	if !strings.HasPrefix(entry.SourceID, BuiltinSourcePrefix) {
		return score
	}
	bonus := 0
	bonus += ecommerceProductRelevance(entry.Title, text, category, loweredQuery)
	// Platform rules titled for one marketplace only apply to that platform.
	bonus += ecommercePlatformRelevance(entry.Title, loweredQuery)
	// General methods titled for another category (e.g. "母婴家庭温馨场景")
	// should not be chosen for a sofa.
	for other, labels := range ecommerceCategoryTitleLabels {
		if other == category {
			continue
		}
		for _, label := range labels {
			if strings.Contains(entry.Title, label) && !strings.Contains(loweredQuery, label) {
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
