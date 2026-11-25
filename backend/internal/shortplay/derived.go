package shortplay

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"yingce/backend/internal/model"
	"yingce/backend/internal/production"
)

// 衍生形象（造型）的读模型与命名规则。本文件只有纯函数，不碰数据库。
//
// 一条行若 DerivedFrom == 0 就是基础资产，name / aliases / voice 读本行；
// 若不等于 0 就是衍生行，这三项读父资产，VariantName 必有值。

// DerivedDisplayNameRunes 是显示名 `{父名}-{形象名}` 的总长上限。
// 只在创建/改形象名时校验：父资产改名不校验（不该让造型名绑架角色改名）。
const DerivedDisplayNameRunes = 20

// VariantNameSeparator 是显示名的分隔符，形象名本身不允许包含它。
const VariantNameSeparator = "-"

// ResolveAsset 把衍生行合成为「父资产 + 本行覆盖」的读模型，不做图解析（图走
// current_image_id）。所有读设定的地方都走这里，禁止各自拼 firstNonEmpty。
func ResolveAsset(base, row *model.ShortplayAsset) model.ShortplayAsset {
	if row == nil {
		return model.ShortplayAsset{}
	}
	if row.DerivedFrom == 0 || base == nil {
		return *row
	}
	resolved := *row
	resolved.Kind = base.Kind
	resolved.Name = DerivedDisplayName(base.Name, row.VariantName)
	resolved.AliasesJSON = base.AliasesJSON
	if strings.TrimSpace(row.Voice) == "" {
		resolved.Voice = base.Voice
	}
	if strings.TrimSpace(row.Description) == "" {
		resolved.Description = base.Description
	}
	if strings.TrimSpace(row.RenderRule) == "" {
		resolved.RenderRule = base.RenderRule
	}
	return resolved
}

// DerivedDisplayName 合成衍生形象显示名。不落库：父资产改名要能自动传导。
func DerivedDisplayName(baseName, variantName string) string {
	baseName = strings.TrimSpace(baseName)
	variantName = strings.TrimSpace(variantName)
	if variantName == "" {
		return baseName
	}
	if baseName == "" {
		return variantName
	}
	return baseName + VariantNameSeparator + variantName
}

// ValidateVariantName 校验形象名与显示名预算；parent 是父资产行。
// 20 字预算按「父名 + 分隔符 + 形象名」算，超限时回报父名占了多少字、还能写多少。
func ValidateVariantName(parent *model.ShortplayAsset, variantName string) error {
	variantName = strings.TrimSpace(variantName)
	if variantName == "" {
		return fmt.Errorf("衍生形象必须提供 variantName（如「战袍」）")
	}
	if strings.Contains(variantName, VariantNameSeparator) {
		return fmt.Errorf("形象名不能含 %s：它是显示名的分隔符", VariantNameSeparator)
	}
	if parent == nil {
		return fmt.Errorf("衍生形象必须指定父资产")
	}
	parentName := strings.TrimSpace(parent.Name)
	budget := DerivedDisplayNameRunes - utf8.RuneCountInString(parentName) - utf8.RuneCountInString(VariantNameSeparator)
	if budget < 1 {
		return fmt.Errorf("父资产名占 %d 字，已没有空间再写形象名（显示名上限 %d 字）",
			utf8.RuneCountInString(parentName), DerivedDisplayNameRunes)
	}
	if utf8.RuneCountInString(variantName) > budget {
		return fmt.Errorf("父资产名占 %d 字，形象名最多 %d 字（显示名上限 %d 字）",
			utf8.RuneCountInString(parentName), budget, DerivedDisplayNameRunes)
	}
	normalized := production.NormalizeName(variantName)
	if normalized == production.NormalizeName(parentName) {
		return fmt.Errorf("形象名不能与角色名同名")
	}
	for _, alias := range MergeAliases(nil, SplitAliasesJSON(parent.AliasesJSON)) {
		if normalized == production.NormalizeName(alias) {
			return fmt.Errorf("形象名不能与角色别名「%s」同名", alias)
		}
	}
	return nil
}

// ResolveLook 把「模型写的造型名」解析成具体资产行 ID：命中衍生行返回其 ID，
// 未命中返回 0 表示用父资产本身（不报错、不回落失败）。
//
// 匹配顺序（第一命中即返回）：完整显示名 → variantName → base 自身 name/aliases → 0 →
// 基础造型词表 → 0。第 2 步排在第 4 步之前是关键：数据里的真实造型永远优先于词表回落。
func ResolveLook(base *model.ShortplayAsset, derived []model.ShortplayAsset, name string) int {
	key := production.NormalizeName(name)
	if key == "" {
		return 0
	}
	if base != nil {
		for _, row := range derived {
			if row.DerivedFrom == 0 {
				continue
			}
			if production.NormalizeName(DerivedDisplayName(base.Name, row.VariantName)) == key {
				return row.ID
			}
		}
		for _, row := range derived {
			if row.DerivedFrom == 0 {
				continue
			}
			if production.NormalizeName(row.VariantName) == key {
				return row.ID
			}
		}
		if production.NormalizeName(base.Name) == key {
			return 0
		}
		for _, alias := range SplitAliasesJSON(base.AliasesJSON) {
			if production.NormalizeName(alias) == key {
				return 0
			}
		}
	}
	if production.IsBaseLookName(name) {
		return 0
	}
	return 0
}
