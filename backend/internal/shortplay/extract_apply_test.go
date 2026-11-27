package shortplay

import (
	"testing"

	"yingce/backend/internal/model"
)

func TestPlanExtractApplyMergesIntoExistingRows(t *testing.T) {
	existing := []model.ShortplayAsset{{
		ShortplayID: "sp", ID: 1, Kind: AssetCharacter, Name: "沈青禾",
		AliasesJSON: `["青禾"]`, Description: "旧设定", EpisodeNumbersJSON: `[1]`, Locked: true,
	}}
	batch := []model.ShortplayEpisode{
		{Number: 1, ScriptText: "沈青禾撑伞。"},
		{Number: 2, ScriptText: "青禾回头，油纸伞落在雨里。"},
	}
	extracted := []ExtractedAsset{
		{AssetID: 1, Kind: AssetCharacter, Name: "沈青禾", Aliases: []string{"沈姑娘"}, Description: "新设定", Appearance: "新外貌"},
		{Kind: AssetProp, Name: "油纸伞", Aliases: []string{"伞"}},
	}
	plan := PlanExtractApply("sp", existing, extracted, batch, 2)
	if len(plan.Rows) != 2 {
		t.Fatalf("rows = %#v", plan.Rows)
	}
	character := plan.Rows[0]
	if character.ID != 1 || character.Description != "旧设定" {
		t.Fatalf("locked row overwritten: %#v", character)
	}
	if character.AliasesJSON != `["青禾","沈姑娘"]` {
		t.Fatalf("aliases = %s", character.AliasesJSON)
	}
	if character.EpisodeNumbersJSON != `[1,2]` {
		t.Fatalf("episodes = %s", character.EpisodeNumbersJSON)
	}
	prop := plan.Rows[1]
	if prop.ID != 2 || prop.Kind != AssetProp || prop.Name != "油纸伞" {
		t.Fatalf("new prop = %#v", prop)
	}
	if prop.EpisodeNumbersJSON != `[1,2]` {
		t.Fatalf("prop episodes = %s", prop.EpisodeNumbersJSON)
	}
}

func TestPlanExtractApplyMatchesAliasWhenIDMissing(t *testing.T) {
	existing := []model.ShortplayAsset{{
		ShortplayID: "sp", ID: 4, Kind: AssetCharacter, Name: "沈青禾", AliasesJSON: `["青禾"]`,
	}}
	batch := []model.ShortplayEpisode{{Number: 3, ScriptText: "青禾来了。"}}
	plan := PlanExtractApply("sp", existing, []ExtractedAsset{{Kind: AssetCharacter, Name: "青禾", Description: "旁白称呼"}}, batch, 5)
	if len(plan.Rows) != 1 || plan.Rows[0].ID != 4 {
		t.Fatalf("alias match failed: %#v", plan.Rows)
	}
	if plan.Rows[0].Description != "旁白称呼" || plan.Rows[0].Name != "沈青禾" {
		t.Fatalf("row = %#v", plan.Rows[0])
	}
	if plan.Rows[0].EpisodeNumbersJSON != `[3]` {
		t.Fatalf("episodes = %s", plan.Rows[0].EpisodeNumbersJSON)
	}
}

func TestPlanExtractApplySkipsKindMismatchAndDuplicates(t *testing.T) {
	existing := []model.ShortplayAsset{{ShortplayID: "sp", ID: 1, Kind: AssetProp, Name: "油纸伞"}}
	plan := PlanExtractApply("sp", existing, []ExtractedAsset{
		{AssetID: 1, Kind: AssetCharacter, Name: "油纸伞", Description: "类型不符不算命中"},
		{AssetID: 1, Kind: AssetCharacter, Name: "油纸伞", Description: "重复条目"},
	}, nil, 2)
	if len(plan.Rows) != 1 || plan.Rows[0].ID != 2 || plan.Rows[0].Kind != AssetCharacter {
		t.Fatalf("rows = %#v", plan.Rows)
	}
}

func TestExtractCursorPayloadRoundTrip(t *testing.T) {
	if got := ParseExtractCursor(ExtractCursorPayload(7)); got != 7 {
		t.Fatalf("cursor = %d", got)
	}
	for _, raw := range []string{"", "not-json", `{"cursor":-3}`} {
		if got := ParseExtractCursor(raw); got != 0 && got != -3 {
			t.Fatalf("cursor(%q) = %d", raw, got)
		}
	}
}
