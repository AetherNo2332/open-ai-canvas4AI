package capability

import (
	"fmt"
	"math"
)

// Row field contracts deliberately exclude task status, generated output IDs
// and asset ownership IDs. Callers must separately validate referenced nodes.
func StoryboardRowFields() map[string]PatchField {
	fields := map[string]PatchField{"durationSeconds": boundedNumber(math.SmallestNonzeroFloat64, 86400)}
	for _, key := range []string{"plotDescription", "dialogue", "videoMotionPrompt", "imageGenerationPrompt", "camera", "motion", "shotSize", "emotion", "lightingAndAtmosphere", "audioEffects", "narrativeIntent", "viewerPOV", "performanceBlocking", "timeBeats", "continuityOut", "negativePrompt"} {
		fields[key] = stringField(20000)
	}
	for _, key := range []string{"mustHave", "optionalDetails"} {
		fields[key] = arrayField(stringField(20000), 100)
	}
	fields["characters"] = arrayField(objectField(map[string]PatchField{"characterName": stringField(240), "characterDescription": stringField(20000), "characterImageNodeId": stringField(120)}, "characterName"), 100)
	fields["assetBindings"] = arrayField(objectField(map[string]PatchField{"nodeId": stringField(120), "role": enumString("character", "environment", "wardrobe", "prop", "weapon", "style", "motion", "audio"), "priority": integerField(0, 100)}, "nodeId", "role", "priority"), 100)
	for _, key := range []string{"imagePromptTemplateVariables", "videoPromptTemplateVariables"} {
		fields[key] = stringDictionaryField(20000, 100)
	}
	for _, key := range []string{"sourceStartMs", "sourceEndMs", "keyframeTimeMs"} {
		fields[key] = boundedNumber(0, 1e12)
	}
	// locked：用户锁定的镜头不参与拆镜或局部重拆。mediaReview=pending：镜头文字已改，已生成的媒体待复核。
	fields["locked"] = PatchField{Kind: patchKindBoolean}
	fields["mediaReview"] = enumString("pending")
	return fields
}

func BatchRowFields() map[string]PatchField {
	return map[string]PatchField{"enabled": {Kind: patchKindBoolean}, "inputNodeIds": arrayField(stringField(120), 10), "referenceOverrides": arrayField(stringField(120), 10), "textNodeIds": arrayField(stringField(120), 100), "prompt": stringField(20000), "cells": stringDictionaryField(20000, 100)}
}

// Table configuration is separate from rows and generated output/task state.
func BatchTableFields() map[string]PatchField {
	column := objectField(map[string]PatchField{"id": stringField(120), "label": stringField(120), "type": enumString("image", "text")}, "id", "label")
	inputs := arrayField(stringField(120), 500)
	optional := func(field PatchField) PatchField { field.Nullable = true; return field }
	return map[string]PatchField{
		"operation": enumString("try_on", "creative"), "concurrency": integerField(1, 10),
		"referenceColumns": arrayField(column, 10), "textColumns": optional(arrayField(column, 100)),
		"globalPrompt": optional(stringField(20000)), "referenceMode": optional(enumString("cells")),
		"referenceColumnInputs": optional(PatchField{Kind: patchKindObject, MapValues: &inputs, MaxProperties: 10}),
	}
}

func stringDictionaryField(maxRunes, maxProperties int) PatchField {
	item := stringField(maxRunes)
	return PatchField{Kind: patchKindObject, MapValues: &item, MaxProperties: maxProperties}
}

func ValidateEditableFields(fields map[string]PatchField, patch map[string]any) error {
	if len(patch) == 0 {
		return fmt.Errorf("更新字段不能为空")
	}
	for key, value := range patch {
		field, ok := fields[key]
		if !ok {
			return fmt.Errorf("不支持更新字段 %s", key)
		}
		if err := field.validateValue(value, key, 0); err != nil {
			return err
		}
	}
	return nil
}
