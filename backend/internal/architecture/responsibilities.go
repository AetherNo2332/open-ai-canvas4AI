package architecture

import (
	"go/ast"
	"go/token"
	"strings"
)

// facadeFiles 列出迁移后保留在 app 的门面文件。它们只能包含类型别名、常量、
// 直接再导出变量与单 return 透传函数；业务实现在对应域包。清单按精确路径登记，
// 不做文件名猜测；新增迁移单元时在这里显式登记。
var facadeFiles = map[string]string{
	"internal/app/cloud_agent_memory_document_api.go": "长期记忆文档门面；业务实现在 internal/agentmemory/document",
}

// facadeFindings 约束登记的门面文件：类型必须是别名，常量/变量初始化只能是字面量
// 或对已导出符号的直接引用，函数只能是单 return 透传，且不得声明 init。
// 规则无法判断的形态一律报告，不静默当成合法。
func (s *scanner) facadeFindings(src *source) []Finding {
	reason, ok := facadeFiles[src.path]
	if !ok {
		return nil
	}
	findings := []Finding{}
	for _, decl := range src.file.Decls {
		switch typed := decl.(type) {
		case *ast.FuncDecl:
			if typed.Recv == nil && typed.Name.Name == "init" {
				findings = append(findings, facadeFinding(src.path, "init", reason+": facade files must not declare init"))
				continue
			}
			if typed.Body == nil || len(typed.Body.List) != 1 {
				findings = append(findings, facadeFinding(src.path, functionName(typed), reason+": facade functions must be a single pass-through return"))
				continue
			}
			if _, ok := typed.Body.List[0].(*ast.ReturnStmt); !ok {
				findings = append(findings, facadeFinding(src.path, functionName(typed), reason+": facade functions must be a single pass-through return"))
			}
		case *ast.GenDecl:
			if typed.Tok == token.IMPORT {
				continue
			}
			for _, spec := range typed.Specs {
				findings = append(findings, facadeSpecFindings(src, typed.Tok, spec, reason)...)
			}
		}
	}
	return findings
}

func facadeSpecFindings(src *source, tok token.Token, spec ast.Spec, reason string) []Finding {
	valueSpec, ok := spec.(*ast.ValueSpec)
	if !ok {
		typed, isType := spec.(*ast.TypeSpec)
		if !isType {
			// 未知声明形态：保守报告。
			return []Finding{facadeFinding(src.path, "unknown", reason+": unrecognized declaration in facade file")}
		}
		if typed.Assign == token.NoPos {
			return []Finding{facadeFinding(src.path, typed.Name.Name, reason+": facade types must be aliases of the owning package")}
		}
		return nil
	}
	if len(valueSpec.Values) == 0 {
		return nil
	}
	kind := "const"
	if tok == token.VAR {
		kind = "var"
	}
	for _, value := range valueSpec.Values {
		if !isDirectReference(value) {
			return []Finding{facadeFinding(src.path, strings.Join(names(valueSpec.Names), ","), reason+": facade "+kind+" initializers must reference the owning package directly")}
		}
	}
	return nil
}

// isDirectReference 允许基本字面量（含一元符号）与 pkg.Sym 选择器；调用、复合
// 字面量等带计算或副作用的初始化一律拒绝。
func isDirectReference(expr ast.Expr) bool {
	switch typed := expr.(type) {
	case *ast.BasicLit:
		return true
	case *ast.UnaryExpr:
		return isDirectReference(typed.X)
	case *ast.SelectorExpr:
		_, ok := typed.X.(*ast.Ident)
		return ok
	default:
		return false
	}
}

func names(idents []*ast.Ident) []string {
	out := make([]string, 0, len(idents))
	for _, ident := range idents {
		out = append(out, ident.Name)
	}
	return out
}

func facadeFinding(path, name, detail string) Finding {
	return Finding{ID: path + ":facade:" + name, Rule: "facade_declaration", Value: 1, Limit: 0, Detail: detail}
}

// handlerPersistenceViolation 阻止 handler 层直接依赖 ORM/SQL：持久化事实由
// repository 返回，业务错误由 service/app 归属，handler 只映射响应。
func handlerPersistenceViolation(src *source, imported string) (string, bool) {
	if layerOf(src.path) != "handler" {
		return "", false
	}
	for _, banned := range []string{"gorm.io/gorm", "database/sql"} {
		if imported == banned || strings.HasPrefix(imported, banned+"/") {
			return "handler must not import " + banned + "; adapt persistence errors in service or app", true
		}
	}
	if strings.HasPrefix(imported, "gorm.io/driver/") {
		return "handler must not import ORM drivers; adapt persistence errors in service or app", true
	}
	return "", false
}
