package app

import "testing"

// rebuildFrozenCapabilities 在夹具改动权限、Skill 授权或工具目录后重算冻结目录身份：
// 目录版本、hash、披露清单和 canonical 工具表必须一起更新，否则运行会被判为目录已变化。
func rebuildFrozenCapabilities(t *testing.T, s *Service, state *cloudAgentRuntime, hasSkills bool) {
	t.Helper()
	definition, err := s.ResolveAgentDefinition(agentBusinessScope(state.Request).Type, "")
	if err != nil {
		t.Fatal(err)
	}
	capabilities, tools, err := compileAgentRunCapabilityDirectory(definition, state.Request, hasSkills)
	if err != nil {
		t.Fatal(err)
	}
	capabilities.DirectoryVersion = agentCapabilityDirectoryVersion
	if capabilities.DirectoryHash, err = agentCapabilityDirectoryHash(capabilities, tools); err != nil {
		t.Fatal(err)
	}
	state.Capabilities = &capabilities
	state.Canonical.Tools = tools
	state.DisclosureVersion = cloudAgentToolDisclosureVersion
	state.AdvertisedToolNames = cloudAgentSelectAdvertisedTools(tools)
}

// resealFrozenDirectoryHash 在夹具直接改写 canonical 工具目录（例如模拟已退役工具仍留在
// 冻结目录中）后重算 hash，让目录内容身份与工具表保持一致。
func resealFrozenDirectoryHash(t *testing.T, state *cloudAgentRuntime) {
	t.Helper()
	if state.Capabilities == nil {
		t.Fatal("fixture has no frozen capability directory")
	}
	hash, err := agentCapabilityDirectoryHash(*state.Capabilities, state.Canonical.Tools)
	if err != nil {
		t.Fatal(err)
	}
	state.Capabilities.DirectoryHash = hash
}
