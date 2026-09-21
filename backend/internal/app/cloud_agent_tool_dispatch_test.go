package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
	"testing"
)

// 本文件是一致性守卫：工具表（模型看到的契约）与运行期分派必须始终一致。
//
// 背景：把上游 v1.5.2–v1.5.7 合进来时，布局工具出现过"算法与测试都在、注册与分派全丢"的
// 半状态——`cloud_agent_layout.go` 里 642 行实现和 6 个用例都是绿的，但 `cloud_agent_tools.go`
// 的工具表里没有它的 schema，运行期的审批/执行分派也没有它的分支，于是模型永远看不到这个工具，
// 而全套测试仍然全绿。这类退化必须在 CI 立刻变红。
//
// 实现方式与取舍：`advanceCloudAgentTool` 的分派是写在事务闭包里的 switch，没有可注入的
// 分派表，无法用纯行为断言覆盖"这个工具名有没有分支"；因此这里**用 go/ast 读源码**
// 抽取三条集合，再做集合断言（用户已允许在结构做不到纯断言时退一步做源码级断言）。
// 读源码的好处是它检查的正是"分派有没有写"，而不会被运行期提前 return 掩盖。
func TestCloudAgentToolTableMatchesRuntimeDispatch(t *testing.T) {
	registered := cloudAgentRegisteredToolNames(t)
	supported := CloudAgentSupportedToolNames()
	dispatched := cloudAgentDispatchedToolNames(t)

	// 1. 平台支持集合里每个名字都能被执行分派处理（读/写两条分派路径都算）。
	missing := difference(supported, dispatched)
	if len(missing) > 0 {
		t.Fatalf("平台声明支持但运行期没有分派分支的工具：%s", strings.Join(missing, ", "))
	}

	// 2. 工具表里所有 add(...) 注册的名字都有分派分支（集合相等，而不是子集）。
	if extra := difference(dispatched, registered); len(extra) > 0 {
		t.Fatalf("运行期有分派分支但没有注册进工具表的工具：%s", strings.Join(extra, ", "))
	}
	if missing := difference(registered, dispatched); len(missing) > 0 {
		t.Fatalf("工具表注册了但没有分派分支的工具：%s", strings.Join(missing, ", "))
	}
	// 工具表全集必须与 CloudAgentSupportedToolNames() 逐一相同（后者从工具表派生，
	// 这条断言守的是"派生用的请求确实覆盖了所有条件暴露的分支"）。
	if missing := difference(registered, supported); len(missing) > 0 {
		t.Fatalf("CloudAgentSupportedToolNames() 漏掉了工具表里的：%s", strings.Join(missing, ", "))
	}
	if missing := difference(supported, registered); len(missing) > 0 {
		t.Fatalf("CloudAgentSupportedToolNames() 多出了工具表里没有的：%s", strings.Join(missing, ", "))
	}

	// 3. 被判定为"写画布"的名字 ⊆ 可执行集合，且都在写入分派里。
	canvasWrites := cloudAgentCanvasWriteToolNames(t)
	if extra := difference(canvasWrites, registered); len(extra) > 0 {
		t.Fatalf("被判为写画布但不在工具表里的工具：%s", strings.Join(extra, ", "))
	}
	if missing := difference(canvasWrites, dispatched); len(missing) > 0 {
		t.Fatalf("被判为写画布但没有分派分支的工具：%s", strings.Join(missing, ", "))
	}
	for _, name := range canvasWrites {
		if !cloudAgentWrite(name) {
			t.Fatalf("写画布工具 %s 不在 cloudAgentWrite 名单里，不会进审批链", name)
		}
	}
}

// cloudAgentRegisteredToolNames 收集 compileCloudAgentTools 里所有 add("name", …) 的名字全集。
func cloudAgentRegisteredToolNames(t *testing.T) []string {
	t.Helper()
	file := parseCloudAgentFile(t, "cloud_agent_tools.go")
	names := map[string]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		ident, ok := call.Fun.(*ast.Ident)
		if !ok || ident.Name != "add" || len(call.Args) == 0 {
			return true
		}
		if literal, ok := call.Args[0].(*ast.BasicLit); ok && literal.Kind == token.STRING {
			names[strings.Trim(literal.Value, `"`)] = true
		}
		return true
	})
	if len(names) == 0 {
		t.Fatal("没有从 cloud_agent_tools.go 解析到任何 add(...) 工具注册")
	}
	return sortedKeys(names)
}

