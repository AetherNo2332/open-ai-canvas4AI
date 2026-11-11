package app

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"yingce/backend/internal/model"
	"yingce/backend/internal/payment"
)

func (s *Service) preparePaymentOrder(userID, productID, providerID, idempotencyKey, requestedMethod string) (*model.PaymentOrder, payment.Provider, payment.Config, error) {
	product, err := s.repo.TopupProduct(productID)
	if err != nil || !product.Enabled {
		return nil, nil, nil, BadAuthRequest("充值商品不存在或已停用")
	}
	// 积分功能关闭时积分商品不可购买；存储空间商品不依赖积分功能。
	if topupProductKind(product.Kind) == model.TopupProductKindCredits {
		if err := s.RequireFeature(FeatureCredits); err != nil {
			return nil, nil, nil, err
		}
	}
	provider, ok := s.paymentRegistry.Get(providerID)
	if !ok {
		return nil, nil, nil, BadAuthRequest("未知支付渠道")
	}
	view, config, err := s.paymentProviderView(provider.Descriptor())
	if err != nil {
		return nil, nil, nil, err
	}
	if !view.Enabled || !view.Configured || config == nil {
		return nil, nil, nil, Forbidden("支付渠道未启用或尚未配置")
	}
	values, err := s.decryptPaymentConfig(config)
	if err != nil {
		return nil, nil, nil, err
	}
	options, err := s.paymentProviderOptions(provider.Descriptor().ID, values)
	if err != nil {
		return nil, nil, nil, err
	}
	method, err := resolvePaymentMethod(options.Methods, requestedMethod)
	if err != nil {
		return nil, nil, nil, err
	}
	// 商品行总有货币（列默认 CNY），这里的兜底与列默认值一致。
	productCurrency := firstNonEmpty(product.Currency, model.PriceCurrencyCNY)
	rateMicros := int64(0)
	if productCurrency != options.SettlementCurrency {
		if rateMicros, err = s.paymentRateMicros(); err != nil {
			return nil, nil, nil, err
		}
	}
	chargeAmount, appliedRate, err := convertPaymentAmount(product.AmountFen, productCurrency, options.SettlementCurrency, rateMicros)
	if err != nil {
		return nil, nil, nil, err
	}
	activeCount, err := s.repo.ActivePaymentOrderCount(userID)
	if err != nil {
		return nil, nil, nil, err
	}
	if activeCount >= maxActivePaymentOrdersPerUser {
		return nil, nil, nil, NewAppError(http.StatusConflict, "未支付订单过多，请先完成或关闭已有订单")
	}
	now := time.Now()
	order := &model.PaymentOrder{
		ID: newID(), UserID: userID, IdempotencyKey: idempotencyKey, MerchantOrderNo: newID(),
		ProductID: product.ID, ProductName: product.Name, ProductKind: topupProductKind(product.Kind),
		StorageBytes: product.StorageBytes, StorageDurationDays: product.StorageDurationDays, ProviderID: provider.Descriptor().ID,
		PluginID: provider.Descriptor().PluginID, PluginVersion: provider.Descriptor().PluginVersion, ProviderConfigID: config.ID, ProviderConfigVersion: config.Version,
		AmountFen: product.AmountFen, Currency: productCurrency, CreditsMicrocredits: product.CreditsMicrocredits,
		ChargeAmountFen: chargeAmount, ChargeCurrency: options.SettlementCurrency, ExchangeRateMicros: appliedRate, PaymentMethod: method,
		Status: model.PaymentOrderCreated, CheckoutMode: provider.Descriptor().CheckoutMode,
		ExpiresAt: now.Add(time.Duration(config.CloseAfterMinutes) * time.Minute),
	}
	return order, provider, values, nil
}

// resolvePaymentMethod 校验用户选择的支付方式属于渠道当前启用的方式。只有一种方式时自动选中；
// 渠道没有声明方式时必须留空，由插件使用自己的默认方式。
func resolvePaymentMethod(methods []PaymentMethodView, requested string) (string, error) {
	if len(methods) == 0 {
		if requested != "" {
			return "", BadAuthRequest("该支付渠道不支持选择支付方式")
		}
		return "", nil
	}
	if requested == "" {
		if len(methods) == 1 {
			return methods[0].ID, nil
		}
		return "", BadAuthRequest("请选择支付方式")
	}
	for _, method := range methods {
		if method.ID == requested {
			return requested, nil
		}
	}
	return "", BadAuthRequest("所选支付方式不可用，请刷新后重试")
}

// paymentCreateRequest 按订单快照组装渠道下单请求；首次下单与重建收银台共用，保证金额、货币和方式不漂移。
func paymentCreateRequest(order *model.PaymentOrder, description string, values payment.Config, configID string) payment.CreateRequest {
	baseURL := strings.TrimRight(values["publicBaseUrl"], "/")
	return payment.CreateRequest{
		MerchantOrderNo: order.MerchantOrderNo, Description: description, AmountFen: order.ChargeAmountFen,
		Currency: order.ChargeCurrency, Method: order.PaymentMethod, ExpiresAt: order.ExpiresAt,
		NotifyURL: baseURL + "/api/payments/notify/" + url.PathEscape(order.ProviderID) + "/" + url.PathEscape(configID),
		ReturnURL: baseURL + "/api/payments/return/" + url.PathEscape(order.ProviderID) + "?orderId=" + url.QueryEscape(order.ID),
	}
}
