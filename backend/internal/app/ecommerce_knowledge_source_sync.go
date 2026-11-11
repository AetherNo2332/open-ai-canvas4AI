package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"yingce/backend/internal/model"
)

func (s *Service) reuseEcommerceKnowledgeSource(spec commerceSourceSpec, version string, sources []model.EcommerceKnowledgeSource) (int, bool, error) {
	for _, existing := range sources {
		if existing.ID != "src-"+spec.ID || existing.Commit != spec.Commit || existing.Version != version || existing.Status != "ready" || existing.License != spec.License || existing.LicenseText == "" || existing.LastSyncedAt == nil {
			continue
		}
		count, err := s.repo.EcommerceKnowledgeSourceEntryCount(existing.ID, spec.Commit)
		if err != nil {
			return 0, false, err
		}
		if count < int64(len(spec.Documents)) {
			continue
		}
		existing.LastDuplicateCount = count
		if err := s.repo.UpsertEcommerceKnowledgeSource(&existing); err != nil {
			return 0, false, err
		}
		return int(count), true, nil
	}
	return 0, false, nil
}

func (s *Service) prepareEcommerceKnowledgeSource(ctx context.Context, spec commerceSourceSpec, version string, sources []model.EcommerceKnowledgeSource) (*model.EcommerceKnowledgeSource, bool, error) {
	source := model.EcommerceKnowledgeSource{ID: "src-" + spec.ID, Name: spec.Name, URL: "https://github.com/" + spec.Repository, Repository: spec.Repository, Commit: spec.Commit, License: spec.License, Version: version, Status: "syncing"}
	for _, existing := range sources {
		if existing.ID == source.ID && existing.Commit == source.Commit {
			source.LastSyncedAt, source.LicenseText = existing.LastSyncedAt, existing.LicenseText
		}
	}
	if err := s.repo.UpsertEcommerceKnowledgeSource(&source); err != nil {
		return nil, false, err
	}
	licensePath := "LICENSE"
	if spec.ID == "shadcn-design-tokens" {
		licensePath = "LICENSE.md"
	}
	license, err := commerceFetchDocument(ctx, spec, licensePath, 32<<10)
	if err != nil || !commerceLicenseVerified(spec.License, string(license)) {
		source.Status, source.ErrorMessage = "license_unverified", fmt.Sprintf("license check failed: %v", err)
		// A transient transport failure must not invalidate previously checked content.
		if err != nil && source.LastSyncedAt != nil && source.LicenseText != "" {
			failure := source.ErrorMessage
			for _, existing := range sources {
				if existing.ID == source.ID && existing.Commit == source.Commit {
					source = existing
					source.ErrorMessage = failure
					break
				}
			}
		}
		_ = s.repo.UpsertEcommerceKnowledgeSource(&source)
		return &source, false, nil
	}
	source.LicenseText = string(license)
	return &source, true, nil
}

func commerceLicenseVerified(license, text string) bool {
	switch license {
	case "MIT":
		return strings.Contains(text, "MIT License") || (strings.Contains(text, "Permission is hereby granted") && strings.Contains(text, "THE SOFTWARE IS PROVIDED"))
	case "CC0-1.0":
		return strings.Contains(text, "CC0 1.0 Universal")
	default:
		return true
	}
}

type commerceSourceImportResult struct {
	imported, duplicates int
	failed               []string
}

func (s *Service) importEcommerceKnowledgeDocuments(ctx context.Context, spec commerceSourceSpec, source *model.EcommerceKnowledgeSource) (commerceSourceImportResult, error) {
	var result commerceSourceImportResult
	for _, doc := range spec.Documents {
		entries, err := commerceDocumentEntries(ctx, spec, doc)
		if err != nil {
			result.failed = append(result.failed, spec.Repository+"/"+doc.Path)
			continue
		}
		count, duplicates, err := s.storeEcommerceKnowledgeEntries(entries, spec.ID == "shopify-taxonomy")
		if err != nil {
			return result, err
		}
		result.imported += count
		result.duplicates += duplicates
	}
	now := time.Now().UTC()
	source.LastSyncedAt, source.Status = &now, "ready"
	source.LastDuplicateCount = int64(result.duplicates)
	if result.imported == 0 && result.duplicates == 0 {
		source.Status = "empty"
	} else if len(result.failed) > 0 {
		source.Status = "partial"
	}
	return result, s.repo.UpsertEcommerceKnowledgeSource(source)
}

func commerceDocumentEntries(ctx context.Context, spec commerceSourceSpec, doc commerceDocumentSpec) ([]model.EcommerceKnowledgeEntry, error) {
	if spec.ID == "shopify-taxonomy" {
		data, err := commerceFetch(ctx, "https://github.com/Shopify/product-taxonomy/releases/download/v2026-08/categories.en.txt.gz", 1<<20)
		if err != nil {
			return nil, err
		}
		return commerceTaxonomyEntries(spec, data)
	}
	data, err := commerceFetchDocument(ctx, spec, doc.Path, 256<<10)
	if err != nil {
		return nil, err
	}
	if doc.KnowledgeType == "knowledge_index" {
		return commerceIndexEntries(spec, doc.Path, data)
	}
	title, body := commerceMarkdownGuidance(data)
	if len([]rune(body)) < 80 {
		body = strings.TrimSpace(string(data))
		if len([]rune(body)) > 1800 {
			body = string([]rune(body)[:1800])
		}
	}
	entries := []model.EcommerceKnowledgeEntry{commerceEntry(spec, doc.Path, title, doc.KnowledgeType, doc.Category, doc.Platforms, body)}
	commerceApplyReviewedExcerpt(&entries[0], spec, doc.Path, data)
	return entries, nil
}

func (s *Service) storeEcommerceKnowledgeEntries(entries []model.EcommerceKnowledgeEntry, taxonomy bool) (int, int, error) {
	if taxonomy {
		inserted, err := s.repo.InsertEcommerceKnowledgeEntries(entries)
		return int(inserted), len(entries) - int(inserted), err
	}
	count, duplicates := 0, 0
	for i := range entries {
		exists, err := s.repo.EcommerceKnowledgeEntryExists(entries[i].DedupHash)
		if err != nil {
			return 0, 0, err
		}
		if exists {
			duplicates++
			continue
		}
		if err := s.repo.UpsertEcommerceKnowledgeEntry(&entries[i]); err != nil {
			return 0, 0, err
		}
		count++
	}
	return count, duplicates, nil
}
