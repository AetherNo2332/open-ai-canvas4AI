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
