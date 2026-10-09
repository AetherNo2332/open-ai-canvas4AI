package app

import (
	"errors"
	"strings"
)

// 整批预检（handoff 工作项 B 第 2 步）：模型一次可能返回多个工具调用，而执行是逐个进行的，
// 整批跑完模型才拿到反馈。于是"一个错误方案"会被放大成一批业务失败——取证里同一步 8 个
// 缺 size 的视频调用就是这样来的，而且每一步都可能真的按下了收费按钮。
//
// 预检在这里做三件事（都在**任何业务副作用之前**）：
//  1. 本批每个调用是否在本轮工具表内、是否获授权、参数是否满足本轮下发的 schema；
//  2. 本步最多一个写入/生成调用（只读工具不受限）——避免"一次模型输出批量提交收费任务"；
//  3. 一旦某个写入调用未通过校验，同批**后续**写入调用整批取消，只让模型看到一条关键错误。
//
// 预检结论会随检查点持久化：一次推进只执行一个调用、整批跨越多次转移，每次转移都从
// StateJSON 重新解码，进程内字段活不到下一个调用（与图片缓冲同理）。

// 预检结论的取值：前三个复用工具失败归类的口径，后两个是批次策略结果。
const (
	cloudAgentAdmissionInvalidOutput = "invalid_model_output"
	cloudAgentAdmissionPermission    = "permission_violation"
	cloudAgentAdmissionDuplicateCall = "duplicate_call"
	cloudAgentAdmissionSingleWrite   = "single_write_per_step"
	cloudAgentAdmissionBatchCancel   = "batch_cancelled_after_write_failure"
)

// cloudAgentCallAdmission 是一次工具调用的预检结论。
type cloudAgentCallAdmission struct {
	CallID  string `json:"callId"`
	Allowed bool   `json:"allowed"`
	// Issue 是机器可读的结论，取值是工具失败归类的口径（schema_error /
	// invalid_model_output / permission_violation）或批次策略结论（duplicate_call /
	// single_write_per_step / batch_cancelled_after_write_failure）。
	Issue string `json:"issue,omitempty"`
	// FieldIssue 只在 Issue=schema_error 时有值：本地 schema 校验给出的字段级问题
	// （required / type_mismatch / unknown_field / invalid_value / item_count / invalid_json）。
	FieldIssue string `json:"fieldIssue,omitempty"`
	Field      string `json:"field,omitempty"`
	Message    string `json:"message,omitempty"`
}

// cloudAgentCallAdmissionError 是"预检没放行"的统一错误类型，携带机器可读的结论标签。
// 参数类结论仍走 cloudAgentFieldArgumentError（保留字段级纠错与纠错名额），这个类型只用于
// 批次策略结论（同批第二个写入、前面的写入已失败）与权限/输出类结论，让回执能给出
// reason=call_skipped / admission=<issue>，而不落进"参数不符合契约"里误导模型。
type cloudAgentCallAdmissionError struct {
	error
	Admission string
	Field     string
}

func (e *cloudAgentCallAdmissionError) Unwrap() error { return e.error }

// cloudAgentAdmissionIsSkip 判断这条结论是不是"没执行、但不是模型的参数错"。
// 只有这类结论会被当作 call_skipped（可重发、不消耗纠错名额）。
func cloudAgentAdmissionIsSkip(issue string) bool {
	switch issue {
	case cloudAgentAdmissionSingleWrite, cloudAgentAdmissionBatchCancel, cloudAgentAdmissionDuplicateCall:
		return true
	default:
		return false
	}
}

// cloudAgentAdvertisedTool 在本轮下发给模型的工具表里找同名工具，返回它的参数 schema。
func cloudAgentAdvertisedTool(state *cloudAgentRuntime, name string) (map[string]any, bool) {
	if state == nil || name == "" {
		return nil, false
	}
	if state.DisclosureVersion >= cloudAgentToolDisclosureVersion {
		found := false
		for _, advertised := range state.AdvertisedToolNames {
			if advertised == name {
				found = true
				break
			}
		}
		if !found {
			return nil, false
		}
	}
	for _, tool := range state.Canonical.Tools {
		function, _ := tool["function"].(map[string]any)
		if stringField(function, "name") != name {
			continue
		}
		parameters, _ := function["parameters"].(map[string]any)
		return parameters, true
	}
	return nil, false
}

