package capability

import "sort"

func boundedNumber(min, max float64) PatchField {
	return PatchField{Kind: patchKindNumber, Min: &min, Max: &max}
}
func enumString(values ...string) PatchField {
	return PatchField{Kind: patchKindString, Enum: values, MaxRunes: 240}
}
func stringField(maxRunes int) PatchField {
	return PatchField{Kind: patchKindString, MaxRunes: maxRunes}
}
func objectField(properties map[string]PatchField, required ...string) PatchField {
	return PatchField{Kind: patchKindObject, Properties: properties, Required: required}
}
func arrayField(items PatchField, maxItems int) PatchField {
	return PatchField{Kind: patchKindArray, Items: &items, MaxItems: maxItems}
}

func remainingBuiltinDescriptors() []Descriptor {
	return []Descriptor{
		{Type: "drawing", Version: "1", Label: "绘图", DefaultWidth: 440, DefaultHeight: 300, Purpose: "编辑同步后的原生绘图记录；未同步的本地文档需先由编辑器打开并保存，图片资源由平台托管。", CreateMetadata: func(string) map[string]any { return map[string]any{"status": "success"} }},
		{Type: "skill", Version: "1", Label: "技能", DefaultWidth: 360, DefaultHeight: 220, Purpose: "承载技能说明正文；技能库身份、版本和快照由技能服务绑定。", InputKind: "text", Connection: ConnectionPolicy{CanSource: true}, PatchFields: editableNodeFields("metadata.content", "技能正文", "技能正文草稿，不覆盖已绑定技能的身份或快照"), SummaryFields: []string{"content", "skillId", "skillSnapshot"}, DetailFields: []string{"content", "skillId", "skillSnapshot"}, CreateMetadata: func(content string) map[string]any { return map[string]any{"status": "success", "content": content} }},
		{Type: "config", Version: "1", Label: "生成配置", DefaultWidth: 480, DefaultHeight: 390, Purpose: "编辑下一次生成的模式、模型与参数；提交仍须经过模型目录和任务准入。", Connection: ConnectionPolicy{CanTarget: true, AcceptedInputKinds: []string{"text", "image", "video", "audio", "character"}}, PatchFields: editableNodeFields("metadata.composerContent", "生成提示词", "下一次生成使用的提示词草稿"), CreateMetadata: func(content string) map[string]any {
			meta := generatedMetadata(content)
			meta["generationMode"] = "image"
			return meta
		}},
		{Type: "svg", Version: "1", Label: "SVG", DefaultWidth: 420, DefaultHeight: 320, Purpose: "编辑 SVG 源码并显示矢量图。", InputKind: "text", Connection: ConnectionPolicy{CanSource: true, CanTarget: true, AcceptedInputKinds: []string{"text"}}, PatchFields: editableNodeFields("metadata.content", "SVG 源码", "SVG 源码"), SummaryFields: []string{"content"}, DetailFields: []string{"content"}},
		{Type: "html", Version: "1", Label: "HTML", DefaultWidth: 520, DefaultHeight: 380, Purpose: "编辑 HTML 源码并在受隔离的预览中显示。", InputKind: "text", Connection: ConnectionPolicy{CanSource: true, CanTarget: true, AcceptedInputKinds: []string{"text"}}, PatchFields: editableNodeFields("metadata.content", "HTML 源码", "HTML 源码"), SummaryFields: []string{"content"}, DetailFields: []string{"content"}},
		{Type: "panorama", Version: "1", Label: "全景", DefaultWidth: 520, DefaultHeight: 300, Purpose: "查看上游全景图片并配置投影方式；图片绑定通过资源服务。", Connection: ConnectionPolicy{CanTarget: true, MaxInputCount: 1, AcceptedInputKinds: []string{"image"}}, SummaryFields: []string{"panoramaConfig"}, DetailFields: []string{"panoramaConfig"}},
		{Type: "compare", Version: "1", Label: "对比", DefaultWidth: 520, DefaultHeight: 320, Purpose: "比较两个上游图片；连接决定比较素材。", Connection: ConnectionPolicy{CanTarget: true, MaxInputCount: 2, AcceptedInputKinds: []string{"image"}}, CreateMetadata: func(string) map[string]any { return map[string]any{"status": "idle"} }},
		{Type: "chart", Version: "1", Label: "图表", DefaultWidth: 480, DefaultHeight: 320, Purpose: "编辑 JSON 或 CSV 数据并显示柱状图或折线图。", InputKind: "text", Connection: ConnectionPolicy{CanTarget: true, AcceptedInputKinds: []string{"text"}}, PatchFields: editableNodeFields("metadata.content", "图表数据", "JSON 数组或 CSV 图表数据"), SummaryFields: []string{"content", "chartKind"}, DetailFields: []string{"content", "chartKind"}},
		{Type: "colorgrade", Version: "1", Label: "调色", DefaultWidth: 420, DefaultHeight: 360, Purpose: "配置上游图片的亮度、对比度、饱和度与色相；导出和资源发布需要浏览器渲染。", Connection: ConnectionPolicy{CanTarget: true, MaxInputCount: 1, AcceptedInputKinds: []string{"image"}}, SummaryFields: []string{"colorGrade"}, DetailFields: []string{"colorGrade"}, CreateMetadata: func(string) map[string]any { return map[string]any{"status": "idle"} }},
		{Type: "media-conversion", Version: "1", Label: "转换", DefaultWidth: 480, DefaultHeight: 460, Purpose: "选择媒体转换操作；转换执行与结果由转换服务管理。", Connection: ConnectionPolicy{CanTarget: true, MaxInputCount: 1, AcceptedInputKinds: []string{"image", "video"}}, SummaryFields: []string{"mediaConversion"}, DetailFields: []string{"mediaConversion"}, CreateMetadata: func(string) map[string]any { return map[string]any{"status": "idle"} }},
	}
}

