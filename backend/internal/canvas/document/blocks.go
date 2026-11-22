package document

import (
	"sort"

	"yingce/backend/internal/canvas/capability"
)

type FieldBlock struct {
	Key      string
	Writable bool
	Start    int
	End      int
	Line     int
}

// FieldBlocks indexes field byte ranges using the same emitter as Render, so
// block ranges cannot drift from the printed document.
func FieldBlocks(descriptor capability.Descriptor, node map[string]any) ([]FieldBlock, error) {
	_, blocks, err := renderDocument(descriptor, node)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(blocks, func(i, j int) bool { return blocks[i].Start < blocks[j].Start })
	return blocks, nil
}
