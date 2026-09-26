package app

import "strings"

const cloudAgentToolDisclosureVersion = 1

// A category is named after its parent tool. Only one eligible child category
// is sent on a normal model step; the complete catalog stays server-side.
func cloudAgentToolCategory(name string) string {
	switch name {
	case "plan_update", "ask_user", "finish_run", "task_get":
		return "agent_tools_control"
	case "agent_profile_read", "recall_lessons", "remember_lesson":
		return "agent_tools_memory"
	case "skill_search", "skill_read_file":
		return "agent_tools_skills"
	case "canvas_list_node_types", "canvas_get_state", "canvas_read_batch_table", "canvas_read_storyboard":
		return "agent_tools_canvas_read"
	case "image_text_detect", "image_annotation_render", "canvas_inspect_image":
		return "agent_tools_image"
	case "canvas_create_storyboard", "canvas_edit_storyboard", "canvas_edit_batch_table", "canvas_apply_ops", "canvas_arrange_nodes":
		return "agent_tools_canvas_edit"
	case "model_list", "generate_media", "image_layer_split":
		return "agent_tools_generation"
	default:
		return ""
	}
}

func cloudAgentIsToolCategory(name string) bool {
	switch name {
	case "agent_tools_control", "agent_tools_memory", "agent_tools_skills", "agent_tools_canvas_read", "agent_tools_image", "agent_tools_canvas_edit", "agent_tools_generation":
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

// The parent schema records only tool names from its own category on the
// preceding model step. Arguments and results remain in the transcript.
func cloudAgentCategoryCallRecord(calls []cloudAgentCall, category string) string {
	names := []string{}
	seen := map[string]bool{}
	for _, call := range calls {
		name := call.Function.Name
		if name != category && cloudAgentToolCategory(name) != category {
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		if len(names) < 6 {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	record := strings.Join(names, "、")
	if len(seen) > len(names) {
		record += "等"
	}
	return " " + strings.ReplaceAll(cloudAgentToolText("previous_step_calls"), "{names}", record)
}

// The existing repair circuit can temporarily replace the category view with
// its smaller corrective tool set. It never adds a tool outside the catalog.
func cloudAgentVisibleTools(all []map[string]any, selected string, previous []cloudAgentCall, repairScope []string) []map[string]any {
	return cloudAgentVisibleToolsForCategories(all, []string{selected}, previous, repairScope)
}

// Opened categories remain available for the rest of this Agent run. A new run
// starts with an empty set and therefore advertises only eligible parents.
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

func cloudAgentVisibleToolsForCategories(all []map[string]any, categories []string, previous []cloudAgentCall, repairScope []string) []map[string]any {
	repairAllowed := map[string]bool{}
	for _, name := range repairScope {
		repairAllowed[name] = true
	}
	active := map[string]bool{}
	for _, category := range categories {
		if cloudAgentIsToolCategory(category) {
			active[category] = true
		}
	}
	visible := make([]map[string]any, 0, len(all))
	for _, tool := range all {
		function, _ := tool["function"].(map[string]any)
		name := stringField(function, "name")
		if !cloudAgentIsToolCategory(name) {
			continue
		}
		if len(repairScope) > 0 {
			allowed := false
			for _, child := range cloudAgentCategoryChildren(all, name) {
				if repairAllowed[child] {
					allowed = true
					break
				}
			}
			if !allowed {
				continue
			}
		}
		copyFunction := cloneStringAnyMap(function)
		copyFunction["description"] = stringField(function, "description") + cloudAgentCategoryCallRecord(previous, name)
		visible = append(visible, map[string]any{"type": "function", "function": copyFunction})
	}
	for _, tool := range all {
		function, _ := tool["function"].(map[string]any)
		name := stringField(function, "name")
		if active[cloudAgentToolCategory(name)] && (len(repairScope) == 0 || repairAllowed[name]) {
			visible = append(visible, tool)
		}
	}
	return visible
}
