package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"infinite-canvas/backend/internal/prompts"
)

const (
	cloudAgentCompilerVersion  = "cloud-agent-policy-compiler/v4"
	cloudAgentDefaultReasoning = "off"
)

type cloudAgentPolicySnapshot struct {
	SystemPolicyID       string `json:"systemPolicyId"`
	SystemPolicyVersion  int    `json:"systemPolicyVersion"`
	SystemPolicyHash     string `json:"systemPolicyHash"`
	MediaPolicyID        string `json:"mediaPolicyId"`
	MediaPolicyVersion   int    `json:"mediaPolicyVersion"`
	MediaPolicyHash      string `json:"mediaPolicyHash"`
	CapabilitySetVersion string `json:"capabilitySetVersion"`
	CapabilitySetHash    string `json:"capabilitySetHash"`
	ReasoningMode        string `json:"reasoningMode"`
	CompilerVersion      string `json:"compilerVersion"`
	ProfileRevision      string `json:"profileRevision,omitempty"`
	ProfileHash          string `json:"profileHash,omitempty"`
	// SystemSegments 记录编译出来的系统提示由哪些块组成。压力读数把它摊开上报，
	// 排查时不必再从提示词正文反推"系统提示里谁是画布摘要、谁是个人记忆"。
	SystemSegments []cloudAgentContextSegment `json:"systemSegments,omitempty"`
}

// cloudAgentRecordSystemSegment 登记"编译之后"追加进系统提示的块（当前是个人记忆索引），
// 让计量器的系统提示分段合计与实际 system 桶一致；同 key 重复调用只保留最新一次，
// 因此它既能在创建运行登记，也能在每一步幂等补登记。
// （上游还把同一函数抄了一份到本文件下方，合并后只留这一处，避免重复定义。）
func cloudAgentRecordSystemSegment(policy *cloudAgentPolicySnapshot, key, label, text string) {
	if policy == nil || strings.TrimSpace(text) == "" {
		return
	}
	segment := cloudAgentContextSegment{Key: key, Label: label, Bytes: len(text), Tokens: estimateCloudAgentTokens([]byte(text))}
	for index := range policy.SystemSegments {
		if policy.SystemSegments[index].Key == key {
			policy.SystemSegments[index] = segment
			return
		}
	}
	policy.SystemSegments = append(policy.SystemSegments, segment)
}

// cloudAgentContextSegment 是系统提示里的一块（编译期按写入顺序量出来的）。
type cloudAgentContextSegment struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Bytes  int    `json:"bytes"`
	Tokens int    `json:"tokens"`
	// ScaledTokens 是按上游实测锚点比例校准后的读数；没有锚点时与 Tokens 相同
	// （编译期只记 Tokens，换算在压力读数里做，所以录制阶段它保持 0）。
	ScaledTokens int `json:"scaledTokens,omitempty"`
}

// cloudAgentSegmentRecorder 在编译系统提示时逐块量尺寸：它只记录"上一次标记之后追加了
// 多少字节"，因此分段合计与最终 system 正文逐字节一致（含块之间的标题与分隔符）。
type cloudAgentSegmentRecorder struct {
	segments []cloudAgentContextSegment
	last     int
}

func (r *cloudAgentSegmentRecorder) mark(builder *strings.Builder, key, label string) {
	end := builder.Len()
	if size := end - r.last; size > 0 {
		text := builder.String()[r.last:end]
		r.segments = append(r.segments, cloudAgentContextSegment{Key: key, Label: label, Bytes: size, Tokens: estimateCloudAgentTokens([]byte(text))})
	}
	r.last = end
}

type cloudAgentProfileSnapshot struct {
	Revision string              `json:"revision"`
	Hash     string              `json:"hash"`
	Layers   []AgentProfileLayer `json:"layers"`
}

func cloudAgentReasoningMode(req CloudAgentRequest) string {
	mode := strings.ToLower(strings.TrimSpace(req.ReasoningMode))
	if mode == "off" || mode == "auto" || mode == "deep" {
		return mode
	}
	return cloudAgentDefaultReasoning
}

