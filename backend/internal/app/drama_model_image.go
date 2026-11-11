package app

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"yingce/backend/internal/production"
)

// dramaImageSpec 读取图片模型的能力合同；与云端 Agent 生图选项同一加载方式。
func (s *Service) dramaImageSpec(choice DramaModelChoice) (CapabilitySpec, map[string]any, bool) {
	defaults := map[string]any{}
	if choice.LogicalModelID != "" {
		snapshot, err := s.routeCatalogSnapshot()
		if err != nil {
			return CapabilitySpec{}, nil, false
		}
		cached, exists := snapshot.Models[choice.LogicalModelID]
		if !exists {
			return CapabilitySpec{}, nil, false
		}
		return cached.ProductSpec, cached.Defaults, true
	}
	if choice.ChannelID == "" {
		return CapabilitySpec{}, nil, false
	}
	item, err := s.repo.ChannelModelByKey(choice.ChannelID, choice.ModelKey)
	if err != nil {
		return CapabilitySpec{}, nil, false
	}
	profile, err := normalizedChannelModelCapability(item, s.protocolRegistry())
	if err != nil {
		return CapabilitySpec{}, nil, false
	}
	spec, err := CapabilitySpecFromModelCapabilityConfig(profile, item.Capability)
	if err != nil {
		return CapabilitySpec{}, nil, false
	}
	applyChannelCapabilityDefaults(defaults, "image", profile)
	return spec, defaults, true
}

// dramaAssetRatios 与 production.AssetSettings.Normalized 接受的角色参考图画幅一致。
var dramaAssetRatios = []string{"16:9", "4:3", "1:1", "3:4", "9:16"}

// 参考图画幅允许的偏差：宽高比的对数差不超过 0.18（约 ±20%）。16:9 可以落到 3:2，
// 不会落到 4:3 或 1:1；超出时直接报错，不能让模型悄悄出方图。
const dramaRatioTolerance = 0.18

// DramaImageTiers 是参考图可指定的分辨率档位，与模型能力合同的 tier 命名一致。
var DramaImageTiers = []string{"1k", "1.5k", "2k", "4k"}

// 允许自定义像素尺寸的模型按档位的像素总量换算宽高。
var dramaTierPixels = map[string]float64{"1k": 1024 * 1024, "1.5k": 1536 * 1536, "2k": 2048 * 2048, "4k": 4096 * 4096}

// dramaImageRequest 是一次参考图生成的尺寸参数：size 是模型真正接受的取值（像素或比例），
// resolution 是明确指定的分辨率档位；Issue 非空表示所选模型给不出这个画幅或档位。
type dramaImageRequest struct {
	Size       string
	Resolution string
	Issue      string
}

func (r dramaImageRequest) apply(config map[string]any) {
	if r.Size != "" {
		config["size"] = r.Size
	}
	if r.Resolution != "" {
		config["imageResolution"] = r.Resolution
	}
}

// dramaImageRequestFor 把期望画幅与档位换算成所选模型的尺寸参数。流水线无人值守、按张计费，
// 参考图画幅直接决定视频里人物与场景的构图，因此只在比例足够接近时替换，绝不退到模型默认的方图。
func (s *Service) dramaImageRequestFor(choice DramaModelChoice, ratio, tier string) dramaImageRequest {
	spec, defaults, ok := s.dramaImageSpec(choice)
	if !ok {
		// 能力合同读不到（例如测试替身或刚下架）时按原比例提交，由任务准入严格校验。
		return dramaImageRequest{Size: ratio}
	}
	return dramaImageRequestForSpec(spec, defaults, ratio, tier)
}

