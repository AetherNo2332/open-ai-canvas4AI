// Package relation is the single field-to-edge projection rule. It reads only
// node fields; persisted connections are a rendering cache and never a second
// source of truth.
package relation

import (
	"fmt"
	"strings"

	"yingce/backend/internal/canvas/capability"
)

type Family string

const (
	FamilyMediaReference Family = "R1"
	FamilyVideoFrame     Family = "R1-frame"
	FamilyTextReference  Family = "R1-text"
)

type Edge struct {
	Family     Family
	FromNodeID string
	ToNodeID   string
	Field      string
}

func MediaReferenceEdgeID(fromNodeID, toNodeID string) string {
	return "conn:r1:" + toNodeID + ":" + fromNodeID
}

func VideoFrameEdgeID(field, fromNodeID, toNodeID string) string {
	return "conn:frame:" + field + ":" + toNodeID + ":" + fromNodeID
}

func (e Edge) ID() string {
	if e.Family == FamilyVideoFrame {
		return VideoFrameEdgeID(e.Field, e.FromNodeID, e.ToNodeID)
	}
	return MediaReferenceEdgeID(e.FromNodeID, e.ToNodeID)
}

var frameFields = []string{"videoStartFrameNodeId", "videoEndFrameNodeId"}

// Project projects fields into ordered edges. Order is target-node order first,
// then each target's field-array order. R1 wins over frame/text for the same
// source and the result is deduplicated by edge identity.
func Project(nodes []map[string]any) []Edge {
	edges := make([]Edge, 0, len(nodes))
	for _, node := range nodes {
		targetID := text(node["id"])
		if targetID == "" {
			continue
		}
		metadata, _ := node["metadata"].(map[string]any)
		declared := stringList(metadata["referenceNodeIds"])
		frames := frameReferences(metadata)
		textDeclared := stringList(metadata["referenceTextNodeIds"])
		if len(declared) == 0 && len(frames) == 0 && len(textDeclared) == 0 {
			continue
		}
		seen := make(map[string]bool, len(declared)+len(frames)+len(textDeclared))
		for _, sourceID := range declared {
			if seen[sourceID] {
				continue
			}
			seen[sourceID] = true
			edges = append(edges, Edge{
				Family: FamilyMediaReference, FromNodeID: sourceID, ToNodeID: targetID, Field: "referenceNodeIds",
			})
		}
		for _, frame := range frames {
			if seen[frame.ID] {
				continue
			}
			seen[frame.ID] = true
			edges = append(edges, Edge{
				Family: FamilyVideoFrame, FromNodeID: frame.ID, ToNodeID: targetID, Field: frame.Field,
			})
		}
		for _, sourceID := range textDeclared {
			if seen[sourceID] {
				continue
			}
			seen[sourceID] = true
			edges = append(edges, Edge{
				Family: FamilyTextReference, FromNodeID: sourceID, ToNodeID: targetID, Field: "referenceTextNodeIds",
			})
		}
	}
	seenEdges := make(map[string]bool, len(edges))
	deduped := make([]Edge, 0, len(edges))
	for _, edge := range edges {
		key := edge.ID()
		if seenEdges[key] {
			continue
		}
		seenEdges[key] = true
		deduped = append(deduped, edge)
	}
	return deduped
}

func ProjectConsumed(nodes []map[string]any) []Edge {
	return ProjectConsumedWith(capability.BuiltinRegistry(), nodes)
}

func ProjectConsumedWith(registry *capability.Registry, nodes []map[string]any) []Edge {
	existing := make(map[string]map[string]any, len(nodes))
	for _, node := range nodes {
		if id := text(node["id"]); id != "" {
			existing[id] = node
		}
	}
	edges := make([]Edge, 0, len(nodes))
	for _, edge := range Project(nodes) {
		if existing[edge.FromNodeID] == nil {
			continue
		}
		target := existing[edge.ToNodeID]
		if target == nil {
			continue
		}
		if edge.Family == FamilyVideoFrame {
			if text(target["type"]) == "video" {
				edges = append(edges, edge)
			}
			continue
		}
		if text(target["type"]) == "text" {
			edges = append(edges, edge)
			continue
		}
		descriptor, known := registry.Resolve(text(target["type"]))
		if known && descriptor.GenerationMode != "" {
			edges = append(edges, edge)
		}
	}
	return edges
}

type frameReference struct {
	Field string
	ID    string
}

func frameReferences(metadata map[string]any) []frameReference {
	frames := make([]frameReference, 0, len(frameFields))
	seen := make(map[string]bool, len(frameFields))
	for _, key := range frameFields {
		id := text(metadata[key])
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		frames = append(frames, frameReference{Field: key, ID: id})
	}
	return frames
}

func stringList(value any) []string {
	switch items := value.(type) {
	case []any:
		ids := make([]string, 0, len(items))
		for _, item := range items {
			if id := text(item); id != "" {
				ids = append(ids, id)
			}
		}
		return ids
	case []string:
		ids := make([]string, 0, len(items))
		for _, item := range items {
			if id := text(item); id != "" {
				ids = append(ids, id)
			}
		}
		return ids
	}
	return nil
}

func text(value any) string {
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "<nil>" {
		return ""
	}
	return text
}