func completeBuiltinEditableFields(d *Descriptor) {
	// Graph associations follow the visible editor ports. Generation reference
	// eligibility remains independent and still requires materialized media.
	d.Connection.CanGraphSource = d.Type != "frame"
	d.Connection.CanGraphTarget = d.Type != "frame"
	if d.InputKind == "" {
		switch d.Type {
		case "drawing", "panorama", "compare", "colorgrade", "media-conversion":
			d.InputKind = "image"
		case "script":
			d.InputKind = "text"
		}
	}
	if d.PatchFields == nil {
		d.PatchFields = map[string]PatchField{}
	}
	d.CanUpdate = true
	add := func(key, path, label string, field PatchField) {
		field.Path = path
		field.Label = label
		field.Order = 40 + len(d.PatchFields)
		d.PatchFields[key] = field
	}
	for key, field := range positionPatchFields() {
		d.PatchFields[key] = field
	}
	if d.Variant == nil {
		if _, exists := d.PatchFields["title"]; !exists {
			field := stringField(maxAgentNodeTitleRunes)
			field.Order = 10
			field.Path = "title"
			field.Label = "节点名称"
			d.PatchFields["title"] = field
		}
	}
	add("width", "width", "节点宽度", boundedNumber(1, 100000))
	add("height", "height", "节点高度", boundedNumber(1, 100000))
	add("locked", "metadata.locked", "锁定节点", PatchField{Kind: patchKindBoolean})
	add("fontSize", "metadata.fontSize", "字号", boundedNumber(8, 120))
	d.Actions = append(d.Actions, "update_node", "delete_node", "duplicate_node", "set_parent", "reorder_nodes")
	if d.Variant != nil {
		return
	}
	switch d.Type {
	case "text":
		add("listMode", "metadata.listMode", "列表模式", PatchField{Kind: patchKindBoolean})
		add("richText", "metadata.richText", "富文本正文", richTextField())
	case "frame":
		add("frame", "metadata.frame", "背板折叠与展开尺寸", objectField(map[string]PatchField{"collapsed": {Kind: patchKindBoolean}, "expandedWidth": boundedNumber(1, 100000), "expandedHeight": boundedNumber(1, 100000)}, "collapsed", "expandedWidth", "expandedHeight"))
		add("folderStyle", "metadata.folder.style", "背板样式", enumString("glass", "stacked", "midnight", "paper", "cinema", "compact"))
		add("folderTheme", "metadata.folder.theme", "背板主题", enumString("aurora", "obsidian", "ember", "pearl"))
		d.DetailFields = append(d.DetailFields, "frame", "folder")
	case "script":
		add("visibleColumns", "metadata.storyboard.visibleColumns", "可见分镜列", arrayField(enumString("shotNumber", "durationSeconds", "plotDescription", "dialogue", "narrativeIntent", "viewerPOV", "performanceBlocking", "shotSize", "emotion", "lightingAndAtmosphere", "audioEffects", "camera", "motion", "timeBeats", "imageGenerationPrompt", "videoMotionPrompt", "assets", "continuityOut", "negativePrompt"), 20))
		add("storyboardShotDuration", "metadata.storyboardShotDuration", "分镜时长", enumString("auto", "5", "10", "15", "30"))
		add("storyboardShotCount", "metadata.storyboardShotCount", "分镜数量", enumString("auto", "1", "2", "3", "4", "5", "6", "7", "8", "9", "10"))
		add("storyboardVideoInputMode", "metadata.storyboardVideoInputMode", "视频分镜输入模式", enumString("direct", "keyframe"))
		d.Actions = append(d.Actions, "reorder_rows")
	case "chart":
		add("chartKind", "metadata.chartKind", "图表类型", enumString("bar", "line"))
	case "panorama":
		add("panoramaConfig", "metadata.panoramaConfig", "全景配置", objectField(map[string]PatchField{"projection": enumString("spherical", "cylindrical"), "sourceMode": enumString("ai", "image"), "smartBase": {Kind: patchKindBoolean}}, "projection", "sourceMode", "smartBase"))
	case "colorgrade":
		add("colorGrade", "metadata.colorGrade", "调色参数", objectField(map[string]PatchField{"brightness": boundedNumber(0, 200), "contrast": boundedNumber(0, 200), "saturate": boundedNumber(0, 200), "hueRotate": boundedNumber(-180, 180)}, "brightness", "contrast", "saturate", "hueRotate"))
	case "media-conversion":
		add("conversionOperation", "metadata.mediaConversion.operation", "转换操作", enumString("grayscale", "edge-canny", "lineart", "depth", "pose", "cutout"))
	case "drawing":
		add("drawingEngine", "metadata.drawingEngine", "绘图引擎", enumString("tldraw", "excalidraw"))
	case "batch-table":
		d.Actions = append(d.Actions, "reorder_rows")
	}
	if d.Type == "config" || d.Type == "image" || d.Type == "video" || d.Type == "audio" || d.Type == "text" {
		addGenerationEditableFields(d, add)
	}
	if d.Type == "image" {
		add("freeResize", "metadata.freeResize", "自由调整尺寸", PatchField{Kind: patchKindBoolean})
	}
	if d.Type == "video" {
		add("subtitleEntries", "metadata.subtitleEntries", "字幕内容", subtitleEntriesField())
		add("subtitleHighlights", "metadata.subtitleHighlights", "字幕重点", subtitleHighlightsField())
		add("subtitleStyle", "metadata.subtitleStyle", "字幕样式", subtitleStyleField())
	}
	// Detail projection is still an allow-list: only declared editable fields are exposed.
	keys := make([]string, 0, len(d.PatchFields))
	for key := range d.PatchFields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		field := d.PatchFields[key]
		if len(field.Path) > 9 && field.Path[:9] == "metadata." {
			d.DetailFields = append(d.DetailFields, field.Path[9:])
		}
	}
}

