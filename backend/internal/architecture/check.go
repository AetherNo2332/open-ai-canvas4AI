// Package architecture enforces size, complexity, dependency and protocol
// boundaries for backend production Go code using Go syntax trees. It parses
// sources without type checking and has no dependency on app, service, or a
// linter plugin. Size and protocol rules follow upstream ddcat
// backend/internal/agent/architecture; the layer rules follow AGENTS.md section 3.
package architecture

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	fileLineLimit     = 600
	functionLineLimit = 120
	complexityLimit   = 20
)

type Finding struct {
	ID     string `json:"id"`
	Rule   string `json:"rule"`
	Value  int    `json:"value"`
	Limit  int    `json:"limit"`
	Detail string `json:"detail"`
}

type Allowance struct {
	ID     string `json:"id"`
	Max    int    `json:"max"`
	Reason string `json:"reason"`
}

type Baseline struct {
	Version    int         `json:"version"`
	Allowances []Allowance `json:"allowances"`
}

type source struct {
	path    string
	pkg     string
	file    *ast.File
	imports map[string]string
	body    []byte
	// agent marks cloud Agent files; only they carry the exported-map rule.
	agent bool
}

type typeDecl struct {
	expr ast.Expr
	src  *source
}

type scanner struct {
	module string
	files  []*source
	types  map[string]typeDecl
	fset   *token.FileSet
}

// InScope reports whether a backend-relative Go file is production code this
// guard scans: everything in the backend module except tests, testdata, and the
// vendored third_party module.
func InScope(rel string) bool {
	rel = filepath.ToSlash(rel)
	if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
		return false
	}
	for _, part := range strings.Split(path.Dir(rel), "/") {
		if part == "testdata" || part == "third_party" || strings.HasPrefix(part, ".") {
			return false
		}
	}
	return true
}

// InAgentScope reports whether a backend-relative Go file is part of the cloud
// Agent code (AGENTS.md section 3). Agent files follow the backend rules and
// additionally may not expose untyped maps in exported APIs. Test files are
// matched by their production name.
func InAgentScope(rel string) bool {
	rel = filepath.ToSlash(rel)
	dir, base := path.Dir(rel), path.Base(rel)
	base = strings.TrimSuffix(base, "_test.go")
	if !strings.HasSuffix(base, ".go") {
		base += ".go"
	}
	match := func(pattern string) bool {
		ok, _ := path.Match(pattern, base)
		return ok
	}
	switch {
	case dir == "internal/app":
		return base == "cloud_agent.go" || match("cloud_agent_*.go") || match("agent_*.go")
	case dir == "internal/handler":
		return match("agent*.go") || base == "internal_agent.go"
	}
	for _, domain := range []string{"internal/agentcontext", "internal/agentmemory"} {
		if dir == domain || strings.HasPrefix(dir, domain+"/") {
			return true
		}
	}
	return false
}

// layerOf returns the internal top-level package a backend-relative path or
// import belongs to ("" outside internal/).
func layerOf(rel string) string {
	rel = filepath.ToSlash(rel)
	rest, ok := strings.CutPrefix(rel, "internal/")
	if !ok {
		return ""
	}
	return strings.Split(rest, "/")[0]
}

// layerViolation applies the dependency direction of AGENTS.md section 3
// (handler -> service -> app -> domain packages -> repository/model). It keeps
// the rule set of the earlier internal/architecture import test: handler stays
// off app/repository/database, repository and domain packages never reach back
// into handler/service/app, and model depends on no layer above it. Entry
// points outside internal/ (cmd, payment plugins) wire layers freely.
func layerViolation(from, to string) bool {
	upper := to == "handler" || to == "service" || to == "app"
	switch from {
	case "", "service", "app":
		return false
	case "handler":
		return to == "app" || to == "repository" || to == "database"
	case "model":
		return upper || to == "repository" || to == "database"
	default:
		return upper
	}
}

func DecodeBaseline(body []byte) (Baseline, error) {
	var baseline Baseline
	if err := json.Unmarshal(body, &baseline); err != nil {
		return baseline, err
	}
	if baseline.Version != 1 {
		return baseline, fmt.Errorf("baseline version must be 1")
	}
	seen := make(map[string]bool)
	for _, row := range baseline.Allowances {
		if row.ID == "" || strings.ContainsAny(row.ID, "*?") || row.Max < 1 || strings.TrimSpace(row.Reason) == "" || seen[row.ID] {
			return baseline, fmt.Errorf("invalid or duplicate baseline allowance %q", row.ID)
		}
		seen[row.ID] = true
	}
	return baseline, nil
}

// Check never writes or expands the baseline. Reductions must lower/remove an
// existing allowance; additions and increased values fail the check. To prevent
// changing code and allowance together, also run CompareBaselines against the
// explicit trusted Git base (cmd/check-architecture-baseline).
func Check(backendRoot string, baseline Baseline) ([]Finding, error) {
	metrics, err := Scan(backendRoot)
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]Allowance)
	for _, row := range baseline.Allowances {
		allowed[row.ID] = row
	}
	findings := []Finding{}
	for _, metric := range metrics {
		row, ok := allowed[metric.ID]
		delete(allowed, metric.ID)
		if !ok {
			findings = append(findings, metric)
			continue
		}
		if metric.Rule == "dependency" {
			findings = append(findings, metric)
			continue
		}
		if metric.Value > row.Max {
			metric.Limit = row.Max
			findings = append(findings, metric)
		}
		if metric.Value < row.Max {
			findings = append(findings, Finding{ID: metric.ID, Rule: "stale_baseline", Value: metric.Value, Limit: row.Max, Detail: "lower the existing baseline to the improved value; do not restore old debt"})
		}
	}
	for id, row := range allowed {
		findings = append(findings, Finding{ID: id, Rule: "stale_baseline", Limit: row.Max, Detail: "remove resolved baseline allowance"})
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].ID < findings[j].ID })
	return findings, nil
}

