package ecommerceagent

import (
	"strings"
	"unicode"
)

// SelectRecommendation reuses only an exact ID from the current analysis.
// An empty ID means the request did not select a direction.
func SelectRecommendation(recommendations []Recommendation, id string) (*Recommendation, bool) {
	if id == "" {
		return nil, true
	}
	for i := range recommendations {
		if recommendations[i].ID == id {
			return &recommendations[i], true
		}
	}
	return nil, false
}

// SafeGeneratedImagePrompt keeps model-proposed planning text only when it
// anchors the real product, avoids unsupported commercial claims and does not
// introduce numbers absent from the seller's own facts.
func SafeGeneratedImagePrompt(candidate, sellerFacts string) bool {
	if len(candidate) < 40 || len(candidate) > 4000 || !strings.Contains(candidate, "same product as reference image, identical shape/color/logo placement") {
		return false
	}
	lower := strings.ToLower(candidate)
	for _, forbidden := range []string{"best seller", "no.1", "fda approved", "certified", "guaranteed", "price", "discount", "free shipping", "第一", "最好", "认证", "销量"} {
		if strings.Contains(lower, forbidden) {
			return false
		}
	}
	if strings.IndexFunc(candidate, unicode.IsDigit) >= 0 && strings.IndexFunc(sellerFacts, unicode.IsDigit) < 0 {
		return false
	}
	return true
}

// RecommendationMarkup projects the current recommendation labels into the
// workbench option contract without reordering or de-duplicating them.
func RecommendationMarkup(recommendations []Recommendation) string {
	parts := make([]string, 0, len(recommendations))
	for _, recommendation := range recommendations {
		parts = append(parts, "<agent-option>"+recommendation.Label+"</agent-option>")
	}
	return strings.Join(parts, " ")
}
