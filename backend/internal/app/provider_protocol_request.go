// 声明式协议插件的请求组装：请求体（JSON/表单/二进制）、媒体引用与 URL。

package app

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"

	"yingce/backend/internal/model"
	"yingce/backend/internal/protocol"
)

func protocolRequestFromInput(input canvasGenerationInput) protocol.GenerationRequest {
	resolution := strings.TrimSpace(input.Config.VQuality)
	if input.Mode == "image" {
		resolution = strings.TrimSpace(input.Config.ImageResolution)
	}
	if input.Mode == "video" {
		if declared := videoResolutionNameRequest(input.VideoCapability, resolution); declared != "" {
			resolution = declared
		}
	}
	aspectRatio := input.Config.Size
	if input.Mode == "image" && strings.TrimSpace(input.Config.InterfaceType) == string(model.ChannelInterfaceOpenAIImage) {
		aspectRatio = normalizePixelSize(aspectRatio)
	}
	request := protocol.GenerationRequest{
		ImageParameters: protocolImageParameterValues(input.Config.ImageParameters),
		Capability:      protocol.Capability(input.Mode),
		Model:           input.Config.Model,
		Prompt:          input.Prompt,
		Instructions:    strings.TrimSpace(input.Config.SystemPrompt),
		Images:          protocolImageReferences(input),
		Videos:          protocolMediaReferences(input.ReferenceVideos, "video"),
		Audios:          protocolMediaReferences(input.ReferenceAudios, "audio"),
		AspectRatio:     aspectRatio,
		Resolution:      resolution,
		Quality:         input.Config.Quality,
		GenerateAudio:   parseBool(input.Config.VideoGenerateAudio, false),
		Watermark:       parseBool(input.Config.VideoWatermark, false),
		Operation:       firstNonEmpty(metadataString(input.Metadata, "videoEditOperation"), metadataString(input.Metadata, "videoOperation")),
		// InterfaceType 让声明式插件宿主按协议线格式适配参考素材内容块
		// （如 Anthropic Messages 用 type=image + source，不用 OpenAI image_url）。
		InterfaceType: strings.TrimSpace(input.Config.InterfaceType),
		BaseURL:       strings.TrimSpace(input.Config.BaseURL),
		Extra: map[string]any{
			"videoSeconds":      input.Config.VideoSeconds,
			"audioVoice":        input.Config.AudioVoice,
			"audioFormat":       input.Config.AudioFormat,
			"audioSpeed":        input.Config.AudioSpeed,
			"audioInstructions": input.Config.AudioInstructions,
			"audioLanguage":     input.Config.AudioLanguage,
			"audioDialect":      input.Config.AudioDialect,
			"count":             input.Config.Count,
		},
	}
	for _, message := range input.TextHistory {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		if role != "user" && role != "assistant" && role != "system" {
			continue
		}
		if content := strings.TrimSpace(message.Content); content != "" {
			request.Messages = append(request.Messages, protocol.Message{Role: role, Content: content})
		}
	}
	request.Inputs = append(request.Inputs, request.Images...)
	request.Inputs = append(request.Inputs, request.Videos...)
	request.Inputs = append(request.Inputs, request.Audios...)
	if input.MaxOutputTokens > 0 {
		request.Extra["max_output_tokens"] = input.MaxOutputTokens
		request.Extra["max_tokens"] = input.MaxOutputTokens
	}
	if duration, err := strconv.Atoi(strings.TrimSpace(input.Config.VideoSeconds)); err == nil && duration > 0 {
		request.Duration = duration
	}
	if count, err := strconv.Atoi(strings.TrimSpace(input.Config.Count)); err == nil && count > 0 {
		request.ImageCount = count
	}
	request.Output = protocol.OutputOptions{
		Count: request.ImageCount, Duration: request.Duration, AspectRatio: request.AspectRatio,
		Resolution: request.Resolution, Quality: request.Quality, GenerateAudio: request.GenerateAudio,
		Watermark: request.Watermark, Format: input.Config.AudioFormat,
	}
	request.ProviderOptions = make(map[string]map[string]any)
	if configured, ok := input.Metadata["providerOptions"].(map[string]any); ok && input.Config.ImageParameters == nil {
		for namespace, raw := range configured {
			if options, ok := raw.(map[string]any); ok {
				request.ProviderOptions[strings.TrimSpace(namespace)] = options
			}
		}
	}
	if input.Mode == "image" && input.Config.InterfaceType == string(model.ChannelInterfaceGrokImage) {
		options := make(map[string]any)
		for name, value := range request.ProviderOptions[input.Config.InterfaceType] {
			options[name] = value
		}
		if responseFormat := grokImageResponseFormat(input.ImageCapability); responseFormat != "" {
			options["response_format"] = responseFormat
		} else {
			delete(options, "response_format")
		}
		if len(options) > 0 {
			request.ProviderOptions[input.Config.InterfaceType] = options
		} else {
			delete(request.ProviderOptions, input.Config.InterfaceType)
		}
	}
	if input.Mode == "image" && input.Config.InterfaceType == string(model.ChannelInterfaceOpenAIImage) {
		options := make(map[string]any)
		for name, value := range request.ProviderOptions[input.Config.InterfaceType] {
			options[name] = value
		}
		if responseFormat := configuredImageResponseFormat(input.ImageCapability); responseFormat != "" {
			options["response_format"] = responseFormat
		} else {
			// A stale providerOptions value must not override the model-level
			// "auto" choice after an administrator changes the setting.
			delete(options, "response_format")
		}
		if len(options) > 0 {
			request.ProviderOptions[input.Config.InterfaceType] = options
		} else {
			delete(request.ProviderOptions, input.Config.InterfaceType)
		}
	}
	return request
}

