// Package references owns declared canvas inputs and their visual connections.
package references

import "infinite-canvas/backend/internal/canvas/contract"

const Version = 1
const ManagedBy = "reference-fields-v1"

type Scope struct{ Kind, NodeID, RowID, Mode string }
type EdgeIntent struct{ Key, OwnerNodeID, RowID, BindingID, FromNodeID, ToNodeID, FromHandleID, ToHandleID, Relation string }
type Resolution struct {
	Bindings          []contract.ReferenceBinding
	DependencyNodeIDs []string
	Edges             []EdgeIntent
}
type Result struct {
	Document    map[string]any
	ReadNodeIDs []string
	ReadEdgeIDs []string
}
type Error struct{ Reason, Field, Message string }

func (e *Error) Error() string { return e.Message }