func dramaImageRequestForSpec(spec CapabilitySpec, defaults map[string]any, ratio, tier string) dramaImageRequest {
	target, ok := dramaRatioValue(ratio)
	if !ok {
		return dramaImageRequest{Issue: "参考图画幅无效：" + ratio}
	}
	constraint, declared := spec.Options["size"]
	if !declared || (spec.ImageSize != nil && spec.ImageSize.Parameter == "none") {
		return dramaImageRequest{Issue: "所选图片模型不能指定画幅，参考图会按模型默认尺寸（通常是 1:1）出图；请换用能指定尺寸的图片模型"}
	}
	tier = strings.ToLower(strings.TrimSpace(tier))
	if tier != "" && !dramaImageTierAvailable(spec, tier) {
		return dramaImageRequest{Issue: "所选图片模型不支持 " + strings.ToUpper(tier) + " 分辨率"}
	}
	if tier == "" {
		tier = dramaDefaultImageTier(spec, defaults)
	}
	request := dramaImageRequest{}
	if constraint, independent := spec.Options["imageResolution"]; independent && tier != "" && matchOptionConstraint("imageResolution", constraint, tier) {
		// 档位比较不分大小写，下发时仍用声明的原值（如 2K）。
		request.Resolution = dramaDeclaredOptionValue(constraint, tier)
	}
	candidates := dramaSizeCandidates(spec, constraint, tier)
	best, bestRatio, bestScore := "", "", math.Inf(1)
	for _, candidate := range candidates {
		value, ok := dramaRatioValue(firstNonEmpty(candidate.ratio, candidate.value))
		if !ok {
			continue
		}
		score := math.Abs(math.Log(value) - math.Log(target))
		if score < bestScore-1e-9 {
			best, bestRatio, bestScore = candidate.value, firstNonEmpty(candidate.ratio, candidate.value), score
		}
	}
	if best != "" && bestScore <= dramaRatioTolerance {
		request.Size = best
		return request
	}
	if dramaSizeAllowsCustom(constraint) {
		if spec.ImageSize != nil && spec.ImageSize.Parameter == "size" {
			request.Size = dramaCustomPixelSize(target, tier)
		} else {
			request.Size = ratio
		}
		return request
	}
	if best == "" {
		return dramaImageRequest{Issue: "所选图片模型没有声明可用的画面尺寸，无法按 " + ratio + " 出图"}
	}
	return dramaImageRequest{Issue: fmt.Sprintf("所选图片模型没有接近 %s 的尺寸（最接近的是 %s），参考图会变形或变成方图；请换用支持 %s 的图片模型，或改参考图画幅", ratio, dramaRatioLabel(bestRatio), ratio)}
}

// dramaDeclaredOptionValue 在声明值里查找给定档位并返回声明原值，不区分大小写。
func dramaDeclaredOptionValue(constraint OptionConstraint, value string) string {
	for _, candidate := range constraint.Values {
		if text, ok := candidate.(string); ok && strings.EqualFold(strings.TrimSpace(text), value) {
			return text
		}
	}
	return value
}

type dramaSizeCandidate struct {
	value string
	ratio string
}

// dramaSizeCandidates 列出模型在该档位下接受的尺寸；档位没有专属预设时使用全部声明值。
func dramaSizeCandidates(spec CapabilitySpec, constraint OptionConstraint, tier string) []dramaSizeCandidate {
	candidates := []dramaSizeCandidate{}
	if spec.ImageSize != nil && len(spec.ImageSize.Presets) > 0 {
		inTier := false
		for _, preset := range spec.ImageSize.Presets {
			if tier != "" && preset.Tier == tier {
				inTier = true
				break
			}
		}
		for _, preset := range spec.ImageSize.Presets {
			if inTier && preset.Tier != tier {
				continue
			}
			value := strings.TrimSpace(preset.Ratio)
			if spec.ImageSize.Parameter == "size" {
				value = strings.TrimSpace(preset.Size)
			}
			if value != "" && matchOptionConstraint("size", constraint, value) {
				candidates = append(candidates, dramaSizeCandidate{value: value, ratio: strings.TrimSpace(preset.Ratio)})
			}
		}
		if inTier {
			return candidates
		}
	}
	for _, value := range constraint.Values {
		if text := strings.TrimSpace(stringValue(value)); text != "" && text != "*" {
			candidates = append(candidates, dramaSizeCandidate{value: text})
		}
	}
	return candidates
}

func dramaSizeAllowsCustom(constraint OptionConstraint) bool {
	for _, value := range constraint.Values {
		if normalizedScalar(value) == "*" {
			return true
		}
	}
	return false
}

// dramaImageTierAvailable 判断模型是否提供该档位：独立分辨率参数按其取值，否则按尺寸预设的档位。
func dramaImageTierAvailable(spec CapabilitySpec, tier string) bool {
	if constraint, independent := spec.Options["imageResolution"]; independent {
		return matchOptionConstraint("imageResolution", constraint, tier)
	}
	if spec.ImageSize != nil {
		for _, preset := range spec.ImageSize.Presets {
			if preset.Tier == tier {
				return true
			}
		}
	}
	return false
}

// dramaImageTiersFor 返回模型可选的分辨率档位（按 DramaImageTiers 排序）。
func dramaImageTiersFor(spec CapabilitySpec) []string {
	tiers := []string{}
	for _, tier := range DramaImageTiers {
		if dramaImageTierAvailable(spec, tier) {
			tiers = append(tiers, tier)
		}
	}
	return tiers
}

