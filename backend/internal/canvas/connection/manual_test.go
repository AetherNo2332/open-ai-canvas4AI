package connection

import "testing"

func TestManualInputsRejectInvalidGraphAssociations(t *testing.T) {
	nodes := []Node{
		{ID: "plain", Type: "text"},
		{ID: "hero", Type: "text", WorkflowKind: "character"},
		{ID: "hero-2", Type: "text", WorkflowKind: "character"},
		{ID: "image", Type: "image"},
		{ID: "video", Type: "video"},
		{ID: "audio", Type: "audio"},
		{ID: "commentary", Type: "text"},
		{ID: "convert", Type: "media-conversion"},
		{ID: "table", Type: "batch-table"},
		{ID: "drawing", Type: "drawing"},
		{ID: "config", Type: "config"},
	}
	tests := []struct {
		name string
		edge Edge
		ok   bool
	}{
		{name: "character to plain text", edge: Edge{From: "hero", To: "plain"}},
		{name: "character to generation", edge: Edge{From: "hero", To: "config"}, ok: true},
		{name: "video to image", edge: Edge{From: "video", To: "image"}},
		{name: "audio to text", edge: Edge{From: "audio", To: "commentary"}},
		{name: "image to conversion", edge: Edge{From: "image", To: "convert"}, ok: true},
		{name: "drawing to conversion", edge: Edge{From: "drawing", To: "convert"}},
		{name: "image to batch table", edge: Edge{From: "image", To: "table"}, ok: true},
		{name: "text to batch table", edge: Edge{From: "plain", To: "table"}},
		{name: "image to audio", edge: Edge{From: "image", To: "audio"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateManualInputs(nodes, nil, tc.edge)
			if (err == nil) != tc.ok {
				t.Fatalf("allowed=%v, err=%v", tc.ok, err)
			}
		})
	}
}

func TestManualInputsLimitAudioCharactersAndConversionInputs(t *testing.T) {
	nodes := []Node{
		{ID: "hero", Type: "text", WorkflowKind: "character"},
		{ID: "hero-2", Type: "text", WorkflowKind: "character"},
		{ID: "voice", Type: "audio"},
		{ID: "convert", Type: "media-conversion"},
		{ID: "image-1", Type: "image"},
		{ID: "image-2", Type: "image"},
	}
	edges := []Edge{{From: "hero", To: "voice"}}
	if err := ValidateManualInputs(nodes, edges, Edge{From: "hero-2", To: "voice"}); err == nil {
		t.Fatal("audio target accepted two character inputs")
	}
	if got := GraphInputLimit("media-conversion"); got != 1 {
		t.Fatalf("conversion input limit = %d", got)
	}
	if got := GraphInputLimit("image"); got != 0 {
		t.Fatalf("image input limit = %d", got)
	}
	if err := ValidateManualInputs(nodes, []Edge{{From: "image-1", To: "convert"}}, Edge{From: "image-2", To: "convert"}); err != nil {
		t.Fatalf("manual input rules should leave distinct-count enforcement to the caller: %v", err)
	}
}