func cloudAgentReasoningEnabled(mode string) bool { return mode == "auto" || mode == "deep" }

func cloudAgentCapabilityGuide() string {
	var b strings.Builder
	b.WriteString("节点能力速查（由服务端能力注册表生成，只用于自主路由，不是工具授权）：\n")
	for _, descriptor := range canvasCapabilityRegistry.List() {
		b.WriteString("- ")
		b.WriteString(descriptor.Label)
		b.WriteString("（")
		b.WriteString(descriptor.Type)
		b.WriteString("）：")
		b.WriteString(descriptor.Purpose)
		if len(descriptor.GoodFor) > 0 {
			b.WriteString(" 适合：")
			b.WriteString(strings.Join(descriptor.GoodFor, "、"))
			b.WriteString("。")
		}
		if len(descriptor.NotIdealFor) > 0 {
			b.WriteString(" 不适合：")
			b.WriteString(strings.Join(descriptor.NotIdealFor, "、"))
			b.WriteString("。")
		}
		if len(descriptor.Tradeoffs) > 0 {
			b.WriteString(" 维护取舍：")
			b.WriteString(strings.Join(descriptor.Tradeoffs, "；"))
			b.WriteString("。")
		}
		connection, _ := json.Marshal(map[string]any{
			"inputKind": descriptor.InputKind, "canSource": descriptor.Connection.CanSource,
			"canTarget": descriptor.Connection.CanTarget, "canReference": descriptor.Connection.CanReference,
			"acceptedInputKinds": descriptor.Connection.AcceptedInputKinds,
			"rejectedInputKinds": descriptor.Connection.RejectedInputKinds,
			"maxInputCount":      descriptor.Connection.MaxInputCount,
		})
		b.WriteString(" 连线能力：")
		b.Write(connection)
		b.WriteString("\n")
	}
	return b.String()
}

// cloudAgentSkillFileLimit bounds the inlined file list. The list is a routing
// aid, not a permission: skill_read_file lists the full directory on demand.
const cloudAgentSkillFileLimit = 60

// cloudAgentSkillManifestDescription bounds the system prompt: the description
// is author-supplied public metadata, so it is trimmed and capped before it
// enters the compiled policy.
func cloudAgentSkillManifestDescription(description string) string {
	const maxRunes = 500
	trimmed := strings.TrimSpace(description)
	if utf8.RuneCountInString(trimmed) <= maxRunes {
		return trimmed
	}
	runes := []rune(trimmed)
	return strings.TrimSpace(string(runes[:maxRunes])) + "…"
}

func cloudAgentSkillManifest(skill cloudAgentSkill) map[string]any {
	paths := cloudAgentSkillPaths(skill)
	manifest := map[string]any{"skillId": skill.ID, "name": skill.Name, "description": cloudAgentSkillManifestDescription(skill.Description), "version": skill.Version, "hash": skill.Hash, "entryPath": cloudAgentSkillEntryPath}
	if omitted := len(paths) - cloudAgentSkillFileLimit; omitted > 0 {
		manifest["files"] = paths[:cloudAgentSkillFileLimit]
		manifest["filesOmitted"] = omitted
		manifest["filesHint"] = "清单已截断；用 skill_read_file 传空 path 列出完整文件"
		return manifest
	}
	manifest["files"] = paths
	return manifest
}