// dramaDefaultImageTier 是作品未指定分辨率时的档位：模型默认分辨率，其次是默认尺寸所在的预设档位。
func dramaDefaultImageTier(spec CapabilitySpec, defaults map[string]any) string {
	if tier := strings.ToLower(stringValue(defaults["imageResolution"])); tier != "" && tier != "auto" && dramaImageTierAvailable(spec, tier) {
		return tier
	}
	if spec.ImageSize == nil || len(spec.ImageSize.Presets) == 0 {
		return ""
	}
	defaultSize := stringValue(defaults["size"])
	for _, preset := range spec.ImageSize.Presets {
		if defaultSize != "" && (strings.EqualFold(preset.Size, defaultSize) || preset.Ratio == defaultSize) {
			return preset.Tier
		}
	}
	return spec.ImageSize.Presets[0].Tier
}

// dramaCustomPixelSize 按档位像素总量换算宽高，取 16 的倍数。
func dramaCustomPixelSize(ratio float64, tier string) string {
	pixels := dramaTierPixels[tier]
	if pixels == 0 {
		pixels = dramaTierPixels["1k"]
	}
	height := math.Sqrt(pixels / ratio)
	width := height * ratio
	round := func(value float64) int { return max(16, int(math.Round(value/16))*16) }
	return fmt.Sprintf("%dx%d", round(width), round(height))
}

// dramaRatioLabel 把 "1024x1024" 这类像素值显示成约分后的比例。
func dramaRatioLabel(value string) string {
	ratio, ok := dramaRatioValue(value)
	if !ok {
		return value
	}
	for _, label := range []string{"21:9", "16:9", "3:2", "4:3", "1:1", "3:4", "2:3", "9:16"} {
		if known, _ := dramaRatioValue(label); math.Abs(math.Log(known)-math.Log(ratio)) < 0.02 {
			return label
		}
	}
	return value
}

// dramaImageRatioMismatch 检查生成结果的实际画幅；宽高未知（如部分 WebP）时不判断。
func dramaImageRatioMismatch(width, height int, ratio string) bool {
	target, ok := dramaRatioValue(ratio)
	if !ok || width <= 0 || height <= 0 {
		return false
	}
	return math.Abs(math.Log(float64(width)/float64(height))-math.Log(target)) > dramaRatioTolerance
}

// dramaRatioValue 解析 "9:16" 或 "1024x1824" 为宽高比；auto 等非尺寸值返回 false。
func dramaRatioValue(value string) (float64, bool) {
	value = strings.TrimSpace(strings.ToLower(value))
	separator := ":"
	if !strings.Contains(value, ":") {
		separator = "x"
	}
	left, right, found := strings.Cut(value, separator)
	if !found {
		return 0, false
	}
	width, widthErr := strconv.ParseFloat(strings.TrimSpace(left), 64)
	height, heightErr := strconv.ParseFloat(strings.TrimSpace(right), 64)
	if widthErr != nil || heightErr != nil || width <= 0 || height <= 0 {
		return 0, false
	}
	return width / height, true
}

// dramaVideoScreen 是剧情段视频按作品画幅出片时发送的画面参数。声明了比例参数的模型发 Size；
// 能力里带 fixedScreenSpec 的模型（如 AutoDL）用完整分辨率标签表达横竖，任务准入会清空它们的 size，
// 只能发 Resolution（例如 768p竖）。Issue 非空表示所选模型给不出这个画幅。
type dramaVideoScreen struct {
	Size       string
	Resolution string
	Issue      string
}

func (s dramaVideoScreen) apply(config map[string]any) {
	if s.Size != "" {
		config["size"] = s.Size
	}
	if s.Resolution != "" {
		config["vquality"] = s.Resolution
	}
}

