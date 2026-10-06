package app

const cloudAgentToolDisclosureVersion = 2

// Categories classify concrete tools for policy, telemetry, and UI grouping.
// They are never model-callable tools: every eligible concrete tool is advertised
// at the beginning of each Pi model step.
func cloudAgentToolCategory(name string) string {
	switch name {
	case "web_search":
		return "agent_tools_web"
	case "plan_update", "ask_user", "finish_run", "task_get":
		return "agent_tools_control"
	case "agent_profile_read", "recall_lessons", "remember_lesson":
		return "agent_tools_memory"
	case "skill_search", "skill_read_file":
		return "agent_tools_skills"
	case "read":
		return "native_skill"
	case "canvas_list_node_types", "canvas_get_state", "canvas_read_batch_table", "canvas_read_storyboard", "previs_scene_read":
		return "agent_tools_canvas_read"
	case "image_text_detect", "image_annotation_render", "canvas_inspect_image":
		return "agent_tools_image"
	case "canvas_create_storyboard", "canvas_edit_storyboard", "canvas_edit_batch_table", "canvas_apply_ops", "canvas_arrange_nodes", "canvas_create_character", "previs_scene_create", "previs_apply_patch", "previs_preview":
		return "agent_tools_canvas_edit"
	case "model_list", "generate_media", "image_layer_split":
		return "agent_tools_generation"
	default:
		return ""
	}
}

func cloudAgentIsToolCategory(name string) bool {
	switch name {
	case "agent_tools_web", "agent_tools_control", "agent_tools_memory", "agent_tools_skills", "agent_tools_canvas_read", "agent_tools_image", "agent_tools_canvas_edit", "agent_tools_generation":
		return true
	default:
		return false
	}
}

func cloudAgentToolNames(tools []map[string]any) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		function, _ := tool["function"].(map[string]any)
		if name := stringField(function, "name"); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func cloudAgentAdvertisedTools(state *cloudAgentRuntime) []map[string]any {
	if state == nil {
		return nil
	}
	if state.DisclosureVersion < cloudAgentToolDisclosureVersion {
		return state.Canonical.Tools
	}
	set := map[string]bool{}
	for _, name := range state.AdvertisedToolNames {
		set[name] = true
	}
	tools := make([]map[string]any, 0, len(set))
	for _, tool := range state.Canonical.Tools {
		function, _ := tool["function"].(map[string]any)
		if set[stringField(function, "name")] {
			tools = append(tools, tool)
		}
	}
	return tools
}

func cloudAgentCategoryChildren(tools []map[string]any, category string) []string {
	names := []string{}
	for _, tool := range tools {
		function, _ := tool["function"].(map[string]any)
		name := stringField(function, "name")
		if cloudAgentToolCategory(name) == category {
			names = append(names, name)
		}
	}
	return names
}

// Legacy arguments remain accepted so old checkpoints can be read, but the
// activated category set no longer controls disclosure. A repair scope may
// still temporarily narrow the concrete tool set.
func cloudAgentVisibleTools(all []map[string]any, selected string, previous []cloudAgentCall, repairScope []string) []map[string]any {
	return cloudAgentVisibleToolsForCategories(all, []string{selected}, previous, repairScope)
}

// Read category state from older checkpoints for compatibility. New Pi runs do
// not use category activation to decide which tools are exposed.
func cloudAgentActivatedCategories(state *cloudAgentRuntime) []string {
	if state == nil {
		return nil
	}
	active := append([]string(nil), state.ActivatedToolCategories...)
	// Checkpoints created before the persistent set had only the selected field.
	return cloudAgentAppendActivatedCategory(active, state.SelectedToolCategory)
}

func cloudAgentAppendActivatedCategory(active []string, category string) []string {
	if !cloudAgentIsToolCategory(category) {
		return active
	}
	for _, existing := range active {
		if existing == category {
			return active
		}
	}
	return append(active, category)
}

func cloudAgentVisibleToolsForCategories(all []map[string]any, categories []string, _ []cloudAgentCall, repairScope []string) []map[string]any {
	_ = categories // kept for callers restoring v1 checkpoints
	repairAllowed := map[string]bool{}
	for _, name := range repairScope {
		repairAllowed[name] = true
	}
	visible := make([]map[string]any, 0, len(all))
	for _, tool := range all {
		function, _ := tool["function"].(map[string]any)
		name := stringField(function, "name")
		if name == "" || cloudAgentIsToolCategory(name) || (len(repairScope) > 0 && !repairAllowed[name]) {
			continue
		}
		visible = append(visible, tool)
	}
	return visible
}
