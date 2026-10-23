package architecture

import (
	"bufio"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const module = "infinite-canvas/backend/internal/"

// Keep legacy violations explicit by source file and imported package. New edges fail.
func forbiddenImports(filename string, source any) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), filename, source, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	var violations []string
	for _, spec := range f.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(path, module) {
			continue
		}
		to := strings.Split(strings.TrimPrefix(path, module), "/")[0]
		from := strings.Split(filepath.ToSlash(filename), "/")[0]
		forbidden := (from == "handler" && (to == "repository" || to == "database" || to == "app")) ||
			(from == "repository" && (to == "handler" || to == "service" || to == "app")) ||
			(from == "model" && (to == "handler" || to == "service" || to == "app" || to == "repository" || to == "database")) ||
			(from != "handler" && from != "service" && from != "app" && from != "repository" && from != "model" && (to == "handler" || to == "service" || to == "app"))
		if forbidden {
			violations = append(violations, filepath.ToSlash(filename)+" -> "+path)
		}
	}
	return violations, nil
}

func TestDependencyBaseline(t *testing.T) {
	root := ".."
	var actual []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		violations, err := forbiddenImports(rel, source)
		if err != nil {
			return err
		}
		actual = append(actual, violations...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(actual)
	baseline, err := os.Open("dependency-baseline.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer baseline.Close()
	var expected []string
	scan := bufio.NewScanner(baseline)
	for scan.Scan() {
		if line := strings.TrimSpace(scan.Text()); line != "" && !strings.HasPrefix(line, "#") {
			expected = append(expected, line)
		}
	}
	if err := scan.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(expected)
	if strings.Join(actual, "\n") != strings.Join(expected, "\n") {
		t.Errorf("dependency baseline differs; update only after reviewing each exact edge\nactual:\n%s\nexpected:\n%s", strings.Join(actual, "\n"), strings.Join(expected, "\n"))
	}
}

func TestDependencyRuleFixtures(t *testing.T) {
	for _, fixture := range []struct {
		file, src string
		reject    bool
	}{
		{"handler/bad.go", `package handler; import "infinite-canvas/backend/internal/repository"`, true},
		{"canvas/bad.go", `package canvas; import "infinite-canvas/backend/internal/app"`, true},
		{"repository/bad.go", `package repository; import "infinite-canvas/backend/internal/service"`, true},
		{"model/bad.go", `package model; import "infinite-canvas/backend/internal/database"`, true},
		{"handler/good.go", `package handler; import "infinite-canvas/backend/internal/service"`, false},
		{"app/good.go", `package app; import "infinite-canvas/backend/internal/platform"`, false},
	} {
		got, err := forbiddenImports(fixture.file, fixture.src)
		if err != nil {
			t.Fatal(err)
		}
		if (len(got) != 0) != fixture.reject {
			t.Errorf("%s: violations=%v reject=%v", fixture.file, got, fixture.reject)
		}
	}
}