// cloudAgentPreflightBatch 对本批调用做一次纯函数预检，返回与 calls 等长的结论。
func cloudAgentPreflightBatch(state *cloudAgentRuntime, calls []cloudAgentCall) []cloudAgentCallAdmission {
	admissions := make([]cloudAgentCallAdmission, len(calls))
	if state == nil {
		return admissions
	}
	writeAdmitted := false
	writeFailed := false
	seenCalls := map[string]bool{}
	categoryAdmitted := false
	for index, call := range calls {
		admission := cloudAgentCallAdmission{CallID: call.ID, Allowed: true}
		name := call.Function.Name
		schema, advertised := cloudAgentAdvertisedTool(state, name)
		// 工具表为空（旧检查点或测试构造的状态）时不拿"没下发"当罪证，退回平台工具集判断。
		if !advertised && len(state.Canonical.Tools) == 0 {
			advertised = cloudAgentPlatformToolNames()[name]
		}
		switch {
		case !advertised:
			admission = cloudAgentRejectCall(call, cloudAgentAdmissionInvalidOutput, "", "模型调用了不存在的工具「"+truncateRunes(name, 60)+"」，本步已拒绝；请只使用本轮工具表里列出的工具")
		case !cloudAgentToolInScope(state, name):
			// 上一个同类错误的自动纠错进入收紧档：本步只开放修复所需的工具。
			admission = cloudAgentRejectCall(call, cloudAgentAdmissionInvalidOutput, "", "上一步的同类错误尚未修正，本步只开放修复所需的工具（"+strings.Join(state.ToolScope, "、")+"）；请先用它们改正后再继续")
		case !cloudAgentToolAllowed(state.Request, name):
			admission = cloudAgentRejectCall(call, cloudAgentAdmissionPermission, "", "工具未获本轮权限授权")
		case call.ID != "" && seenCalls[call.ID]:
			admission = cloudAgentRejectCall(call, cloudAgentAdmissionDuplicateCall, "callId", "同一批里 callId 重复：「"+truncateRunes(call.ID, 60)+"」；本步只执行第一次出现的调用")
		case cloudAgentIsToolCategory(name) && categoryAdmitted:
			admission = cloudAgentRejectCall(call, cloudAgentAdmissionInvalidOutput, "", "每个模型步只可打开一种工具类型；请在下一步选择其他类型")
		default:
			if err := validateCloudAgentToolArguments(schema, call.Function.Arguments); err != nil {
				admission = cloudAgentRejectArgumentError(call, err)
			}
		}
		if admission.Allowed && cloudAgentIsToolCategory(name) {
			categoryAdmitted = true
		}
		if call.ID != "" {
			seenCalls[call.ID] = true
		}
		if cloudAgentWrite(name) {
			switch {
			case writeFailed && admission.Allowed:
				admission = cloudAgentSkipCall(call, cloudAgentAdmissionBatchCancel, "同一批里前面的写入/生成调用未通过校验，本调用已跳过；请先修正那条调用的参数，再在下一步单独提交")
			case !admission.Allowed:
				writeFailed = true
			case writeAdmitted:
				admission = cloudAgentSkipCall(call, cloudAgentAdmissionSingleWrite, "本步已经执行了一个写入/生成调用，本调用已跳过；画布写入与生成每步只放行一个，请在下一步提交（并先重新读取画布拿到最新 snapshotHash）")
			default:
				writeAdmitted = true
			}
		}
		admissions[index] = admission
	}
	return admissions
}

func cloudAgentRejectCall(call cloudAgentCall, issue, field, message string) cloudAgentCallAdmission {
	return cloudAgentCallAdmission{CallID: call.ID, Allowed: false, Issue: issue, Field: field, Message: message}
}

// cloudAgentRejectArgumentError 把本地 schema 校验的错误原样转成预检结论：字段级错误
// 保留 field/issue，模型据此知道改哪个字段。
func cloudAgentRejectArgumentError(call cloudAgentCall, err error) cloudAgentCallAdmission {
	admission := cloudAgentCallAdmission{CallID: call.ID, Allowed: false, Issue: "schema_error", Message: cloudAgentSafeToolError(err)}
	var fieldErr *cloudAgentFieldArgumentError
	if errors.As(err, &fieldErr) {
		admission.Field, admission.FieldIssue = fieldErr.Field, fieldErr.Issue
	}
	return admission
}

func cloudAgentSkipCall(call cloudAgentCall, issue, message string) cloudAgentCallAdmission {
	return cloudAgentCallAdmission{CallID: call.ID, Allowed: false, Issue: issue, Message: message}
}

// cloudAgentAdmissionFor 取第 index 个调用的预检结论。旧检查点没有这份数据时返回"放行"：
// 预检只对升级后新建的批次生效，不会让在跑的旧运行卡住。
func cloudAgentAdmissionFor(state *cloudAgentRuntime, index int) (cloudAgentCallAdmission, bool) {
	if state == nil || index < 0 || index >= len(state.CallAdmissions) {
		return cloudAgentCallAdmission{Allowed: true}, false
	}
	return state.CallAdmissions[index], true
}

// cloudAgentAdmissionError 把预检结论变成给模型的工具错误。
func cloudAgentAdmissionError(admission cloudAgentCallAdmission) error {
	message := strings.TrimSpace(admission.Message)
	if admission.Issue == "schema_error" || admission.Issue == "" {
		field := admission.Field
		if field == "" {
			field = "arguments"
		}
		issue := admission.FieldIssue
		if issue == "" {
			issue = "invalid_value"
		}
		return cloudAgentFieldError(field, issue, message)
	}
	wrapped := BadAuthRequest(message)
	if cloudAgentAdmissionIsSkip(admission.Issue) {
		// 没执行 ≠ 参数错：用 400 之外的语义不合适，保持 AppError 400 但由回执改写 reason。
		wrapped = BadAuthRequest(message)
	}
	return &cloudAgentCallAdmissionError{error: wrapped, Admission: admission.Issue, Field: admission.Field}
}

// cloudAgentAdmissionPayload 是预检拒绝对事件载荷的附加标注（机器可读的"为什么没执行"）。
func cloudAgentAdmissionPayload(admission cloudAgentCallAdmission) map[string]any {
	payload := map[string]any{"admission": admission.Issue}
	if admission.Field != "" {
		payload["field"] = admission.Field
	}
	if admission.FieldIssue != "" {
		payload["fieldIssue"] = admission.FieldIssue
	}
	return payload
}
