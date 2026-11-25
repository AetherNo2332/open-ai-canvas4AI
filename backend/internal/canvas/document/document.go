// Package document implements the node-json v1 external canvas document:
// Render, Parse, ParseWithOptions, FieldBlocks and the single Normalize entry.
// Field names, kinds, constraints and print order come only from
// capability.Field declarations.
package document

import (
	"fmt"
	"strings"

	"yingce/backend/internal/canvas/capability"
)

const (
	CodeSyntax        = "E_SYNTAX"
	CodeUnknownField  = "E_UNKNOWN_FIELD"
	CodeDuplicate     = "E_DUPLICATE"
	CodeType          = "E_TYPE"
	CodeValue         = "E_VALUE"
	CodeNodeMismatch  = "E_NODE_MISMATCH"
	CodeNotInDocument = "E_NOT_IN_DOCUMENT"
	CodeUnsupported   = "E_UNSUPPORTED"
)

const (
	RepairCRLF                = "crlf"
	RepairBOM                 = "bom"
	RepairMissingFinalNewline = "missing-final-newline"
	RepairSentenceWrap        = "sentence-wrap"
)

type Patch struct {
	Type   string
	ID     string
	Values map[string]any
}

type Repair struct {
	Line   int
	Kind   string
	Detail string
}

type Error struct {
	Code   string
	Line   int
	Detail string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Line > 0 {
		return fmt.Sprintf("node-json 第 %d 行 [%s]：%s", e.Line, e.Code, e.Detail)
	}
	return fmt.Sprintf("node-json [%s]：%s", e.Code, e.Detail)
}

func VerifyIdentity(patch Patch, node map[string]any) error {
	declared := strings.TrimSpace(patch.ID)
	if declared == "" {
		return nil
	}
	target, _ := node["id"].(string)
	target = strings.TrimSpace(target)
	if target == declared {
		return nil
	}
	if target == "" {
		return &Error{Code: CodeNodeMismatch, Detail: fmt.Sprintf("文件声明 id 是 %q，目标节点没有 id（新建）；要改已有节点请用该节点的输出", declared)}
	}
	return &Error{Code: CodeNodeMismatch, Detail: fmt.Sprintf("文件声明的 id 是 %q，目标节点是 %q；这是另一个节点的文件", declared, target)}
}

func documentFields(fields capability.Fields) capability.Fields {
	return fields.Where(func(field capability.Field) bool {
		return field.Writable || field.Editable
	})
}

func excludedDocumentFields(fields capability.Fields) capability.Fields {
	return fields.Where(func(field capability.Field) bool {
		return !field.Writable && !field.Editable
	})
}

func notInDocumentDetail(key string, field capability.Field) string {
	return fmt.Sprintf("字段 %s 不在文档里：该字段既不 Writable 也不 Editable（只读投影或程序托管）；用 cat 查看，用对应动作修改", key)
}
