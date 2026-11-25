package app

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"

	"yingce/backend/internal/production"
	"yingce/backend/internal/shortplay"
)

// 短剧设置 = 制作意图（production.Settings 里短剧真正会用的那几项）+ 分步骤模型 + 参考图方案。
//
// 为什么不复用 drama 的 DramaSettings：短剧只做到「剧本分集 → 集画布 → 资产提取与出图」，
// drama 的段落衔接、审阅方式、视频分辨率、并发预算在这里全都没有对应物，共用类型会把它们
// 一并带进 settingsJson，读的人会以为短剧也按这些参数生产。
//
// 但 JSON 键沿用 drama 的同义名（models.text / models.characterImage / assets.mode /
// extractSkill …）：前端因此可以直接复用 drama 的模型下拉、参考图方案面板与技能选择器，
// 不必为同一件事写第二份交互。

// shortplayModelChoice 是一个模型选择，形状与 DramaModelChoice 一致但不复用其类型：
// 短剧设置是独立契约，drama 增删字段不该波及这里（读目录与换算规格时再转成 drama 形状）。
type shortplayModelChoice struct {
	LogicalModelID string `json:"logicalModelId,omitempty"`
	ChannelID      string `json:"channelId,omitempty"`
	ModelKey       string `json:"modelKey,omitempty"`
	Label          string `json:"label,omitempty"`
}

// empty 判「没选」。平台目录模型只有 logicalModelId；渠道模型要 channelId + modelKey 成对。
func (c shortplayModelChoice) empty() bool {
	return strings.TrimSpace(c.LogicalModelID) == "" &&
		(strings.TrimSpace(c.ChannelID) == "" || strings.TrimSpace(c.ModelKey) == "")
}

// normalized 去空白；不完整的选择退化成「没选」而不是留半截 —— 半截会让执行期
// 拿 channelId 去解模型却缺 modelKey。Label 只是显示缓存，长度也要收。
func (c shortplayModelChoice) normalized() shortplayModelChoice {
	if c.empty() {
		return shortplayModelChoice{}
	}
	return shortplayModelChoice{
		LogicalModelID: truncateRunes(strings.TrimSpace(c.LogicalModelID), 80),
		ChannelID:      truncateRunes(strings.TrimSpace(c.ChannelID), 80),
		ModelKey:       truncateRunes(strings.TrimSpace(c.ModelKey), 160),
		Label:          truncateRunes(strings.TrimSpace(c.Label), 120),
	}
}

// shortplayModels 是各步骤 / 各类参考图的模型选择。空 = 跟随平台目录默认，
// 不是「不许用模型」—— 兜底在 shortplayTextModel / shortplayImageModel 里。
type shortplayModels struct {
	// Text 是默认文本模型，「制作模型」里选的那个。
	Text shortplayModelChoice `json:"text"`
	// BibleText 是资产提取单独指定的文本模型；为空跟随 Text。
	BibleText shortplayModelChoice `json:"bibleText"`
	// Image 是三类参考图共用的图片模型；按类别覆盖见下面三个。
	Image          shortplayModelChoice `json:"image"`
	CharacterImage shortplayModelChoice `json:"characterImage"`
	PropImage      shortplayModelChoice `json:"propImage"`
	SceneImage     shortplayModelChoice `json:"sceneImage"`
	// Audio 是音色样本使用的音频模型；为空跟随平台目录第一个可用项。
	Audio shortplayModelChoice `json:"audio"`
}

// imageFor 返回某类参考图实际使用的图片模型：按类别覆盖优先，否则跟随默认图片模型。
func (m shortplayModels) imageFor(kind string) shortplayModelChoice {
	switch kind {
	case shortplay.AssetCharacter:
		if !m.CharacterImage.empty() {
			return m.CharacterImage
		}
	case shortplay.AssetProp:
		if !m.PropImage.empty() {
			return m.PropImage
		}
	case shortplay.AssetScene:
		if !m.SceneImage.empty() {
			return m.SceneImage
		}
	}
	return m.Image
}

func (m shortplayModels) normalized() shortplayModels {
	return shortplayModels{
		Text:           m.Text.normalized(),
		BibleText:      m.BibleText.normalized(),
		Image:          m.Image.normalized(),
		CharacterImage: m.CharacterImage.normalized(),
		PropImage:      m.PropImage.normalized(),
		SceneImage:     m.SceneImage.normalized(),
		Audio:          m.Audio.normalized(),
	}
}

// shortplaySkillRef 指向用户技能库里的一个技能文件，作为资产提取的写作方法。
// 与 DramaSkillRef 同形（字段名也一致），便于复用 drama 的技能选择器与读取逻辑。
type shortplaySkillRef struct {
	SkillID   string `json:"skillId"`
	SkillName string `json:"skillName"`
	Path      string `json:"path"`
}

