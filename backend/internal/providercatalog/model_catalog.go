package providercatalog

import "strings"

// ModelOption is one selectable value advertised by an upstream model catalog.
type ModelOption struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
}

// NormalizeModelType keeps only the model capabilities supported by the public
// channel catalog and canonicalizes their casing.
func NormalizeModelType(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "text", "image", "video", "audio":
		return normalized
	default:
		return ""
	}
}

// NormalizeModelOptions trims option values and labels, drops empty values, and
// removes repeated values while preserving first-seen order.
func NormalizeModelOptions(options []ModelOption) []ModelOption {
	seen := make(map[string]bool, len(options))
	normalized := make([]ModelOption, 0, len(options))
	for _, option := range options {
		value := strings.TrimSpace(option.Value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		normalized = append(normalized, ModelOption{Value: value, Label: strings.TrimSpace(option.Label)})
	}
	return normalized
}

// NormalizeEndpointTypes trims endpoint names, drops empty values, and removes
// repeats while preserving first-seen order.
func NormalizeEndpointTypes(values []string) []string {
	seen := make(map[string]bool, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		item := strings.TrimSpace(value)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		normalized = append(normalized, item)
	}
	return normalized
}

// NormalizeModelID trims an upstream model identifier and removes the single
// Gemini catalog prefix. Inner identifiers and casing remain unchanged.
func NormalizeModelID(value string) string {
	return strings.TrimPrefix(strings.TrimSpace(value), "models/")
}
