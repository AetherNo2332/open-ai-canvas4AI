package commands

import (
	"fmt"
	"infinite-canvas/backend/internal/canvas/capability"
	"infinite-canvas/backend/internal/canvas/mediaversions"
	"infinite-canvas/backend/internal/canvas/references"
	"infinite-canvas/backend/internal/identity"
	"time"
)

const ProtocolVersion = 1

var OperationTypes = []string{"add_node", "update_node", "delete_node", "duplicate_node", "select_node_version", "use_media_version", "set_parent", "connect_nodes", "delete_connection", "update_connection", "replace_text", "reorder_nodes", "reorder_connections", "reorder_rows", "edit_drawing", "edit_storyboard", "bind_storyboard_output", "edit_batch_table", "edit_image_batch", "create_previs_scene", "edit_previs_scene", "delete_previs_scene", "bind_asset", "bind_resource", "bind_character", "rename_canvas", "delete_canvas", "edit_canvas_settings", "edit_timeline", "migrate_references"}

type Snapshot struct {
	Protocol          int              `json:"protocolVersion"`
	CanvasID          string           `json:"canvasId"`
	DocumentEpoch     string           `json:"documentEpoch"`
	Revision          int64            `json:"revision"`
	EventSeq          int64            `json:"eventSeq"`
	Document          Document         `json:"document"`
	EntityVersions    map[string]int64 `json:"entityVersions"`
	EntityGenerations map[string]int64 `json:"entityGenerations,omitempty"`
	// ReferenceMigration 只出现在读取投影里，且只在文档缺少 referenceFieldsVersion
	// 时填充：客户端据此区分“需要一次性迁移”和“已规范化”。nil 表示无需迁移。
	ReferenceMigration *references.MigrationPreview `json:"referenceMigration,omitempty"`
}
type Receipt struct {
	Protocol          int              `json:"protocolVersion"`
	CanvasID          string           `json:"canvasId"`
	DocumentEpoch     string           `json:"documentEpoch"`
	OperationID       string           `json:"operationId"`
	ClientID          string           `json:"clientId"`
	ActorID           string           `json:"actorId"`
	Revision          int64            `json:"revision"`
	EventSeq          int64            `json:"eventSeq"`
	Changes           ChangeSet        `json:"changes"`
	EntityVersions    map[string]int64 `json:"entityVersions"`
	EntityGenerations map[string]int64 `json:"entityGenerations,omitempty"`
	Reset             *Snapshot        `json:"reset,omitempty"`
}
type CreateCanvasRequest struct {
	OperationID       string      `json:"operationId"`
	ClientID          string      `json:"clientId"`
	Title             string      `json:"title"`
	ProjectID         string      `json:"projectId,omitempty"`
	InitialOperations []Operation `json:"initialOperations,omitempty"`
}

type Operation struct {
	Type                   string                  `json:"type"`
	ID                     string                  `json:"id"`
	NodeType               string                  `json:"nodeType,omitempty"`
	Title                  *string                 `json:"title,omitempty"`
	Content                *string                 `json:"content,omitempty"`
	Patch                  map[string]any          `json:"patch,omitempty"`
	FieldBaselines         *NodeFieldBaselines     `json:"fieldBaselines,omitempty"`
	X                      *float64                `json:"x,omitempty"`
	Y                      *float64                `json:"y,omitempty"`
	FromNodeID             string                  `json:"fromNodeId,omitempty"`
	ToNodeID               string                  `json:"toNodeId,omitempty"`
	SourceNodeID           string                  `json:"sourceNodeId,omitempty"`
	CopiedFromNodeID       string                  `json:"copiedFromNodeId,omitempty"`
	ParentID               string                  `json:"parentId,omitempty"`
	Match                  string                  `json:"match,omitempty"`
	Replacement            string                  `json:"replacement,omitempty"`
	NodeIDs                []string                `json:"nodeIds,omitempty"`
	RowIDs                 []string                `json:"rowIds,omitempty"`
	FromHandleID           string                  `json:"fromHandleId,omitempty"`
	ToHandleID             string                  `json:"toHandleId,omitempty"`
	FromAnchorRatio        *float64                `json:"fromAnchorRatio,omitempty"`
	ToAnchorRatio          *float64                `json:"toAnchorRatio,omitempty"`
	Relation               string                  `json:"relation,omitempty"`
	StoryboardRowID        string                  `json:"storyboardRowId,omitempty"`
	Engine                 string                  `json:"engine,omitempty"`
	DrawingOperations      []DrawingOperation      `json:"drawingOperations,omitempty"`
	DrawingID              string                  `json:"drawingId,omitempty"`
	DrawingSnapshot        map[string]any          `json:"drawingSnapshot,omitempty"`
	DrawingElementIDs      []string                `json:"drawingElementIds,omitempty"`
	DrawingOptions         map[string]any          `json:"drawingOptions,omitempty"`
	Action                 string                  `json:"action,omitempty"`
	RowID                  string                  `json:"rowId,omitempty"`
	OutputMode             string                  `json:"outputMode,omitempty"`
	ColumnID               string                  `json:"columnId,omitempty"`
	BatchOperation         string                  `json:"batchOperation,omitempty"`
	Concurrency            int                     `json:"concurrency,omitempty"`
	GlobalPrompt           string                  `json:"globalPrompt,omitempty"`
	TemplateID             string                  `json:"templateId,omitempty"`
	PrevisOperations       []PrevisOperation       `json:"previsOperations,omitempty"`
	PrevisEntityOperations []PrevisEntityOperation `json:"previsEntityOperations,omitempty"`
	AssetID                string                  `json:"assetId,omitempty"`
	ResourceID             string                  `json:"resourceId,omitempty"`
	VersionID              string                  `json:"versionId,omitempty"`
	TimelineOperations     []TimelineOperation     `json:"timelineOperations,omitempty"`
}

