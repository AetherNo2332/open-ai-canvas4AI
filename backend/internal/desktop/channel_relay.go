package desktop

import (
	"bytes"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"infinite-canvas/backend/internal/app"
	"infinite-canvas/backend/internal/handler"
	"net/http"
	"time"
)

type relayInput struct {
	ChannelID string         `json:"channelId"`
	ModelID   string         `json:"modelId"`
	Operation string         `json:"operation"`
	Payload   map[string]any `json:"payload"`
}

func serveChannelRelay(c *gin.Context, svc *app.Service) {
	var input relayInput
	if c.Request.URL.RawQuery != "" || c.GetHeader("X-Canvas-Upstream-URL") != "" || c.GetHeader("X-Canvas-Upstream-Headers") != "" || !strictJSON(c, &input) {
		if !c.IsAborted() {
			deny(c, 400)
		}
		return
	}
	actor, err := desktopActor(c, svc)
	if settingsError(c, err) {
		return
	}
	channel, err := svc.SystemChannel(input.ChannelID)
	if err != nil {
		deny(c, 404)
		return
	}
	items, err := svc.AdminChannelModels(actor, channel.ID)
	if settingsError(c, err) {
		return
	}
	path := ""
	binary := false
	switch input.Operation {
	case "models":
		path = "/models"
	case "chat.completions":
		path = "/chat/completions"
	case "images.generations":
		path = "/images/generations"
	case "audio.speech":
		path = "/audio/speech"
		binary = true
	default:
		deny(c, 400)
		return
	}
	if input.Payload == nil {
		input.Payload = map[string]any{}
	}
	if input.Operation != "models" {
		authorized := false
		for _, m := range items {
			if m.ID != input.ModelID || !m.Enabled {
				continue
			}
			if (input.Operation == "chat.completions" && string(m.Protocol) != "chat-completion") || (input.Operation == "images.generations" && string(m.Protocol) != "openai-image") || (input.Operation == "audio.speech" && string(m.Protocol) != "openai-audio") {
				continue
			}
			input.Payload["model"] = m.ProviderModelKey
			if m.ProviderModelKey == "" {
				input.Payload["model"] = m.ModelKey
			}
			authorized = true
			break
		}
		if !authorized {
			deny(c, 403)
			return
		}
	}
	policy, err := svc.RuntimePolicy()
	if settingsError(c, err) {
		return
	}
	target, err := svc.ValidateChannelOutboundURL(app.ChannelAPIURLForProtocol(channel.BaseURL, path, ""))
	if err != nil {
		deny(c, 400)
		return
	}
	body, err := json.Marshal(input.Payload)
	if settingsError(c, err) {
		return
	}
	method := "POST"
	if input.Operation == "models" {
		method = "GET"
		body = nil
	}
	request, err := http.NewRequestWithContext(c.Request.Context(), method, target.String(), bytes.NewReader(body))
	if settingsError(c, err) {
		return
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+channel.APIKey)
	headers, err := app.ParseOutboundHeadersJSON(channel.HeadersJSON)
	if settingsError(c, err) {
		return
	}
	app.ApplyOutboundHeaders(request, headers)
	app.ApplyDefaultOutboundHeaders(request)
	release, _, err := svc.AcquireChannelSlot(c.Request.Context(), channel.ID, "", time.Duration(policy.Request.CustomRelayTimeoutMinutes+1)*time.Minute)
	if settingsError(c, err) {
		return
	}
	defer release()
	response, err := svc.OutboundHTTPClientForChannel(time.Duration(policy.Request.CustomRelayTimeoutMinutes)*time.Minute, target).Do(request)
	if err != nil {
		deny(c, 502)
		return
	}
	defer response.Body.Close()
	handler.WriteDesktopRelayResponse(c, svc, response, channel.APIKey, policy.Request.CustomRelayResponseMB<<20, binary)
}
