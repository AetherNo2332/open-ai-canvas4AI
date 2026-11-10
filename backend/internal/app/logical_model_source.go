package app

func (s *Service) applyLogicalModelSource(req *LogicalModelRequest, sourceChannelModelID string, creating bool) (string, error) {
	source, err := s.repo.ChannelModel(sourceChannelModelID)
	if err != nil {
		return "", BadAuthRequest("系统渠道模型不存在")
	}
	if _, err := s.repo.AdminSystemChannel(source.ChannelID); err != nil {
		return "", BadAuthRequest("前台模型只能同步系统渠道模型")
	}
	derivedSpec, err := channelModelCapabilitySpec(*source, s.protocolRegistry())
	if err != nil {
		return "", err
	}
	derivedDefaults, err := channelModelDefaultOptions(*source, derivedSpec, s.protocolRegistry())
	if err != nil {
		return "", err
	}
	if len(req.Routes) == 0 {
		req.CapabilitySpec = derivedSpec
		req.DefaultOptions = derivedDefaults
		req.Routes = []LogicalRouteRequest{{ChannelModelID: source.ID, Enabled: true, Priority: 100, Weight: 100}}
	}
	req.PricePolicy = "channel"
	req.BillingMode = "fixed_request"
	req.UnitPriceMicrocredits, req.InputPriceMicrocredits, req.OutputPriceMicrocredits, req.CachedPriceMicrocredits, req.CacheWritePriceMicrocredits = 0, 0, 0, 0, 0
	// 未完成定价的系统模型仅在后台目录保留同步记录，不能暴露到创作端。
	if creating {
		req.Enabled = source.Enabled && channelModelHasActivePriceTier(*source)
	}
	return normalizeCapability(source.Capability), nil
}
