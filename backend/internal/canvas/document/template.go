package document

import "yingce/backend/internal/canvas/capability"

func Template(descriptor capability.Descriptor) (string, error) {
	return Render(descriptor, map[string]any{
		"type":     descriptor.Type,
		"title":    "",
		"metadata": descriptor.Metadata(""),
	})
}
