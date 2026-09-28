package app

import "encoding/json"

// providerPiUsageFromBody converts provider-reported text usage to Pi's field
// names. It returns only metrics actually reported upstream; missing provider
// usage must remain unknown, not become zeroes. Billing continues to use the
// separately persisted API-call log.
func providerPiUsageFromBody(body []byte, protocol string) map[string]any {
	if len(body) == 0 {
		return nil
	}
	payloads := providerResponsePayloads(body)
	if len(payloads) == 0 {
		return nil
	}

	usage := make(map[string]any)
	for _, payload := range payloads {
		usagePayload := payload
		if response, ok := payload["response"].(map[string]any); ok {
			usagePayload = mergeUsagePayload(payload, response)
		}
		if message, ok := payload["message"].(map[string]any); ok {
			usagePayload = mergeUsagePayload(usagePayload, message)
		}

		if providerUsage, ok := usagePayload["usage"].(map[string]any); ok {
			if protocol == "claude-api" {
				assignUsageValue(usage, "input", providerUsage, "input_tokens")
				assignUsageValue(usage, "output", providerUsage, "output_tokens")
				assignUsageValue(usage, "cacheRead", providerUsage, "cache_read_input_tokens")
				assignUsageValue(usage, "cacheWrite", providerUsage, "cache_creation_input_tokens", "cache_creation_tokens")
			} else {
				// OpenAI-compatible input counts include cached tokens when a split is
				// reported. Keep the split unknown when the provider omits it.
				assignUsageValue(usage, "input", providerUsage, "input_tokens", "prompt_tokens")
				assignUsageValue(usage, "output", providerUsage, "output_tokens", "completion_tokens")
				assignUsageValue(usage, "totalTokens", providerUsage, "total_tokens")
				assignUsageValue(usage, "cacheRead", providerUsage, "cached_tokens", "cache_read_input_tokens", "prompt_cache_hit_tokens")
				assignUsageValue(usage, "cacheWrite", providerUsage, "cache_write_tokens", "cache_creation_input_tokens", "cache_creation_tokens")
			}
			if protocol != "claude-api" {
				if details, ok := providerUsage["input_tokens_details"].(map[string]any); ok {
					assignUsageValue(usage, "cacheRead", details, "cached_tokens", "cache_read_input_tokens")
				}
				if details, ok := providerUsage["prompt_tokens_details"].(map[string]any); ok {
					assignUsageValue(usage, "cacheRead", details, "cached_tokens", "cache_read_input_tokens")
				}
			}
			if protocol == "claude-api" {
				assignUsageValue(usage, "totalTokens", providerUsage, "total_tokens")
			}
		}
		if metadata, ok := usagePayload["usageMetadata"].(map[string]any); ok {
			assignUsageValue(usage, "input", metadata, "promptTokenCount")
			assignUsageValue(usage, "output", metadata, "candidatesTokenCount")
			assignUsageValue(usage, "cacheRead", metadata, "cachedContentTokenCount")
			assignUsageValue(usage, "totalTokens", metadata, "totalTokenCount")
		}
	}
	if len(usage) == 0 {
		return nil
	}
	if protocol != "claude-api" {
		if input, inputOK := usage["input"].(int64); inputOK {
			if cacheRead, readOK := usage["cacheRead"].(int64); readOK {
				usage["input"] = max(input-cacheRead, int64(0))
			}
		}
	}
	return usage
}

func mergeUsagePayload(base, extra map[string]any) map[string]any {
	merged := make(map[string]any, len(base)+len(extra))
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range extra {
		if _, exists := merged[key]; !exists {
			merged[key] = value
		}
	}
	return merged
}

func assignUsageValue(target map[string]any, name string, values map[string]any, keys ...string) {
	if value, ok := firstInt64Value(values, keys...); ok {
		target[name] = value
	}
}

// providerPiUsageFromManifestUsage accepts a declarative provider's explicit
// Pi-shaped usage mapping. Fields are not synthesized: an incomplete mapping
// is omitted from Pi while the raw provider log remains available to billing.
func providerPiUsageFromManifestUsage(values map[string]any) map[string]any {
	usage := make(map[string]any)
	for _, field := range []string{"input", "output", "cacheRead", "cacheWrite", "totalTokens"} {
		if value, ok := firstInt64Value(values, field); ok {
			usage[field] = value
		}
	}
	if len(usage) == 0 {
		return nil
	}
	return usage
}

// providerPiUsageFromProviderUsage accepts the raw usage object already decoded
// by a declarative adapter. It supports either a provider-native usage object or
// an explicitly Pi-shaped mapping, while preserving omitted metrics as unknown.
func providerPiUsageFromProviderUsage(values map[string]any, protocol string) map[string]any {
	if usage := providerPiUsageFromManifestUsage(values); usage != nil {
		return usage
	}
	if len(values) == 0 {
		return nil
	}
	body, err := json.Marshal(map[string]any{"usage": values})
	if err != nil {
		return nil
	}
	return providerPiUsageFromBody(body, protocol)
}
