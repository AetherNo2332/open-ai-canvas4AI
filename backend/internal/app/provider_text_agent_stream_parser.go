package app

import (
	"encoding/json"
	"strings"

	"yingce/backend/internal/protocol"
)

// streamingAgentParser keeps the app-facing parser API while the SSE and
// provider event state machines live in internal/protocol.
type streamingAgentParser struct {
	*protocol.TextAgentStreamParser
	emitReasoning func(string)
}

func newStreamingAgentParser(wire string, emit func(string)) *streamingAgentParser {
	parser := &streamingAgentParser{}
	parser.TextAgentStreamParser = protocol.NewTextAgentStreamParser(protocol.TextAgentStreamOptions{
		Wire:     wire,
		EmitText: emit,
		EmitReasoning: func(delta string) {
			if parser.emitReasoning != nil {
				parser.emitReasoning(delta)
			}
		},
		ValidatePayload: func(raw json.RawMessage) error {
			payload, err := textAgentStreamPayload(raw)
			if err != nil {
				return err
			}
			return validateTextPayload(payload)
		},
		ParseCompleted: func(raw json.RawMessage) (json.RawMessage, error) {
			payload, err := textAgentStreamPayload(raw)
			if err != nil {
				return nil, err
			}
			result, err := parseAgentToolPayload(payload, wire)
			if err != nil {
				return nil, err
			}
			return json.Marshal(result)
		},
	})
	return parser
}

func textAgentStreamPayload(raw json.RawMessage) (map[string]interface{}, error) {
	var payload map[string]interface{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func (p *streamingAgentParser) consume(mimeType string, chunk []byte) {
	if p == nil || p.TextAgentStreamParser == nil {
		return
	}
	p.TextAgentStreamParser.Consume(mimeType, chunk)
}

func (p *streamingAgentParser) flush() {
	if p == nil || p.TextAgentStreamParser == nil {
		return
	}
	p.TextAgentStreamParser.Flush()
}

func (p *streamingAgentParser) result() (map[string]interface{}, error) {
	if p == nil || p.TextAgentStreamParser == nil {
		return nil, nil
	}
	raw, err := p.TextAgentStreamParser.Result()
	if err != nil || len(raw) == 0 {
		return nil, err
	}
	return textAgentStreamPayload(raw)
}

func resultString(value map[string]interface{}, key string) string {
	return stringField(value, key)
}

func extractResponseReasoning(payload map[string]interface{}) string {
	var chunks []string
	for _, value := range interfaceSlice(payload["output"]) {
		item, _ := value.(map[string]interface{})
		if stringField(item, "type") != "reasoning" {
			continue
		}
		for _, key := range []string{"summary", "content"} {
			for _, part := range interfaceSlice(item[key]) {
				record, _ := part.(map[string]interface{})
				if text := strings.TrimSpace(stringField(record, "text")); text != "" {
					chunks = append(chunks, text)
				}
			}
		}
	}
	return strings.Join(chunks, "\n")
}

func interfaceSlice(value interface{}) []interface{} {
	items, _ := value.([]interface{})
	return items
}

func cloneStringAnyMap(value map[string]interface{}) map[string]interface{} {
	cloned := make(map[string]interface{}, len(value)+1)
	for key, item := range value {
		cloned[key] = item
	}
	return cloned
}

func attachProviderPiUsage(result map[string]interface{}, usage map[string]any) {
	if len(usage) > 0 {
		result["usage"] = usage
	}
}
