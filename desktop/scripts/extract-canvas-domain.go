// Command extracts reachable pure declarations from the fixed upstream baseline.
// It does not edit the source. Review the generated dependency report before use.
package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type declaration struct {
	node ast.Node
	file *ast.File
	path string
}

func main() {
	if len(os.Args) != 3 {
		panic("usage: extract-canvas-domain app-dir output-file")
	}
	fs := token.NewFileSet()
	decls := map[string]declaration{}
	files, err := filepath.Glob(filepath.Join(os.Args[1], "*.go"))
	if err != nil {
		panic(err)
	}
	for _, p := range files {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		f, e := parser.ParseFile(fs, p, nil, 0)
		if e != nil {
			panic(e)
		}
		for _, d := range f.Decls {
			switch n := d.(type) {
			case *ast.FuncDecl:
				if n.Recv == nil {
					decls[n.Name.Name] = declaration{n, f, p}
				}
			case *ast.GenDecl:
				if n.Tok == token.IMPORT {
					continue
				}
				for _, s := range n.Specs {
					single := &ast.GenDecl{Tok: n.Tok, Specs: []ast.Spec{s}}
					switch v := s.(type) {
					case *ast.TypeSpec:
						decls[v.Name.Name] = declaration{single, f, p}
					case *ast.ValueSpec:
						for _, name := range v.Names {
							decls[name.Name] = declaration{single, f, p}
						}
					}
				}
			}
		}
	}
	manual := map[string]bool{}
	for _, n := range []string{"BadAuthRequest", "cloudAgentFieldError", "cloudAgentArgumentError", "cloudAgentSafeToolError", "validateCloudAgentID", "canvasCapabilityRegistry", "agentCanvasOp"} {
		manual[n] = true
	}
	queue := []string{"applyCloudAgentCanvasPlanInPlace", "editCloudAgentDrawingDocument", "storyboardNodeFromDocument", "cloudAgentStoryboardRowDefaults", "validateCloudAgentStoryboardRow", "validateCloudAgentStoryboardReferences", "validateCloudAgentStoryboardSourceRange", "batchTableNodeFromDocument", "validateCloudAgentBatchRowPatch", "cloudAgentBatchInputIDs", "cloudAgentPrevisSceneCreateTemplate", "cloudAgentPrevisApplyPatchOperation", "cloudAgentPrevisScenesSize", "cloudAgentPrevisValidateScene", "cloudAgentPrevisValidateCharacterBindingInCanvas"}
	queue = append(queue, "cloudAgentPrevisMaxScenes", "cloudAgentPrevisMaxOperations", "validateCloudAgentCanvasConnection", "cloudAgentConnectionNodes", "cloudAgentConnectionEdge", "validateCloudAgentGenerationReferences")
	queue = append(queue, "previsRenderSourceHash", "previsRenderInput", "previsDefaultActorURL")
	seen := map[string]declaration{}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if manual[name] {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		d, ok := decls[name]
		if !ok {
			panic("missing declaration " + name)
		}
		seen[name] = d
		ast.Inspect(d.node, func(n ast.Node) bool {
			if selector, ok := n.(*ast.SelectorExpr); ok {
				ast.Inspect(selector.X, func(x ast.Node) bool {
					if id, ok := x.(*ast.Ident); ok {
						if _, exists := decls[id.Name]; exists {
							queue = append(queue, id.Name)
						}
					}
					return true
				})
				return false
			}
			id, ok := n.(*ast.Ident)
			if !ok || id.Name == "_" {
				return true
			}
			if id.Obj != nil {
				switch id.Obj.Decl.(type) {
				case *ast.AssignStmt, *ast.Field, *ast.RangeStmt:
					return true
				}
			}
			if _, exists := decls[id.Name]; exists {
				queue = append(queue, id.Name)
			}
			return true
		})
	}
	imports := map[string]string{}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	var body bytes.Buffer
	for _, name := range names {
		d := seen[name]
		fmt.Printf("%s <- %s\n", name, filepath.Base(d.path))
		format.Node(&body, fs, d.node)
		body.WriteString("\n\n")
		aliases := map[string]string{}
		for _, s := range d.file.Imports {
			path, _ := strconv.Unquote(s.Path.Value)
			alias := filepath.Base(path)
			if s.Name != nil {
				alias = s.Name.Name
			}
			aliases[alias] = path
		}
		ast.Inspect(d.node, func(n ast.Node) bool {
			if s, ok := n.(*ast.SelectorExpr); ok {
				if id, ok := s.X.(*ast.Ident); ok {
					if p, ok := aliases[id.Name]; ok {
						imports[id.Name] = p
					}
				}
			}
			return true
		})
	}
	var out bytes.Buffer
	out.WriteString("// Extracted from Canary f1d8ee00, AGPL-3.0. Pure canvas operations.\npackage commands\n\nimport (\n")
	keys := []string{}
	for k := range imports {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&out, "%s %q\n", k, imports[k])
	}
	out.WriteString(")\n\n")
	text := body.String()
	replacer := strings.NewReplacer("agentCanvasOp", "Operation", "cloudAgentApprovalPreviewItem", "OperationSummary", "cloudAgentArgumentError", "ArgumentError", "cloudAgent", "canvas", "CloudAgent", "Canvas", "CreationCanvasOp", "creationNodeSpec")
	text = replacer.Replace(text)
	text = strings.ReplaceAll(text, "time.Now()", "p.now()")
	text = strings.ReplaceAll(text, "Agent", "画布命令")
	out.WriteString(text)
	generated, e := parser.ParseFile(fs, "generated.go", out.Bytes(), 0)
	if e != nil {
		panic(e)
	}
	methods := map[string]bool{}
	for _, d := range generated.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok {
			if fn.Name.Name == "editCanvasDrawingDocument" {
				// Native initialize/order/options commands validate an empty
				// engine envelope through the same record planner; the public
				// wrapper still rejects a completely empty drawing command.
				guard := fn.Body.List[0].(*ast.IfStmt)
				guard.Cond, _ = parser.ParseExpr("len(args.Operations) > 10000")
				call := guard.Body.List[0].(*ast.ReturnStmt).Results[0].(*ast.CallExpr)
				call.Args[0] = &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote("绘图记录修改最多包含 10000 项")}
			}
			methods[fn.Name.Name] = true
			fn.Recv = &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{ast.NewIdent("p")}, Type: &ast.StarExpr{X: ast.NewIdent("planner")}}}}
		}
	}
	for _, d := range generated.Decls {
		ast.Inspect(d, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if id, ok := call.Fun.(*ast.Ident); ok && methods[id.Name] {
					call.Fun = &ast.SelectorExpr{X: ast.NewIdent("p"), Sel: ast.NewIdent(id.Name)}
				}
			}
			if selector, ok := n.(*ast.SelectorExpr); ok {
				if id, ok := selector.X.(*ast.Ident); ok && id.Name == "canvasCapabilityRegistry" {
					selector.X = &ast.SelectorExpr{X: ast.NewIdent("p"), Sel: ast.NewIdent("registry")}
				}
			}
			return true
		})
	}
	out.Reset()
	out.WriteString("// Adapted from Canary f1d8ee00 pure canvas declarations; AGPL-3.0.\n")
	if e = format.Node(&out, fs, generated); e != nil {
		panic(e)
	}
	if e = os.WriteFile(os.Args[2], out.Bytes(), 0644); e != nil {
		panic(e)
	}
}
