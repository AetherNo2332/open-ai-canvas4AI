package paymentplugins

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

func epayGateway(config Config) (string, error) {
	gateway := strings.TrimSpace(config["gateway"])
	if gateway == "" {
		return "", errors.New("易支付配置缺少支付网关 gateway")
	}
	parsed, err := url.Parse(gateway)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("易支付网关必须是有效的 HTTP(S) 根地址")
	}
	return strings.TrimRight(gateway, "/"), nil
}

func epayVersion(config Config) string {
	version := strings.TrimSpace(config["version"])
	if version == "" {
		return "0"
	}
	return version
}

func epayCheckoutMode(config Config) string {
	if strings.TrimSpace(config["checkoutMode"]) == "qr_code" {
		return "qr_code"
	}
	return "redirect"
}

// epaySettlementCurrency 是网关实际收款货币。money 参数本身不带币种，由网关按自己的结算货币解释；
// 宿主读取同名配置换算实收金额，插件只接受与之一致的订单货币。
func epaySettlementCurrency(config Config) (string, error) {
	switch strings.ToUpper(strings.TrimSpace(config["settlementCurrency"])) {
	case "", "CNY":
		return "CNY", nil
	case "USD":
		return "USD", nil
	default:
		return "", errors.New("易支付结算货币 settlementCurrency 必须是 CNY 或 USD")
	}
}

// epayPayTypes 解析已启用的支付方式：payTypes 为逗号分隔的 type 或 type:名称；
// 旧配置只有单个 payType 时沿用它，两者都为空时默认 alipay。
func epayPayTypes(config Config) ([]string, error) {
	raw := strings.NewReplacer("\n", ",", "\r", ",", "，", ",").Replace(config["payTypes"])
	types := make([]string, 0)
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		id, _, _ := strings.Cut(entry, ":")
		id = strings.ToLower(strings.TrimSpace(id))
		if !validEpayPayType(id) {
			return nil, fmt.Errorf("易支付支付方式 %q 无效", entry)
		}
		types = append(types, id)
	}
	if len(types) > 0 {
		return types, nil
	}
	if legacy := strings.ToLower(strings.TrimSpace(config["payType"])); legacy != "" {
		if !validEpayPayType(legacy) {
			return nil, fmt.Errorf("易支付支付方式 %q 无效", legacy)
		}
		return []string{legacy}, nil
	}
	return []string{"alipay"}, nil
}

// epayRequestPayType 返回本次下单的 type 参数。宿主传入的方式必须是已启用的方式之一，
// 防止绕过管理员配置；未传方式时使用第一个启用的方式。
func epayRequestPayType(config Config, method string) (string, error) {
	types, err := epayPayTypes(config)
	if err != nil {
		return "", err
	}
	method = strings.ToLower(strings.TrimSpace(method))
	if method == "" {
		return types[0], nil
	}
	for _, item := range types {
		if item == method {
			return method, nil
		}
	}
	return "", &ProviderError{Code: "epay_method_disabled", Message: "易支付未启用所选支付方式"}
}

func validEpayPayType(value string) bool {
	if len(value) == 0 || len(value) > 32 {
		return false
	}
	for index, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || (index > 0 && (char == '-' || char == '_')) {
			continue
		}
		return false
	}
	return true
}
