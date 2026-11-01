package app

// 工具清单在一轮开始时就是完整的合格目录，运行中不再按提示词关键词或画布内容增减。
// 合格目录仍按权限模式、画布上下文、识图、记忆、联网和子代理身份编译（compileCloudAgentTools），
// AdvertisedToolNames 是对它的冻结记录：preflight 只放行已下发的工具，Pi worker 只注册已下发的工具。
// 每步刷新按当前权限重算：保持已冻结的顺序，剔除不再允许的工具；运行中放宽权限时
// （cloudAgentExtendToolCatalog 已把新工具补进运行目录）在末尾追加目录里现在允许的工具。

func cloudAgentSelectAdvertisedTools(eligible []map[string]any) []string {
	return cloudAgentToolNames(eligible)
}

func cloudAgentAdvertisedToolNames(req CloudAgentRequest) []string {
	return cloudAgentSelectAdvertisedTools(cloudAgentTools(req))
}

// cloudAgentNextAdvertisedToolNames 返回下一步下发的工具：已冻结的工具保持原顺序并去掉当前不允许的，
// 运行目录里其余当前允许的工具追加在后面。进行中的模型步或工具批次沿用已准入的清单，不做任何调整。
func cloudAgentNextAdvertisedToolNames(state *cloudAgentRuntime) []string {
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
		if name == "read" && state.SkillRuntimeMode == cloudAgentSkillRuntimeNative || cloudAgentToolAllowed(state.Request, name) {
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
	if len(state.AdvertisedToolNames) == 0 {
		return true
	}
	for _, advertised := range state.AdvertisedToolNames {
		if advertised == name {
			return true
		}
	}
	return false
}
