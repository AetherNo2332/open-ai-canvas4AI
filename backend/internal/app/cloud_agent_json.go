package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
)

var errCloudAgentJSONSingleObject = errors.New("参数必须是单个 JSON 对象")

// Only explicitly classified input errors may be repaired by the model;
// authorization and unsupported mutations still fail before any write/approval.
type cloudAgentArgumentError struct{ error }

func (e *cloudAgentArgumentError) Unwrap() error { return e.error }

type cloudAgentFieldArgumentError struct {
	error
	Field string
	Issue string
}

func (e *cloudAgentFieldArgumentError) Unwrap() error { return e.error }

func cloudAgentFieldError(field, issue, message string) error {
	return &cloudAgentFieldArgumentError{
		error: &cloudAgentArgumentError{BadAuthRequest(message)}, Field: field, Issue: issue,
	}
}

// Decode operations individually so feedback identifies the failing array item.
// Only field names declared by our structs are echoed; arbitrary keys/values
// supplied by a model are never used as diagnostics.
func decodeCloudAgentCanvasArgs(raw string) (agentCanvasArgs, error) {
	var envelope struct {
		SnapshotHash string            `json:"snapshotHash"`
		Ops          []json.RawMessage `json:"ops"`
	}
	args := agentCanvasArgs{}
	if err := decodeCloudAgentJSONObject(raw, &envelope); err != nil {
		return args, cloudAgentJSONArgumentError(err)
	}
	if envelope.SnapshotHash == "" {
		return args, cloudAgentFieldError("snapshotHash", "required", "画布参数 snapshotHash 不能为空，请先读取画布")
	}
	if len(envelope.Ops) < 1 || len(envelope.Ops) > 20 {
		return args, cloudAgentFieldError("ops", "item_count", "画布参数 ops 必须包含1到20项操作")
	}
	args.SnapshotHash = envelope.SnapshotHash
	ops := make([]agentCanvasOp, 0, len(envelope.Ops))
	for i, rawOp := range envelope.Ops {
		path := fmt.Sprintf("ops[%d]", i)
		var op agentCanvasOp
		if err := decodeCloudAgentJSONObject(string(rawOp), &op); err != nil {
			field, issue := path, "invalid_value"
			name := ""
			var typeErr *json.UnmarshalTypeError
			if errors.As(err, &typeErr) {
				issue = "type_mismatch"
				name = typeErr.Field
			} else if strings.HasPrefix(err.Error(), "json: unknown field ") {
				issue = "unexpected_field"
				name = strings.Trim(strings.TrimPrefix(err.Error(), "json: unknown field "), "\"")
				if cloudAgentNodeStyleField(name) {
					return args, cloudAgentNodeStyleError(path, name)
				}
			}
		knownField:
			for _, typ := range []reflect.Type{reflect.TypeOf(agentCanvasArgs{}), reflect.TypeOf(agentCanvasOp{})} {
				for j := 0; j < typ.NumField(); j++ {
					if strings.Split(typ.Field(j).Tag.Get("json"), ",")[0] == name {
						field += "." + name
						break knownField
					}
				}
			}
			return args, cloudAgentFieldError(field, issue, fmt.Sprintf("画布参数 %s 无效：%s", field, cloudAgentSafeToolError(cloudAgentJSONArgumentError(err))))
		}
		ops = append(ops, op)
	}
	for i, op := range ops {
		path := fmt.Sprintf("ops[%d]", i)
		// 连线端点与端口字段已从 canvas_apply_ops 合同删除。struct 里保留这些字段只为识别旧
		// 载荷：旧边操作由 validateCloudAgentReferenceOperations 统一判成
		// connection_ops_disabled（提示模型改用引用字段，而不是"缺 fromNodeId"），其他操作
		// 携带这些字段一律按不支持的字段拒绝，不能静默忽略模型输入。
		legacyConnection := cloudAgentConnectionOperationDisabled(op.Type)
		var rawFields map[string]json.RawMessage
		if json.Unmarshal(envelope.Ops[i], &rawFields) == nil {
			for _, key := range []string{"fromHandleId", "toHandleId"} {
				value, exists := rawFields[key]
				if !exists {
					continue
				}
				var handle *string
				if json.Unmarshal(value, &handle) != nil || handle == nil {
					return args, cloudAgentFieldError(path+"."+key, "type_mismatch", "handle 必须是字符串")
				}
				if legacyConnection {
					continue
				}
				return args, cloudAgentFieldError(path+"."+key, "unexpected_field", fmt.Sprintf("画布参数 %s.%s 不再属于 canvas_apply_ops：端口由服务端按引用字段派生", path, key))
			}
			if !legacyConnection {
				for _, key := range []string{"fromNodeId", "toNodeId"} {
					if _, exists := rawFields[key]; exists {
						return args, cloudAgentFieldError(path+"."+key, "unexpected_field", fmt.Sprintf("画布参数 %s.%s 不再属于 canvas_apply_ops：引用请写入 generationSpec.referenceBindings、metadata.inputBindings 或分镜行 assetBindings", path, key))
					}
				}
			}
		}
		required := ""
		switch {
		case op.Type == "":
			required = "type"
		case op.ID == "":
			required = "id"
		case op.Type == "add_node" && op.NodeType == "":
			required = "nodeType"
		case op.Type == "update_node" && len(op.Patch) == 0:
			required = "patch"
		case op.Type == "duplicate_node" && op.SourceNodeID == "":
			required = "sourceNodeId"
		case op.Type == "replace_text" && op.Match == "":
			required = "match"
		case op.Type == "reorder_nodes" && len(op.NodeIDs) == 0:
			required = "nodeIds"
		case op.Type == "reorder_rows" && len(op.RowIDs) == 0:
			required = "rowIds"
		case op.Type == "use_media_version" && op.VersionID == "":
			required = "versionId"
		}
		if required != "" {
			field := path + "." + required
			return args, cloudAgentFieldError(field, "required", fmt.Sprintf("画布参数 %s 不能为空", field))
		}
		args.Ops = append(args.Ops, op)
	}
	return args, nil
}

// Report the failure category without echoing model-controlled keys or values.
// The runtime attaches compact hints from the advertised contract.
func cloudAgentJSONArgumentError(err error) error {
	message := "工具参数 JSON 格式无效，请按参数规范修正后重试"
	var typeErr *json.UnmarshalTypeError
	switch {
	case errors.Is(err, errCloudAgentJSONSingleObject):
		message = "工具参数必须是单个 JSON 对象，不能是字符串、数组、null 或多个对象；无参数时传 {}"
	case errors.As(err, &typeErr):
		message = "工具参数字段类型不匹配，请按参数规范使用整数、字符串或数组，不要把数字或数组写成字符串"
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		message = "工具参数含有不支持的字段，请移除参数规范以外的字段后重试"
	}
	return &cloudAgentArgumentError{BadAuthRequest(message)}
}

// decodeCloudAgentJSONObject is used for model tool arguments. Tool arguments
// are an untrusted protocol boundary: reject non-objects, unknown fields and
// trailing JSON instead of silently accepting an ambiguous payload.
func decodeCloudAgentJSONObject(raw string, target any) error {
	data := bytes.TrimSpace([]byte(raw))
	if len(data) == 0 || data[0] != '{' {
		return errCloudAgentJSONSingleObject
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errCloudAgentJSONSingleObject
		}
		return err
	}
	return nil
}
