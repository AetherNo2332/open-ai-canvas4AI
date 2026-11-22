// Package legacymigrate previews legacy shortplay canvas documents before they
// are switched to the declared reference-fields format. It is read-only: the
// caller keeps the original payload, ZIP envelope and revision.
package legacymigrate

import (
	"encoding/json"
	"fmt"

	"yingce/backend/internal/canvas/capability"
	"yingce/backend/internal/canvas/references"
)

const SchemaVersion = 1

type Source string

const (
	SourceDatabase Source = "database"
	SourceZip      Source = "zip"
)

// Options carries the caller's revision-checked choices and optional resource
// ownership lookup. A nil Registry uses the built-in capability registry.
type Options struct {
	Source         Source
	Choices        map[string]string
	Registry       *capability.Registry
	ResourceExists func(kind, id string) bool
}

type Preview struct {
	SchemaVersion int                            `json:"schemaVersion"`
	Source        Source                         `json:"source"`
	Required      bool                           `json:"required"`
	Ready         bool                           `json:"ready"`
	Candidate     map[string]any                 `json:"candidate,omitempty"`
	Diff          []Diff                         `json:"diff"`
	Resources     []Resource                     `json:"resources"`
	Blocking      []Issue                        `json:"blocking"`
	Warnings      []Issue                        `json:"warnings"`
	Conflicts     []references.MigrationConflict `json:"conflicts"`
	Identity      Identity                       `json:"identity"`
	Explicit      Explicit                       `json:"explicit"`
	Archive       *Archive                       `json:"archive,omitempty"`
}

type Diff struct {
	Path   string `json:"path"`
	Before any    `json:"before,omitempty"`
	After  any    `json:"after,omitempty"`
}

type Resource struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	NodeID string `json:"nodeId,omitempty"`
	Role   string `json:"role,omitempty"`
	Exists *bool  `json:"exists,omitempty"`
}

type Issue struct {
	Code    string `json:"code"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}

type Identity struct {
	Nodes   []NodeIdentity   `json:"nodes"`
	Rows    []RowIdentity    `json:"rows"`
	Columns []ColumnIdentity `json:"columns"`
}

type NodeIdentity struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

type RowIdentity struct {
	NodeID string `json:"nodeId"`
	Kind   string `json:"kind"`
	RowID  string `json:"rowId"`
}

type ColumnIdentity struct {
	TableID  string `json:"tableId"`
	ColumnID string `json:"columnId"`
	Kind     string `json:"kind"`
}

type Explicit struct {
	RowOutputs   []HandleRef   `json:"rowOutputs"`
	RowInputs    []HandleRef   `json:"rowInputs"`
	BatchColumns []HandleRef   `json:"batchColumns"`
	BatchOutputs []BatchOutput `json:"batchOutputs"`
	EmptyCells   []CellRef     `json:"emptyCells"`
	Frames       []FrameRef    `json:"frames"`
}

type HandleRef struct {
	EdgeID   string `json:"edgeId"`
	NodeID   string `json:"nodeId"`
	HandleID string `json:"handleId"`
	RefID    string `json:"refId"`
}

type BatchOutput struct {
	TableID string `json:"tableId"`
	RowID   string `json:"rowId"`
	NodeID  string `json:"nodeId"`
}

type CellRef struct {
	TableID  string `json:"tableId"`
	RowID    string `json:"rowId"`
	ColumnID string `json:"columnId"`
}

type FrameRef struct {
	NodeID string `json:"nodeId"`
	Kind   string `json:"kind"`
	RefID  string `json:"refId"`
}

// PreviewPayload previews a database PayloadJSON value.
func PreviewPayload(payload string, options Options) (Preview, error) {
	options.Source = SourceDatabase
	var doc map[string]any
	if err := json.Unmarshal([]byte(payload), &doc); err != nil {
		return Preview{}, fmt.Errorf("画布 PayloadJSON 格式错误: %w", err)
	}
	return PreviewDocument(doc, options)
}

// PreviewDocument previews a decoded canvas document. It never mutates doc.
func PreviewDocument(doc map[string]any, options Options) (Preview, error) {
	if doc == nil {
		return Preview{}, fmt.Errorf("画布文档为空")
	}
	if options.Source == "" {
		options.Source = SourceDatabase
	}
	identity, explicit, resources, blocking, warnings := inspect(doc, options)
	preview := Preview{
		SchemaVersion: SchemaVersion, Source: options.Source,
		Diff: []Diff{}, Resources: resources, Blocking: blocking, Warnings: warnings,
		Conflicts: []references.MigrationConflict{}, Identity: identity, Explicit: explicit,
	}
	if len(blocking) > 0 {
		return preview, nil
	}
	if references.Enabled(doc) {
		preview.Ready = true
		return preview, nil
	}
	preview.Required = true
	working, err := references.Clone(doc)
	if err != nil {
		preview.Blocking = append(preview.Blocking, Issue{Code: "clone_failed", Message: err.Error()})
		return preview, nil
	}
	consumed, batchWarnings := applyBatchOutputFields(working)
	preview.Warnings = append(preview.Warnings, batchWarnings...)
	// Preview IDs must be deterministic so the same archive can be replayed and
	// compared; the real switch (S13) allocates production identities.
	nextID := 0
	normalized, err := references.NormalizeLegacy(working, options.Choices, options.Registry, func() string {
		nextID++
		return fmt.Sprintf("preview-%d", nextID)
	})
	if err != nil {
		preview.Blocking = append(preview.Blocking, Issue{Code: "normalize_failed", Message: err.Error()})
		return preview, nil
	}
	preview.Conflicts = append(preview.Conflicts, normalized.Conflicts...)
	if len(normalized.Conflicts) > 0 {
		return preview, nil
	}
	preview.Candidate = dropConsumedEdges(normalized.Document, consumed)
	preview.Diff = DiffDocuments(doc, preview.Candidate)
	preview.Ready = true
	return preview, nil
}
