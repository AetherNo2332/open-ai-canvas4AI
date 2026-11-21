package ecommerceknowledge

import (
	"testing"

	"yingce/backend/internal/model"
)

func TestRelevancePreservesRankingContract(t *testing.T) {
	entry := func() model.EcommerceKnowledgeEntry {
		return model.EcommerceKnowledgeEntry{
			Category:    "apparel",
			Title:       "通用方法",
			TagsJSON:    "[]",
			ContentJSON: "{}",
		}
	}
	tests := []struct {
		name     string
		category string
		query    string
		entry    func(*model.EcommerceKnowledgeEntry)
		want     int
	}{
		{name: "base weight", category: "home", query: "", want: 1},
		{
			name: "matching category", category: "home", query: "",
			entry: func(entry *model.EcommerceKnowledgeEntry) { entry.Category = "home" },
			want:  5,
		},
		{
			name: "official taxonomy trust", category: "home", query: "",
			entry: func(entry *model.EcommerceKnowledgeEntry) { entry.TrustLevel = "official_taxonomy" },
			want:  3,
		},
		{
			name: "first party trust", category: "home", query: "",
			entry: func(entry *model.EcommerceKnowledgeEntry) { entry.TrustLevel = "first_party" },
			want:  6,
		},
		{
			name: "reviewed community trust", category: "home", query: "",
			entry: func(entry *model.EcommerceKnowledgeEntry) { entry.TrustLevel = "reviewed_community" },
			want:  7,
		},
		{
			name: "creation type rule", category: "home", query: "",
			entry: func(entry *model.EcommerceKnowledgeEntry) { entry.KnowledgeType = "creation_type_rule" },
			want:  31,
		},
		{
			name: "reference method", category: "home", query: "",
			entry: func(entry *model.EcommerceKnowledgeEntry) { entry.KnowledgeType = "reference_method" },
			want:  13,
		},
		{
			name: "image style craft", category: "home", query: "",
			entry: func(entry *model.EcommerceKnowledgeEntry) {
				entry.SourceID = "image-style-craft"
				entry.KnowledgeType = "visual_style"
			},
			want: 19,
		},
		{
			name: "image style craft requires source", category: "home", query: "",
			entry: func(entry *model.EcommerceKnowledgeEntry) {
				entry.SourceID = "normal-source"
				entry.KnowledgeType = "visual_style"
			},
			want: 1,
		},
		{
			name: "query weights repeated title token", category: "home", query: "a ab ab",
			entry: func(entry *model.EcommerceKnowledgeEntry) { entry.Title = "AB" },
			want:  5,
		},
		{
			name: "query matches tag", category: "home", query: "耳机",
			entry: func(entry *model.EcommerceKnowledgeEntry) { entry.TagsJSON = `["耳机"]` },
			want:  3,
		},
		{
			name: "query matches content", category: "home", query: "耳机",
			entry: func(entry *model.EcommerceKnowledgeEntry) {
				entry.ContentJSON = `{"guidance":"耳机"}`
			},
			want: 3,
		},
		{name: "query miss", category: "home", query: "耳机", want: 1},
		{
			name: "builtin product term in title", category: "other", query: "婴儿奶瓶",
			entry: func(entry *model.EcommerceKnowledgeEntry) {
				entry.SourceID = "meiye-kb-test"
				entry.Title = "奶瓶"
			},
			want: 15,
		},
		{
			name: "builtin product term in content", category: "other", query: "婴儿奶瓶",
			entry: func(entry *model.EcommerceKnowledgeEntry) {
				entry.SourceID = "meiye-kb-test"
				entry.ContentJSON = `{"guidance":"奶瓶"}`
			},
			want: 4,
		},
		{
			name: "normal source skips product bonus", category: "other", query: "婴儿奶瓶",
			entry: func(entry *model.EcommerceKnowledgeEntry) {
				entry.Title = "奶瓶"
			},
			want: 1,
		},
		{
			name: "builtin category term without product term", category: "home", query: "场景",
			entry: func(entry *model.EcommerceKnowledgeEntry) {
				entry.SourceID = "meiye-kb-test"
				entry.Title = "空间方法"
				entry.ContentJSON = `{"guidance":"客厅"}`
			},
			want: 2,
		},
		{
			name: "platform alias title match", category: "home", query: "taobao",
			entry: func(entry *model.EcommerceKnowledgeEntry) {
				entry.SourceID = "meiye-kb-test"
				entry.Title = "天猫"
			},
			want: 15,
		},
		{
			name: "platform title without request", category: "home", query: "",
			entry: func(entry *model.EcommerceKnowledgeEntry) {
				entry.SourceID = "meiye-kb-test"
				entry.Title = "天猫"
			},
			want: -9,
		},
		{
			name: "other category title penalty", category: "home", query: "",
			entry: func(entry *model.EcommerceKnowledgeEntry) {
				entry.SourceID = "meiye-kb-test"
				entry.Title = "母婴"
			},
			want: -7,
		},
		{
			name: "bonus cap leaves base outside cap", category: "other", query: "婴儿奶瓶 taobao",
			entry: func(entry *model.EcommerceKnowledgeEntry) {
				entry.SourceID = "meiye-kb-test"
				entry.Title = "奶瓶 天猫"
			},
			want: 21,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			candidate := entry()
			if tc.entry != nil {
				tc.entry(&candidate)
			}
			if got := Relevance(candidate, tc.category, tc.query); got != tc.want {
				t.Fatalf("Relevance() = %d, want %d", got, tc.want)
			}
		})
	}
}
