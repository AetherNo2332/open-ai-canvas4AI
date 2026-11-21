package layout

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

const arrangeCoordCap = 1e6

// ErrEmptyGroups reports that every requested arrangement group was empty.
var ErrEmptyGroups = errors.New("分组里没有可整理的节点")

// NodeNotFoundError reports an explicit layout selection that references a
// node absent from the current canvas document.
type NodeNotFoundError struct{ NodeID string }

func (e *NodeNotFoundError) Error() string {
	return fmt.Sprintf("要整理的节点 %s 不在当前画布，请重新读取画布", e.NodeID)
}

// Document is an opaque canvas document projection. The raw map stays inside
// the layout package so the Agent-facing API does not expose untyped payloads.
type Document struct {
	value map[string]any
}

// ParseDocument wraps an already decoded canvas document. Permissions,
// snapshot checks and persistence remain the caller's responsibility.
func ParseDocument(value any) Document {
	document, _ := value.(map[string]any)
	return Document{value: document}
}

// Lane maps a canvas node type to its deterministic layout lane.
func Lane(nodeType string) (string, bool) {
	switch nodeType {
	case "video":
		return "video", true
	case "audio":
		return "audio", true
	case "image":
		return "image", true
	case "text", "markdown", "script", "batch-table":
		return "text", true
	default:
		return "", false
	}
}

// Nodes projects a canvas document into the immutable node snapshot used by
// layout rules.
func (doc Document) Nodes() []Node {
	raw := documentMaps(doc.value["nodes"])
	nodes := make([]Node, 0, len(raw))
	for _, node := range raw {
		id := documentString(node["id"])
		if id == "" {
			continue
		}
		lane, supported := Lane(documentString(node["type"]))
		item := Node{
			ID: id, Type: documentString(node["type"]), Lane: lane,
			NoMove: !supported, Movable: movable(node),
		}
		if position, ok := node["position"].(map[string]any); ok {
			item.X, _ = position["x"].(float64)
			item.Y, _ = position["y"].(float64)
		}
		item.Width, _ = node["width"].(float64)
		item.Height, _ = node["height"].(float64)
		if meta, ok := node["metadata"].(map[string]any); ok {
			item.Locked = meta["locked"] == true
		}
		nodes = append(nodes, item)
	}
	return nodes
}

// Edges projects valid document connections into dependency edges.
func (doc Document) Edges() []Edge {
	edges := make([]Edge, 0)
	for _, edge := range documentMaps(doc.value["connections"]) {
		from, to := documentString(edge["fromNodeId"]), documentString(edge["toNodeId"])
		if from == "" || to == "" {
			continue
		}
		edges = append(edges, Edge{From: from, To: to})
	}
	return edges
}

// movable reports whether the Agent may move a node. Locked nodes, containers,
// hidden batch children and nodes already owned by a container stay in place.
func movable(node map[string]any) bool {
	meta, _ := node["metadata"].(map[string]any)
	if meta["locked"] == true {
		return false
	}
	if _, supported := Lane(documentString(node["type"])); !supported {
		return false
	}
	if strings.TrimSpace(documentString(node["parentId"])) != "" {
		return false
	}
	if strings.TrimSpace(documentString(meta["batchRootId"])) != "" {
		return false
	}
	return true
}

// Select resolves explicit node IDs or group memberships into movable nodes.
// Missing explicit IDs are errors; locked, container and hidden nodes are
// skipped and reported back to the caller.
func (doc Document) Select(nodeIDs []string, groups [][]string) ([]Node, []string, error) {
	all := doc.Nodes()
	byID := make(map[string]Node, len(all))
	for _, node := range all {
		byID[node.ID] = node
	}

	requested := nodeIDs
	if len(groups) > 0 {
		requested = nil
		seen := map[string]bool{}
		for _, group := range groups {
			for _, id := range group {
				id = strings.TrimSpace(id)
				if id == "" || seen[id] {
					continue
				}
				seen[id] = true
				requested = append(requested, id)
			}
		}
	}

	selected := make([]Node, 0, len(requested))
	skipped := make([]string, 0)
	if len(requested) == 0 {
		for _, node := range all {
			if node.NoMove || !node.Movable {
				continue
			}
			selected = append(selected, node)
		}
		return selected, skipped, nil
	}
	for _, id := range requested {
		node, ok := byID[id]
		if !ok {
			return nil, nil, &NodeNotFoundError{NodeID: id}
		}
		if node.NoMove || !node.Movable {
			skipped = append(skipped, id)
			continue
		}
		selected = append(selected, node)
	}
	return selected, skipped, nil
}

// ArrangeGroup is one band of nodes in a grouped arrangement request.
type ArrangeGroup struct {
	Label   string
	NodeIDs []string
	Mode    Mode
}

