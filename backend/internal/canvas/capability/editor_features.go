package capability

func addEditorFeatureFields(d *Descriptor, add func(string, string, string, PatchField)) {
	optional := func(key string, field PatchField) {
		field.Resettable = true
		add(key, "metadata."+key, key, field)
	}
	switch d.Type {
	case "text", "image", "video", "audio", "frame":
		optional("workflowKind", enumString("free", "script", "story_input", "scene", "storyboard", "shot", "final", "styleboard", "reference_set", "reference_video", "action_board"))
		optional("workflowTitle", stringField(240))
		optional("workflowDescription", stringField(2000))
		optional("shotIndex", integerField(1, 100000))
		optional("referenceSetId", stringField(80))
	}
	if d.Type == "frame" {
		optional("referenceAssetNodeIds", arrayField(stringField(80), 100))
	}
	if d.Type == "text" {
		optional("stylePresetId", stringField(128))
		optional("styleProfileJson", stringField(maxAgentDocumentRunes))
	}
	if d.Type == "image" {
		optional("actionBoardRows", integerField(1, 12))
		optional("actionBoardColumns", integerField(1, 12))
		optional("batchSourceNodeId", stringField(80))
		optional("batchRowId", stringField(120))
		optional("batchOperation", enumString("try_on", "creative"))
		optional("batchInputNodeIds", arrayField(stringField(80), 10))
	}
	if d.Type == "video" {
		optional("videoEditOperation", enumString("text_to_video", "image_to_video", "reference_to_video", "extend", "inpaint", "replace_element", "camera_motion", "style_transfer", "audio_to_video", "compare_versions", "concat"))
		for _, key := range []string{"videoStartFrameNodeId", "videoEndFrameNodeId", "videoFrameSourceNodeId", "previsSceneId", "previsShotId", "previsPreviewNodeId", "previsDepthNodeId", "previsNormalNodeId", "previsClayVideoNodeId"} {
			optional(key, stringField(80))
		}
	}
	if d.Type == "media-conversion" {
		add("conversionState", "metadata.mediaConversion", "转换状态", objectField(map[string]PatchField{
			"schemaVersion": integerField(1, 1), "operation": enumString("grayscale", "edge-canny", "lineart", "depth", "pose", "cutout"),
			"status":       enumString("idle", "processing", "completed", "stale", "skipped", "unavailable", "error"),
			"sourceNodeId": stringField(80), "sourceFingerprint": stringField(16384), "outputKind": enumString("image", "video"),
			"resultStorageKey": stringField(100), "resultWidth": integerField(1, 100000), "resultHeight": integerField(1, 100000),
			"detectedPeople": integerField(0, 10000), "errorCode": stringField(240), "errorMessage": stringField(4000),
			"startedAt": stringField(80), "updatedAt": stringField(80),
		}, "schemaVersion", "operation", "status"))
	}
}
