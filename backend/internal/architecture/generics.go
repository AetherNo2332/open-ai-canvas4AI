package architecture

import (
	"bytes"
	"go/ast"
	"go/printer"
	"go/token"
)

// Instantiate only the local generic declaration being inspected. Bind arguments
// with their original source so aliases from other packages resolve correctly.
func (s *scanner) instantiate(expr ast.Expr, src *source) (*scanner, ast.Expr, *source, string, bool) {
	var target ast.Expr
	var args []ast.Expr
	switch typed := expr.(type) {
	case *ast.IndexExpr:
		target, args = typed.X, []ast.Expr{typed.Index}
	case *ast.IndexListExpr:
		target, args = typed.X, typed.Indices
	default:
		return nil, nil, nil, "", false
	}
	body, owner, key, ok := s.resolve(target, src)
	if !ok {
		return nil, nil, nil, "", false
	}
	decl := s.types[key]
	if decl.params == nil {
		return nil, nil, nil, "", false
	}
	inst := *s
	inst.types = make(map[string]typeDecl, len(s.types)+len(args))
	for name, value := range s.types {
		inst.types[name] = value
	}
	index := 0
	for _, field := range decl.params.List {
		for _, name := range field.Names {
			if index >= len(args) {
				return nil, nil, nil, "", false
			}
			argument := typeDecl{expr: args[index], src: src, bound: true}
			if resolved, source, _, ok := s.resolve(args[index], src); ok {
				argument = typeDecl{expr: resolved, src: source, bound: true}
			}
			inst.types[owner.pkg+"."+name.Name] = argument
			index++
		}
	}
	var signature bytes.Buffer
	_ = printer.Fprint(&signature, token.NewFileSet(), expr)
	return &inst, body, owner, key + ":" + src.pkg + ":" + signature.String(), true
}

func boundTypeKey(expr ast.Expr, src *source) string {
	var signature bytes.Buffer
	_ = printer.Fprint(&signature, token.NewFileSet(), expr)
	return src.pkg + ":" + signature.String()
}
