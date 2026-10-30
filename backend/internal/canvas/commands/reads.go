package commands

import "yingce/backend/internal/canvas/capability"

func Capabilities() map[string]any {
	registry := capability.BuiltinRegistry()
	nodes := []any{}
	for _, d := range registry.List() {
		fields := map[string]any{}
		for name, field := range d.PatchFields {
			schema := field.JSONSchema()
			schema["description"] = field.Description
			schema["path"] = field.Path
			fields[name] = schema
		}
		node := map[string]any{"type": d.Type, "version": d.Version, "label": d.Label, "purpose": d.Purpose, "defaultSize": map[string]any{"width": d.DefaultWidth, "height": d.DefaultHeight}, "inputKind": d.InputKind, "generationMode": d.GenerationMode, "canUpdate": d.CanUpdate, "patchFields": fields, "creatable": d.Variant == nil}
		if d.Variant != nil {
			node["baseType"], node["workflowKind"] = d.Variant.BaseType, d.Variant.WorkflowKind
		}
		nodes = append(nodes, node)
	}
	structured := map[string]any{}
	previs := map[string]any{}
	for _, kind := range []string{"settings", "object", "camera", "light", "shot"} {
		previs[kind] = previsFields[kind]
	}
	for kind, fields := range map[string]map[string]capability.PatchField{"storyboard": capability.StoryboardRowFields(), "batch": capability.BatchRowFields(), "batchTable": capability.BatchTableFields()} {
		schemas := map[string]any{}
		for key, field := range fields {
			schemas[key] = field.JSONSchema()
		}
		structured[kind] = schemas
	}
	return map[string]any{"protocolVersion": ProtocolVersion, "capabilityVersion": capability.SetVersion, "operations": append([]string{}, OperationTypes...), "nodes": nodes, "structuredFields": structured, "timelineFields": map[string]any{"track": timelineTrackFields, "clip": timelineClipFields, "media": timelineMediaFields}, "previsFields": previs, "personalState": []string{"viewport", "selection", "panelState", "presence"}}
}
