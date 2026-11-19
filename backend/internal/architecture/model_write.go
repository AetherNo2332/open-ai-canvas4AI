package architecture

import (
	"fmt"
	"go/ast"
	"go/token"
)

// gormWriteMethods are the GORM APIs that change stored rows or execute
// arbitrary SQL. Builder methods (Model, Where, Session, ...) stay out of this
// set; a write appended to a builder chain is caught at its terminal call.
var gormWriteMethods = map[string]bool{
	"Create": true, "CreateInBatches": true,
	"Save":   true,
	"Update": true, "Updates": true, "UpdateColumn": true, "UpdateColumns": true,
	"Delete": true, "Exec": true,
	// Raw runs arbitrary SQL, including writes through a following Scan.
	"Raw":           true,
	"FirstOrCreate": true, "FindOrCreate": true,
	"Append": true, "Replace": true,
}

// modelWriteFindings forbids internal/model production code from executing
// GORM writes. Model files may declare structs, tags and read-only hooks; the
// persistence itself belongs to internal/database or internal/repository.
func (s *scanner) modelWriteFindings(src *source) []Finding {
	if layerOf(src.path) != "model" {
		return nil
	}
	aliases, dotImported := gormImports(src)
	if len(aliases) == 0 && !dotImported {
		return nil
	}
	fields := gormFieldNames(src.file, aliases, dotImported)
	globals := gormPackageHandles(src.file, fields, aliases, dotImported)
	findings := []Finding{}
	for _, decl := range src.file.Decls {
		switch typed := decl.(type) {
		case *ast.FuncDecl:
			if typed.Body == nil {
				continue
			}
			handles := cloneNames(globals)
			collectGormHandleParams(typed.Type, handles, aliases, dotImported)
			collectGormLocals(typed.Body, handles, fields, aliases, dotImported)
			findings = append(findings, s.gormWriteFindings(src, typed.Body, handles, fields, aliases, dotImported)...)
		case *ast.GenDecl:
			findings = append(findings, s.gormWriteFindings(src, typed, globals, fields, aliases, dotImported)...)
		}
	}
	return findings
}

func gormImports(src *source) (map[string]bool, bool) {
	aliases := map[string]bool{}
	dotImported := false
	for alias, imported := range src.imports {
		if imported != "gorm.io/gorm" {
			continue
		}
		switch alias {
		case "_":
		case ".":
			dotImported = true
		default:
			aliases[alias] = true
		}
	}
	return aliases, dotImported
}

func gormFieldNames(file *ast.File, aliases map[string]bool, dotImported bool) map[string]bool {
	fields := map[string]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		structType, ok := node.(*ast.StructType)
		if !ok {
			return true
		}
		for _, field := range structType.Fields.List {
			if !isGormHandleType(field.Type, aliases, dotImported) {
				continue
			}
			for _, name := range field.Names {
				fields[name.Name] = true
			}
		}
		return true
	})
	return fields
}

func gormPackageHandles(file *ast.File, fields, aliases map[string]bool, dotImported bool) map[string]bool {
	handles := map[string]bool{}
	changed := true
	for changed {
		changed = false
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				if gormValueSpecHandles(value, handles, fields, aliases, dotImported) {
					changed = true
				}
			}
		}
	}
	return handles
}

func collectGormHandleParams(fn *ast.FuncType, handles map[string]bool, aliases map[string]bool, dotImported bool) {
	for _, list := range []*ast.FieldList{fn.Params, fn.Results} {
		if list == nil {
			continue
		}
		for _, field := range list.List {
			if !isGormHandleType(field.Type, aliases, dotImported) {
				continue
			}
			for _, name := range field.Names {
				handles[name.Name] = true
			}
		}
	}
}

// collectGormLocals follows assignments inside one function body until no new
// handle is found. Nested function literals get their own pass so outer names
// do not leak into their scope.
func collectGormLocals(body *ast.BlockStmt, handles, fields, aliases map[string]bool, dotImported bool) {
	changed := true
	for changed {
		changed = false
		ast.Inspect(body, func(node ast.Node) bool {
			switch typed := node.(type) {
			case *ast.FuncLit:
				return false
			case *ast.AssignStmt:
				if collectGormAssignment(typed, handles, fields, aliases, dotImported) {
					changed = true
				}
			case *ast.DeclStmt:
				if collectGormVarDecl(typed, handles, fields, aliases, dotImported) {
					changed = true
				}
			}
			return true
		})
	}
}

func collectGormAssignment(stmt *ast.AssignStmt, handles, fields, aliases map[string]bool, dotImported bool) bool {
	if len(stmt.Lhs) == len(stmt.Rhs) {
		added := false
		for index, rhs := range stmt.Rhs {
			if isGormValue(rhs, handles, fields, aliases, dotImported) && addGormHandle(stmt.Lhs[index], handles) {
				added = true
			}
		}
		return added
	}
	// gorm.Open and similar constructors return (*gorm.DB, error).
	if len(stmt.Rhs) == 1 && len(stmt.Lhs) > 1 && isGormValue(stmt.Rhs[0], handles, fields, aliases, dotImported) {
		return addGormHandle(stmt.Lhs[0], handles)
	}
	return false
}

