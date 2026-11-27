package shortplay

import (
	"encoding/json"
	"strings"
	"testing"

	"yingce/backend/internal/model"
)

func TestNewBaseAssetRowValidatesIdentity(t *testing.T) {
	rule := "只画半身"
	row, err := NewBaseAssetRow("sp", 3, UpsertAssetRequest{
		Kind: AssetCharacter, Name: " 沈青禾 ", Aliases: []string{"青禾", " ", "青禾"},
		Description: "二十岁女性", Appearance: "齐肩黑发", Voice: "青年女声", RenderRule: &rule,
	})
	if err != nil {
		t.Fatal(err)
	}
	if row.ID != 3 || row.Kind != AssetCharacter || row.Name != "沈青禾" {
		t.Fatalf("row = %#v", row)
	}
	if row.AliasesJSON != `["青禾"]` {
		t.Fatalf("aliases = %s", row.AliasesJSON)
	}
	if row.Voice != "青年女声" || row.RenderRule != "只画半身" {
		t.Fatalf("row = %#v", row)
	}
	prop, err := NewBaseAssetRow("sp", 4, UpsertAssetRequest{Kind: AssetProp, Name: "油纸伞", Voice: "不该保存"})
	if err != nil {
		t.Fatal(err)
	}
	if prop.Voice != "" {
		t.Fatalf("prop voice = %q", prop.Voice)
	}
}

func TestNewBaseAssetRowRejectsInvalidWrites(t *testing.T) {
	for name, req := range map[string]UpsertAssetRequest{
		"kind":     {Kind: "vehicle", Name: "马车"},
		"name":     {Kind: AssetScene, Name: "  "},
		"too-long": {Kind: AssetScene, Name: strings.Repeat("景", 121)},
		"derived":  {Kind: AssetCharacter, Name: "沈青禾-战袍", DerivedFrom: 1, VariantName: "战袍"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewBaseAssetRow("sp", 1, req); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestApplyBaseAssetWriteKeepsUnsentFields(t *testing.T) {
	existing := model.ShortplayAsset{
		ShortplayID: "sp", ID: 1, Kind: AssetCharacter, Name: "沈青禾",
		AliasesJSON: `["青禾"]`, Description: "旧设定", Appearance: "旧外貌",
		Voice: "旧女声", Revision: 4, RenderRule: "旧纪律", Locked: true,
	}
	row, err := ApplyBaseAssetWrite(existing, UpsertAssetRequest{
		AssetID: 1, Name: "沈青禾", Aliases: []string{"沈姑娘", "青禾"}, Appearance: "新外貌", Locked: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if row.Description != "旧设定" || row.Voice != "旧女声" || row.RenderRule != "旧纪律" {
		t.Fatalf("empty fields overwrote existing: %#v", row)
	}
	if row.Appearance != "新外貌" || row.Locked || row.Revision != 4 {
		t.Fatalf("row = %#v", row)
	}
	if row.AliasesJSON != `["青禾","沈姑娘"]` {
		t.Fatalf("aliases = %s", row.AliasesJSON)
	}
	cleared, err := ApplyBaseAssetWrite(row, UpsertAssetRequest{AssetID: 1, RenderRule: ptrString(""), Voice: "新女声"})
	if err != nil {
		t.Fatal(err)
	}
	if cleared.RenderRule != "" || cleared.Voice != "新女声" {
		t.Fatalf("explicit clear/voice: %#v", cleared)
	}
	if _, err := ApplyBaseAssetWrite(row, UpsertAssetRequest{AssetID: 1, DerivedFrom: 9, VariantName: "战袍"}); err == nil {
		t.Fatal("derived write must be rejected in W04")
	}
}

func TestBuildAssetSummaryProjectsRow(t *testing.T) {
	summary := BuildAssetSummary(model.ShortplayAsset{
		ID: 2, Kind: AssetProp, Name: "油纸伞", AliasesJSON: `["伞"]`, ImageStatus: ImageRunning,
		ImageResourceID: "res-1", ImageVersion: 2, EpisodeNumbersJSON: `[1,3]`, Revision: 5,
	})
	if summary.ImageStatus != ImageRunning || summary.Aliases[0] != "伞" || summary.EpisodeNumbers[1] != 3 {
		t.Fatalf("summary = %#v", summary)
	}
	if summary.ImageResourceID != "res-1" || summary.ImageVersion != 2 || summary.Revision != 5 {
		t.Fatalf("summary = %#v", summary)
	}
}

func ptrString(value string) *string { return &value }

// 读模型的别名与分集绑定必须是 []；nil 切片会序列化成 null，前端类型是数组，
// 曾因此让资产卡在真实页面崩溃（Cannot read properties of null (reading "length")）。
func TestBuildAssetSummaryEncodesEmptyListsAsArrays(t *testing.T) {
	row := model.ShortplayAsset{ShortplayID: "sp", ID: 1, Kind: AssetCharacter, Name: "沈青禾", AliasesJSON: "", EpisodeNumbersJSON: ""}
	raw, err := json.Marshal(BuildAssetSummary(row))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, `"aliases":[]`) || !strings.Contains(text, `"episodeNumbers":[]`) {
		t.Fatalf("summary lists must encode as arrays, got %s", text)
	}
	if aliases := SplitAliasesJSON("not-json"); aliases == nil || len(aliases) != 0 {
		t.Fatalf("invalid aliases json = %#v", aliases)
	}
	if numbers := SplitEpisodeNumbersJSON("not-json"); numbers == nil || len(numbers) != 0 {
		t.Fatalf("invalid episode numbers json = %#v", numbers)
	}
}