type TimelineOperation struct {
	Type     string         `json:"type"`
	ID       string         `json:"id,omitempty"`
	Patch    map[string]any `json:"patch,omitempty"`
	TrackIDs []string       `json:"trackIds,omitempty"`
	ClipIDs  []string       `json:"clipIds,omitempty"`
}

type PrevisEntityOperation struct {
	Type  string         `json:"type"`
	ID    string         `json:"id,omitempty"`
	Patch map[string]any `json:"patch,omitempty"`
	IDs   []string       `json:"ids,omitempty"`
}

type Document map[string]any

type FieldBaseline struct {
	Exists bool `json:"exists"`
	Value  any  `json:"value"`
}

type NodeFieldBaselines struct {
	NodeType     string                   `json:"nodeType"`
	Generation   *int64                   `json:"generation,omitempty"`
	WorkflowKind string                   `json:"workflowKind,omitempty"`
	Fields       map[string]FieldBaseline `json:"fields"`
}
type Command struct {
	Protocol               int              `json:"protocolVersion"`
	CanvasID               string           `json:"canvasId"`
	DocumentEpoch          string           `json:"documentEpoch"`
	OperationID            string           `json:"operationId"`
	ClientID               string           `json:"clientId"`
	BaseRevision           int64            `json:"baseRevision"`
	ExpectedEntityVersions map[string]int64 `json:"expectedEntityVersions,omitempty"`
	Operations             []Operation      `json:"operations"`
}
type ValidationContext struct {
	Principal         identity.Principal
	Registry          *capability.Registry
	AllowedOperations map[string]bool
	ResourceAllowed   func(resourceID string) bool
	// Zero uses a stable epoch timestamp. The service supplies its commit time.
	Now              time.Time
	ModelInputLimits map[string]map[string]int
	Asset            func(assetID string) (AssetMetadata, error)
	// Resources contains only ready, owner-authorized resource records hydrated by the host.
	Resources              map[string]ResourceMetadata
	CharacterBindings      map[string]map[string]any
	FolderBindings         map[string]bool
	MediaVersionSelections map[string]mediaversions.Selection
}
type ResourceMetadata struct {
	ID, Kind, MimeType string
	Size, DurationMs   int64
	Width, Height      int
}
type AssetMetadata struct {
	ID, Title, Kind, Status, Content, ResourceID, MimeType string
	Size                                                   int64
	Width, Height                                          int
	DurationMs                                             int64
}
type EntityChange struct {
	Key    string         `json:"key"`
	Value  map[string]any `json:"value,omitempty"`
	Delete bool           `json:"delete,omitempty"`
}
type FieldChange struct {
	Value  any  `json:"value,omitempty"`
	Delete bool `json:"delete,omitempty"`
}
type ChangeSet struct {
	Entities          []EntityChange         `json:"entities"`
	Fields            map[string]FieldChange `json:"fields,omitempty"`
	NodeOrder         []string               `json:"nodeOrder,omitempty"`
	ConnectionOrder   []string               `json:"connectionOrder,omitempty"`
	RecreatedEntities map[string]bool        `json:"recreatedEntities,omitempty"`
}
type PlanResult struct {
	Document    Document           `json:"document"`
	Changes     ChangeSet          `json:"changes"`
	ReadSet     map[string]bool    `json:"readSet"`
	WriteSet    map[string]bool    `json:"writeSet"`
	Inverse     ChangeSet          `json:"inverse"`
	ResourceIDs []string           `json:"resourceIds"`
	Summary     []OperationSummary `json:"summary"`
	// Internal proof from the registry, operation provenance and actual diff.
	// Never accepted from the wire or used to remove related read-set entries.
	FieldMergeTargets map[string]bool `json:"-"`
}
type Error struct {
	Reason  string `json:"reason"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

func (e *Error) Error() string             { return e.Message }
func failure(reason, message string) error { return &Error{Reason: reason, Message: message} }
func BadAuthRequest(message string) error  { return failure("invalid_operation", message) }
func canvasFieldError(field, reason, message string) error {
	return &Error{Reason: reason, Field: field, Message: message}
}
func canvasSafeToolError(err error) string { return err.Error() }

type ArgumentError struct{ error }

func (e *ArgumentError) Unwrap() error { return e.error }

func validateCanvasID(value, label string, maximum int) error {
	if value == "" || len(value) > maximum {
		return BadAuthRequest(fmt.Sprintf("%s长度无效", label))
	}
	for _, ch := range value {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '-' || ch == ':' || ch == '.') {
			return BadAuthRequest(label + "包含非法字符")
		}
	}
	return nil
}

type DrawingOperation = canvasDrawingOp
type PrevisOperation = canvasPrevisPatchOperation
type planner struct {
	registry          *capability.Registry
	now               func() time.Time
	copiedNodeIDs     map[string]string
	copiedNodeSources map[string]map[string]any
}
