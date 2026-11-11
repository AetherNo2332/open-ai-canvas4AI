package app

import "yingce/backend/internal/model"

func (s *Service) validateCustomChannelTaskCapability(input map[string]any, taskInput canvasGenerationInput) error {
	if taskInput.Mode == "image" {
		profile := DefaultImageCapabilityConfig(taskInput.Config.InterfaceType, taskInput.Config.Model)
		if taskInput.Config.CapabilityConfig != nil && taskInput.Config.CapabilityConfig.Image != nil {
			profile = taskInput.Config.CapabilityConfig.Image
		}
		normalized, err := NormalizeModelCapabilityConfigForModel("image", taskInput.Config.InterfaceType, taskInput.Config.Model, &ModelCapabilityConfig{Version: 1, Image: profile}, s.protocolRegistry())
		if err != nil {
			return err
		}
		profile = normalized.Image
		if profile.Parameters == nil {
			fillMissingImageSizePresets(profile, taskInput.Config.InterfaceType)
		}
		if err := imageSizePresetReadyError(profile, taskInput.Config.InterfaceType); err != nil {
			return err
		}
		return validateImageTaskWithResolvedSize(input, &taskInput, profile)
	}
	profile := taskInput.Config.CapabilityConfig
	if profile == nil || profile.Video == nil {
		if taskInput.Config.InterfaceType != string(model.ChannelInterfaceAgnesVideo) {
			return nil
		}
		profile = DefaultModelCapabilityConfigForModel(taskInput.Config.InterfaceType, taskInput.Config.Model)
	}
	normalized, err := NormalizeModelCapabilityConfigForModel("video", taskInput.Config.InterfaceType, taskInput.Config.Model, profile)
	if err != nil || normalized == nil || normalized.Video == nil {
		return BadAuthRequest("当前视频模型能力参数无效")
	}
	applyAutoDLVideoScreenSpec(&taskInput, normalized.Video)
	if config, ok := input["config"].(map[string]any); ok {
		config["size"], config["vquality"] = taskInput.Config.Size, taskInput.Config.VQuality
	}
	return validateVideoTask(normalized.Video, taskInput)
}
