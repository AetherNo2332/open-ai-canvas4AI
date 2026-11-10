package handler

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"
	"yingce/backend/internal/model"
	"yingce/backend/internal/service"
)

func refundSystemProxyBilling(svc *service.Service, billingOrderID string, reason string) {
	if billingOrderID == "" {
		return
	}
	if err := svc.RefundBilling(billingOrderID, reason); err != nil {
		log.Printf("system proxy billing refund failed: billing_order_id=%s error=%v", billingOrderID, err)
		// 退款写失败不能当没发生：转待核对，避免订单停在 running 被当成已扣费。
		markSystemProxyBillingUncertain(svc, billingOrderID, "退款失败，费用状态待核对："+err.Error())
	}
}

func markSystemProxyBillingUncertain(svc *service.Service, billingOrderID string, reason string) {
	if billingOrderID == "" {
		return
	}
	if err := svc.MarkBillingUncertain(billingOrderID, reason); err != nil {
		log.Printf("system proxy billing uncertainty update failed: billing_order_id=%s error=%v", billingOrderID, err)
	}
}

func apiCallLog(user *model.User, channel *model.ModelChannel, billingOrderID string, capability string, protocol model.ChannelInterfaceType, method string, path string, target string, body []byte, contentType string, status model.ApiCallStatus, statusCode int, duration time.Duration, errorText string, concurrencyLimit int) model.ApiCallLog {
	requestKind := "create"
	apiFormat := "openai"
	if protocol == model.ChannelInterfaceGeminiVeo || protocol == model.ChannelInterfaceGeminiImage {
		apiFormat = "gemini"
	}
	if method == http.MethodGet {
		requestKind = "poll"
		if strings.HasSuffix(strings.TrimRight(path, "/"), "/content") {
			requestKind = "download"
		}
	}
	return model.ApiCallLog{
		UserID:             user.ID,
		ChannelID:          channel.ID,
		BillingOrderID:     billingOrderID,
		Source:             "system-channel",
		Capability:         capability,
		RequestKind:        requestKind,
		Billable:           method == http.MethodPost,
		APIFormat:          apiFormat,
		Method:             method,
		Path:               path,
		Model:              readPayloadModel(body),
		Status:             status,
		StatusCode:         statusCode,
		DurationMs:         duration.Milliseconds(),
		Error:              errorText,
		ConcurrencyLimit:   concurrencyLimit,
		UpstreamURL:        target,
		RequestContentType: contentType,
		RequestBody:        service.SanitizeAPICallPayload(body, contentType),
	}
}

func logSystemProxyCall(svc *service.Service, log model.ApiCallLog, responseBody []byte) error {
	log.ResponseBody = service.SanitizeAPICallPayload(responseBody, "")
	svc.EnrichAPICallLog(&log, responseBody)
	return svc.LogAPICall(log)
}

func readPayloadModel(body []byte) string {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	if modelName, ok := payload["model"].(string); ok {
		return modelName
	}
	return ""
}
