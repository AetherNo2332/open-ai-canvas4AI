package app

import "infinite-canvas/backend/internal/canvas/references"

func validateCloudAgentGenerationReferences(doc map[string]any, nodeID string) error {
	return cloudAgentReferenceError(references.ValidateGenerationReferences(doc, nodeID, nil))
}
