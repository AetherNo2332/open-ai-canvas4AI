package architecture

import "testing"

// 门面文件只允许类型别名、直接引用初始化与单 return 透传；业务函数体、init、
// 带计算的初始化和新类型定义都会被阻断。
func TestFacadeFilesRejectBusinessDeclarations(t *testing.T) {
	facade := map[string]string{
		"internal/app/cloud_agent_memory_document_api.go": `package app

import "yingce/backend/internal/agentmemory/document"

type (
	View  = document.View
	Entry = document.EntryInput
)

type NotAlias struct{ Field string }

var direct = document.DefaultCapacity

var computed = document.New(nil)

func init() {}

func (s *Service) Get(userID string) (View, error) {
	if userID == "" {
		return View{}, nil
	}
	return s.memoryDocuments().Document(userID)
}

func (s *Service) Pass(userID string) (View, error) {
	return s.memoryDocuments().Document(userID)
}
`,
	}
	findings, err := Check(testBackend(t, facade), Baseline{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"internal/app/cloud_agent_memory_document_api.go:facade:NotAlias",
		"internal/app/cloud_agent_memory_document_api.go:facade:computed",
		"internal/app/cloud_agent_memory_document_api.go:facade:init",
		"internal/app/cloud_agent_memory_document_api.go:facade:Service.Get",
	} {
		if !hasID(findings, want) {
			t.Fatalf("facade violation %q not reported: %+v", want, findings)
		}
	}
	if hasID(findings, "internal/app/cloud_agent_memory_document_api.go:facade:Service.Pass") {
		t.Fatalf("pass-through function must stay legal: %+v", findings)
	}
	if hasID(findings, "internal/app/cloud_agent_memory_document_api.go:facade:direct") {
		t.Fatalf("direct package reference initializer must stay legal: %+v", findings)
	}
}

func hasID(findings []Finding, id string) bool {
	for _, finding := range findings {
		if finding.ID == id {
			return true
		}
	}
	return false
}

// handler 不得直接依赖 ORM/SQL；app 作为组合层仍然允许。
func TestHandlerPersistenceImportsRejected(t *testing.T) {
	root := testBackend(t, map[string]string{
		"internal/handler/bad.go": "package handler\nimport (\n _ \"gorm.io/gorm\"\n _ \"database/sql\"\n _ \"gorm.io/driver/sqlite\"\n)\n",
		"internal/app/ok.go":      "package app\nimport _ \"gorm.io/gorm\"\n",
	})
	findings, err := Check(root, Baseline{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 3 || !hasRule(findings, "dependency") {
		t.Fatalf("handler persistence imports not caught exactly: %+v", findings)
	}
}

// 协议规则覆盖新记忆合同归属：document 包改任何文件名仍受约束。
func TestProtocolRulesCoverDocumentPackage(t *testing.T) {
	root := testBackend(t, map[string]string{
		"internal/agentmemory/document/renamed_contract.go": `package document

type Payload map[string]any

func Send(value Payload) Payload { return value }
`,
	})
	findings, err := Check(root, Baseline{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) < 2 || !hasRule(findings, "untyped_protocol_map") {
		t.Fatalf("document package protocol maps not caught after rename: %+v", findings)
	}
}

// 泛型包装、点导入与跨文件别名同样逃不过 map/any 检查；合法的递归类型不被误报。
func TestProtocolRulesCoverWrapperAndAliasForms(t *testing.T) {
	root := testBackend(t, map[string]string{
		"internal/agentmemory/document/box.go": `package document

type Box[T any] struct{ Value T }

func Send(value Box[map[string]any]) Box[any] { return Box[any]{Value: value} }
`,
		"internal/agentmemory/document/dot.go": `package document

import . "encoding/json"

type Loose map[string]any

func Build() RawMessage { return nil }
`,
		"internal/agentmemory/document/alias_chain.go": `package document

type AliasA = AliasB

type AliasB = map[string]any

func Send(value AliasA) AliasA { return value }
`,
		"internal/agentmemory/document/recursive.go": `package document

type Node struct {
	Next    *Node
	Entries []Node
	Meta    map[string]any
}

func Send(value Node) Node { return value }
`,
	})
	findings, err := Check(root, Baseline{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !hasID(findings, "internal/agentmemory/document/box.go:signature:Send") {
		t.Fatalf("Box[map[string]any] parameter must be caught: %+v", findings)
	}
	if !hasID(findings, "internal/agentmemory/document/dot.go:signature:Loose") {
		t.Fatalf("dot-import file map type must be caught: %+v", findings)
	}
	if !hasID(findings, "internal/agentmemory/document/alias_chain.go:signature:Send") {
		t.Fatalf("cross-file alias chain must be caught: %+v", findings)
	}
	if !hasID(findings, "internal/agentmemory/document/recursive.go:signature:Send") {
		t.Fatalf("map inside recursive type must be caught: %+v", findings)
	}
	// Node 上的 map 是唯一可达违规；递归引用自身不得产生重复或误报条目。
	for _, finding := range findings {
		if finding.ID == "internal/agentmemory/document/recursive.go:signature:Node" {
			t.Fatalf("unexported usage must not duplicate findings: %+v", findings)
		}
	}
}
