package ecommercecatalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"yingce/backend/internal/model"
)

const (
	TaxonomySourceID       = "meiye-industry-taxonomy"
	IndustryBranchSourceID = "meiye-kb19"
)

// TaxonomyKnowledgeEntries builds the official classification rows without
// touching persistence. Installation and receipt handling stay in the app.
func TaxonomyKnowledgeEntries(now time.Time) []model.EcommerceKnowledgeEntry {
	meta := Metadata()
	verifiedAt := now
	entries := make([]model.EcommerceKnowledgeEntry, 0, meta.Nodes)
	for _, node := range All() {
		content, _ := json.Marshal(map[string]any{"taxonomyId": node.ID, "parentId": node.ParentID, "rootId": node.RootID, "path": node.Path, "pathEn": node.PathEn, "guidance": "官方分类路径：" + node.Path + "；英文：" + node.PathEn + "。只用于行业、品类和子品类定位，不证明任何具体商品的参数、功能或图片规则。"})
		id := "industry-taxonomy-" + node.ID
		sum := sha256.Sum256([]byte(id + string(content)))
		entries = append(entries, model.EcommerceKnowledgeEntry{
			ID:              id,
			SourceID:        TaxonomySourceID,
			KnowledgeDomain: "KB18",
			KnowledgeType:   "category_taxonomy",
			Title:           truncateKnowledgeTitle(node.Path, 240),
			ContentJSON:     string(content),
			Category:        "taxonomy",
			PlatformsJSON:   "[]",
			LanguagesJSON:   "[]",
			RegionsJSON:     "[]",
			TagsJSON:        "[]",
			SourceURL:       meta.URL,
			SourceCommit:    meta.Commit,
			License:         "MIT",
			Version:         meta.Version,
			TrustLevel:      "official_taxonomy",
			ReviewStatus:    "approved",
			DedupHash:       hex.EncodeToString(sum[:]),
			VerifiedAt:      &verifiedAt,
			CreatedAt:       now,
			UpdatedAt:       now,
		})
	}
	return entries
}

// IndustryBranchKnowledgeEntries builds the KB19 routing entries for every
// second-level category. The bootstrap remains responsible for database writes.
func IndustryBranchKnowledgeEntries() []model.EcommerceKnowledgeEntry {
	var entries []model.EcommerceKnowledgeEntry
	for _, node := range LevelTwo() {
		match := Method(node)
		content, _ := json.Marshal(map[string]string{"guidance": Context(match), "skillId": "ecom-industry-" + node.RootID, "taxonomyId": node.ID, "methodScope": match.MethodScope, "methodAncestorId": match.MethodAncestorID})
		entries = append(entries, model.EcommerceKnowledgeEntry{
			ID:              "kb19-industry-" + node.ID,
			SourceID:        IndustryBranchSourceID,
			KnowledgeDomain: "KB19",
			KnowledgeType:   "industry_method",
			Title:           node.Path + " · 子品类创作路由",
			ContentJSON:     string(content),
			Category:        match.Group,
			PlatformsJSON:   "[]",
			LanguagesJSON:   "[]",
			RegionsJSON:     "[]",
			TagsJSON:        "[]",
			SourceURL:       "builtin://meiyeai/industry-methods/" + node.ID,
			Version:         "kb19-v1-20261101",
			License:         "MeiYeAI Original",
			TrustLevel:      "first_party",
			ReviewStatus:    "approved",
		})
	}
	return entries
}

func truncateKnowledgeTitle(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "..."
}