// normalizeShortplaySkillRef 截断字段长度；没有技能 ID 时视为没选（不明知不完整还保留）。
func normalizeShortplaySkillRef(value *shortplaySkillRef) *shortplaySkillRef {
	if value == nil {
		return nil
	}
	ref := shortplaySkillRef{
		SkillID:   truncateRunes(strings.TrimSpace(value.SkillID), 64),
		SkillName: truncateRunes(strings.TrimSpace(value.SkillName), 120),
		Path:      truncateRunes(strings.TrimSpace(value.Path), 300),
	}
	if ref.SkillID == "" {
		return nil
	}
	return &ref
}

// shortplaySettings 是 shortplays.settingsJson 的解析形状。
//
// 嵌入 production.Settings 是为了兼容读取既有行（建档时前端可直接提交同一形状），
// 但写入只用 encode：它只写短剧会读的那几项，分镜规则与单集时长不落库。
type shortplaySettings struct {
	production.Settings
	Models shortplayModels `json:"models"`
	// ExtractSkill 是资产提取的写作技能；为空按内置规则，输出结构始终由系统固定。
	ExtractSkill *shortplaySkillRef `json:"extractSkill,omitempty"`
}

func parseShortplaySettings(raw string) shortplaySettings {
	var settings shortplaySettings
	_ = json.Unmarshal([]byte(raw), &settings)
	return settings
}

// Normalized 补默认值并清掉不完整的选择。执行期与保存期读同一份结果：
// 保存时归一过的值就是执行时用的值，不存在「保存能过、跑起来是另一套」。
func (s shortplaySettings) Normalized() shortplaySettings {
	s.Settings = s.Settings.Normalized()
	s.Models = s.Models.normalized()
	s.ExtractSkill = normalizeShortplaySkillRef(s.ExtractSkill)
	return s
}

