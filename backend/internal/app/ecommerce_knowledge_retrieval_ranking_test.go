package app

import (
	"slices"
	"strings"
	"testing"

	"yingce/backend/internal/ecommercecatalog"
	"yingce/backend/internal/model"
)

func TestEcommerceRetrievalRankingKeepsSelectedOrderAndIndustryBoost(t *testing.T) {
	svc, db := knowledgeTestService(t)
	t.Cleanup(func() { _ = svc.Close() })

	sources := []model.EcommerceKnowledgeSource{
		{ID: "ready-source", Name: "Ready source", Status: "ready"},
		{ID: "disabled-source", Name: "Disabled source", Status: "disabled"},
		{ID: industryTaxonomySourceID, Name: "Industry taxonomy", Status: "ready"},
	}
	// Existing built-in source rows suppress request-time fallback entries, so
	// the controlled database rows below are the only KB candidates in play.
	for _, spec := range ecommerceBuiltinSourceSpecs {
		sources = append(sources, model.EcommerceKnowledgeSource{
			ID: ecommerceBuiltinSourceID(spec.Domain), Name: spec.Name, Status: "ready",
		})
	}
	for i := range sources {
		if err := svc.repo.UpsertEcommerceKnowledgeSource(&sources[i]); err != nil {
			t.Fatal(err)
		}
	}

	req := EcommerceCreativeDirectionsRequest{
		ProductName: "婴儿奶瓶",
		Category:    "母婴",
		Language:    "zh-CN",
	}
	match := ecommercecatalog.ResolveProduct(req.Category, req.ProductName)
	industryID := "industry-taxonomy-" + match.Node.ID
	entries := []model.EcommerceKnowledgeEntry{
		{
			ID: "tie-b", SourceID: "ready-source", KnowledgeType: "creation_type_rule",
			Title: "Tie B", Category: "general", ContentJSON: `{}`,
			TrustLevel: "reviewed_community", ReviewStatus: "approved",
		},
		{
			ID: "tie-a", SourceID: "ready-source", KnowledgeType: "creation_type_rule",
			Title: "Tie A", Category: "general", ContentJSON: `{}`,
			TrustLevel: "reviewed_community", ReviewStatus: "approved",
		},
		{
			ID: "low-score", SourceID: "ready-source", KnowledgeType: "reference_method",
			Title: "Low score", Category: "general", ContentJSON: `{}`,
			ReviewStatus: "approved",
		},
		{
			ID: "disabled-high", SourceID: "disabled-source", KnowledgeType: "creation_type_rule",
			Title: "ZZ disabled high", Category: "general", ContentJSON: `{}`,
			TrustLevel: "reviewed_community", ReviewStatus: "approved",
		},
		{
			ID: industryID, SourceID: industryTaxonomySourceID, KnowledgeType: "category_taxonomy",
			Title: match.Node.Path, Category: "taxonomy", ContentJSON: `{}`,
			TrustLevel: "official_taxonomy", ReviewStatus: "approved",
		},
	}
	for i := range entries {
		entries[i].PlatformsJSON = "[]"
		entries[i].LanguagesJSON = "[]"
		entries[i].RegionsJSON = "[]"
		entries[i].TagsJSON = "[]"
		entries[i].DedupHash = "retrieval-ranking-" + entries[i].ID
		if err := db.Create(&entries[i]).Error; err != nil {
			t.Fatal(err)
		}
	}

	// The repository's explicit knowledge_type/title order gives the equal-score
	// pair a deterministic input order without depending on SQL natural order.
	found, err := svc.repo.SearchEcommerceKnowledgeEntries(
		ecommerceProductCategory(req),
		strings.Join([]string{req.ProductName, req.Category}, " "),
	)
	if err != nil {
		t.Fatal(err)
	}
	tieOrder := []string{}
	for _, entry := range found {
		if entry.ID == "tie-a" || entry.ID == "tie-b" {
			tieOrder = append(tieOrder, entry.ID)
		}
	}
	if !slices.Equal(tieOrder, []string{"tie-a", "tie-b"}) {
		t.Fatalf("equal-score candidate input order = %v, want [tie-a tie-b]", tieOrder)
	}

	result, err := svc.retrieveEcommerceKnowledge(req)
	if err != nil {
		t.Fatal(err)
	}
	controlled := map[string]bool{
		industryID: true, "tie-a": true, "tie-b": true, "low-score": true, "disabled-high": true,
	}
	selected := []string{}
	for _, entry := range result.Selected {
		if controlled[entry.ID] {
			selected = append(selected, entry.ID)
		}
		if entry.ID == "disabled-high" {
			t.Fatal("disabled source entry reached selected knowledge")
		}
	}
	want := []string{industryID, "tie-a", "tie-b", "low-score"}
	if !slices.Equal(selected, want) {
		t.Fatalf("selected controlled IDs = %v, want %v", selected, want)
	}
}
