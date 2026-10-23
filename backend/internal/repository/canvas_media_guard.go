package repository

import (
	"errors"
	"infinite-canvas/backend/internal/model"
	"reflect"
)

var ErrCanvasMediaVersionManaged = errors.New("媒体由版本记录管理，请通过版本命令更改")

func mediaString(value any) string { s, _ := value.(string); return s }

func guardLegacyMediaVersions(before, after map[string]any) error {
	prior := map[string]map[string]any{}
	if before != nil {
		if nodes, ok := before["nodes"].([]any); ok {
			for _, value := range nodes {
				if node, ok := value.(map[string]any); ok {
					prior[mediaString(node["id"])] = node
				}
			}
		}
	}
	nodes, _ := after["nodes"].([]any)
	for _, value := range nodes {
		node, ok := value.(map[string]any)
		if !ok {
			continue
		}
		old := prior[mediaString(node["id"])]
		meta, _ := node["metadata"].(map[string]any)
		if old == nil {
			if meta["mediaVersion"] != nil {
				return ErrCanvasMediaVersionManaged
			}
			continue
		}
		oldMeta, _ := old["metadata"].(map[string]any)
		if oldMeta["mediaVersion"] != nil && !reflect.DeepEqual(model.CanvasMediaProjection(old), model.CanvasMediaProjection(node)) {
			return ErrCanvasMediaVersionManaged
		}
	}
	return nil
}
