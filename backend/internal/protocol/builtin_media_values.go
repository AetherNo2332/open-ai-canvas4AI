package protocol

import (
	"strings"
)

func mediaValues(values []MediaReference) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if item := mediaValue(value); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func mediaFromArray(value any, capability Capability) []MediaReference {
	values, _ := value.([]any)
	result := make([]MediaReference, 0, len(values))
	kind := string(capability)
	for _, item := range values {
		if value, ok := item.(string); ok && strings.TrimSpace(value) != "" {
			result = append(result, MediaReference{URL: strings.TrimSpace(value), Kind: kind})
			continue
		}
		if m, ok := item.(map[string]any); ok {
			if capability == CapabilityImage {
				if value := firstString(m, "b64_json", "data"); value != "" {
					if !strings.HasPrefix(value, "data:") {
						value = "data:image/png;base64," + value
					}
					result = append(result, MediaReference{DataURL: value, Kind: kind})
					continue
				}
			}
			if value := firstString(m, "url", "image_url", "video_url", "audio_url"); value != "" {
				result = append(result, MediaReference{URL: value, Kind: kind})
			}
		}
	}
	return result
}

func dataMIME(value string) string {
	if index := strings.Index(value, ";"); strings.HasPrefix(value, "data:") && index > 5 {
		return strings.TrimPrefix(value[5:index], "")
	}
	return "application/octet-stream"
}

func dataPayload(value string) string {
	if index := strings.Index(value, ","); strings.HasPrefix(value, "data:") && index >= 0 {
		return value[index+1:]
	}
	return value
}
