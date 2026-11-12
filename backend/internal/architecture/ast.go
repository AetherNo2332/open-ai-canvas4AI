package architecture

import (
	"go/ast"
	"go/token"
	"strconv"
	"strings"
)

func (s *scanner) functionFindings(src *source, fn *ast.FuncDecl) []Finding {
	name := functionName(fn)
	findings := []Finding{}
	if fn.Body != nil {
		findings = append(findings, s.bodyFindings(src, src.path+":func:"+name, fn.Pos(), fn.End(), fn.Body)...)
		findings = append(findings, s.closureFindings(src, name, fn.Body)...)
	}
	if src.agent && ast.IsExported(fn.Name.Name) && s.containsUntypedMap(fn.Type, src, map[string]bool{}) {
		findings = append(findings, signatureFinding(src.path, name))
	}
	return findings
}

// bodyFindings 计算一个函数体的行数与圈复杂度。行数从 func 关键字到右花括号，含空行与注释。
func (s *scanner) bodyFindings(src *source, id string, start, end token.Pos, body *ast.BlockStmt) []Finding {
	findings := []Finding{}
	lines := s.fset.Position(end).Line - s.fset.Position(start).Line + 1
	if lines > functionLineLimit {
		findings = append(findings, Finding{ID: id + ":lines", Rule: "function_lines", Value: lines, Limit: functionLineLimit, Detail: "function exceeds 120 lines"})
	}
	complexity := cyclomatic(body)
	if complexity > complexityLimit {
		findings = append(findings, Finding{ID: id + ":complexity", Rule: "complexity", Value: complexity, Limit: complexityLimit, Detail: "cyclomatic complexity exceeds 20"})
	}
	return findings
}

// closureFindings 单独度量函数字面量（函数体内的闭包和包级 var 初始化里的函数）：圈复杂度不把闭包算进外层函数，
// 不单独度量的话，把逻辑挪进闭包就能绕过上限。ID 用外层函数名或变量名加闭包在其中的序号（从 1 起，按源码顺序），
// 与行号无关，外层上方增删代码不会改变 ID。
func (s *scanner) closureFindings(src *source, outer string, body ast.Node) []Finding {
	findings := []Finding{}
	index := 0
	ast.Inspect(body, func(node ast.Node) bool {
		lit, ok := node.(*ast.FuncLit)
		if !ok {
			return true
		}
		index++
		id := src.path + ":func:" + outer + ".func" + strconv.Itoa(index)
		findings = append(findings, s.bodyFindings(src, id, lit.Pos(), lit.End(), lit.Body)...)
		return true
	})
	return findings
}

func (s *scanner) exportedTypeFindings(src *source, decl *ast.GenDecl) []Finding {
	findings := []Finding{}
	for _, spec := range decl.Specs {
		typed, ok := spec.(*ast.TypeSpec)
		if !ok || !ast.IsExported(typed.Name.Name) {
			continue
		}
		if fields, ok := typed.Type.(*ast.StructType); ok {
			for _, field := range fields.Fields.List {
				if !s.containsUntypedMap(field.Type, src, map[string]bool{}) {
					continue
				}
				for _, name := range field.Names {
					if ast.IsExported(name.Name) {
						findings = append(findings, signatureFinding(src.path, typed.Name.Name+"."+name.Name))
					}
				}
				if len(field.Names) == 0 {
					findings = append(findings, signatureFinding(src.path, typed.Name.Name+".embedded"))
				}
			}
			continue
		}
		if s.containsUntypedMap(typed.Type, src, map[string]bool{}) {
			findings = append(findings, signatureFinding(src.path, typed.Name.Name))
		}
	}
	return findings
}

func signatureFinding(path, name string) Finding {
	return Finding{ID: path + ":signature:" + name, Rule: "untyped_protocol_map", Value: 1, Limit: 0, Detail: "exported protocol exposes an untyped map; use a DTO or json.RawMessage at the serialization boundary"}
}

func functionName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	return receiverName(fn.Recv.List[0].Type) + "." + fn.Name.Name
}

func receiverName(expr ast.Expr) string {
	switch typed := expr.(type) {
	case *ast.StarExpr:
		return receiverName(typed.X)
	case *ast.IndexExpr:
		return receiverName(typed.X)
	case *ast.IndexListExpr:
		return receiverName(typed.X)
	case *ast.Ident:
		return typed.Name
	default:
		return "receiver"
	}
}

func cyclomatic(body *ast.BlockStmt) int {
	complexity := 1
	ast.Inspect(body, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.FuncLit:
			return false
		case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt:
			complexity++
		case *ast.CaseClause:
			if typed.List != nil {
				complexity++
			}
		case *ast.CommClause:
			if typed.Comm != nil {
				complexity++
			}
		case *ast.BinaryExpr:
			if typed.Op == token.LAND || typed.Op == token.LOR {
				complexity++
			}
		}
		return true
	})
	return complexity
}

