package protocol

import (
	"encoding/json"
	"net/url"
	"strings"
)

func firstPathJSONValue(payload map[string]any, paths ...string) string {
	for _, path := range paths {
		value := pathValue(payload, path)
		switch typed := value.(type) {
		case string:
			if strings.TrimSpace(typed) != "" {
				return strings.TrimSpace(typed)
			}
		case nil:
			continue
		default:
			encoded, err := json.Marshal(typed)
			if err == nil && len(encoded) > 0 {
				return string(encoded)
			}
		}
	}
	return ""
}

func setMapPath(target map[string]any, path string, value any) {
	parts := strings.Split(strings.Trim(path, "."), ".")
	current := target
	for _, part := range parts[:len(parts)-1] {
		next, ok := current[part].(map[string]any)
		if !ok {
			next = map[string]any{}
			current[part] = next
		}
		current = next
	}
	if len(parts) > 0 {
		current[parts[len(parts)-1]] = value
	}
}

func isRelativePath(path string) bool {
	parsed, err := url.Parse(strings.TrimSpace(path))
	return err == nil && parsed.Path != "" && strings.HasPrefix(parsed.Path, "/") && !strings.HasPrefix(parsed.Path, "//") && parsed.Host == "" && parsed.User == nil && parsed.Scheme == ""
}

func validManifestIdentifier(value string) bool {
	if len(value) == 0 || len(value) > 96 {
		return false
	}
	for index, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' || char == '_' || char == '.' {
			if index == 0 && (char == '-' || char == '_' || char == '.') {
				return false
			}
			continue
		}
		return false
	}
	return true
}
