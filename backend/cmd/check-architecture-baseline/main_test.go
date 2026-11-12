package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRejectsMissingBase(t *testing.T) {
	for _, base := range []string{"", "0000000000000000000000000000000000000000", "missing-revision-for-test"} {
		if run(base) == nil {
			t.Fatalf("accepted %q", base)
		}
	}
}

func TestMoveScansBothGitRevisions(t *testing.T) {
	root := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		target := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init")
	git("config", "user.email", "fixture@example.invalid")
	git("config", "user.name", "Fixture")
	write("backend/go.mod", "module yingce/backend\n\ngo 1.25.0\n")
	code := "package canvas\n" + strings.Repeat("// existing debt\n", 600)
	write("backend/internal/canvas/old.go", code)
	baseline := func(file string) string {
		return `{"version":1,"allowances":[{"id":"internal/canvas/` + file + `.go:file_lines","max":601,"reason":"existing"}]}`
	}
	write("backend/internal/architecture/baseline.json", baseline("old"))
	git("add", ".")
	git("commit", "-m", "base")
	base := git("rev-parse", "HEAD")
	if err := os.Rename(filepath.Join(root, "backend/internal/canvas/old.go"), filepath.Join(root, "backend/internal/canvas/new.go")); err != nil {
		t.Fatal(err)
	}
	write("backend/internal/architecture/baseline.json", baseline("new"))
	write("backend/internal/architecture/baseline-moves.json", `[{"from":"internal/canvas/old.go:file_lines","to":"internal/canvas/new.go:file_lines","reason":"extract"}]`)
	before, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(before)
	if err = run(base); err != nil {
		t.Fatal(err)
	}
	write("backend/internal/canvas/old.go", code)
	if run(base) == nil {
		t.Fatal("move accepted while old debt remains")
	}
}
