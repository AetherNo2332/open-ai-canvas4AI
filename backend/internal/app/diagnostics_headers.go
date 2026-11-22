package app

import (
	"encoding/json"
	"regexp"
	"strings"
)

var diagnosticHeaderField = regexp.MustCompile(`(?i)(?:"(?:proxy-authorization|authorization|set-cookie|cookie)"|\b(?:proxy-authorization|authorization|set-cookie|cookie))(?:[ \t]*[:=][ \t]*|[ \t]+)`)

func redactDiagnosticHeaders(value string) string {
	var result strings.Builder
	for {
		match := diagnosticHeaderField.FindStringIndex(value)
		if match == nil {
			return result.String() + value
		}
		result.WriteString(value[:match[1]])
		quotedKey := value[match[0]] == '"'
		value = value[match[1]:]
		if quotedKey {
			decoder := json.NewDecoder(strings.NewReader(value))
			var field json.RawMessage
			if decoder.Decode(&field) == nil {
				result.WriteString(`"[REDACTED]"`)
				value = value[decoder.InputOffset():]
				continue
			}
		}
		result.WriteString("[REDACTED]")
		newline := strings.IndexAny(value, "\r\n")
		if newline < 0 {
			return result.String()
		}
		value = value[newline:]
	}
}
