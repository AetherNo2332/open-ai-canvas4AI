package app

import (
	"strings"
	"testing"

	"yingce/backend/internal/model"
	"yingce/backend/internal/shortplay"
)

// W05：衍生形象（造型）的写入与继承合同。基础资产改名后显示名要跟着变，
// 别名/音色/分类只读父资产，形象名在同一角色下去重。

func seedBaseCharacter(t *testing.T, svc *Service, shortplayID string) int {
	t.Helper()
	row := &model.ShortplayAsset{
		ShortplayID: shortplayID, ID: 1, Kind: shortplay.AssetCharacter, Name: "沈青禾",
		AliasesJSON: `["青禾","沈姑娘"]`, Description: "卖伞人家的女儿", Appearance: "月白旧衫", Voice: "清冷女声",
		Revision: 1,
	}
	if err := svc.repo.CreateShortplayAsset(row); err != nil {
		t.Fatal(err)
	}
	return row.ID
}

func TestCreateShortplayDerivedAssetInheritsParent(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "p-derived", "sp-derived")
	parentID := seedBaseCharacter(t, svc, "sp-derived")

	created, err := svc.UpsertShortplayAsset("owner", "sp-derived", shortplay.UpsertAssetRequest{
		DerivedFrom: parentID, VariantName: "战袍", Appearance: "红衣银甲", Description: "出嫁前的战袍造型",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID != 2 || created.Kind != shortplay.AssetCharacter {
		t.Fatalf("summary = %#v", created)
	}
	if created.Name != "沈青禾-战袍" {
		t.Fatalf("display name = %q, want 沈青禾-战袍", created.Name)
	}
	if strings.Join(created.Aliases, ",") != "青禾,沈姑娘" || created.Voice != "清冷女声" {
		t.Fatalf("inherited aliases/voice = %#v / %q", created.Aliases, created.Voice)
	}
	if created.Appearance != "红衣银甲" || created.Description != "出嫁前的战袍造型" {
		t.Fatalf("own fields = %q / %q", created.Appearance, created.Description)
	}

	// 父资产改名：衍生行的显示名与继承值随读模型变化，不落库。
	base, err := svc.repo.ShortplayAssetByID("sp-derived", parentID)
	if err != nil {
		t.Fatal(err)
	}
	renamed := *base
	renamed.Name = "青禾"
	if applied, err := svc.repo.UpdateShortplayAsset(&renamed, base.Revision); err != nil || !applied {
		t.Fatalf("rename base = %v, %v", applied, err)
	}
	items, err := svc.ShortplayAssets("owner", "sp-derived")
	if err != nil {
		t.Fatal(err)
	}
	if items[1].Name != "青禾-战袍" {
		t.Fatalf("after parent rename display = %q", items[1].Name)
	}
}

func TestCreateShortplayDerivedAssetRejectsInvalidParents(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "p-bad", "sp-bad")
	parentID := seedBaseCharacter(t, svc, "sp-bad")
	if err := svc.repo.CreateShortplayAsset(&model.ShortplayAsset{
		ShortplayID: "sp-bad", ID: 2, Kind: shortplay.AssetProp, Name: "油纸伞", Revision: 1,
	}); err != nil {
		t.Fatal(err)
	}

	cases := map[string]shortplay.UpsertAssetRequest{
		"missing parent": {DerivedFrom: 99, VariantName: "战袍"},
		"prop parent":    {DerivedFrom: 2, VariantName: "战袍"},
		"with name":      {DerivedFrom: parentID, VariantName: "战袍", Name: "沈青禾-战袍"},
		"with aliases":   {DerivedFrom: parentID, VariantName: "战袍", Aliases: []string{"青禾"}},
		"with voice":     {DerivedFrom: parentID, VariantName: "战袍", Voice: "少年声"},
		"with kind":      {DerivedFrom: parentID, VariantName: "战袍", Kind: shortplay.AssetProp},
		"separator":      {DerivedFrom: parentID, VariantName: "战-袍"},
		"same as parent": {DerivedFrom: parentID, VariantName: "沈青禾"},
		"same as alias":  {DerivedFrom: parentID, VariantName: "青禾"},
	}
	for label, req := range cases {
		if _, err := svc.UpsertShortplayAsset("owner", "sp-bad", req); err == nil {
			t.Fatalf("%s: accepted as derived asset", label)
		}
	}

	// 父资产不能是另一个衍生形象。
	if _, err := svc.UpsertShortplayAsset("owner", "sp-bad", shortplay.UpsertAssetRequest{DerivedFrom: parentID, VariantName: "战袍"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpsertShortplayAsset("owner", "sp-bad", shortplay.UpsertAssetRequest{DerivedFrom: 3, VariantName: "雨夜"}); err == nil {
		t.Fatal("derived-on-derived was accepted")
	}
}

func TestCreateShortplayDerivedAssetDeduplicatesVariantNames(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "p-dup", "sp-dup")
	parentID := seedBaseCharacter(t, svc, "sp-dup")
	if _, err := svc.UpsertShortplayAsset("owner", "sp-dup", shortplay.UpsertAssetRequest{DerivedFrom: parentID, VariantName: "战袍"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpsertShortplayAsset("owner", "sp-dup", shortplay.UpsertAssetRequest{DerivedFrom: parentID, VariantName: " 战袍 "}); err == nil {
		t.Fatal("duplicate variant name was accepted")
	}
}

func TestUpdateShortplayDerivedAssetKeepsInheritance(t *testing.T) {
	svc, db := newShortplayWorkflowService(t)
	seedShortplay(t, db, "owner", "p-up", "sp-up")
	parentID := seedBaseCharacter(t, svc, "sp-up")
	first, err := svc.UpsertShortplayAsset("owner", "sp-up", shortplay.UpsertAssetRequest{DerivedFrom: parentID, VariantName: "战袍"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.UpsertShortplayAsset("owner", "sp-up", shortplay.UpsertAssetRequest{DerivedFrom: parentID, VariantName: "便装"})
	if err != nil {
		t.Fatal(err)
	}

	updated, err := svc.UpsertShortplayAsset("owner", "sp-up", shortplay.UpsertAssetRequest{
		AssetID: first.ID, VariantName: "红衣战袍", Appearance: "红衣银甲", Locked: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "沈青禾-红衣战袍" || !updated.Locked || updated.Voice != "清冷女声" {
		t.Fatalf("updated = %#v", updated)
	}
	if _, err := svc.UpsertShortplayAsset("owner", "sp-up", shortplay.UpsertAssetRequest{AssetID: first.ID, VariantName: "便装"}); err == nil {
		t.Fatal("rename collided with sibling variant name")
	}
	if _, err := svc.UpsertShortplayAsset("owner", "sp-up", shortplay.UpsertAssetRequest{AssetID: first.ID, Voice: "少年声"}); err == nil {
		t.Fatal("derived voice override was accepted")
	}
	if _, err := svc.UpsertShortplayAsset("owner", "sp-up", shortplay.UpsertAssetRequest{AssetID: second.ID, Name: "随便改名"}); err == nil {
		t.Fatal("derived name override was accepted")
	}
	// 改挂到另一个父资产被拒绝：衍生关系创建后固定。
	if err := svc.repo.CreateShortplayAsset(&model.ShortplayAsset{
		ShortplayID: "sp-up", ID: 9, Kind: shortplay.AssetCharacter, Name: "顾行舟", Revision: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpsertShortplayAsset("owner", "sp-up", shortplay.UpsertAssetRequest{AssetID: second.ID, DerivedFrom: 9, VariantName: "便装"}); err == nil {
		t.Fatal("derived asset accepted repointing to another parent")
	}
}
