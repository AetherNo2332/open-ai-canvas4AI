package layout

import (
	"errors"
	"testing"
)

func documentNode(id, nodeType string, x, y float64, metadata ...map[string]any) map[string]any {
	meta := map[string]any{}
	if len(metadata) > 0 {
		for key, value := range metadata[0] {
			meta[key] = value
		}
	}
	return map[string]any{
		"id": id, "type": nodeType,
		"position": map[string]any{"x": x, "y": y},
		"width":    300.0, "height": 200.0, "metadata": meta,
	}
}

func TestDocumentSelectionSkipsLockedContainersAndHiddenNodes(t *testing.T) {
	doc := map[string]any{"nodes": []any{
		documentNode("text", "text", 0, 0),
		documentNode("locked", "text", 10, 10, map[string]any{"locked": true}),
		documentNode("frame", "frame", 20, 20),
		documentNode("child", "text", 30, 30),
		documentNode("hidden", "text", 40, 40, map[string]any{"batchRootId": "batch"}),
	}}
	doc["nodes"].([]any)[3].(map[string]any)["parentId"] = "frame"

	selected, skipped, err := ParseDocument(doc).Select([]string{"text", "locked", "frame", "child", "hidden"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || selected[0].ID != "text" {
		t.Fatalf("selected = %+v", selected)
	}
	if len(skipped) != 4 {
		t.Fatalf("skipped = %+v", skipped)
	}
}

func TestDocumentProjectionKeepsCanvasScalarStringSemantics(t *testing.T) {
	doc := map[string]any{
		"nodes": []any{
			map[string]any{"id": 7, "type": "text", "position": map[string]any{"x": 10.0, "y": 20.0}},
			map[string]any{"id": "child", "type": "text", "parentId": 9},
		},
		"connections": []any{map[string]any{"fromNodeId": 7, "toNodeId": "child"}},
	}
	document := ParseDocument(doc)
	nodes := document.Nodes()
	if len(nodes) != 2 || nodes[0].ID != "7" || nodes[0].Type != "text" || nodes[0].X != 10 || nodes[0].Y != 20 {
		t.Fatalf("scalar node fields = %+v", nodes)
	}
	edges := document.Edges()
	if len(edges) != 1 || edges[0] != (Edge{From: "7", To: "child"}) {
		t.Fatalf("scalar connection fields = %+v", edges)
	}
	selected, skipped, err := document.Select(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || selected[0].ID != "7" || len(skipped) != 0 {
		t.Fatalf("scalar projection changed movable selection: selected=%+v skipped=%+v", selected, skipped)
	}
}

func TestDocumentSelectionRejectsUnknownNode(t *testing.T) {
	doc := map[string]any{"nodes": []any{documentNode("text", "text", 0, 0)}}
	_, _, err := ParseDocument(doc).Select([]string{"missing"}, nil)
	var missing *NodeNotFoundError
	if !errors.As(err, &missing) || missing.NodeID != "missing" {
		t.Fatalf("unexpected selection error: %v", err)
	}
}

func TestDocumentArrangeRejectsEmptyGroups(t *testing.T) {
	doc := map[string]any{"nodes": []any{documentNode("a", "text", 0, 0), documentNode("b", "text", 10, 10)}}
	document := ParseDocument(doc)
	selected, _, err := document.Select(nil, [][]string{{}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = document.Arrange(selected, ArrangeOptions{Groups: []ArrangeGroup{{NodeIDs: []string{}}}})
	if !errors.Is(err, ErrEmptyGroups) {
		t.Fatalf("unexpected empty-group error: %v", err)
	}
}

func TestDocumentEdgesIgnoreEmptySegments(t *testing.T) {
	doc := map[string]any{"connections": []any{
		map[string]any{"fromNodeId": "", "toNodeId": "b"},
		map[string]any{"fromNodeId": "a", "toNodeId": ""},
		map[string]any{"fromNodeId": "a", "toNodeId": "b"},
	}}
	edges := ParseDocument(doc).Edges()
	if len(edges) != 1 || edges[0] != (Edge{From: "a", To: "b"}) {
		t.Fatalf("incomplete connections were not ignored: %+v", edges)
	}
}

func TestDocumentArrangeAutoFallsBackWithoutConnections(t *testing.T) {
	doc := map[string]any{"nodes": []any{
		documentNode("text", "text", 0, 0),
		documentNode("image", "image", 10, 10),
	}}
	document := ParseDocument(doc)
	selected, _, err := document.Select(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := document.Arrange(selected, ArrangeOptions{Mode: ModeAuto})
	if err != nil {
		t.Fatal(err)
	}
	if result.Positions["image"].Y <= result.Positions["text"].Y {
		t.Fatalf("auto layout without connections did not fall back to lanes: %+v", result.Positions)
	}
}

func TestDocumentPlaceNodeAvoidsOverlap(t *testing.T) {
	doc := map[string]any{"nodes": []any{documentNode("text", "text", 0, 0)}}
	position, ok := ParseDocument(doc).PlaceNode(nil, "text", 300, 200, nil)
	if !ok {
		t.Fatal("text node was not placeable")
	}
	if position.X == 0 && position.Y == 0 {
		t.Fatalf("new node overlaps the existing node: %+v", position)
	}
	if _, ok := ParseDocument(doc).PlaceNode(nil, "frame", 300, 200, nil); ok {
		t.Fatal("container node received an automatic position")
	}
}
