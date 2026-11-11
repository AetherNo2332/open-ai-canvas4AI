package app

import (
	"fmt"
	"maps"
	"strings"
)

func applyChannelCapabilityDefaults(config map[string]any, capability string, profile *ModelCapabilityConfig) {
	setDefault := func(key string, value any) {
		if existing, exists := config[key]; !exists || existing == nil || strings.TrimSpace(fmt.Sprint(existing)) == "" {
			// providerConfig uses string-valued controls, including booleans and counts.
			config[key] = fmt.Sprint(value)
		}
	}
	switch normalizeCapability(capability) {
	case "image":
		if profile == nil || profile.Image == nil {
			return
		}
		if profile.Image.Parameters != nil {
			for _, parameter := range profile.Image.Parameters {
				if parameter.IsEnabled() && parameter.Default != nil && isCapabilityOptionFor("image", parameter.Name) {
					setDefault(parameter.Name, parameter.Default)
				}
			}
			return
		}
		if profile.Image.Size.Parameter != "none" {
			setDefault("size", profile.Image.Size.Default)
		}
		if profile.Image.Resolution.Supported {
			setDefault("imageResolution", profile.Image.Resolution.Default)
		}
		if profile.Image.Quality.Supported {
			setDefault("quality", profile.Image.Quality.Default)
		}
		setDefault("transparentBackground", profile.Image.TransparentBackground.Default)
		setDefault("count", 1)
	case "video":
		if profile == nil || profile.Video == nil {
			return
		}
		if videoDurationSupported(profile.Video) {
			setDefault("videoSeconds", profile.Video.Duration.Default)
		}
		setDefault("size", profile.Video.DefaultRatio)
		setDefault("vquality", profile.Video.DefaultResolution)
		setDefault("videoGenerateAudio", profile.Video.GenerateAudio.Default)
		setDefault("videoWatermark", profile.Video.Watermark.Default)
	}
}

func capabilityOptionsFromConfig(capability string, config map[string]any, declared map[string]OptionConstraint) map[string]any {
	options := map[string]any{}
	if capability == "image" {
		if parameters, ok := config["imageParameters"].(map[string]any); ok {
			return maps.Clone(parameters)
		}
	}
	for key, value := range config {
		canonical := canonicalCapabilityOptionName(key)
		if !isCapabilityOptionFor(capability, canonical) || value == nil || strings.TrimSpace(fmt.Sprint(value)) == "" {
			continue
		}
		if declared != nil {
			if _, ok := declared[canonical]; !ok {
				continue
			}
		}
		options[canonical] = value
	}
	return options
}

func taskInputUsesCustomChannel(input map[string]any) bool {
	if taskInputUsesWorkflowProvider(input) {
		return false
	}
	config, ok := input["config"].(map[string]any)
	if !ok {
		return false
	}
	channelID, _ := config["channelId"].(string)
	baseURL, _ := config["baseUrl"].(string)
	apiKey, _ := config["apiKey"].(string)
	if strings.TrimSpace(channelID) != "" || systemChannelIDFromBaseURL(baseURL) != "" {
		return false
	}
	return strings.TrimSpace(baseURL) != "" && strings.TrimSpace(apiKey) != ""
}

func taskInputUsesSystemChannel(input map[string]any) bool {
	config, ok := input["config"].(map[string]any)
	if !ok {
		return false
	}
	channelID, _ := config["channelId"].(string)
	return strings.TrimSpace(channelID) != ""
}

func taskInputUsesWorkflowProvider(input map[string]any) bool {
	config, ok := input["config"].(map[string]any)
	if !ok {
		return false
	}
	// 系统渠道的 interfaceType 是客户端缓存，不是授权事实；必须先走系统模型 admission，
	// 不能通过伪造工作流协议绕开渠道模型、能力和价格校验。
	if strings.TrimSpace(stringValue(config["channelId"])) != "" {
		return false
	}
	return isWorkflowProviderInterface(strings.TrimSpace(fmt.Sprint(config["interfaceType"])))
}

func compactPersistedValue(value interface{}) interface{} {
	switch item := value.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{}, len(item))
		for key, child := range item {
			if text, ok := child.(string); ok && strings.HasPrefix(text, "data:") {
				result[key] = ""
				continue
			}
			result[key] = compactPersistedValue(child)
		}
		return result
	case []interface{}:
		result := make([]interface{}, len(item))
		for index, child := range item {
			result[index] = compactPersistedValue(child)
		}
		return result
	default:
		return value
	}
}

func (s *Service) prepareEcommerceTaskModel(req CreateTaskRequest, normalizedInput map[string]any) error {
	if metadata, ok := normalizedInput["metadata"].(map[string]any); ok && stringValue(metadata["source"]) == "ecommerce-agent" {
		stage := stringValue(metadata["stage"])
		if stage == "planning" || stage == "creative-directions" || stage == "product-understanding" {
			setting, settingErr := s.EcommerceAgentSetting()
			if settingErr != nil {
				return settingErr
			}
			if stage == "planning" {
				if !ecommerceTaskUsesModel(setting.planningRef(), req.LogicalModelID, normalizedInput) {
					return InvalidModelSelection("电商 Agent 策划模型已由管理员更新，请刷新页面后重试")
				}
			} else if !ecommerceTaskUsesModel(setting.visionRef(), req.LogicalModelID, normalizedInput) {
				return InvalidModelSelection("电商 Agent 识图模型已由管理员更新，请刷新页面后重试")
			}
			// 工作台走通用任务队列，阶段思考意图同模型一样由后台配置决定，不能信任客户端覆盖。
			thinking := setting.VisionThinkingLevel
			if stage == "planning" {
				thinking = setting.ThinkingLevel
			}
			options, _ := normalizedInput["textOptions"].(map[string]any)
			if options == nil {
				options = map[string]any{}
			}
			options["thinkingLevel"] = defaultString(strings.TrimSpace(thinking), ThinkingIntentAuto)
			normalizedInput["textOptions"] = options
		}
	}
	return nil
}