// dramaVideoScreenFor 只按能力合同声明的取值换算；给不出时返回原因，不让上游按默认画幅（多为横屏）出片。
// resolution 是作品指定的分辨率档位，为空时不发分辨率，由任务准入按模型默认补齐；模型没有这一档时返回原因，不悄悄降档。
func dramaVideoScreenFor(video *VideoCapabilityConfig, ratio string, resolution string) dramaVideoScreen {
	target, ok := dramaRatioValue(ratio)
	if !ok {
		return dramaVideoScreen{Issue: "作品画幅无效：" + ratio}
	}
	if video == nil {
		return dramaVideoScreen{Issue: "模型未声明视频能力"}
	}
	if video.FixedScreenSpec != nil {
		return dramaLabeledVideoScreen(video, ratio, target, resolution)
	}
	if len(video.Ratios) == 0 {
		return dramaVideoScreen{Issue: "所选视频模型没有声明可选画幅，无法保证按 " + ratio + " 出片；请在后台为该模型配置画面比例，或换用其他视频模型"}
	}
	if !videoRatioAllowed(video.Ratios, ratio) {
		return dramaVideoScreen{Issue: fmt.Sprintf("所选视频模型不支持 %s 画幅（可选 %s），请换用支持 %s 的视频模型", ratio, strings.Join(video.Ratios, "、"), ratio)}
	}
	if resolution == "" {
		return dramaVideoScreen{Size: ratio}
	}
	if name := videoResolutionNameRequest(video, resolution); name != "" {
		return dramaVideoScreen{Size: ratio, Resolution: name}
	}
	if len(video.Resolutions) == 0 {
		return dramaVideoScreen{Issue: fmt.Sprintf("所选视频模型不提供可选分辨率，无法按 %s 出片；请把视频分辨率改为“模型默认”，或换用其他视频模型", resolution)}
	}
	return dramaVideoScreen{Issue: fmt.Sprintf("所选视频模型不支持 %s 分辨率（可选 %s），请改选分辨率或换用其他视频模型", resolution, strings.Join(video.Resolutions, "、"))}
}

// dramaLabeledVideoScreen 从分辨率标签里挑与作品画幅一致的一档：作品指定了分辨率时只取同档，
// 否则优先模型默认分辨率，其次与默认同档（默认 768p横、作品竖屏时取 768p竖），再按后台配置的顺序取第一个。
func dramaLabeledVideoScreen(video *VideoCapabilityConfig, ratio string, target float64, resolution string) dramaVideoScreen {
	matches := []string{}
	for _, label := range video.Resolutions {
		if value, ok := dramaResolutionLabelRatio(label); ok && math.Abs(math.Log(value)-math.Log(target)) <= dramaRatioTolerance {
			matches = append(matches, label)
		}
	}
	if len(matches) == 0 {
		return dramaVideoScreen{Issue: fmt.Sprintf("所选视频模型的分辨率里没有 %s 画幅（可选 %s），请换用其他视频模型，或在后台为该模型添加对应分辨率", ratio, strings.Join(video.Resolutions, "、"))}
	}
	if resolution != "" {
		tier := dramaResolutionTier(resolution)
		for _, label := range matches {
			if strings.EqualFold(label, resolution) || (tier != "" && dramaResolutionTier(label) == tier) {
				return dramaVideoScreen{Resolution: label}
			}
		}
		return dramaVideoScreen{Issue: fmt.Sprintf("所选视频模型没有 %s 画幅的 %s 分辨率（可选 %s），请改选分辨率或换用其他视频模型", ratio, resolution, strings.Join(matches, "、"))}
	}
	for _, label := range matches {
		if strings.EqualFold(label, video.DefaultResolution) {
			return dramaVideoScreen{Resolution: label}
		}
	}
	if tier := dramaResolutionTier(video.DefaultResolution); tier != "" {
		for _, label := range matches {
			if dramaResolutionTier(label) == tier {
				return dramaVideoScreen{Resolution: label}
			}
		}
	}
	return dramaVideoScreen{Resolution: matches[0]}
}

var (
	dramaLabelPixelsPattern = regexp.MustCompile(`(\d{2,5})\s*[*xX×]\s*(\d{2,5})`)
	dramaLabelRatioPattern  = regexp.MustCompile(`(\d{1,2})\s*:\s*(\d{1,2})`)
	dramaLabelTierPattern   = regexp.MustCompile(`^\s*(\d{3,4})\s*[pP]`)
)

// dramaResolutionLabelRatio 读出分辨率标签表达的宽高比：写了像素（768*1344）或比例（1:1）的按数值，
// 否则“竖”按 9:16、“横”按 16:9；读不出方向时返回 false，这一档不参与画幅匹配。
func dramaResolutionLabelRatio(label string) (float64, bool) {
	for _, pattern := range []*regexp.Regexp{dramaLabelPixelsPattern, dramaLabelRatioPattern} {
		if match := pattern.FindStringSubmatch(label); match != nil {
			width, _ := strconv.ParseFloat(match[1], 64)
			height, _ := strconv.ParseFloat(match[2], 64)
			if width > 0 && height > 0 {
				return width / height, true
			}
		}
	}
	switch {
	case strings.Contains(label, "竖"):
		return 9.0 / 16.0, true
	case strings.Contains(label, "横"):
		return 16.0 / 9.0, true
	}
	return 0, false
}

