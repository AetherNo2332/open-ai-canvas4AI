package app

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"yingce/backend/internal/model"
	"yingce/backend/internal/production"
	"yingce/backend/internal/repository"
	"yingce/backend/internal/shortplay"

	"gorm.io/gorm"
)

// W05 衍生形象（造型）的写入：derived_from != 0 的行只在这里处理，
// 基础资产的写入仍在 shortplay_assets.go，两类身份规则不塞进同一个函数。
//
// 合同（对齐供体 13731aae6 §3.5）：父资产必须是同一作品下的基础角色；
// 形象用 variantName，显示名由父名合成；别名、音色、分类继承父资产，不能单独指定；
// 同一角色下形象名去重；父资产改名自动传导显示名。

func (s *Service) createShortplayDerivedAsset(shortplayID string, req shortplay.UpsertAssetRequest) (shortplay.AssetSummary, error) {
	parent, err := s.shortplayParentForVariant(shortplayID, req.DerivedFrom)
	if err != nil {
		return shortplay.AssetSummary{}, err
	}
	if strings.TrimSpace(req.Name) != "" {
		return shortplay.AssetSummary{}, BadAuthRequest("衍生形象的名称用 variantName；显示名由父资产名合成")
	}
	if len(shortplay.MergeAliases(nil, req.Aliases)) > 0 {
		return shortplay.AssetSummary{}, BadAuthRequest("衍生形象继承父资产的别名，不能单独设置")
	}
	if strings.TrimSpace(req.Voice) != "" {
		return shortplay.AssetSummary{}, BadAuthRequest("衍生形象继承父资产的音色，不能单独设置 voice")
	}
	if kind := strings.TrimSpace(req.Kind); kind != "" && kind != parent.Kind {
		return shortplay.AssetSummary{}, BadAuthRequest("衍生形象的分类继承父资产，不能单独指定")
	}
	variantName := strings.TrimSpace(req.VariantName)
	if err := shortplay.ValidateVariantName(parent, variantName); err != nil {
		return shortplay.AssetSummary{}, BadAuthRequest(err.Error())
	}
	siblings, err := s.repo.ShortplayAssetsByParent(shortplayID, parent.ID)
	if err != nil {
		return shortplay.AssetSummary{}, err
	}
	if err := shortplayVariantNameAvailable(siblings, variantName, 0); err != nil {
		return shortplay.AssetSummary{}, err
	}
	rows, err := s.repo.ShortplayAssets(shortplayID)
	if err != nil {
		return shortplay.AssetSummary{}, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		row := model.ShortplayAsset{
			ShortplayID:        shortplayID,
			ID:                 shortplay.NextAssetID(rows),
			Kind:               parent.Kind,
			AliasesJSON:        "[]",
			Description:        strings.TrimSpace(req.Description),
			Appearance:         strings.TrimSpace(req.Appearance),
			Locked:             req.Locked,
			DerivedFrom:        parent.ID,
			VariantName:        variantName,
			RenderRule:         shortplayRenderRuleValue(req.RenderRule),
			Revision:           1,
			EpisodeNumbersJSON: "[]",
			Position:           nextShortplayAssetPosition(rows),
		}
		if err := s.repo.CreateShortplayAsset(&row); err == nil {
			resolved := shortplay.ResolveAsset(parent, &row)
			return shortplay.BuildAssetSummary(resolved), nil
		} else if attempt == 0 && isShortplayUniqueViolation(err) {
			if rows, err = s.repo.ShortplayAssets(shortplayID); err != nil {
				return shortplay.AssetSummary{}, err
			}
			continue
		} else {
			return shortplay.AssetSummary{}, err
		}
	}
	return shortplay.AssetSummary{}, NewAppError(http.StatusConflict, "资产序号冲突，请重试")
}

