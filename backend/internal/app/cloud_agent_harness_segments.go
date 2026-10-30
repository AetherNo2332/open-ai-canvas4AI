package app

import "strings"

// cloudAgentWorkspaceSegmentMarker 是 Node 在系统提示末尾拼接工作区/Harness 文件时的段标题
// （见 agent/src/system-prompt.ts 的 renderSystemPrompt，格式固定为 "\n\n## Workspace <name>\n"）。
const cloudAgentWorkspaceSegmentMarker = "\n\n## Workspace "

// cloudAgentRecordHarnessSegments 把 Node 拼在系统提示末尾的 Harness 工作区文件登记为
// 独立分段；不单独登记就落进"其它"，占用面板看不出那一大截到底是什么。只登记确实出现在
// 这份信封里的块，被装配层跳过的文件不会被凭空计量。
func cloudAgentRecordHarnessSegments(policy *cloudAgentPolicySnapshot, parts *cloudAgentHarnessSnapshot, system string) {
	if policy == nil || parts == nil {
		return
	}
	for _, entry := range parts.Context {
		if strings.TrimSpace(entry.Name) == "" || strings.TrimSpace(entry.Text) == "" {
			continue
		}
		block := cloudAgentWorkspaceSegmentMarker + entry.Name + "\n" + entry.Text
		if !strings.Contains(system, block) {
			continue
		}
		cloudAgentRecordSystemSegment(policy, "workspace:"+entry.Name, "Workspace "+entry.Name, block)
	}
}