// dramaResolutionTier 是分辨率标签的清晰度档位，例如 768p竖 与 768p横 同为 768。
func dramaResolutionTier(label string) string {
	if match := dramaLabelTierPattern.FindStringSubmatch(label); match != nil {
		return match[1]
	}
	return ""
}

// dramaSizeRejected 判断准入错误是否只因尺寸参数；只看结构化字段，不解析文案。

func (s *Service) dramaModelOption(choice DramaModelChoice, capability, label, priceLabel string) DramaModelOption {
	option := DramaModelOption{Choice: choice, Capability: capability, Label: label, PriceLabel: priceLabel}
	if capability == "video" {
		if profile, err := s.dramaVideoProfile(choice); err == nil {
			option.MaxVideoSeconds = profile.maxSeconds()
			option.MaxAudios = profile.MaxAudios
			option.MinReferenceImages = profile.MinImages
			option.TailFrameChain = profile.supports("image_to_video")
			option.VideoRatios = profile.ratios()
			audio := profile.GenerateAudio
			option.GenerateAudio = &audio
			option.VideoResolutions, option.DefaultVideoResolution = dramaVideoResolutionsFor(profile.video)
		}
	}
	if capability == "image" {
		if spec, defaults, ok := s.dramaImageSpec(choice); ok {
			option.ImageResolutions = dramaImageTiersFor(spec)
			option.DefaultImageResolution = dramaDefaultImageTier(spec, defaults)
			for _, ratio := range dramaAssetRatios {
				if dramaImageRequestForSpec(spec, defaults, ratio, "").Issue == "" {
					option.ImageRatios = append(option.ImageRatios, ratio)
				}
			}
		}
	}
	return option
}

// dramaVideoResolutionsFor 是作品可指定的视频分辨率档位与模型默认档位。用分辨率标签区分横竖的模型
// （fixedScreenSpec，如 768p竖 / 768p横）按清晰度合并成一档（768p），横竖由作品画幅决定。
func dramaVideoResolutionsFor(video *VideoCapabilityConfig) ([]string, string) {
	if video == nil {
		return nil, ""
	}
	tierOf := func(label string) string {
		label = strings.TrimSpace(label)
		if video.FixedScreenSpec != nil {
			if tier := dramaResolutionTier(label); tier != "" {
				return tier + "p"
			}
		}
		return label
	}
	values := []string{}
	for _, label := range video.Resolutions {
		if value := tierOf(label); value != "" && !slices.Contains(values, value) {
			values = append(values, value)
		}
	}
	fallback := ""
	if value := tierOf(video.DefaultResolution); slices.Contains(values, value) {
		fallback = value
	}
	return values, fallback
}

// dramaDurationOptions 只取能力合同里生效的那组时长声明：枚举只用档位，范围只用上下限与步长。
// 合同切换过选择方式时另一组字段可能残留旧值，混用会选出任务准入拒绝的秒数。
func dramaDurationOptions(duration VideoDurationConfig) production.DurationOptions {
	switch duration.Selection {
	case "enum":
		return production.DurationOptions{Values: append([]int(nil), duration.Values...)}
	case "range":
		return production.DurationOptions{Min: duration.Min, Max: duration.Max, Step: duration.Step}
	}
	return production.DurationOptions{Values: duration.Values, Min: duration.Min, Max: duration.Max, Step: duration.Step}
}

// dramaMaxVideoSeconds 是视频模型单次可生成的最长秒数，也是一个剧情段的时长上限。
func dramaMaxVideoSeconds(options production.DurationOptions) int {
	longest := options.Max
	for _, value := range options.Values {
		longest = max(longest, value)
	}
	return longest
}

// dramaWritingSkill 读取作品为某个文字步骤选定的写作技能；技能不可读时报错，不悄悄退回内置写法。label 用于错误提示。
func (s *Service) dramaWritingSkill(userID string, ref *DramaSkillRef, label string) (*production.WritingSkill, error) {
	if ref == nil {
		return nil, nil
	}
	file, err := s.SkillPackageFile(userID, ref.SkillID, ref.Path)
	if err != nil {
		return nil, BadAuthRequest(label + "技能「" + firstNonEmpty(ref.SkillName, ref.SkillID) + "」不可用：" + err.Error())
	}
	if file.Binary || strings.TrimSpace(file.Content) == "" {
		return nil, BadAuthRequest(label + "技能文件 " + ref.Path + " 不是文本或为空")
	}
	return &production.WritingSkill{Name: firstNonEmpty(ref.SkillName, ref.SkillID), Path: ref.Path, Content: file.Content}, nil
}
