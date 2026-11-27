package shortplay

import (
	"errors"
	"strings"

	"yingce/backend/internal/model"
) // 资产写入的纯规则。请求/读模型的形状在 asset_contract.go，提取落库计划在 extract_apply.go；
// 本文件只负责「一次提交怎么校验、哪些字段保留」。

// ValidateAssetWrite 校验一次写入的类型与名称，供新建与更新共用。
func ValidateAssetWrite(kind, name string) (string, string, error) {
	kind = strings.TrimSpace(kind)
	name = strings.TrimSpace(name)
	if err := ValidateAssetKind(kind); err != nil {
		return "", "", err
	}
	if name == "" {
		return "", "", errors.New("资产名称不能为空")
	}
	if len([]rune(name)) > 120 {
		return "", "", errors.New("资产名称不能超过 120 字")
	}
	return kind, name, nil
}

// NewBaseAssetRow 组装新建基础资产行；衍生形象在 W05 接入，这里直接拒绝。
func NewBaseAssetRow(shortplayID string, id int, req UpsertAssetRequest) (model.ShortplayAsset, error) {
	if req.DerivedFrom != 0 || strings.TrimSpace(req.VariantName) != "" {
		return model.ShortplayAsset{}, errors.New("衍生造型暂未开放")
	}
	kind, name, err := ValidateAssetWrite(req.Kind, req.Name)
	if err != nil {
		return model.ShortplayAsset{}, err
	}
	voice := ""
	if kind == AssetCharacter {
		voice = strings.TrimSpace(req.Voice)
	}
	row := model.ShortplayAsset{
		ShortplayID: shortplayID,
		ID:          id,
		Kind:        kind,
		Name:        name,
		Description: strings.TrimSpace(req.Description),
		Appearance:  strings.TrimSpace(req.Appearance),
		Voice:       voice,
		Locked:      req.Locked,
	}
	if len(req.Aliases) > 0 {
		row.AliasesJSON = encodeStrings(MergeAliases(nil, req.Aliases))
	}
	if req.RenderRule != nil {
		row.RenderRule = strings.TrimSpace(*req.RenderRule)
	}
	return row, nil
}

// ApplyBaseAssetWrite 把一次更新写进已有基础资产行的副本。
//
// 合同：空字符串不覆盖已有设定（页面只提交改过的字段时不清空旧值）；别名按集合并入；
// renderRule 为 nil 时保留原值，显式传入空串才清空。
func ApplyBaseAssetWrite(existing model.ShortplayAsset, req UpsertAssetRequest) (model.ShortplayAsset, error) {
	if existing.DerivedFrom != 0 {
		return model.ShortplayAsset{}, errors.New("衍生造型暂未开放")
	}
	if req.DerivedFrom != 0 || strings.TrimSpace(req.VariantName) != "" {
		return model.ShortplayAsset{}, errors.New("衍生造型暂未开放")
	}
	kind := existing.Kind
	if strings.TrimSpace(req.Kind) != "" {
		kind = req.Kind
	}
	name := existing.Name
	if strings.TrimSpace(req.Name) != "" {
		name = req.Name
	}
	kind, name, err := ValidateAssetWrite(kind, name)
	if err != nil {
		return model.ShortplayAsset{}, err
	}
	row := existing
	row.Kind = kind
	row.Name = name
	if aliases := MergeAliases(SplitAliasesJSON(existing.AliasesJSON), req.Aliases); len(aliases) > 0 {
		row.AliasesJSON = encodeStrings(aliases)
	}
	if value := strings.TrimSpace(req.Description); value != "" {
		row.Description = value
	}
	if value := strings.TrimSpace(req.Appearance); value != "" {
		row.Appearance = value
	}
	if row.Kind == AssetCharacter {
		if value := strings.TrimSpace(req.Voice); value != "" {
			row.Voice = value
		}
	} else {
		row.Voice = ""
	}
	row.Locked = req.Locked
	if req.RenderRule != nil {
		row.RenderRule = strings.TrimSpace(*req.RenderRule)
	}
	return row, nil
}
