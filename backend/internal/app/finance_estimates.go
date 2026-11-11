package app

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
)

func estimateTaskTokens(input map[string]any) tokenBillingEstimate {
	encoded, _ := json.Marshal(input)
	return tokenBillingEstimate{InputTokens: estimatedTokens(encoded), OutputTokens: maxOutputTokens(input)}
}

func estimateTaskBillingTokens(input map[string]any, capability string) tokenBillingEstimate {
	if capability == "video" {
		estimate := estimateArkVideoTokens(input)
		estimate.ReferenceVideos = referenceVideoUsageFromInput(input)
		return estimate
	}
	return estimateTaskTokens(input)
}

func estimateProxyTokens(body []byte) tokenBillingEstimate {
	var payload map[string]any
	_ = json.Unmarshal(body, &payload)
	return tokenBillingEstimate{InputTokens: estimatedTokens(body), OutputTokens: maxOutputTokens(payload)}
}

func estimatedTokens(value []byte) int64 {
	count := int64((len([]rune(string(value))) + 3) / 4)
	if count < 1 {
		return 1
	}
	return count
}

func maxOutputTokens(payload map[string]any) int64 {
	for _, key := range []string{"max_output_tokens", "max_tokens", "maxOutputTokens"} {
		value, err := strconv.ParseInt(strings.TrimSpace(fmt.Sprint(payload[key])), 10, 64)
		if err == nil && value > 0 {
			if value > 131072 {
				return 131072
			}
			return value
		}
	}
	if config, ok := payload["config"].(map[string]any); ok {
		return maxOutputTokens(config)
	}
	return 4096
}

// Token 单价按每百万 Token 配置；预授权按输入价与缓存写入价中较高者估算全部输入 Token，
// 缓存写入通常贵于普通输入，避免写入缓存后实际费用超出预留。真实结算再按 usage 拆分。
func tokenEstimateAmount(item *model.ChannelModel, estimate tokenBillingEstimate, multiplierBPS int64) (int64, error) {
	if estimate.Err != nil {
		return 0, estimate.Err
	}
	if item == nil || estimate.InputTokens < 0 || estimate.OutputTokens <= 0 || multiplierBPS <= 0 {
		return 0, errors.New("Token 计费参数无效")
	}
	amount, err := kernel.TokenBillingAmount(multiplierBPS,
		kernel.TokenBillingTerm{Tokens: estimate.InputTokens, PriceMicrocredits: max(item.InputTokenPriceMicrocredits, item.CacheWriteTokenPriceMicrocredits)},
		kernel.TokenBillingTerm{Tokens: estimate.OutputTokens, PriceMicrocredits: item.OutputTokenPriceMicrocredits},
	)
	if errors.Is(err, kernel.ErrTokenBillingOverflow) {
		return 0, errors.New("Token 计费金额溢出")
	}
	if err != nil {
		return 0, errors.New("Token 计费参数无效")
	}
	return amount, nil
}

func billingQuantity(capability string, value any) int64 {
	if capability != "video" && capability != "audio" {
		return 1
	}
	quantity, err := strconv.ParseInt(strings.TrimSpace(fmt.Sprint(value)), 10, 64)
	if err != nil || quantity <= 0 {
		return 0
	}
	return quantity
}

// requestedBillingQuantity is the quantity reserved before a provider has
// returned the generated media. Audio output length is not known at request
// time, so an audio per-second order uses one second as the minimum
// preauthorization unless the caller explicitly supplied audioSeconds.
// Settlement must replace this estimate with the measured output duration.
func requestedBillingQuantity(capability string, config map[string]any) int64 {
	if capability == "audio" {
		if config == nil {
			return 1
		}
		value, present := config["audioSeconds"]
		if !present || strings.TrimSpace(fmt.Sprint(value)) == "" {
			return 1
		}
		return billingQuantity(capability, value)
	}
	if config == nil {
		return billingQuantity(capability, nil)
	}
	return billingQuantity(capability, config["videoSeconds"])
}

func (s *Service) MarkBillingRunning(orderID string) error {
	return s.taskBilling().MarkBillingRunning(orderID)
}

func (s *Service) SettleBilling(orderID string, providerRequestID string) error {
	return s.taskBilling().SettleBilling(orderID, providerRequestID)
}

func (s *Service) RefundBilling(orderID string, errorText string) error {
	return s.taskBilling().RefundBilling(orderID, errorText)
}

func (s *Service) MarkBillingUncertain(orderID string, errorText string) error {
	return s.taskBilling().MarkBillingUncertain(orderID, errorText)
}

func (s *Service) BillingFailureRequiresReview(orderID string, taskID string, err error) bool {
	return s.taskBilling().BillingFailureRequiresReview(orderID, taskID, err)
}

func newRedeemCode() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return hex.EncodeToString(raw[:]), nil
}

func hashRedeemCode(code string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(code))))
	return hex.EncodeToString(sum[:])
}