// cloudAgentCanvasWriteToolNames 收集 cloudAgentCanvasWriteTool 的 case 字面量。
func cloudAgentCanvasWriteToolNames(t *testing.T) []string {
	t.Helper()
	file := parseCloudAgentFile(t, "cloud_agent_tools.go")
	names := map[string]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		decl, ok := node.(*ast.FuncDecl)
		if !ok || decl.Name.Name != "cloudAgentCanvasWriteTool" || decl.Body == nil {
			return true
		}
		collectStringLiterals(decl.Body, names)
		return false
	})
	if len(names) == 0 {
		t.Fatal("没有解析到 cloudAgentCanvasWriteTool 的工具名")
	}
	return sortedKeys(names)
}

// cloudAgentDispatchedToolNames 收集运行期两条分派路径覆盖的工具名：
// advanceCloudAgentTool（含审批预演、主执行 switch 与媒体/看图等特例分支）与
// cloudAgentReadTool（默认读取分派）。
func cloudAgentDispatchedToolNames(t *testing.T) []string {
	t.Helper()
	names := map[string]bool{}
	for _, target := range []struct {
		file string
		fn   string
	}{
		{"cloud_agent_runtime.go", "advanceCloudAgentTool"},
		{"cloud_agent_tools.go", "cloudAgentReadTool"},
	} {
		file := parseCloudAgentFile(t, target.file)
		found := false
		ast.Inspect(file, func(node ast.Node) bool {
			decl, ok := node.(*ast.FuncDecl)
			if !ok || decl.Name.Name != target.fn || decl.Body == nil {
				return true
			}
			found = true
			collectToolNameBranches(decl.Body, names)
			return false
		})
		if !found {
			t.Fatalf("%s 里找不到 %s：分派实现被重命名或搬走了，守卫需要同步更新", target.file, target.fn)
		}
	}
	if len(names) == 0 {
		t.Fatal("没有解析到任何分派分支的工具名")
	}
	return sortedKeys(names)
}

// collectToolNameBranches 收集两处分派写法涉及的工具名字面量：
//   - `call.Function.Name == "x"`（含嵌套在 && / || 里的）
//   - `switch call.Function.Name { case "x", "y": … }`
func collectToolNameBranches(body *ast.BlockStmt, out map[string]bool) {
	ast.Inspect(body, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.BinaryExpr:
			if typed.Op != token.EQL {
				return true
			}
			if literal, ok := comparedToolName(typed); ok {
				out[literal] = true
			}
		case *ast.SwitchStmt:
			if !isFunctionNameSelector(typed.Tag) {
				return true
			}
			for _, item := range typed.Body.List {
				clause, ok := item.(*ast.CaseClause)
				if !ok {
					continue
				}
				collectStringLiteralsOf(clause.List, out)
			}
		}
		return true
	})
}

// comparedToolName 判断 `X == "name"` 形式里 X 是不是 `…Function.Name`，返回字面量。
func comparedToolName(binary *ast.BinaryExpr) (string, bool) {
	for _, pair := range [][2]ast.Expr{{binary.X, binary.Y}, {binary.Y, binary.X}} {
		if !isFunctionNameSelector(pair[0]) {
			continue
		}
		if literal, ok := pair[1].(*ast.BasicLit); ok && literal.Kind == token.STRING {
			return strings.Trim(literal.Value, `"`), true
		}
	}
	return "", false
}

// isFunctionNameSelector 判断表达式是否是 `X.Function.Name`（工具调用名的统一读法）。
func isFunctionNameSelector(node ast.Node) bool {
	outer, ok := node.(*ast.SelectorExpr)
	if !ok || outer.Sel.Name != "Name" {
		return false
	}
	inner, ok := outer.X.(*ast.SelectorExpr)
	if !ok || inner.Sel.Name != "Function" {
		return false
	}
	_, ok = inner.X.(*ast.Ident)
	return ok
}

func collectStringLiterals(node ast.Node, out map[string]bool) {
	ast.Inspect(node, func(item ast.Node) bool {
		literal, ok := item.(*ast.BasicLit)
		if ok && literal.Kind == token.STRING {
			out[strings.Trim(literal.Value, `"`)] = true
		}
		return true
	})
}

func collectStringLiteralsOf(nodes []ast.Expr, out map[string]bool) {
	for _, node := range nodes {
		collectStringLiterals(node, out)
	}
}

func parseCloudAgentFile(t *testing.T, name string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
	if err != nil {
		t.Fatalf("解析 %s 失败：%v", name, err)
	}
	return file
}

func sortedKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func difference(from, without []string) []string {
	index := make(map[string]bool, len(without))
	for _, item := range without {
		index[item] = true
	}
	diff := make([]string, 0)
	for _, item := range from {
		if !index[item] {
			diff = append(diff, item)
		}
	}
	return diff
}
