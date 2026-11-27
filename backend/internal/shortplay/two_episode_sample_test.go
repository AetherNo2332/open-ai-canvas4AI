package shortplay

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"yingce/backend/internal/model"
)

// W01 夹具的域规则重放：同一份 canonical 样本（sample/two-episodes.json）既被
// repository 的落库回放使用，也用在这里验证纯函数（读模型回落、造型解析、去重键）。
// 两条证据共用一份样本，避免各写一份形状。

type shortplayWorkbenchSample struct {
	Provenance struct {
		DonorCommit string `json:"donorCommit"`
		Kind        string `json:"kind"`
	} `json:"provenance"`
	Episodes []struct {
		Number   int   `json:"number"`
		AssetIDs []int `json:"assetIds"`
	} `json:"episodes"`
	Assets []struct {
		ID          int      `json:"id"`
		Kind        string   `json:"kind"`
		Name        string   `json:"name"`
		Aliases     []string `json:"aliases"`
		Description string   `json:"description"`
		Appearance  string   `json:"appearance"`
		Voice       string   `json:"voice"`
		DerivedFrom int      `json:"derivedFrom"`
		VariantName string   `json:"variantName"`
	} `json:"assets"`
	Jobs []struct {
		Kind      string `json:"kind"`
		DedupeKey string `json:"dedupeKey"`
	} `json:"jobs"`
	Expectations struct {
		DerivedLook struct {
			AssetID             int    `json:"assetId"`
			DisplayName         string `json:"displayName"`
			ResolvedVoice       string `json:"resolvedVoice"`
			ResolvedDescription string `json:"resolvedDescription"`
		} `json:"derivedLook"`
		ResolveLookNames []struct {
			Name    string `json:"name"`
			AssetID int    `json:"assetId"`
		} `json:"resolveLookNames"`
		EpisodeAssetIDs map[string][]int `json:"episodeAssetIds"`
	} `json:"expectations"`
}

func loadWorkbenchSample(t *testing.T) shortplayWorkbenchSample {
	t.Helper()
	path := filepath.Join("..", "..", "..", "testdata", "shortplay-workbench-port", "sample", "two-episodes.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read sample: %v", err)
	}
	var sample shortplayWorkbenchSample
	if err := json.Unmarshal(raw, &sample); err != nil {
		t.Fatalf("decode sample: %v", err)
	}
	return sample
}

func TestWorkbenchSampleDerivedLookRules(t *testing.T) {
	sample := loadWorkbenchSample(t)
	if sample.Provenance.DonorCommit != "13731aae6c4cefadc2a1719e3e7e59b22e249f94" {
		t.Fatalf("sample donor = %q, want 13731aae6", sample.Provenance.DonorCommit)
	}
	if len(sample.Episodes) != 2 {
		t.Fatalf("episodes = %d, want 2", len(sample.Episodes))
	}

	// 两集共用资产 1；衍生造型只进第二集。
	for _, episode := range sample.Episodes {
		want, ok := sample.Expectations.EpisodeAssetIDs[strconv.Itoa(episode.Number)]
		if !ok {
			continue
		}
		if len(episode.AssetIDs) != len(want) {
			t.Fatalf("episode %d asset ids = %v, want %v", episode.Number, episode.AssetIDs, want)
		}
		for index := range want {
			if episode.AssetIDs[index] != want[index] {
				t.Fatalf("episode %d asset ids = %v, want %v", episode.Number, episode.AssetIDs, want)
			}
		}
	}

	var base, derived *model.ShortplayAsset
	for _, asset := range sample.Assets {
		aliases, err := json.Marshal(asset.Aliases)
		if err != nil {
			t.Fatal(err)
		}
		row := &model.ShortplayAsset{ID: asset.ID, Kind: asset.Kind, Name: asset.Name, AliasesJSON: string(aliases), Description: asset.Description, Appearance: asset.Appearance, Voice: asset.Voice, DerivedFrom: asset.DerivedFrom, VariantName: asset.VariantName}
		if asset.DerivedFrom != 0 {
			derived = row
			continue
		}
		if asset.Kind == AssetCharacter {
			base = row
		}
	}
	if base == nil || derived == nil {
		t.Fatal("sample must contain one base character and one derived look")
	}

	resolved := ResolveAsset(base, derived)
	if resolved.Name != sample.Expectations.DerivedLook.DisplayName {
		t.Fatalf("derived display name = %q, want %q", resolved.Name, sample.Expectations.DerivedLook.DisplayName)
	}
	if resolved.Voice != sample.Expectations.DerivedLook.ResolvedVoice {
		t.Fatalf("derived voice = %q, want %q", resolved.Voice, sample.Expectations.DerivedLook.ResolvedVoice)
	}
	if resolved.Description != sample.Expectations.DerivedLook.ResolvedDescription {
		t.Fatalf("derived description = %q, want %q", resolved.Description, sample.Expectations.DerivedLook.ResolvedDescription)
	}
	if resolved.Appearance == base.Appearance {
		t.Fatal("derived row must keep its own appearance")
	}

	derivedRows := []model.ShortplayAsset{*derived}
	for _, expectation := range sample.Expectations.ResolveLookNames {
		if got := ResolveLook(base, derivedRows, expectation.Name); got != expectation.AssetID {
			t.Fatalf("resolve look %q = %d, want %d", expectation.Name, got, expectation.AssetID)
		}
	}

	for _, job := range sample.Jobs {
		switch job.Kind {
		case "extract":
			if job.DedupeKey != JobDedupeKey(JobExtract, "sp-w01") {
				t.Fatalf("extract dedupe key = %q", job.DedupeKey)
			}
		case "asset_image":
			if job.DedupeKey != JobDedupeKey(JobAssetImage, "sp-w01|1") && job.DedupeKey != JobDedupeKey(JobAssetImage, "sp-w01|2") {
				t.Fatalf("asset_image dedupe key = %q", job.DedupeKey)
			}
		}
	}

	if err := ValidateVariantName(base, derived.VariantName); err != nil {
		t.Fatalf("validate variant name: %v", err)
	}
}
