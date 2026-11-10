package protocol

import "strings"

func manifestUserContent(request GenerationRequest) []any {
	userContent := make([]any, 0)
	if strings.TrimSpace(request.Prompt) != "" {
		userContent = append(userContent, map[string]any{"type": "text", "text": request.Prompt})
	}
	// Anthropic Messages 等协议拒绝 OpenAI image_url 内容块（HTTP 422 unknown variant）；
	// 参考图必须组 type=image + source(base64|url) 块。按宿主解析的协议接口判断线格式；
	// 接口 id 缺失或不可信（如渠道 api_format 标成 openai 但实际打 /messages）时，
	// 退回按端点 + BaseURL 形态识别，覆盖社区里 Anthropic 兼容网关的多种写法。
	isClaudeWire := request.InterfaceType == TextWireClaude
	if !isClaudeWire && request.Capability == CapabilityText {
		isClaudeWire = TextWireForPath(request.LinePath) == TextWireClaude ||
			AnthropicWireForBaseURL(request.BaseURL)
	}
	for _, img := range request.Images {
		val := defaultValue(img.URL, img.DataURL)
		if val == "" {
			continue
		}
		if isClaudeWire {
			source := map[string]any{"type": "url", "url": val}
			if strings.HasPrefix(val, "data:") {
				if mimeType, data, ok := manifestSplitDataURL(val); ok {
					source = map[string]any{"type": "base64", "media_type": mimeType, "data": data}
				}
			}
			userContent = append(userContent, map[string]any{"type": "image", "source": source})
		} else {
			userContent = append(userContent, map[string]any{"type": "image_url", "image_url": map[string]any{"url": val}})
		}
	}
	for _, vid := range request.Videos {
		val := defaultValue(vid.URL, vid.DataURL)
		if val != "" {
			userContent = append(userContent, map[string]any{"type": "video_url", "video_url": map[string]any{"url": val}})
		}
	}
	for _, aud := range request.Audios {
		val := defaultValue(aud.URL, aud.DataURL)
		if val != "" {
			userContent = append(userContent, map[string]any{"type": "audio_url", "audio_url": map[string]any{"url": val}})
		}
	}
	return userContent
}