func addGenerationEditableFields(d *Descriptor, add func(string, string, string, PatchField)) {
	for _, key := range []string{"model", "size", "quality", "transparentBackground", "seconds", "vquality", "generateAudio", "watermark", "audioVoice", "audioFormat", "audioSpeed", "audioInstructions", "audioEmotionControlMethod", "audioEmotionRandom", "audioEmotionHappy", "audioEmotionAngry", "audioEmotionSad", "audioEmotionAfraid", "audioEmotionDisgusted", "audioEmotionMelancholic", "audioEmotionSurprised", "audioEmotionCalm", "videoCameraMoveId", "videoCameraMovePrompt", "promptTemplateOperation"} {
		add(key, "metadata."+key, key, stringField(maxAgentNodeContentRunes))
	}
	add("generationMode", "metadata.generationMode", "生成模式", enumString("text", "image", "video", "audio"))
	if d.GenerationMode != "" {
		add("generationMode", "metadata.generationMode", "生成模式", enumString(d.GenerationMode))
	}
	add("generationType", "metadata.generationType", "图片生成类型", enumString("generation", "edit"))
	add("count", "metadata.count", "生成数量", integerField(1, 100))
	add("textCount", "metadata.textCount", "文本数量", integerField(1, 100))
	add("generationResultPlacement", "metadata.generationResultPlacement", "生成结果位置", enumString("replace-node", "new-version"))
	add("workflowProvider", "metadata.workflowProvider", "工作流来源", enumString("model", "runninghub"))
	add("runningHubWorkflowId", "metadata.runningHubWorkflowId", "工作流编号", stringField(240))
	add("runningHubWorkflowKind", "metadata.runningHubWorkflowKind", "工作流类型", enumString("workflow", "app"))
	add("cameraControl", "metadata.cameraControl", "摄影机控制", cameraControlField())
	add("portraitTexture", "metadata.portraitTexture", "人像质感", portraitTextureField())
	if d.Type != "text" {
		add("generationSpec", "metadata.generationSpec", "生成合同", generationSpecField())
	}
}

func integerField(min, max float64) PatchField {
	field := boundedNumber(min, max)
	field.Integer = true
	return field
}

func cameraControlField() PatchField {
	return objectField(map[string]PatchField{"enabled": {Kind: patchKindBoolean}, "camera": stringField(240), "lens": stringField(240), "focalLength": boundedNumber(1, 1200), "aperture": boundedNumber(0.1, 64)}, "enabled", "camera", "lens", "focalLength", "aperture")
}

func portraitTextureField() PatchField {
	return objectField(map[string]PatchField{"personSceneFusion": enumString("light", "natural", "deep"), "lightingFusion": enumString("soft", "natural", "atmosphere"), "skin": enumString("clear", "natural", "real"), "texture": enumString("soft", "natural", "grain"), "sharpness": enumString("soft", "standard", "high")}, "personSceneFusion", "lightingFusion", "skin", "texture", "sharpness")
}
