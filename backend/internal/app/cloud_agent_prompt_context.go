package app

import "encoding/json"

const (
	cloudAgentMaxOutputBytes       = 32000
	cloudAgentRuntimeContextMarker = "【运行状态】"
	cloudAgentContextSourceKey     = "agentContextSource"
)

type cloudAgentRuntimeContextKind string

const (
	cloudAgentContextPlan               cloudAgentRuntimeContextKind = "plan_state"
	cloudAgentContextCompletionBlocked  cloudAgentRuntimeContextKind = "completion_blocked"
	cloudAgentContextEmptyOutput        cloudAgentRuntimeContextKind = "empty_output"
	cloudAgentContextInvalidOutput      cloudAgentRuntimeContextKind = "invalid_output"
	cloudAgentContextTruncatedArguments cloudAgentRuntimeContextKind = "truncated_tool_arguments"
	cloudAgentContextStreamStalled      cloudAgentRuntimeContextKind = "stream_stalled"
	cloudAgentRuntimeCallLimits         cloudAgentRuntimeContextKind = "call_limits"
	cloudAgentRuntimeCanvasReferences   cloudAgentRuntimeContextKind = "canvas_references"
)

// Only serializable fact fields belong here. Behavioral instructions live in
// prompts/agent-system-policy.md and share its version/hash contract.
type cloudAgentRuntimeContext struct {
	Source              string                       `json:"source"`
	Kind                cloudAgentRuntimeContextKind `json:"kind"`
	Items               []cloudAgentPlanItem         `json:"items,omitempty"`
	Detail              string                       `json:"detail,omitempty"`
	MaxToolCalls        int                          `json:"maxToolCalls,omitempty"`
	MaxOutputBytes      int                          `json:"maxOutputBytes,omitempty"`
	CallLimits          *cloudAgentCallLimits        `json:"callLimits,omitempty"`
	ReadToolCalls       int                          `json:"readToolCalls,omitempty"`
	ReferenceCandidates []cloudAgentReferenceAnchor  `json:"referenceCandidates,omitempty"`
}

func cloudAgentRuntimeMessage(context cloudAgentRuntimeContext) map[string]any {
	context.Source = "runtime"
	encoded, _ := json.Marshal(context)
	return map[string]any{
		"role": "user", "content": cloudAgentRuntimeContextMarker + string(encoded),
		cloudAgentContextSourceKey: "runtime",
	}
}

func (anchor cloudAgentCreativeAnchor) appendReferenceContext(messages []map[string]any) []map[string]any {
	if anchor.Version < cloudAgentRuntimeReferenceAnchorVersion || len(anchor.ReferenceAssets) == 0 {
		return messages
	}
	return append(messages[:len(messages):len(messages)], cloudAgentRuntimeMessage(cloudAgentRuntimeContext{
		Kind: cloudAgentRuntimeCanvasReferences, ReferenceCandidates: anchor.ReferenceAssets,
	}))
}

func cloudAgentLatestUserInstruction(messages []map[string]any) string {
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		source := stringField(message, cloudAgentContextSourceKey)
		if stringField(message, "role") == "user" && (source == "" || source == "user_interjection") {
			return stringField(message, "content")
		}
	}
	return ""
}
