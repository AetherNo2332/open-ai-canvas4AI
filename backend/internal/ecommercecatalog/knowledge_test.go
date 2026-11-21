package ecommercecatalog

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestTaxonomyKnowledgeEntriesBuildInstallableRows(t *testing.T) {
	now := time.Date(2026, 11, 21, 8, 30, 0, 0, time.UTC)
	entries := TaxonomyKnowledgeEntries(now)
	if len(entries) != Metadata().Nodes {
		t.Fatalf("entries=%d nodes=%d", len(entries), Metadata().Nodes)
	}
	selected := -1
	for i := range entries {
		if len([]rune(entries[i].Title)) > 243 {
			t.Fatalf("title exceeds established truncation limit: %s", entries[i].ID)
		}
		if entries[i].ID == "industry-taxonomy-hg-11-7-29" {
			selected = i
		}
	}
	if selected < 0 {
		t.Fatal("expected taxonomy entry missing")
	}
	entry := entries[selected]
	if entry.SourceID != TaxonomySourceID || entry.KnowledgeDomain != "KB18" || entry.KnowledgeType != "category_taxonomy" || entry.Category != "taxonomy" {
		t.Fatalf("unexpected taxonomy metadata: %+v", entry)
	}
	if entry.VerifiedAt == nil || !entry.VerifiedAt.Equal(now) || !entry.CreatedAt.Equal(now) || !entry.UpdatedAt.Equal(now) {
		t.Fatalf("timestamps not applied: %+v", entry)
	}
	var content struct {
		TaxonomyID string `json:"taxonomyId"`
		RootID     string `json:"rootId"`
		Path       string `json:"path"`
		Guidance   string `json:"guidance"`
	}
	if err := json.Unmarshal([]byte(entry.ContentJSON), &content); err != nil {
		t.Fatal(err)
	}
	if content.TaxonomyID != "hg-11-7-29" || content.RootID != "hg" || content.Path == "" || !strings.Contains(content.Guidance, "不证明任何具体商品") {
		t.Fatalf("unexpected taxonomy content: %+v", content)
	}
	if got := truncateKnowledgeTitle(strings.Repeat("类", 241), 240); got != strings.Repeat("类", 240)+"..." {
		t.Fatalf("title truncation changed: %q", got)
	}
}

func TestIndustryBranchKnowledgeEntriesCoverLevelTwoCategories(t *testing.T) {
	entries := IndustryBranchKnowledgeEntries()
	levelTwo := LevelTwo()
	if len(entries) != len(levelTwo) {
		t.Fatalf("entries=%d levelTwo=%d", len(entries), len(levelTwo))
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		if seen[entry.ID] {
			t.Fatalf("duplicate knowledge id: %s", entry.ID)
		}
		seen[entry.ID] = true
		if entry.SourceID != IndustryBranchSourceID || entry.KnowledgeDomain != "KB19" || entry.KnowledgeType != "industry_method" {
			t.Fatalf("unexpected branch metadata: %+v", entry)
		}
		if !strings.Contains(entry.Title, "子品类创作路由") || !json.Valid([]byte(entry.ContentJSON)) {
			t.Fatalf("invalid branch knowledge: %+v", entry)
		}
	}
}