func (s *scanner) containsUntypedMap(expr ast.Expr, src *source, seen map[string]bool) bool {
	if expr == nil {
		return false
	}
	if inst, body, owner, key, ok := s.instantiate(expr, src); ok {
		if seen[key] {
			return false
		}
		seen[key] = true
		defer delete(seen, key)
		return inst.containsUntypedMap(body, owner, seen)
	}
	if m, ok := expr.(*ast.MapType); ok && s.containsAny(m.Value, src, map[string]bool{}) {
		return true
	}
	if resolved, owner, key, ok := s.resolve(expr, src); ok {
		if seen[key] {
			// 该类型已在展开中检查过：类型展开是封闭的，map 若可达早已被判出，环本身不改变判定。
			return false
		}
		seen[key] = true
		defer delete(seen, key)
		return s.containsUntypedMap(resolved, owner, seen)
	}
	for _, child := range expressionChildren(expr) {
		if s.containsUntypedMap(child, src, seen) {
			return true
		}
	}
	return false
}

func (s *scanner) containsAny(expr ast.Expr, src *source, seen map[string]bool) bool {
	if inst, body, owner, key, ok := s.instantiate(expr, src); ok {
		if seen[key] {
			return false
		}
		seen[key] = true
		defer delete(seen, key)
		return inst.containsAny(body, owner, seen)
	}
	if ident, ok := expr.(*ast.Ident); ok && ident.Name == "any" {
		return true
	}
	if iface, ok := expr.(*ast.InterfaceType); ok && len(iface.Methods.List) == 0 {
		return true
	}
	if resolved, owner, key, ok := s.resolve(expr, src); ok {
		if seen[key] {
			// 该类型已在展开中检查过：类型展开是封闭的，map 若可达早已被判出，环本身不改变判定。
			return false
		}
		seen[key] = true
		defer delete(seen, key)
		return s.containsAny(resolved, owner, seen)
	}
	for _, child := range expressionChildren(expr) {
		if s.containsAny(child, src, seen) {
			return true
		}
	}
	return false
}

func (s *scanner) resolve(expr ast.Expr, src *source) (ast.Expr, *source, string, bool) {
	key := ""
	switch typed := expr.(type) {
	case *ast.Ident:
		key = src.pkg + "." + typed.Name
	case *ast.SelectorExpr:
		if name, ok := typed.X.(*ast.Ident); ok {
			key = src.imports[name.Name] + "." + typed.Sel.Name
		}
	}
	decl, ok := s.types[key]
	if ok && decl.bound {
		key += ":" + boundTypeKey(decl.expr, decl.src)
	}
	return decl.expr, decl.src, key, ok
}

func expressionChildren(expr ast.Expr) []ast.Expr {
	out := []ast.Expr{}
	fields := func(list *ast.FieldList, exportedOnly bool) {
		if list == nil {
			return
		}
		for _, field := range list.List {
			visible := !exportedOnly || len(field.Names) == 0
			for _, name := range field.Names {
				visible = visible || ast.IsExported(name.Name)
			}
			if visible {
				out = append(out, field.Type)
			}
		}
	}
	switch typed := expr.(type) {
	case *ast.MapType:
		out = append(out, typed.Key, typed.Value)
	case *ast.ArrayType:
		out = append(out, typed.Elt)
	case *ast.StarExpr:
		out = append(out, typed.X)
	case *ast.ParenExpr:
		out = append(out, typed.X)
	case *ast.Ellipsis:
		out = append(out, typed.Elt)
	case *ast.ChanType:
		out = append(out, typed.Value)
	case *ast.FuncType:
		fields(typed.Params, false)
		fields(typed.Results, false)
	case *ast.InterfaceType:
		fields(typed.Methods, true)
	case *ast.StructType:
		fields(typed.Fields, true)
	case *ast.IndexExpr:
		out = append(out, typed.X, typed.Index)
	case *ast.IndexListExpr:
		out = append(out, typed.X)
		out = append(out, typed.Indices...)
	}
	return out
}

func FormatFindings(findings []Finding) string {
	var text strings.Builder
	for _, finding := range findings {
		text.WriteString(finding.ID + ": " + finding.Detail)
		if finding.Rule != "dependency" && finding.Rule != "untyped_protocol_map" {
			text.WriteString(" (value=" + strconv.Itoa(finding.Value) + ", limit=" + strconv.Itoa(finding.Limit) + ")")
		}
		text.WriteString("\n")
	}
	return text.String()
}