// encode 把设置写成 settingsJson。只写短剧真正会读的字段：分镜规则、单集时长属于 drama 的
// 生产形态，写进来会让下一个读这份 JSON 的人以为短剧也按这些参数出片。
func (s shortplaySettings) encode() (string, error) {
	payload := struct {
		AspectRatio  string                   `json:"aspectRatio"`
		StyleName    string                   `json:"styleName"`
		StylePrompt  string                   `json:"stylePrompt"`
		Language     string                   `json:"language"`
		Assets       production.AssetSettings `json:"assets"`
		Models       shortplayModels          `json:"models"`
		ExtractSkill *shortplaySkillRef       `json:"extractSkill,omitempty"`
	}{s.AspectRatio, s.StyleName, s.StylePrompt, s.Language, s.Assets, s.Models, s.ExtractSkill}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// shortplayAssetKinds 是参考图的三类，顺序与页面、出图模板一致。
var shortplayAssetKinds = []string{shortplay.AssetCharacter, shortplay.AssetProp, shortplay.AssetScene}

var shortplayAssetKindLabels = map[string]string{
	shortplay.AssetCharacter: "角色",
	shortplay.AssetProp:      "道具",
	shortplay.AssetScene:     "场景",
}

// shortplayAssetImageRatio 是某类参考图的画幅：角色按方案，道具与场景固定 16:9。
func shortplayAssetImageRatio(assets production.AssetSettings, kind string) string {
	if kind == shortplay.AssetCharacter {
		return firstNonEmpty(strings.TrimSpace(assets.CharacterRatio), production.AssetImageRatio)
	}
	return production.AssetImageRatio
}

// shortplayDramaChoice 把短剧的模型选择转成 drama 的目录校验形状。
func shortplayDramaChoice(choice shortplayModelChoice) DramaModelChoice {
	return DramaModelChoice{
		LogicalModelID: choice.LogicalModelID,
		ChannelID:      choice.ChannelID,
		ModelKey:       choice.ModelKey,
		Label:          choice.Label,
	}
}

// UpdateShortplaySettings 保存「设置」里的改动：分步骤模型、参考图方案、风格与语言。
//
// 画幅不在可改范围内：权威在 projects 容器行，建档后改画幅会让已出的参考图与构图全部错配。
// 请求里带的 aspectRatio 被覆盖成容器行的值，而不是报错 —— 前端只是把整份设置回传，
// 为这个字段单独拦一次会让「改了个模型却保存失败」难以理解。
func (s *Service) UpdateShortplaySettings(userID, projectID string, raw json.RawMessage) (shortplay.SettingsUpdateResult, error) {
	owner, err := s.shortplayRowForUser(userID, projectID)
	if err != nil {
		return shortplay.SettingsUpdateResult{}, err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return shortplay.SettingsUpdateResult{}, BadAuthRequest("设置不能为空")
	}
	var incoming shortplaySettings
	if err := json.Unmarshal(raw, &incoming); err != nil {
		return shortplay.SettingsUpdateResult{}, BadAuthRequest("设置格式无效：" + err.Error())
	}
	next := incoming.Normalized()
	project, err := s.repo.ProjectForUser(owner.UserID, owner.ProjectID)
	if err != nil {
		return shortplay.SettingsUpdateResult{}, err
	}
	next.AspectRatio = firstNonEmpty(strings.TrimSpace(project.AspectRatio), production.AspectRatios[0])
	if !production.ValidAspectRatio(next.AspectRatio) {
		next.AspectRatio = production.AspectRatios[0]
	}
	// 与 drama 保存设置同一道校验：提示词变量、模型可用性、参考图画幅与档位、技能可读。
	// 都在保存时判，避免开工后每张参考图各失败一次。
	if problem := production.ValidateAssetPrompts(next.Assets); problem != "" {
		return shortplay.SettingsUpdateResult{}, BadAuthRequest(problem)
	}
	if err := s.validateShortplayModels(userID, next.Models); err != nil {
		return shortplay.SettingsUpdateResult{}, err
	}
	if err := s.validateShortplayImageSettings(next.Models, next.Assets); err != nil {
		return shortplay.SettingsUpdateResult{}, err
	}
	if _, err := s.shortplayWritingSkill(userID, next.ExtractSkill); err != nil {
		return shortplay.SettingsUpdateResult{}, err
	}
	encoded, err := next.encode()
	if err != nil {
		return shortplay.SettingsUpdateResult{}, err
	}
	if err := s.repo.UpdateShortplaySettings(owner.ID, encoded); err != nil {
		return shortplay.SettingsUpdateResult{}, err
	}
	// 投影「默认图片模型」到容器行：设置权威在 settingsJson，但 agent 的 project_meta
	// 每轮只按 projects.default_image_model 反查。投影失败只记日志：设置已落库，
	// 这一行读数滞后会在下次保存时自动修正。
	if err := s.repo.UpdateProjectDefaultImageModel(owner.UserID, owner.ProjectID, shortplayDefaultImageModelText(next.Models)); err != nil {
		slog.Warn("短剧默认图片模型投影失败，设置本身已保存",
			"projectId", owner.ProjectID, "shortplayId", owner.ID, "error", err)
	}
	return shortplay.SettingsUpdateResult{SettingsJSON: encoded}, nil
}

// validateShortplayModels 确认每个选了模型的步骤都在当前用户可用的短剧模型目录里，
// 挡住提交任意渠道标识（与 drama 的 validateDramaModelOverrides 同一道门）。
func (s *Service) validateShortplayModels(userID string, models shortplayModels) error {
	for _, item := range []struct {
		capability string
		choice     shortplayModelChoice
	}{
		{"text", models.Text},
		{"text", models.BibleText},
		{"image", models.Image},
		{"image", models.CharacterImage},
		{"image", models.PropImage},
		{"image", models.SceneImage},
	} {
		if item.choice.empty() {
			continue
		}
		if err := s.requireDramaModelOption(userID, item.capability, shortplayDramaChoice(item.choice)); err != nil {
			return err
		}
	}
	return nil
}

// validateShortplayImageSettings 确认角色、道具、场景各自的图片模型能按该类参考图的画幅与
// 分辨率出图。画幅与档位用与派发时同一个函数算，避免「保存时能过、出图时失败」。
func (s *Service) validateShortplayImageSettings(models shortplayModels, assets production.AssetSettings) error {
	for _, kind := range shortplayAssetKinds {
		choice := models.imageFor(kind)
		if choice.empty() {
			continue
		}
		request := s.dramaImageRequestFor(shortplayDramaChoice(choice), shortplayAssetImageRatio(assets, kind), assets.ResolutionFor(kind))
		if request.Issue != "" {
			return BadAuthRequest(shortplayAssetKindLabels[kind] + "参考图设置与图片模型不匹配：" + request.Issue)
		}
	}
	return nil
}

// shortplayWritingSkill 读取资产提取选定的写作技能；技能不可读时报错，不悄悄退回内置写法。
func (s *Service) shortplayWritingSkill(userID string, ref *shortplaySkillRef) (*production.WritingSkill, error) {
	if ref == nil {
		return nil, nil
	}
	return s.dramaWritingSkill(userID, &DramaSkillRef{SkillID: ref.SkillID, SkillName: ref.SkillName, Path: ref.Path}, "资产提取")
}

// shortplayDefaultImageModelText 把「默认图片模型」压成一句可读标识，供容器行投影。
// 只投 models.image 这一层，不投三类的单独覆盖：容器行只有一个格子。
func shortplayDefaultImageModelText(models shortplayModels) string {
	if models.Image.empty() {
		return ""
	}
	return firstNonEmpty(
		strings.TrimSpace(models.Image.Label),
		strings.TrimSpace(models.Image.ModelKey),
		strings.TrimSpace(models.Image.LogicalModelID),
	)
}