func (s *Service) updateShortplayDerivedAsset(shortplayID string, existing *model.ShortplayAsset, req shortplay.UpsertAssetRequest) (shortplay.AssetSummary, error) {
	if req.DerivedFrom != 0 && req.DerivedFrom != existing.DerivedFrom {
		return shortplay.AssetSummary{}, BadAuthRequest("衍生关系创建后不能改挂父资产")
	}
	parent, err := s.shortplayParentForVariant(shortplayID, existing.DerivedFrom)
	if err != nil {
		return shortplay.AssetSummary{}, err
	}
	if strings.TrimSpace(req.Name) != "" {
		return shortplay.AssetSummary{}, BadAuthRequest("衍生形象的名称用 variantName；显示名由父资产名合成")
	}
	if len(shortplay.MergeAliases(nil, req.Aliases)) > 0 {
		return shortplay.AssetSummary{}, BadAuthRequest("衍生形象继承父资产的别名，不能单独设置")
	}
	if strings.TrimSpace(req.Voice) != "" {
		return shortplay.AssetSummary{}, BadAuthRequest("衍生形象继承父资产的音色，不能单独设置 voice")
	}
	if kind := strings.TrimSpace(req.Kind); kind != "" && kind != parent.Kind {
		return shortplay.AssetSummary{}, BadAuthRequest("衍生形象的分类继承父资产，不能单独指定")
	}
	row := *existing
	variantName := strings.TrimSpace(req.VariantName)
	if variantName != "" && variantName != existing.VariantName {
		if err := shortplay.ValidateVariantName(parent, variantName); err != nil {
			return shortplay.AssetSummary{}, BadAuthRequest(err.Error())
		}
		siblings, err := s.repo.ShortplayAssetsByParent(shortplayID, parent.ID)
		if err != nil {
			return shortplay.AssetSummary{}, err
		}
		if err := shortplayVariantNameAvailable(siblings, variantName, existing.ID); err != nil {
			return shortplay.AssetSummary{}, err
		}
		row.VariantName = variantName
	}
	if value := strings.TrimSpace(req.Description); value != "" {
		row.Description = value
	}
	if value := strings.TrimSpace(req.Appearance); value != "" {
		row.Appearance = value
	}
	row.Locked = req.Locked
	if req.RenderRule != nil {
		row.RenderRule = strings.TrimSpace(*req.RenderRule)
	}
	applied, err := s.repo.UpdateShortplayAsset(&row, existing.Revision)
	if err != nil {
		return shortplay.AssetSummary{}, err
	}
	if !applied {
		return shortplay.AssetSummary{}, NewAppError(http.StatusConflict, "资产已被其他窗口修改，请刷新后重试")
	}
	row.Revision = existing.Revision + 1
	return shortplay.BuildAssetSummary(shortplay.ResolveAsset(parent, &row)), nil
}

// shortplayParentForVariant 读并校验衍生形象的父资产：必须存在、本身是基础角色。
func (s *Service) shortplayParentForVariant(shortplayID string, parentID int) (*model.ShortplayAsset, error) {
	if parentID <= 0 {
		return nil, BadAuthRequest("衍生形象必须指定父资产")
	}
	parent, err := s.repo.ShortplayAssetByID(shortplayID, parentID)
	if errors.Is(err, repository.ErrShortplayAssetNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, BadAuthRequest("父资产不存在；衍生形象必须指向同一作品下的角色资产")
	}
	if err != nil {
		return nil, err
	}
	if parent.DerivedFrom != 0 {
		return nil, BadAuthRequest("衍生形象不能再衍生：父资产必须是基础角色资产")
	}
	if parent.Kind != shortplay.AssetCharacter {
		return nil, BadAuthRequest("只有角色资产有衍生形象；道具与场景暂无多形态需求")
	}
	return parent, nil
}

// shortplayVariantNameAvailable 检查同一角色下形象名是否已被别的衍生行占用。
// skipID 是更新时排除自己。
func shortplayVariantNameAvailable(siblings []model.ShortplayAsset, variantName string, skipID int) error {
	key := production.NormalizeName(variantName)
	for _, sibling := range siblings {
		if sibling.ID == skipID || sibling.DerivedFrom == 0 {
			continue
		}
		if production.NormalizeName(sibling.VariantName) == key {
			return BadAuthRequest(fmt.Sprintf("形象名「%s」已被同一角色的另一套衍生形象使用", sibling.VariantName))
		}
	}
	return nil
}

// nextShortplayAssetPosition 在现有行之后追加展示位置，不回收已删除的序号。
func nextShortplayAssetPosition(rows []model.ShortplayAsset) int {
	max := 0
	for _, row := range rows {
		if row.Position > max {
			max = row.Position
		}
	}
	return max + 1
}

// shortplayRenderRuleValue 把可选 renderRule 归一成落库字符串；nil 或空串就是没有规则。
func shortplayRenderRuleValue(rule *string) string {
	if rule == nil {
		return ""
	}
	return strings.TrimSpace(*rule)
}

// resolveShortplayAssetRows 把衍生行合成「父资产 + 本行覆盖」的读模型。
// 所有读资产的地方共用这里，避免各自拼 firstNonEmpty 拼出不同显示名。
func resolveShortplayAssetRows(rows []model.ShortplayAsset) []model.ShortplayAsset {
	byID := make(map[int]*model.ShortplayAsset, len(rows))
	for index := range rows {
		byID[rows[index].ID] = &rows[index]
	}
	resolved := make([]model.ShortplayAsset, 0, len(rows))
	for index := range rows {
		row := rows[index]
		if row.DerivedFrom == 0 {
			resolved = append(resolved, row)
			continue
		}
		parent := byID[row.DerivedFrom]
		resolved = append(resolved, shortplay.ResolveAsset(parent, &row))
	}
	return resolved
}