func Scan(backendRoot string) ([]Finding, error) {
	s, err := loadSources(backendRoot)
	if err != nil {
		return nil, err
	}
	out := []Finding{}
	for _, src := range s.files {
		out = append(out, s.fileFindings(src)...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func loadSources(root string) (*scanner, error) {
	module, err := moduleName(root)
	if err != nil {
		return nil, err
	}
	s := &scanner{module: module, fset: token.NewFileSet(), types: map[string]typeDecl{}}
	err = filepath.WalkDir(root, func(file string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(root, file)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			if rel != "." && (entry.Name() == "testdata" || entry.Name() == "third_party" || strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !InScope(rel) {
			return nil
		}
		content, readErr := os.ReadFile(file)
		if readErr != nil {
			return readErr
		}
		parsed, parseErr := parser.ParseFile(s.fset, file, content, 0)
		if parseErr != nil {
			return parseErr
		}
		pkg := module
		if dir := path.Dir(rel); dir != "." {
			pkg += "/" + dir
		}
		src := &source{path: rel, pkg: pkg, file: parsed, body: content, imports: map[string]string{}, agent: InAgentScope(rel)}
		for _, spec := range parsed.Imports {
			imported, _ := strconv.Unquote(spec.Path.Value)
			name := path.Base(imported)
			if spec.Name != nil {
				name = spec.Name.Name
			}
			src.imports[name] = imported
		}
		s.files = append(s.files, src)
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, src := range s.files {
		for _, decl := range src.file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gen.Specs {
				if typed, ok := spec.(*ast.TypeSpec); ok {
					s.types[src.pkg+"."+typed.Name.Name] = typeDecl{expr: typed.Type, src: src}
				}
			}
		}
	}
	return s, nil
}

func moduleName(root string) (string, error) {
	body, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module ")), nil
		}
	}
	return "", fmt.Errorf("go.mod module declaration missing")
}

func (s *scanner) fileFindings(src *source) []Finding {
	findings := s.dependencyFindings(src)
	findings = append(findings, s.facadeFindings(src)...)
	lines := len(strings.Split(strings.TrimSuffix(string(src.body), "\n"), "\n"))
	if lines > fileLineLimit {
		findings = append(findings, Finding{ID: src.path + ":file_lines", Rule: "file_lines", Value: lines, Limit: fileLineLimit, Detail: "production Go file exceeds 600 lines"})
	}
	for _, decl := range src.file.Decls {
		switch typed := decl.(type) {
		case *ast.FuncDecl:
			findings = append(findings, s.functionFindings(src, typed)...)
		case *ast.GenDecl:
			if src.agent {
				findings = append(findings, s.exportedTypeFindings(src, typed)...)
			}
			for _, spec := range typed.Specs {
				if value, ok := spec.(*ast.ValueSpec); ok && len(value.Names) > 0 {
					findings = append(findings, s.closureFindings(src, value.Names[0].Name, value)...)
				}
			}
		}
	}
	return findings
}

// dependencyFindings reports imports that cross the layer order. Dependency
// findings can never be baselined: a violating import must be removed.
func (s *scanner) dependencyFindings(src *source) []Finding {
	findings := []Finding{}
	from := layerOf(src.path)
	for _, spec := range src.file.Imports {
		imported, _ := strconv.Unquote(spec.Path.Value)
		if detail, banned := handlerPersistenceViolation(src, imported); banned {
			findings = append(findings, Finding{ID: src.path + ":import:" + imported, Rule: "dependency", Value: 1, Detail: detail})
		}
		rest, ok := strings.CutPrefix(imported, s.module+"/")
		if !ok {
			continue
		}
		if to := layerOf(rest); layerViolation(from, to) {
			findings = append(findings, Finding{ID: src.path + ":import:" + imported, Rule: "dependency", Value: 1, Detail: "internal/" + from + " must not import internal/" + to + " (AGENTS.md section 3 call chain)"})
		}
		if detail, banned := s.agentMemoryRootImportViolation(src, rest); banned {
			findings = append(findings, Finding{ID: src.path + ":import:" + imported, Rule: "dependency", Value: 1, Detail: detail})
		}
	}
	return findings
}

// agentMemoryRootImportViolation keeps internal/agentmemory a pure rule package:
// persistence stays with agentmemory/document and the service layer, and the
// document implementation must not flow back into the root package.
// imported 是去掉模块前缀的内部路径（如 "internal/repository"）。
func (s *scanner) agentMemoryRootImportViolation(src *source, imported string) (string, bool) {
	if src.pkg != s.module+"/internal/agentmemory" {
		return "", false
	}
	for _, banned := range []string{"internal/repository", "internal/database", "internal/agentmemory/document"} {
		if imported == banned {
			return "internal/agentmemory is a pure rule package and must not import " + banned, true
		}
	}
	return "", false
}
