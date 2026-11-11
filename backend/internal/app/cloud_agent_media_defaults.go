package app

import (
	"encoding/json"
	"strconv"
	"strings"
	"yingce/backend/internal/model"
)

func applyCloudAgentResolvedMediaDefaults(req *CreateTaskRequest, plan *cloudAgentMediaPlan, task *model.Task) error {
	if req == nil || plan == nil || task == nil {
		return BadAuthRequest("媒体生成准入结果无效，未提交生成任务")
	}
	var input map[string]any
	if err := json.Unmarshal([]byte(task.InputJSON), &input); err != nil {
		return err
	}
	config, ok := input["config"].(map[string]any)
	if !ok {
		return BadAuthRequest("媒体生成未解析出模型配置，未提交生成任务")
	}
	plan.Args.Size = stringValue(config["size"])
	if plan.Args.Mode == "image" {
		plan.Args.Quality = stringValue(config["quality"])
		plan.Args.ImageResolution = stringValue(config["imageResolution"])
	}
	if plan.Args.Mode == "video" {
		plan.Args.Quality = stringValue(config["vquality"])
		if raw := stringValue(config["videoSeconds"]); raw != "" {
			seconds, err := strconv.Atoi(raw)
			if err != nil || seconds < 0 {
				return BadAuthRequest("模型目录返回的默认视频时长无效，未提交生成任务")
			}
			plan.Args.Duration = seconds
		}
		if raw := stringValue(config["videoGenerateAudio"]); raw != "" {
			audio, err := strconv.ParseBool(raw)
			if err != nil {
				return BadAuthRequest("模型目录返回的默认音频参数无效，未提交生成任务")
			}
			plan.Args.VideoGenerateAudio = &audio
		}
	}
	plan.Args.Prepared = nil
	req.Input = input
	req.Prompt = stringValue(input["prompt"])
	return nil
}

func (s *Service) cloudAgentMediaModelDefaults(userID string, a cloudAgentMediaArgs) (map[string]any, error) {
	if a.Mode != "image" && a.Mode != "video" {
		return nil, nil
	}
	access, err := s.userGroupAccess(userID)
	if err != nil {
		return nil, err
	}
	if a.LogicalModelID != "" {
		models, err := s.PublicLogicalModels(access, nil)
		if err != nil {
			return nil, err
		}
		for _, item := range models {
			if item.ID != a.LogicalModelID {
				continue
			}
			if !item.Available || normalizeCapability(item.Capability) != a.Mode {
				return nil, BadAuthRequest("所选媒体模型当前不可用或能力不匹配")
			}
			return item.DefaultOptions, nil
		}
		return nil, BadAuthRequest("所选前台模型不存在或已下架")
	}
	catalog, err := s.ModelCatalog(access, nil)
	if err != nil {
		return nil, err
	}
	for _, channel := range catalog.Channels {
		if channel.ID != a.ChannelID {
			continue
		}
		for _, item := range channel.Models {
			if item.ModelKey != a.ChannelModelKey {
				continue
			}
			if !item.Available || normalizeCapability(item.Capability) != a.Mode {
				return nil, BadAuthRequest("所选渠道媒体模型当前不可用或能力不匹配")
			}
			return item.DefaultOptions, nil
		}
		return nil, BadAuthRequest("所选渠道模型不存在或已下架")
	}
	return nil, BadAuthRequest("所选模型渠道不可用")
}

func applyCloudAgentMediaCatalogDefaults(a *cloudAgentMediaArgs, defaults map[string]any) error {
	if a == nil || defaults == nil {
		return nil
	}
	if strings.TrimSpace(a.Size) == "" {
		a.Size = stringValue(defaults["size"])
	}
	if strings.TrimSpace(a.Quality) == "" {
		key := "quality"
		if a.Mode == "video" {
			key = "vquality"
		}
		a.Quality = stringValue(defaults[key])
	}
	if a.Mode == "video" {
		if a.Duration == 0 {
			switch value := defaults["videoSeconds"].(type) {
			case int:
				a.Duration = value
			case int32:
				a.Duration = int(value)
			case int64:
				a.Duration = int(value)
			case float64:
				if value != float64(int(value)) {
					return BadAuthRequest("模型目录返回的默认视频时长无效")
				}
				a.Duration = int(value)
			case json.Number:
				seconds, err := value.Int64()
				if err != nil {
					return BadAuthRequest("模型目录返回的默认视频时长无效")
				}
				a.Duration = int(seconds)
			case string:
				seconds, err := strconv.Atoi(strings.TrimSpace(value))
				if err != nil {
					return BadAuthRequest("模型目录返回的默认视频时长无效")
				}
				a.Duration = seconds
			}
		}
		if a.VideoGenerateAudio == nil {
			if value, ok := defaults["videoGenerateAudio"].(bool); ok {
				a.VideoGenerateAudio = &value
			}
		}
	}
	return nil
}

func (s *Service) applyCloudAgentMediaDefaults(userID string, a *cloudAgentMediaArgs) error {
	defaults, err := s.cloudAgentMediaModelDefaults(userID, *a)
	if err != nil {
		return err
	}
	return applyCloudAgentMediaCatalogDefaults(a, defaults)
}
