package app

import (
	"encoding/json"
	"fmt"
	"regexp"
	"testing"
)

var agentFunctionNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// Every callable function is a JSON Schema object with bounded, closed input.
// The same schema is sent to providers and used by server-side preflight.
func TestCloudAgentFunctionSchemasAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, tool := range cloudAgentTools(categoryTestRequest()) {
		function, ok := tool["function"].(map[string]any)
		if !ok {
			t.Fatalf("missing function definition: %#v", tool)
		}
		name, _ := function["name"].(string)
		if !agentFunctionNamePattern.MatchString(name) || seen[name] {
			t.Fatalf("invalid or duplicate tool name %q", name)
		}
		seen[name] = true
		if function["description"] == "" {
			t.Fatalf("missing tool description: %s", name)
		}
		parameters, ok := function["parameters"].(map[string]any)
		if !ok || parameters["type"] != "object" {
			t.Fatalf("%s has no object input schema", name)
		}
		checkAgentSchemaNode(t, name, parameters, true)
		wire, err := json.Marshal(tool)
		if err != nil || !json.Valid(wire) {
			t.Fatalf("%s is not JSON serializable: %v", name, err)
		}
		if cloudAgentIsToolCategory(name) {
			if err := validateCloudAgentToolArguments(parameters, `{}`); err != nil {
				t.Fatalf("parent %s rejected empty arguments: %v", name, err)
			}
			if err := validateCloudAgentToolArguments(parameters, `{"unexpected":true}`); err == nil {
				t.Fatalf("parent %s accepted unknown arguments", name)
			}
		}
	}
}

func checkAgentSchemaNode(t *testing.T, path string, schema map[string]any, closeObject bool) {
	t.Helper()
	var types []string
	switch value := schema["type"].(type) {
	case []string:
		types = value
	case []any:
		types = make([]string, 0, len(value))
		for _, item := range value {
			kind, ok := item.(string)
			if !ok {
				t.Fatalf("%s has nonstring type", path)
			}
			types = append(types, kind)
		}
	case string, nil:
	default:
		t.Fatalf("%s has malformed type", path)
	}
	if types != nil {
		if len(types) == 0 {
			t.Fatalf("%s has empty type union", path)
		}
		seen := map[string]bool{}
		for _, kind := range types {
			if kind == "" || seen[kind] {
				t.Fatalf("%s has empty or repeated type %q", path, kind)
			}
			seen[kind] = true
			branch := make(map[string]any, len(schema))
			for key, value := range schema {
				branch[key] = value
			}
			branch["type"] = kind
			checkAgentSchemaNode(t, path, branch, closeObject)
		}
		return
	}
	kind, _ := schema["type"].(string)
	switch kind {
	case "object":
		properties, ok := schema["properties"].(map[string]any)
		if !ok {
			// Capability dictionaries are represented as open JSON objects; the
			// runtime validates their values and key safety separately.
			properties = map[string]any{}
			ok = true
		}
		if !ok {
			t.Fatalf("%s object has no properties map", path)
		}
		mapValues, typedMap := schema["additionalProperties"].(map[string]any)
		if typedMap {
			checkAgentSchemaNode(t, path+".*", mapValues, true)
		}
		openCapabilityObject := len(properties) == 0 && !typedMap && schema["additionalProperties"] == nil
		if closeObject && !openCapabilityObject && !typedMap && schema["additionalProperties"] != false {
			t.Fatalf("%s object permits unknown fields", path)
		}
		required := map[string]bool{}
		switch values := schema["required"].(type) {
		case []string:
			for _, value := range values {
				required[value] = true
			}
		case []any:
			for _, value := range values {
				name, ok := value.(string)
				if !ok {
					t.Fatalf("%s has nonstring required key", path)
				}
				required[name] = true
			}
		case nil:
		default:
			t.Fatalf("%s required must be an array", path)
		}
		for name := range required {
			if _, exists := properties[name]; !exists {
				t.Fatalf("%s requires undeclared property %s", path, name)
			}
		}
		for name, raw := range properties {
			child, ok := raw.(map[string]any)
			if !ok {
				t.Fatalf("%s.%s is not a schema", path, name)
			}
			checkAgentSchemaNode(t, path+"."+name, child, true)
		}
	case "array":
		child, ok := schema["items"].(map[string]any)
		if !ok {
			t.Fatalf("%s array has no item schema", path)
		}
		checkAgentSchemaNode(t, path+"[]", child, true)
	case "string", "number", "integer", "boolean", "null":
	case "":
		constraint := len(schema) == 0
		for _, keyword := range []string{"const", "enum", "anyOf", "oneOf", "not", "required", "properties", "items", "minItems", "maxItems", "minLength", "maxLength", "minimum", "maximum", "exclusiveMinimum"} {
			if _, exists := schema[keyword]; exists {
				constraint = true
			}
		}
		if !constraint {
			t.Fatalf("%s has no type or supported constraint", path)
		}
		// Combination branches can constrain a property from the enclosing
		// object without redeclaring its type or all its sibling properties.
		if properties, ok := schema["properties"].(map[string]any); ok {
			for name, raw := range properties {
				child, ok := raw.(map[string]any)
				if !ok {
					t.Fatalf("%s.%s is not a schema", path, name)
				}
				checkAgentSchemaNode(t, path+"."+name, child, true)
			}
		}
	default:
		t.Fatalf("%s has invalid JSON Schema type %q", path, kind)
	}
	if alternatives, ok := schema["oneOf"]; ok {
		branches, ok := alternatives.([]map[string]any)
		if !ok || len(branches) == 0 {
			t.Fatalf("%s has invalid oneOf", path)
		}
		for index, branch := range branches {
			if _, err := json.Marshal(branch); err != nil {
				t.Fatalf("%s oneOf[%d]: %v", path, index, err)
			}
		}
	}
	if alternatives, ok := schema["anyOf"]; ok {
		branches := agentSchemaAlternatives(alternatives)
		if len(branches) == 0 {
			t.Fatalf("%s has invalid anyOf", path)
		}
		for index, branch := range branches {
			checkAgentSchemaNode(t, fmt.Sprintf("%s.anyOf[%d]", path, index), branch, closeObject)
		}
	}
	for _, key := range []string{"minItems", "maxItems", "minLength", "maxLength", "minimum", "maximum", "exclusiveMinimum"} {
		if value, ok := schema[key]; ok {
			if _, numeric := cloudAgentSchemaNumber(value); !numeric {
				t.Fatalf("%s.%s is not numeric: %s", path, key, fmt.Sprint(value))
			}
		}
	}
}

func agentSchemaAlternatives(value any) []map[string]any {
	if branches, ok := value.([]map[string]any); ok {
		return branches
	}
	var result []map[string]any
	if branches, ok := value.([]any); ok {
		for _, raw := range branches {
			branch, ok := raw.(map[string]any)
			if !ok {
				return nil
			}
			result = append(result, branch)
		}
	}
	return result
}

func agentSchemaDeclaresType(schema map[string]any, kind string) bool {
	if schema["type"] == kind {
		return true
	}
	for _, declared := range cloudAgentSchemaStrings(schema["type"]) {
		if declared == kind {
			return true
		}
	}
	for _, branch := range agentSchemaAlternatives(schema["anyOf"]) {
		if agentSchemaDeclaresType(branch, kind) {
			return true
		}
	}
	return false
}
