package app

import (
	"testing"

	"infinite-canvas/backend/internal/platform"
)

// cloudAgentStepMaxOutputTokens 是"单步输出的出厂默认上限"，原先是我们在
// cloud_agent_runtime.go 里导出的常量；该文件本轮整体取上游，这里按同一来源
// （platform.DefaultRuntimeAgentStepOutputTokens）在测试内重建，供 provider 相关用例引用。
const cloudAgentStepMaxOutputTokens = platform.DefaultRuntimeAgentStepOutputTokens

// 输出上限必须"分层取小"：策略上限（管理端/每步预算）与模型能力上限语义不同，
// 谁先谁赢会让能力值悄悄吃掉策略配置（能力 16384 时管理端设 131072 或 0 都失效）。
func TestCloudAgentOutputTokensLayersPolicyAndCapability(t *testing.T) {
	cases := []struct {
		name  string
		input canvasGenerationInput
		want  int
	}{
		{"只有策略上限（画布 Agent 每步下发）", canvasGenerationInput{TextOptions: canvasTextOptions{MaxOutputTokens: 131072}}, 131072},
		{"只有任务级显式上限", canvasGenerationInput{MaxOutputTokens: 32768}, 32768},
		{"只有能力上限", canvasGenerationInput{CapabilityMaxOutputTokens: 16384}, 16384},
		{"策略大于能力时取能力", canvasGenerationInput{TextOptions: canvasTextOptions{MaxOutputTokens: 131072}, CapabilityMaxOutputTokens: 16384}, 16384},
		{"能力大于策略时取策略", canvasGenerationInput{TextOptions: canvasTextOptions{MaxOutputTokens: 8192}, CapabilityMaxOutputTokens: 16384}, 8192},
		{"策略为 0（不限制）时用能力上限", canvasGenerationInput{TextOptions: canvasTextOptions{MaxOutputTokens: 0}, CapabilityMaxOutputTokens: 16384}, 16384},
		{"两侧都不声明则不限制", canvasGenerationInput{}, 0},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			if got := cloudAgentOutputTokens(item.input); got != item.want {
				t.Fatalf("cloudAgentOutputTokens() = %d，期望 %d", got, item.want)
			}
		})
	}
}

// 能力解析后只写 CapabilityMaxOutputTokens，绝不覆盖策略字段。
func TestResolveProviderConfigKeepsPolicyOutputLimit(t *testing.T) {
	input := canvasGenerationInput{
		Mode: "text",
		Config: providerConfig{CapabilityConfig: &ModelCapabilityConfig{
			Version: 1,
			Text:    &TextCapabilityConfig{ContextWindowTokens: 128000, MaxOutputTokens: 16384},
		}},
		TextOptions: canvasTextOptions{MaxOutputTokens: 131072},
	}
	capability := input.Config.CapabilityConfig.Text.MaxOutputTokens
	input.CapabilityMaxOutputTokens = capability
	if input.TextOptions.MaxOutputTokens != 131072 || input.MaxOutputTokens != 0 {
		t.Fatalf("策略字段被能力值覆盖：%+v", input)
	}
	if got := cloudAgentOutputTokens(input); got != 16384 {
		t.Fatalf("实际生效上限 = %d，期望 16384（能力上限生效但不改写策略）", got)
	}
}
