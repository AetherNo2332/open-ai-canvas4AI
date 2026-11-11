package app

import "yingce/backend/internal/model"

// logicalModelBillingAmount keeps unified-price quotes and task reservations on
// the same billing rules, including protocol eligibility and token quantities.
func logicalModelBillingAmount(logicalModel *model.LogicalModel, channelModel *model.ChannelModel, capability string, quantity int64, estimate tokenBillingEstimate, groupBPS int64) (int64, int64, error) {
	var amount int64
	var err error
	switch logicalModel.BillingMode {
	case "fixed_request":
		quantity = 1
		amount, err = creditAmount(logicalModel.UnitPriceMicrocredits, quantity, groupBPS)
	case "per_second":
		if (capability != "video" && capability != "audio") || quantity <= 0 {
			return 0, 0, BadAuthRequest("当前模型按时长计费，但请求未提供有效时长")
		}
		amount, err = creditAmount(logicalModel.UnitPriceMicrocredits, quantity, groupBPS)
	case "token":
		if channelModel.Capability != capability || !supportsTokenBilling(capability, channelModel.Protocol) {
			return 0, 0, BadAuthRequest("当前供应线路不支持前台模型的 Token 计费方式")
		}
		if capability == "video" && (logicalModel.InputPriceMicrocredits != 0 || logicalModel.CachedPriceMicrocredits != 0 || logicalModel.CacheWritePriceMicrocredits != 0) {
			return 0, 0, BadAuthRequest("视频 Token 仅按视频用量定价，请将输入与缓存价格设为 0")
		}
		tokenPricing := &model.ChannelModel{
			InputTokenPriceMicrocredits:      logicalModel.InputPriceMicrocredits,
			OutputTokenPriceMicrocredits:     logicalModel.OutputPriceMicrocredits,
			CachedTokenPriceMicrocredits:     logicalModel.CachedPriceMicrocredits,
			CacheWriteTokenPriceMicrocredits: logicalModel.CacheWritePriceMicrocredits,
		}
		amount, err = tokenEstimateAmount(tokenPricing, estimate, groupBPS)
		quantity = estimate.InputTokens + estimate.OutputTokens
	default:
		return 0, 0, BadAuthRequest("当前模型计费方式暂不支持")
	}
	if err != nil {
		return 0, 0, err
	}
	if amount < 0 {
		return 0, 0, BadAuthRequest("当前模型尚未配置有效的用户价格")
	}
	return amount, quantity, nil
}