func protocolImageParameterValues(values map[string]any) map[string]any {
	return protocol.NormalizeImageParameterValues(values)
}

func protocolImageReferences(input canvasGenerationInput) []protocol.MediaReference {
	images := make([]protocol.MediaReference, 0, len(input.ReferenceImages))
	for index, value := range input.ReferenceImages {
		images = append(images, protocolMediaReference(value, "image", index))
	}
	var mask *protocol.MediaReference
	if input.Mask != nil {
		value := protocolMediaReference(*input.Mask, "image", len(images))
		mask = &value
	}
	return protocol.BuildImageReferences(input.Mode, images, mask, protocol.VideoFrameHints{
		Operation:    metadataString(input.Metadata, "videoEditOperation"),
		StartFrameID: metadataString(input.Metadata, "videoStartFrameNodeId"),
		EndFrameID:   metadataString(input.Metadata, "videoEndFrameNodeId"),
	})
}

func protocolMediaReferences(values []providerMedia, kind string) []protocol.MediaReference {
	normalized := make([]protocol.MediaReference, 0, len(values))
	for index, value := range values {
		normalized = append(normalized, protocolMediaReference(value, kind, index))
	}
	return protocol.NormalizeMediaReferences(normalized, kind)
}

func protocolMediaReference(value providerMedia, kind string, order int) protocol.MediaReference {
	return protocol.NormalizeMediaInput(protocol.MediaInput{
		ID:         value.ID,
		URL:        value.URL,
		DataURL:    value.DataURL,
		Role:       value.Role,
		MIMEType:   firstNonEmpty(strings.TrimSpace(value.MimeType), strings.TrimSpace(value.Type)),
		Name:       value.Name,
		StorageKey: value.StorageKey,
		Bytes:      value.Bytes,
		Width:      value.Width,
		Height:     value.Height,
		DurationMs: value.DurationMs,
	}, kind, order)
}

func executeProtocolRequest(ctx context.Context, config providerConfig, spec protocol.RequestSpec) ([]byte, error) {
	data, _, err := executeProtocolBinaryRequest(ctx, config, spec)
	return data, err
}

// executeProtocolBinaryRequest 是声明式插件与宿主网络能力之间的边界。manifest 只能声明
// method/path/body/auth；最终 URL 校验、凭证注入、SSRF、超时、大小限制和审计仍由宿主统一执行，
// 插件不能通过自定义请求规格绕过这些安全约束。
func executeProtocolBinaryRequest(ctx context.Context, config providerConfig, spec protocol.RequestSpec) ([]byte, string, error) {
	return executeProtocolBinaryRequestWithConsumer(ctx, config, spec, nil)
}

func executeProtocolBinaryRequestWithConsumer(ctx context.Context, config providerConfig, spec protocol.RequestSpec, consume func(string, []byte)) ([]byte, string, error) {
	if err := spec.Validate(); err != nil {
		return nil, "", err
	}
	method := strings.ToUpper(strings.TrimSpace(spec.Method))
	body, contentType, err := protocolRequestBody(ctx, config, spec)
	if err != nil {
		return nil, "", err
	}
	requestURL, err := protocolRequestURL(config.BaseURL, spec)
	if err != nil {
		return nil, "", err
	}
	req, err := http.NewRequestWithContext(ctx, method, requestURL, body)
	if err != nil {
		return nil, "", err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for name, value := range spec.Headers {
		req.Header.Set(name, value)
	}
	ApplyOutboundHeaders(req, config.Headers)
	if err := applyProtocolAuth(req, config, spec.Auth); err != nil {
		return nil, "", err
	}
	if consume != nil {
		req.Header.Set("Accept", "text/event-stream")
		return doBinaryWithConsumer(req, consume)
	}
	return doBinary(req)
}

func protocolRequestBody(ctx context.Context, config providerConfig, spec protocol.RequestSpec) (io.Reader, string, error) {
	return protocol.EncodeRequestBody(ctx, spec, func(ctx context.Context, reference protocol.MediaReference) ([]byte, string, error) {
		return protocolMediaBytes(ctx, config, reference)
	})
}

func protocolBodyObject(value any) map[string]any {
	result, _ := value.(map[string]any)
	return result
}
