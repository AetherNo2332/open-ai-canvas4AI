package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestCanvasAgentManualOperationConformance(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "web", "test", "fixtures", "canvas-agent-operation-cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name     string          `json:"name"`
		Input    map[string]any  `json:"input"`
		Ops      json.RawMessage `json:"ops"`
		Expected map[string]any  `json:"expected"`
	}
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			args, err := decodeCloudAgentCanvasArgs(`{"snapshotHash":"conformance","ops":` + string(fixture.Ops) + `}`)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := applyCloudAgentCanvasPlan(fixture.Input, args.Ops); err != nil {
				t.Fatal(err)
			}
			nodes := creationMaps(fixture.Input["nodes"])
			if float64(len(nodes)) != fixture.Expected["nodeCount"] {
				t.Fatalf("node count %d, want %v", len(nodes), fixture.Expected["nodeCount"])
			}
			findCopied := func(id string) map[string]any {
				for _, node := range nodes {
					if stringValue(cloudAgentNodeMetadata(node)["copiedFromNodeId"]) == id {
						return node
					}
				}
				t.Fatalf("copied node for %s not found", id)
				return nil
			}
			for _, assertion := range creationMaps(fixture.Expected["nodeAssertions"]) {
				var actual map[string]any
				if id := stringValue(assertion["id"]); id != "" {
					for _, node := range nodes {
						if stringValue(node["id"]) == id {
							actual = node
							break
						}
					}
				} else {
					actual = findCopied(stringValue(assertion["copiedFromNodeId"]))
				}
				if actual == nil {
					t.Fatalf("missing node %+v", assertion)
				}
				fields := make(map[string]any)
				for key, value := range assertion {
					switch key {
					case "id", "copiedFromNodeId", "absentNodeFields", "absentMetadata", "parentCopiedFromNodeId", "metadataReferenceAssertions":
					default:
						fields[key] = value
					}
				}
				assertCanvasAgentConformanceSubset(t, actual, fields, "node")
				for _, key := range cloudAgentStrings(assertion["absentNodeFields"]) {
					if actual[key] != nil {
						t.Fatalf("field %s retained: %+v", key, actual)
					}
				}
				meta := cloudAgentNodeMetadata(actual)
				for _, key := range cloudAgentStrings(assertion["absentMetadata"]) {
					if canvasAgentConformancePath(meta, key) != nil {
						t.Fatalf("metadata %s retained: %+v", key, meta)
					}
				}
				if parent := stringValue(assertion["parentCopiedFromNodeId"]); parent != "" && actual["parentId"] != findCopied(parent)["id"] {
					t.Fatalf("copied parent not remapped: %+v", actual)
				}
				if refs, ok := assertion["metadataReferenceAssertions"].(map[string]any); ok {
					for key, value := range refs {
						if canvasAgentConformancePath(meta, key) != findCopied(stringValue(value))["id"] {
							t.Fatalf("copy reference %s not remapped: %+v", key, meta)
						}
					}
				}
			}
			actualEdges, expectedEdges := []map[string]any{}, []map[string]any{}
			for _, edge := range creationMaps(fixture.Input["connections"]) {
				actualEdges = append(actualEdges, map[string]any{"fromNodeId": edge["fromNodeId"], "toNodeId": edge["toNodeId"]})
			}
			for _, edge := range creationMaps(fixture.Expected["connections"]) {
				from, to := edge["fromNodeId"], edge["toNodeId"]
				if from == nil {
					from = findCopied(stringValue(edge["fromCopiedNodeId"]))["id"]
				}
				if to == nil {
					to = findCopied(stringValue(edge["toCopiedNodeId"]))["id"]
				}
				expectedEdges = append(expectedEdges, map[string]any{"fromNodeId": from, "toNodeId": to})
			}
			if !reflect.DeepEqual(actualEdges, expectedEdges) {
				t.Fatalf("edge endpoints %+v, want %+v", actualEdges, expectedEdges)
			}
		})
	}
}

func canvasAgentConformancePath(value any, path string) any {
	for _, key := range strings.Split(path, ".") {
		switch typed := value.(type) {
		case map[string]any:
			value = typed[key]
		case []any:
			index, err := strconv.Atoi(key)
			if err != nil || index < 0 || index >= len(typed) {
				return nil
			}
			value = typed[index]
		default:
			return nil
		}
	}
	return value
}

func assertCanvasAgentConformanceSubset(t *testing.T, actual, expected any, path string) {
	t.Helper()
	switch want := expected.(type) {
	case map[string]any:
		got, ok := actual.(map[string]any)
		if !ok {
			t.Fatalf("%s expected object, got %T", path, actual)
		}
		for key, value := range want {
			assertCanvasAgentConformanceSubset(t, got[key], value, path+"."+key)
		}
	case []any:
		got, ok := actual.([]any)
		if !ok || len(got) != len(want) {
			t.Fatalf("%s expected array %+v, got %+v", path, want, actual)
		}
		for i, value := range want {
			assertCanvasAgentConformanceSubset(t, got[i], value, path)
		}
	default:
		if !reflect.DeepEqual(actual, expected) {
			t.Fatalf("%s = %+v, want %+v", path, actual, expected)
		}
	}
}
