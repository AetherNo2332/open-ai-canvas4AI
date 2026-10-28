// Package references owns declared canvas inputs and their visual connections.
package references

import "yingce/backend/internal/canvas/contract"

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
	// Internal provenance: excludes a scope's owner-self read, but includes it
	// when it is an input to a different scope. ReadNodeIDs remains unchanged.
	ReadInputNodeIDs []string `json:"-"`
}
type Error struct{ Reason, Field, Message string }

func (e *Error) Error() string { return e.Message }
