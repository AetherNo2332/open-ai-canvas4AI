// Package ecommerceknowledge ships the reviewed, offline ecommerce knowledge
// snapshot. Its contents are reference data; no bundled source code is executed.
package ecommerceknowledge

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"

	"yingce/backend/internal/model"
)

//go:embed bundle/knowledge.json.gz
var compressed []byte

//go:embed bundle/manifest.json
var manifestJSON []byte

type Snapshot struct {
	FormatVersion int                              `json:"formatVersion"`
	Sources       []model.EcommerceKnowledgeSource `json:"sources"`
	Entries       []model.EcommerceKnowledgeEntry  `json:"entries"`
}

type SourceSummary struct {
	ID, Name, Repository, Commit, License string
	Entries                               int
}

type Manifest struct {
	FormatVersion int             `json:"formatVersion"`
	SHA256        string          `json:"sha256"`
	SourceCount   int             `json:"sourceCount"`
	EntryCount    int             `json:"entryCount"`
	ReviewCounts  map[string]int  `json:"reviewCounts"`
	DomainCounts  map[string]int  `json:"domainCounts"`
	Sources       []SourceSummary `json:"sources"`
}

func Digest() string {
	digest := sha256.Sum256(compressed)
	return hex.EncodeToString(digest[:])
}

func ReadManifest() (Manifest, error) {
	var manifest Manifest
	err := json.Unmarshal(manifestJSON, &manifest)
	return manifest, err
}

func Read() (*Snapshot, error) {
	manifest, err := ReadManifest()
	if err != nil {
		return nil, err
	}
	if manifest.FormatVersion != 1 || manifest.SHA256 != Digest() {
		return nil, fmt.Errorf("电商知识数据包版本或校验值不匹配")
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	const maxBytes = 64 << 20
	data, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxBytes {
		return nil, fmt.Errorf("电商知识数据包超过大小限制")
	}
	var snapshot Snapshot
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("电商知识数据包包含多余内容")
	}
	if err := snapshot.validate(manifest); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func (s *Snapshot) validate(manifest Manifest) error {
	if s.FormatVersion != 1 || len(s.Sources) != manifest.SourceCount || len(s.Entries) != manifest.EntryCount {
		return fmt.Errorf("电商知识数据包记录数与清单不一致")
	}
	sources, err := validateSources(s.Sources)
	if err != nil {
		return err
	}
	counts, err := validateEntries(s.Entries, sources)
	if err != nil {
		return err
	}
	if !sameCounts(counts.byReview, manifest.ReviewCounts) || !sameCounts(counts.byDomain, manifest.DomainCounts) || len(manifest.Sources) != len(s.Sources) {
		return fmt.Errorf("电商知识数据包审核状态或知识域统计与清单不一致")
	}
	for _, expected := range manifest.Sources {
		source, exists := sources[expected.ID]
		if !exists || source.Name != expected.Name || source.Repository != expected.Repository || source.Commit != expected.Commit || source.License != expected.License || counts.bySource[expected.ID] != expected.Entries {
			return fmt.Errorf("知识源与清单不一致：%s", expected.ID)
		}
	}
	return nil
}

func validateSources(items []model.EcommerceKnowledgeSource) (map[string]model.EcommerceKnowledgeSource, error) {
	sources := make(map[string]model.EcommerceKnowledgeSource, len(items))
	for _, source := range items {
		if source.ID == "" || source.License == "" || (source.Repository != "" && (source.Commit == "" || source.LicenseText == "")) {
			return nil, fmt.Errorf("知识源缺少来源或许可：%s", source.ID)
		}
		if _, duplicate := sources[source.ID]; duplicate {
			return nil, fmt.Errorf("知识源 ID 重复：%s", source.ID)
		}
		sources[source.ID] = source
	}
	return sources, nil
}

type entryCounts struct {
	bySource, byReview, byDomain map[string]int
}

func validateEntries(entries []model.EcommerceKnowledgeEntry, sources map[string]model.EcommerceKnowledgeSource) (entryCounts, error) {
	ids, hashes := map[string]bool{}, map[string]bool{}
	counts := entryCounts{map[string]int{}, map[string]int{}, map[string]int{}}
	for _, entry := range entries {
		source, exists := sources[entry.SourceID]
		if !exists || entry.ID == "" || entry.DedupHash == "" || ids[entry.ID] || hashes[entry.DedupHash] {
			return counts, fmt.Errorf("知识条目存在无效引用或重复标识：%s", entry.ID)
		}
		if entry.License != source.License || entry.SourceCommit != source.Commit || !json.Valid([]byte(entry.ContentJSON)) {
			return counts, fmt.Errorf("知识条目来源、许可或内容无效：%s", entry.ID)
		}
		if entry.ReviewStatus != "approved" && entry.ReviewStatus != "rejected" && entry.ReviewStatus != "pending" {
			return counts, fmt.Errorf("知识条目审核状态无效：%s", entry.ID)
		}
		ids[entry.ID], hashes[entry.DedupHash] = true, true
		counts.bySource[entry.SourceID]++
		counts.byReview[entry.ReviewStatus]++
		if entry.KnowledgeDomain != "" {
			counts.byDomain[entry.KnowledgeDomain]++
		}
	}
	return counts, nil
}

func sameCounts(actual, expected map[string]int) bool {
	if len(actual) != len(expected) {
		return false
	}
	for key, count := range expected {
		if actual[key] != count {
			return false
		}
	}
	return true
}
