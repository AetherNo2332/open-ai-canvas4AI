package app

import "testing"

func TestAgentParityGraphMatchesManualGenericAssociations(t *testing.T) {
	nodes := []map[string]any{{"id": "m", "type": "markdown"}, {"id": "h", "type": "html"}, {"id": "d", "type": "drawing"}, {"id": "c", "type": "chart"}, {"id": "v", "type": "video"}, {"id": "i", "type": "image"}, {"id": "a", "type": "audio"}, {"id": "x", "type": "media-conversion"}, {"id": "f", "type": "future-plugin"}}
	for _, pair := range [][2]string{{"m", "h"}, {"c", "m"}, {"d", "i"}, {"i", "x"}, {"v", "x"}} {
		if err := validateCloudAgentConnection(nodes, pair[0], pair[1]); err != nil {
			t.Fatalf("manual association %v rejected: %v", pair, err)
		}
	}
	for _, pair := range [][2]string{{"v", "i"}, {"a", "i"}, {"d", "x"}, {"f", "m"}} {
		if err := validateCloudAgentConnection(nodes, pair[0], pair[1]); err == nil {
			t.Fatalf("invalid association %v accepted", pair)
		}
	}
}

func TestAgentGraphRejectsInvalidReferences(t *testing.T) {
	nodes := []map[string]any{
		{"id": "plain", "type": "text"},
		{"id": "hero", "type": "text", "metadata": map[string]any{"workflowKind": "character"}},
		{"id": "hero-2", "type": "text", "metadata": map[string]any{"workflowKind": "character"}},
		{"id": "image", "type": "image"},
		{"id": "video", "type": "video"},
		{"id": "audio", "type": "audio"},
		{"id": "convert", "type": "media-conversion"},
		{"id": "table", "type": "batch-table"},
	}
	for _, tc := range []struct {
		name        string
		from, to    string
		connections []map[string]any
	}{
		{name: "character to plain text", from: "hero", to: "plain"},
		{name: "video to image", from: "video", to: "image"},
		{name: "audio to text", from: "audio", to: "plain"},
		{name: "text to conversion", from: "plain", to: "convert"},
		{name: "text to batch table", from: "plain", to: "table"},
		{name: "second character to audio", from: "hero-2", to: "audio", connections: []map[string]any{{"fromNodeId": "hero", "toNodeId": "audio"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			if tc.connections == nil {
				err = validateCloudAgentConnection(nodes, tc.from, tc.to)
			} else {
				err = validateCloudAgentConnection(nodes, tc.from, tc.to, tc.connections)
			}
			if err == nil {
				t.Fatal("invalid graph reference accepted")
			}
		})
	}
	connections := []map[string]any{{"fromNodeId": "image", "toNodeId": "convert"}}
	if err := validateCloudAgentConnection(nodes, "video", "convert", connections); err == nil {
		t.Fatal("media conversion accepted two distinct inputs")
	}
}
