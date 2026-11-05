package app

import (
	"context"
	"strings"

	cp "yingce/backend/internal/creationplatform"
)

// Models 返回当前用户分组可见、可直接用于提交的模型选择：系统渠道模型，以及开放前台模型时的逻辑模型。
// 目录本身复用既有 ModelCatalog / PublicLogicalModels，不另建可见性或价格规则。
func (p *creationPlatform) Models(_ context.Context, caller cp.Caller, capability cp.Capability) (cp.ModelCatalog, error) {
	if err := requireCaller(caller); err != nil {
		return cp.ModelCatalog{}, err
	}
	if !capability.Valid() {
		return cp.ModelCatalog{}, cp.NewError(cp.CodeInvalidRequest, "生成能力无效")
	}
	access, err := p.s.userGroupAccess(caller.UserID)
	if err != nil {
		return cp.ModelCatalog{}, platformError(err)
	}
	intent := &ModelRequestIntent{Capability: string(capability)}
	catalog := cp.ModelCatalog{Models: []cp.ModelOption{}}
	channels, err := p.s.publicSystemChannelCatalog(access, intent)
	if err != nil {
		return cp.ModelCatalog{}, platformError(err)
	}
	for _, channel := range channels {
		for _, item := range channel.Models {
			if normalizeCapability(item.Capability) != string(capability) {
				continue
			}
			label := firstNonEmpty(item.DisplayName, item.ModelKey)
			if name := strings.TrimSpace(firstNonEmpty(channel.DisplayName, channel.Name)); name != "" {
				label = name + " · " + label
			}
			catalog.Models = append(catalog.Models, cp.ModelOption{
				Selection: cp.ModelSelection{ChannelID: channel.ID, ModelKey: item.ModelKey}, Capability: capability,
				Label: label, BillingMode: item.PricingMode, Available: item.Available,
			})
		}
	}
	frontend, err := p.s.FeatureEnabled(FeatureFrontendModels)
	if err != nil || !frontend {
		return catalog, err
	}
	logical, err := p.s.PublicLogicalModels(access, intent)
	if err != nil {
		return cp.ModelCatalog{}, platformError(err)
	}
	for _, item := range logical {
		if normalizeCapability(item.Capability) != string(capability) {
			continue
		}
		catalog.Models = append(catalog.Models, cp.ModelOption{
			Selection: cp.ModelSelection{LogicalModelID: item.ID}, Capability: capability,
			Label: firstNonEmpty(item.Name, item.Code), BillingMode: item.BillingMode, Available: true,
		})
	}
	return catalog, nil
}
