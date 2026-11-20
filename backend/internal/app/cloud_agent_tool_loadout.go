package app

// 工具清单在一轮开始时就是完整的合格目录，运行中不再按提示词关键词或画布内容增减。
// AdvertisedToolNames 记录模型步的实际目录；下一步的权限变更只在冻结目录内生效。

func cloudAgentSelectAdvertisedTools(eligible []map[string]any) []string {
	return cloudAgentToolNames(eligible)
}

func cloudAgentAdvertisedToolNames(req CloudAgentRequest) []string {
	return cloudAgentSelectAdvertisedTools(cloudAgentTools(req))
}

// cloudAgentNextAdvertisedToolNames 返回下一步下发的工具：已冻结的工具保持原顺序并去掉当前不允许的，
// 运行目录里其余当前允许的工具追加在后面。进行中的模型步或工具批次沿用已准入的清单，不做任何调整。
func cloudAgentNextAdvertisedToolNames(state *cloudAgentRuntime) []string {
	if state.Capabilities != nil && state.Capabilities.DirectoryVersion == agentCapabilityDirectoryVersion {
		if state.ActiveTaskID != "" || state.CallIndex < len(state.Calls) {
			return append([]string(nil), state.AdvertisedToolNames...)
		}
		names := make([]string, 0, len(state.Canonical.Tools))
		for _, name := range cloudAgentToolNames(state.Canonical.Tools) {
			if cloudAgentRunAllowsTool(state, name) {
				names = append(names, name)
			}
		}
		return names
	}
	if len(state.AdvertisedToolNames) == 0 {
		return cloudAgentToolNames(state.Canonical.Tools)
	}
	if state.ActiveTaskID != "" || state.CallIndex < len(state.Calls) {
		return append([]string(nil), state.AdvertisedToolNames...)
	}
	eligible := map[string]bool{}
	for _, name := range cloudAgentToolNames(state.Canonical.Tools) {
		eligible[name] = true
	}
	names := make([]string, 0, len(eligible))
	added := map[string]bool{}
	for _, name := range append(append([]string(nil), state.AdvertisedToolNames...), cloudAgentToolNames(state.Canonical.Tools)...) {
		if added[name] || !eligible[name] {
			continue
		}
		if cloudAgentRunAllowsTool(state, name) {
			names = append(names, name)
			added[name] = true
		}
	}
	return names
}

func cloudAgentToolsNamed(all []map[string]any, names []string) []map[string]any {
	set := map[string]bool{}
	for _, name := range names {
		if name != "" {
			set[name] = true
		}
	}
	out := make([]map[string]any, 0, len(set))
	for _, tool := range all {
		function, _ := tool["function"].(map[string]any)
		if set[stringField(function, "name")] {
			out = append(out, tool)
		}
	}
	return out
}

func cloudAgentToolAdvertisedForRun(state *cloudAgentRuntime, name string) bool {
	if state == nil || name == "" {
		return false
	}
	if len(state.AdvertisedToolNames) == 0 && (state.Capabilities == nil || state.Capabilities.DirectoryVersion == 0) {
		return true
	}
	for _, advertised := range state.AdvertisedToolNames {
		if advertised == name {
			return true
		}
	}
	return false
}
