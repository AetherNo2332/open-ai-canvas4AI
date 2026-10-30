package mediaversions

import "yingce/backend/internal/model"

type OutputOwner = model.CanvasMediaOutputOwner

// Ownership is derived from the actual document for every entry point, even
// an ordinary node request that omits the structured Consumer argument.
func OutputOwners(nodes []map[string]any, target string) []OutputOwner {
	return model.CanvasMediaOutputOwners(nodes, target)
}
