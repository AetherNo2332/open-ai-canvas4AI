package skills

import (
	"strings"

	"infinite-canvas/backend/internal/kernel"
)

const (
	SkillSourceGlobal     = "global"
	SkillSourceWorkspace  = "workspace"
	SkillSourceCrewMember = "crew_member"
	SkillSourceUser       = "user"
)

// SkillSelectionItem 是一次运行中某个技能被选中时冻结的三元组（版本 + 内容摘要 + 来源层）。
type SkillSelectionItem struct {
	SkillID     string
	VersionID   string
	ContentHash string
	Source      string
}

// SkillCapacityFacts 汇总单个技能包在运行时的占用事实，由调用方从冻结的文件清单统计。
type SkillCapacityFacts struct {
	SkillID      string
	FileCount    int
	TotalBytes   int64
	ContextBytes int64
}

// SkillCapacityBudgets 是一次运行允许的技能集合总量上限。
type SkillCapacityBudgets struct {
	MaxFiles        int
	MaxTotalBytes   int64
	MaxContextBytes int64
}

const (
	// 单包上限 512 文件 / 20MB 的整数倍余量，覆盖一次运行可挂载的技能集合总量。
	SkillRunMaxFiles      = 1024
	SkillRunMaxTotalBytes = 16 << 20
	// 对 spec 的显式近似：按 piNativeSkillReadMaxRunes=12000 rune ≈ 48KB/技能 × 约 10 技能取整。
	// v1 使用固定运行级上下文预算替代"按当前模型可用输入预算检查"，per-model 预算随 P2 Workspace 计划补齐。
	SkillRunMaxContextBytes = 512 << 10
)

// ResolveSkillSelection 将各层技能选择按优先级从低到高（global → 后续层 → user）合并：
// 同一 SkillID 保留最高优先层的条目；输出顺序稳定——最低优先层的原始顺序在前，各高层新增条目按出现顺序在后。
func ResolveSkillSelection(layers ...[]SkillSelectionItem) ([]SkillSelectionItem, error) {
	resolved := make([]SkillSelectionItem, 0, len(layers)*4)
	positions := make(map[string]int, len(layers)*4)
	for _, layer := range layers {
		for _, item := range layer {
			skillID := strings.TrimSpace(item.SkillID)
			if skillID == "" {
				return nil, kernel.BadAuthRequest("技能选择包含空 ID")
			}
			item.SkillID = skillID
			if position, exists := positions[skillID]; exists {
				resolved[position] = item
				continue
			}
			positions[skillID] = len(resolved)
			resolved = append(resolved, item)
		}
	}
	return resolved, nil
}

// AdmitSkillCapacity 校验技能集合的三项总量预算，超限时返回携带 budget/limit/actual 明细的预算错误；恰好等于限额视为通过。
func AdmitSkillCapacity(facts []SkillCapacityFacts, budgets SkillCapacityBudgets) error {
	var fileCount int
	var totalBytes int64
	var contextBytes int64
	for _, fact := range facts {
		fileCount += fact.FileCount
		totalBytes += fact.TotalBytes
		contextBytes += fact.ContextBytes
	}
	if fileCount > budgets.MaxFiles {
		return kernel.AgentSkillBudgetExceeded(map[string]any{"budget": "files", "limit": budgets.MaxFiles, "actual": fileCount})
	}
	if totalBytes > budgets.MaxTotalBytes {
		return kernel.AgentSkillBudgetExceeded(map[string]any{"budget": "total_bytes", "limit": budgets.MaxTotalBytes, "actual": totalBytes})
	}
	if contextBytes > budgets.MaxContextBytes {
		return kernel.AgentSkillBudgetExceeded(map[string]any{"budget": "context_bytes", "limit": budgets.MaxContextBytes, "actual": contextBytes})
	}
	return nil
}
