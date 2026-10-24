package references

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestResolveGenerationReferencePermission(t *testing.T) {
	for _, role := range []string{"source-text", "reference"} {
		var doc map[string]any
		json.Unmarshal([]byte(fmt.Sprintf(`{"nodes":[{"id":"source","type":"text"},{"id":"target","type":"image","metadata":{"generationSpec":{"version":1,"mode":"image","prompt":"","options":{},"referenceBindings":[{"id":"ref","nodeId":"source","mediaType":"text","role":"%s","order":0,"resolution":"latest"}],"textInputMode":"prompt-only"}}}]}`, role)), &doc)
		_, err := Resolve(doc, Scope{Kind: "generation", NodeID: "target"}, nil)
		if (err == nil) != (role == "source-text") {
			t.Fatalf("ordinary text permission for %s: %v", role, err)
		}
	}
}
