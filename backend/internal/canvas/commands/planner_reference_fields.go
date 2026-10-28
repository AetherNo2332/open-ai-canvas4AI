package commands

import (
	"yingce/backend/internal/canvas/capability"
	"yingce/backend/internal/canvas/references"
)

func reconcileReferenceFields(before, candidate Document, registry *capability.Registry) (references.Result, error) {
	if !references.Enabled(candidate) {
		return references.Result{Document: candidate}, nil
	}
	result, err := references.Reconcile(before, candidate, registry, newID)
	if typed, ok := err.(*references.Error); ok {
		return result, canvasFieldError(typed.Field, typed.Reason, typed.Message)
	}
	return result, err
}