func compileCloudAgentPolicies(req CloudAgentRequest, skills []cloudAgentSkill, canvasSummary string, profile cloudAgentProfileSnapshot, anchors ...cloudAgentCreativeAnchor) (string, cloudAgentPolicySnapshot, error) {
	system, media, err := prompts.LoadAgentPolicies()
	if err != nil {
		return "", cloudAgentPolicySnapshot{}, err
	}
	mode := cloudAgentReasoningMode(req)
	capabilityHash := cloudAgentCapabilitySetHash()
	snapshot := cloudAgentPolicySnapshot{
		SystemPolicyID: system.ID, SystemPolicyVersion: system.Version, SystemPolicyHash: system.Hash,
		MediaPolicyID: media.ID, MediaPolicyVersion: media.Version, MediaPolicyHash: media.Hash,
		CapabilitySetVersion: cloudAgentCapabilitySetVersion, CapabilitySetHash: capabilityHash,
		ReasoningMode: mode, CompilerVersion: cloudAgentCompilerVersion,
		ProfileRevision: profile.Revision, ProfileHash: profile.Hash,
	}
	var b strings.Builder
	var recorder cloudAgentSegmentRecorder
	b.WriteString(system.Text)
	b.WriteString("\n\n")
	recorder.mark(&b, "policy", "系统行为策略")
	b.WriteString(media.Text)
	b.WriteString("\n\n")
	recorder.mark(&b, "mediaPolicy", "媒体策略")
	// 能力指南是一个"工具回执"而不是系统提示常量：它在每一步、每一轮都会被重发。只要
	// canvas_list_node_types 不可用就内联全文——没有画布上下文，或只读运行（只读不会创建
	// 节点，工具本身也不暴露）；否则提示会指向一个不存在的工具。
	// 保留我方的裁剪版（能力指南静态瘦身）：上游此处是无条件内联全文，会让每步都白背一份
	// 工具清单，而 canvas_list_node_types 在可写画布场景下本来就能按需取权威清单。
	if len(req.ContextScope) == 0 || req.PermissionMode == "read_only" {
		b.WriteString(cloudAgentCapabilityGuide())
	} else {
		b.WriteString("节点能力与选型：需要节点类型、默认尺寸、连接约束、适用场景和维护代价时调用 canvas_list_node_types 获取权威清单，不要凭记忆猜测 nodeType；由你按任务复杂度自主选择，不为形式强制使用任何节点——单画面、一次性说明或快速试验优先轻量节点，多镜头、镜头连续性、逐镜审查/生成或后续维护优先评估分镜脚本，普通文本或 Markdown 不能伪装成结构化分镜。\n")
	}
	recorder.mark(&b, "capabilities", "节点能力与选型")
	// Behavior belongs to versioned policies; the compiler only projects facts.
	context := map[string]any{
		"source": "server_snapshot", "permissionMode": req.PermissionMode,
		"reasoningMode": mode,
		"budget": map[string]any{
			"maxCredits": req.Budget.MaxCredits, "maxSteps": cloudAgentStepLimit(req),
			"maxGenerationTasks": req.Budget.MaxGenerationTasks, "maxVideoSeconds": req.Budget.MaxVideoSeconds,
		},
		"maxToolCalls": cloudAgentMaxToolCalls, "maxOutputBytes": cloudAgentMaxOutputBytes,
	}
	if strings.TrimSpace(canvasSummary) != "" {
		// Callers may provide a catalog for policy-contract tests or other
		// isolated compilation paths. The production run path deliberately
		// passes an empty value and places the catalog in canonical messages so
		// the stable system prefix remains cacheable.
		context["canvasSummary"] = canvasSummary
	}
	if len(anchors) > 0 {
		// User intent remains in user messages, never frozen into system context.
		context["referenceCandidates"] = anchors[0].ReferenceAssets
	}
	manifests := make([]map[string]any, 0, len(skills))
	for _, skill := range skills {
		manifests = append(manifests, cloudAgentSkillManifest(skill))
	}
	context["skills"] = manifests
	layers := make([]map[string]any, 0, len(profile.Layers))
	for _, layer := range profile.Layers {
		layers = append(layers, map[string]any{"scope": layer.Scope, "revision": layer.Revision, "hash": layer.Hash, "characters": utf8.RuneCountInString(layer.Content)})
	}
	context["profileLayers"] = layers
	encoded, err := json.Marshal(context)
	if err != nil {
		return "", cloudAgentPolicySnapshot{}, fmt.Errorf("encode Agent execution context: %w", err)
	}
	b.WriteString("\n\n本轮执行上下文：\n")
	b.Write(encoded)
	recorder.mark(&b, "execution", "本轮执行上下文（事实快照）")
	snapshot.SystemSegments = recorder.segments
	text := strings.TrimSpace(b.String())
	if text == "" {
		return "", cloudAgentPolicySnapshot{}, fmt.Errorf("compiled Agent policy is empty")
	}
	return text, snapshot, nil
}
