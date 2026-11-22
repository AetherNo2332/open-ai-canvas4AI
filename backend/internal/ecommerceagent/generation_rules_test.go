package ecommerceagent

import (
	"strings"
	"testing"
)

func TestSelectRecommendationUsesOnlyCurrentExactID(t *testing.T) {
	recommendations := []Recommendation{
		{ID: "r1", Label: "first"},
		{ID: "r2", Label: "second"},
		{ID: "r2", Label: "duplicate"},
	}
	if selected, ok := SelectRecommendation(recommendations, ""); !ok || selected != nil {
		t.Fatalf("empty selection=%+v ok=%v", selected, ok)
	}
	selected, ok := SelectRecommendation(recommendations, "r2")
	if !ok || selected == nil || selected.Label != "second" {
		t.Fatalf("exact selection=%+v ok=%v", selected, ok)
	}
	if _, ok := SelectRecommendation(recommendations, "R2"); ok {
		t.Fatal("case-insensitive or stale selection accepted")
	}
	if _, ok := SelectRecommendation(recommendations, "missing"); ok {
		t.Fatal("unknown selection accepted")
	}
}

func TestRecommendationMarkupPreservesOrderAndLabels(t *testing.T) {
	got := RecommendationMarkup([]Recommendation{{Label: "白底主角"}, {Label: "场景故事"}, {Label: ""}})
	want := "<agent-option>白底主角</agent-option> <agent-option>场景故事</agent-option> <agent-option></agent-option>"
	if got != want {
		t.Fatalf("markup=%q want=%q", got, want)
	}
	if got := RecommendationMarkup(nil); got != "" {
		t.Fatalf("empty markup=%q", got)
	}
}

func TestSafeGeneratedImagePromptKeepsEvidenceBoundaries(t *testing.T) {
	const anchor = "same product as reference image, identical shape/color/logo placement"
	valid := anchor + ", clean studio light, complete product, centered composition"
	if !SafeGeneratedImagePrompt(valid, "") {
		t.Fatal("valid product-anchored prompt rejected")
	}
	withNumber := anchor + ", show 2 USB ports, clean studio light"
	if !SafeGeneratedImagePrompt(withNumber, "卖家确认：2 个 USB 接口") {
		t.Fatal("seller-supported number rejected")
	}
	if SafeGeneratedImagePrompt(withNumber, "") {
		t.Fatal("invented number accepted")
	}
	if SafeGeneratedImagePrompt("clean studio light, complete product, centered composition", "") {
		t.Fatal("missing product anchor accepted")
	}
	if SafeGeneratedImagePrompt(anchor+", Amazon best seller badge, clean studio light", "") {
		t.Fatal("unsupported commercial claim accepted")
	}
	if SafeGeneratedImagePrompt(valid+strings.Repeat("x", 4000), "") {
		t.Fatal("oversized prompt accepted")
	}
}
