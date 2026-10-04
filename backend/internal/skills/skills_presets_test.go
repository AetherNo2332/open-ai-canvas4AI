package skills

import (
	"strings"
	"testing"
)

func TestValidateSkillPresetSkillCount(t *testing.T) {
	ids := make([]string, 12)
	for i := range ids {
		ids[i] = "skill-" + string(rune('a'+i))
	}
	if err := validateSkillPresetSkillCount("large-preset", ids); err != nil {
		t.Fatalf("12 skills should be accepted: %v", err)
	}
	if err := validateSkillPresetSkillCount("empty-preset", nil); err == nil || !strings.Contains(err.Error(), "empty-preset") {
		t.Fatalf("empty preset should fail with its ID: %v", err)
	}
}

func TestSkillPresetsCatalogIsValid(t *testing.T) {
	presets, err := New(nil, "", nil).SkillPresets()
	if err != nil {
		t.Fatal(err)
	}
	if len(presets) == 0 {
		t.Fatal("场景预设目录不能为空")
	}
	seeded := make(map[string]struct{}, 128)
	ids, err := builtinSeedSkillIDs()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		seeded[id] = struct{}{}
	}
	for _, preset := range presets {
		if len(preset.SkillIDs) == 0 {
			t.Fatalf("%s 技能集合为空", preset.PresetID)
		}
		for _, skillID := range preset.SkillIDs {
			if _, ok := seeded[skillID]; !ok {
				t.Fatalf("%s 引用未上架技能 %s", preset.PresetID, skillID)
			}
		}
	}
}