func collectGormVarDecl(stmt *ast.DeclStmt, handles, fields, aliases map[string]bool, dotImported bool) bool {
	gen, ok := stmt.Decl.(*ast.GenDecl)
	if !ok || gen.Tok != token.VAR {
		return false
	}
	added := false
	for _, spec := range gen.Specs {
		value, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		if gormValueSpecHandles(value, handles, fields, aliases, dotImported) {
			added = true
		}
	}
	return added
}

func gormValueSpecHandles(value *ast.ValueSpec, handles, fields, aliases map[string]bool, dotImported bool) bool {
	matches := isGormHandleType(value.Type, aliases, dotImported)
	for _, rhs := range value.Values {
		if isGormValue(rhs, handles, fields, aliases, dotImported) {
			matches = true
		}
	}
	if !matches {
		return false
	}
	added := false
	for _, name := range value.Names {
		if markGormHandle(name.Name, handles) {
			added = true
		}
	}
	return added
}

func addGormHandle(expr ast.Expr, handles map[string]bool) bool {
	name, ok := expr.(*ast.Ident)
	return ok && markGormHandle(name.Name, handles)
}

func markGormHandle(name string, handles map[string]bool) bool {
	if handles[name] {
		return false
	}
	handles[name] = true
	return true
}

func (s *scanner) gormWriteFindings(src *source, node ast.Node, handles, fields, aliases map[string]bool, dotImported bool) []Finding {
	findings := []Finding{}
	ast.Inspect(node, func(current ast.Node) bool {
		switch typed := current.(type) {
		case *ast.FuncLit:
			inner := cloneNames(handles)
			collectGormHandleParams(typed.Type, inner, aliases, dotImported)
			collectGormLocals(typed.Body, inner, fields, aliases, dotImported)
			findings = append(findings, s.gormWriteFindings(src, typed.Body, inner, fields, aliases, dotImported)...)
			return false
		case *ast.CallExpr:
			selector, ok := typed.Fun.(*ast.SelectorExpr)
			if !ok || !gormWriteMethods[selector.Sel.Name] {
				return true
			}
			if !isGormValue(selector.X, handles, fields, aliases, dotImported) {
				return true
			}
			line := s.fset.Position(typed.Pos()).Line
			findings = append(findings, Finding{
				ID:     fmt.Sprintf("%s:%d:model_gorm_write", src.path, line),
				Rule:   "model_gorm_write",
				Value:  1,
				Limit:  0,
				Detail: fmt.Sprintf("%s calls GORM %s; move persistence out of internal/model into internal/database or internal/repository", site(src.path, line), selector.Sel.Name),
			})
		}
		return true
	})
	return findings
}

func site(path string, line int) string {
	return fmt.Sprintf("%s:%d", path, line)
}

func isGormHandleType(expr ast.Expr, aliases map[string]bool, dotImported bool) bool {
	switch typed := expr.(type) {
	case *ast.StarExpr:
		return isGormHandleType(typed.X, aliases, dotImported)
	case *ast.ParenExpr:
		return isGormHandleType(typed.X, aliases, dotImported)
	case *ast.SelectorExpr:
		owner, ok := typed.X.(*ast.Ident)
		return ok && aliases[owner.Name] && typed.Sel.Name == "DB"
	case *ast.Ident:
		return dotImported && typed.Name == "DB"
	}
	return false
}

// isGormValue resolves a receiver or assigned value back to a gorm.DB handle.
// Identifiers are tracked from parameters and assignments; selector fields are
// matched by name against struct fields declared with a gorm.DB type above;
// call chains inherit the handle nature of their receiver.
func isGormValue(expr ast.Expr, handles, fields, aliases map[string]bool, dotImported bool) bool {
	switch typed := expr.(type) {
	case *ast.ParenExpr:
		return isGormValue(typed.X, handles, fields, aliases, dotImported)
	case *ast.Ident:
		return handles[typed.Name]
	case *ast.SelectorExpr:
		if owner, ok := typed.X.(*ast.Ident); ok && aliases[owner.Name] {
			return typed.Sel.Name == "DB"
		}
		return fields[typed.Sel.Name]
	case *ast.CallExpr:
		selector, ok := typed.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		if owner, ok := selector.X.(*ast.Ident); ok && aliases[owner.Name] {
			return true
		}
		return isGormValue(selector.X, handles, fields, aliases, dotImported)
	case *ast.UnaryExpr:
		return typed.Op == token.AND && isGormValue(typed.X, handles, fields, aliases, dotImported)
	case *ast.CompositeLit:
		return isGormHandleType(typed.Type, aliases, dotImported)
	}
	return false
}

func cloneNames(source map[string]bool) map[string]bool {
	copy := make(map[string]bool, len(source))
	for name := range source {
		copy[name] = true
	}
	return copy
}
