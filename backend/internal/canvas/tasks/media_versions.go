package tasks

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"infinite-canvas/backend/internal/canvas/commands"
	"infinite-canvas/backend/internal/canvas/mediaversions"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

var errMediaVersionTargetChanged = errors.New("media version target changed")

func mediaVersionError(err error) error {
	var version *mediaversions.Error
	if errors.As(err, &version) {
		return &commands.Error{Reason: version.Reason, Message: version.Message}
	}
	return err
}

func settleMediaVersion(tx *repository.Repository, doc commands.Document, binding model.CanvasTaskBinding, task model.Task, version model.CanvasMediaVersion, outputs []model.CanvasTaskOutput) (commands.Document, string, error) {
	var consumer Consumer
	if err := json.Unmarshal([]byte(binding.ConsumerJSON), &consumer); err != nil {
		return nil, "", err
	}
	node := findNode(doc, version.NodeID)
	blocked := ""
	var row map[string]any
	if consumer.Kind == "storyboard" || consumer.Kind == "batch" {
		if parent := findNode(doc, binding.TargetNodeID); parent != nil {
			row = consumerRow(parent, consumer)
		}
		field := consumer.Field
		if consumer.Kind == "batch" {
			field = "outputNodeId"
		}
		switch {
		case row == nil:
			blocked = "row_deleted"
		case str(row["taskId"]) != task.ID:
			blocked = "binding_changed"
		case str(row[field]) != version.NodeID || consumer.OutputNodeID != version.NodeID:
			blocked = "output_binding_changed"
		}
	}
	scope := mediaversions.Scope{OwnerID: version.OwnerID, CanvasID: version.CanvasID, Epoch: version.Epoch, NodeID: version.NodeID, Generation: version.Generation}
	if node != nil {
		actual, err := mediaversions.ScopeForNode(tx, scope.OwnerID, scope.CanvasID, scope.NodeID)
		if err != nil {
			node = nil
		} else {
			scope = actual
		}
	}
	resultNode := map[string]any{"id": version.NodeID, "type": version.Mode, "metadata": map[string]any{}}
	if node != nil {
		raw, _ := json.Marshal(node)
		if err := json.Unmarshal(raw, &resultNode); err != nil {
			return nil, "", err
		}
	}
	if task.Status == model.TaskStatusSucceeded {
		if len(outputs) == 0 {
			return nil, "", fmt.Errorf("media version output missing")
		}
		if err := applyOutput(resultNode, task, outputs[0]); err != nil {
			return nil, "", err
		}
	}
	firstMedia := node != nil && mediaversions.ReadState(node).CurrentVersionID == ""
	var frozen struct {
		Geometry     map[string]any              `json:"geometry"`
		OutputOwners []mediaversions.OutputOwner `json:"outputOwners"`
	}
	_ = json.Unmarshal([]byte(version.RequestJSON), &frozen)
	if blocked == "" && frozen.OutputOwners != nil && !reflect.DeepEqual(frozen.OutputOwners, mediaversions.OutputOwners(objectList(doc["nodes"]), version.NodeID)) {
		blocked = "output_binding_changed"
	}
	geometryUnchanged := node != nil && reflect.DeepEqual(frozen.Geometry, map[string]any{"position": node["position"], "width": node["width"], "height": node["height"]})
	adopted, reason, err := mediaversions.Finish(tx, scope, node, task, metadata(resultNode), outputs, blocked)
	if err != nil {
		return nil, "", mediaVersionError(err)
	}
	if adopted && firstMedia && geometryUnchanged {
		node["position"], node["width"], node["height"] = resultNode["position"], resultNode["width"], resultNode["height"]
	}
	claimField := consumer.Field
	if consumer.Kind == "batch" {
		claimField = "outputNodeId"
	}
	if row != nil && str(row["taskId"]) == task.ID && str(row[claimField]) == version.NodeID && (blocked != "" || reason == "output_binding_changed") {
		// A restored row may still carry the original pending marker. Release
		// only that marker; never apply the result into its changed ownership.
		delete(row, "taskId")
		delete(row, "taskStatus")
		delete(row, "errorDetails")
		row["status"] = "idle"
		if node != nil && mediaversions.ReadState(node).CurrentVersionID != "" {
			row["status"] = "success"
		}
	} else if row != nil && str(row["taskId"]) == task.ID && blocked == "" {
		row["taskStatus"] = string(task.Status)
		row["status"] = resultStatus(task.Status)
		if task.Status != model.TaskStatusSucceeded {
			row["errorDetails"] = safeError(task)
		} else {
			delete(row, "errorDetails")
		}
	}
	if reason == "adopted" {
		reason = "applied"
	}
	if reason == "not_adopted" && blocked == "" {
		reason = "applied"
	}
	return doc, reason, nil
}
