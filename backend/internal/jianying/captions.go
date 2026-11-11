package jianying

import "strings"

func buildCaptionMaterials(captions []Caption) ([]any, []any, []any, error) {
	speeds := []any{}
	texts := []any{}
	if len(captions) > 0 {
		textSegments := []any{}
		for _, caption := range captions {
			text := strings.TrimSpace(caption.Text)
			if text == "" || caption.DurationUs <= 0 || caption.StartUs < 0 {
				continue
			}
			materialID := newUUID()
			material, err := subtitleMaterial(materialID, text)
			if err != nil {
				return nil, nil, nil, err
			}
			texts = append(texts, material)
			speed := speedMaterial()
			speeds = append(speeds, speed)
			segment := baseSegment(materialID, caption.StartUs, caption.DurationUs, 1, []string{speed["id"].(string)}, clipSettings(-0.8))
			segment["source_timerange"] = nil
			textSegments = append(textSegments, segment)
		}
		return texts, speeds, textSegments, nil
	}
	return texts, speeds, nil, nil
}
