package app

import "yingce/backend/internal/canvas/connection"

// Mirrors canvas-connection-policy.ts structural admission. A graph association
// never by itself makes an unrendered drawing/filter a generation resource.
func cloudAgentGraphMaxInputs(kind string) int {
	return connection.GraphInputLimit(kind)
}

func cloudAgentManualGraphInputError(nodes []map[string]any, from, to map[string]any, _ string, connections []map[string]any) error {
	edges := make([]connection.Edge, 0, len(connections))
	for _, edge := range connections {
		edges = append(edges, cloudAgentConnectionEdge(edge))
	}
	candidate := connection.Edge{From: stringValue(from["id"]), To: stringValue(to["id"])}
	if err := connection.ValidateManualInputs(cloudAgentConnectionNodes(nodes), edges, candidate); err != nil {
		return BadAuthRequest(err.Error())
	}
	return nil
}
