package app

import (
	"strings"
)

func normalizeCapabilityValue(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func canonicalCapabilityOptionName(value string) string {
	name := strings.TrimSpace(value)
	switch name {
	case "duration":
		return "videoSeconds"
	case "aspectRatio":
		return "size"
	case "resolution":
		return "vquality"
	default:
		return name
	}
}

func canonicalModelOptionName(capability, value string) string {
	if capability == "image" {
		return strings.TrimSpace(value)
	}
	return canonicalCapabilityOptionName(value)
}

func isCapabilityOptionFor(capability string, name string) bool {
	switch normalizeCapability(capability) {
	case "image":
		return name == "size" || name == "quality" || name == "imageResolution" || name == "transparentBackground" || name == "count"
	case "video":
		return name == "size" || name == "videoSeconds" || name == "vquality" || name == "videoGenerateAudio" || name == "videoWatermark" || name == "fpsBand"
	case "audio":
		return name == "audioVoice" || name == "audioFormat" || name == "audioSpeed" || name == "audioInstructions"
	case "text":
		// systemPrompt 是请求内容，不是供应线路能力维度，不能参与路由匹配。
		return false
	default:
		return false
	}
}

func isProviderCapabilityOption(name string) bool {
	return isCapabilityOptionFor("image", name) || isCapabilityOptionFor("video", name) || isCapabilityOptionFor("audio", name) || isCapabilityOptionFor("text", name)
}