// ArrangeOptions contains the already-validated arrangement inputs supplied by
// a tool or editor caller.
type ArrangeOptions struct {
	Groups []ArrangeGroup
	Mode   Mode
	Align  AlignMode
	Gap    float64
}

// ArrangeResult is the deterministic geometric result of an arrangement rule.
type ArrangeResult struct {
	Positions map[string]Position
	Labels    []string
	Mode      Mode
}

// Arrange computes a deterministic position map for the selected nodes.
func (doc Document) Arrange(selected []Node, options ArrangeOptions) (ArrangeResult, error) {
	index := make(map[string]Node, len(selected))
	for _, node := range selected {
		index[node.ID] = node
	}
	positions := map[string]Position{}
	labels := make([]string, 0, len(options.Groups))
	if len(options.Groups) > 0 {
		bands := make([]Band, 0, len(options.Groups))
		for _, group := range options.Groups {
			members := make([]Node, 0, len(group.NodeIDs))
			for _, id := range group.NodeIDs {
				if node, ok := index[strings.TrimSpace(id)]; ok {
					members = append(members, node)
				}
			}
			if len(members) == 0 {
				continue
			}
			mode := group.Mode
			if mode == ModeAuto {
				mode = ModeByLane
			}
			label := strings.TrimSpace(group.Label)
			if label == "" {
				label = fmt.Sprintf("第 %d 组", len(bands)+1)
			}
			labels = append(labels, label)
			if options.Align != "" {
				members = applyPositions(members, Align(members, options.Align))
			}
			bands = append(bands, Band{Label: label, Nodes: members, Mode: mode})
		}
		if len(bands) == 0 {
			return ArrangeResult{}, ErrEmptyGroups
		}
		positions = Bands(bands, options.Gap)
	} else {
		edges := doc.Edges()
		switch options.Mode {
		case ModeAuto:
			positions = Auto(selected, edges)
		case ModeFlow:
			positions = Flow(selected, edges)
		case ModeByLane:
			positions = ByLane(selected)
		default:
			positions = Linear(selected, options.Mode)
		}
		if options.Align != "" {
			positions = mergePositions(positions, Align(applyPositions(selected, positions), options.Align))
		}
	}
	for id, position := range positions {
		positions[id] = clampPosition(position)
	}
	return ArrangeResult{Positions: positions, Labels: labels, Mode: options.Mode}, nil
}

// ApplyPositions writes computed coordinates back into a canvas document in
// memory. Persistence and snapshot/conflict handling remain with the caller.
func (doc Document) ApplyPositions(positions map[string]Position) {
	nodes := documentMaps(doc.value["nodes"])
	for index, node := range nodes {
		position, ok := positions[documentString(node["id"])]
		if !ok {
			continue
		}
		current, _ := node["position"].(map[string]any)
		if current == nil {
			current = map[string]any{}
		}
		current["x"] = position.X
		current["y"] = position.Y
		node["position"] = current
		nodes[index] = node
	}
	doc.value["nodes"] = nodes
}

// PlaceNode finds a non-overlapping position for a new node.
func (doc Document) PlaceNode(pending []Node, nodeType string, width, height float64, preferred *Position) (Position, bool) {
	lane, supported := Lane(nodeType)
	if !supported {
		return Position{}, false
	}
	occupied := append([]Node{}, doc.Nodes()...)
	occupied = append(occupied, pending...)
	return FreeSlot(width, height, lane, occupied, preferred, PlacementGap), true
}

// FindNode returns the projected node with the requested ID.
func FindNode(nodes []Node, id string) (Node, bool) {
	for _, node := range nodes {
		if node.ID == id {
			return node, true
		}
	}
	return Node{}, false
}

func clampPosition(position Position) Position {
	return Position{X: clampCoordinate(position.X), Y: clampCoordinate(position.Y)}
}

func clampCoordinate(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	value = math.Round(value*100) / 100
	if value > arrangeCoordCap {
		return arrangeCoordCap
	}
	if value < -arrangeCoordCap {
		return -arrangeCoordCap
	}
	return value
}

func applyPositions(nodes []Node, positions map[string]Position) []Node {
	applied := make([]Node, 0, len(nodes))
	for _, node := range nodes {
		if position, ok := positions[node.ID]; ok {
			node.X, node.Y = position.X, position.Y
		}
		applied = append(applied, node)
	}
	return applied
}

func mergePositions(base, override map[string]Position) map[string]Position {
	merged := make(map[string]Position, len(base))
	for id, position := range base {
		merged[id] = position
	}
	for id, position := range override {
		merged[id] = position
	}
	return merged
}

func documentMaps(value any) []map[string]any {
	result := []map[string]any{}
	switch items := value.(type) {
	case []any:
		for _, item := range items {
			if mapped, ok := item.(map[string]any); ok {
				result = append(result, mapped)
			}
		}
	case []map[string]any:
		result = items
	}
	return result
}

func documentString(value any) string {
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "<nil>" {
		return ""
	}
	return text
}
