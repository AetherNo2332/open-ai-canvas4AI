// Command check-architecture-baseline compares debt with an explicit Git base.
package main

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"yingce/backend/internal/architecture"
)

func main() {
	base := flag.String("base", "", "trusted Git base revision (required)")
	flag.Parse()
	if err := run(*base); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(base string) error {
	if base == "" || strings.Trim(base, "0") == "" || strings.HasPrefix(base, "-") {
		return fmt.Errorf("a nonzero trusted --base revision is required")
	}
	root, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return fmt.Errorf("Git root unavailable")
	}
	backend := filepath.Join(strings.TrimSpace(string(root)), "backend")
	previous, err := exec.Command("git", "show", base+":backend/internal/architecture/baseline.json").Output()
	if err != nil {
		return fmt.Errorf("base baseline unavailable: %s", base)
	}
	old, err := architecture.DecodeBaseline(previous)
	if err != nil {
		return err
	}
	body, err := os.ReadFile(filepath.Join(backend, "internal/architecture/baseline.json"))
	if err != nil {
		return err
	}
	current, err := architecture.DecodeBaseline(body)
	if err != nil {
		return err
	}
	body, err = os.ReadFile(filepath.Join(backend, "internal/architecture/baseline-moves.json"))
	if err != nil {
		return err
	}
	var moves []architecture.BaselineMove
	if err = json.Unmarshal(body, &moves); err != nil {
		return err
	}
	findings := architecture.CompareBaselines(old, current, moves)
	if len(moves) > 0 {
		before, scanErr := scanRevision(base)
		if scanErr != nil {
			return scanErr
		}
		after, scanErr := architecture.Scan(backend)
		if scanErr != nil {
			return scanErr
		}
		findings = architecture.CheckBaselineMoves(old, current, moves, before, after)
	}
	checked, err := architecture.Check(backend, current)
	if err != nil {
		return err
	}
	findings = append(findings, checked...)
	for _, f := range findings {
		fmt.Printf("%s rule=%s value=%d limit=%d: %s\n", f.ID, f.Rule, f.Value, f.Limit, f.Detail)
	}
	if len(findings) > 0 {
		return fmt.Errorf("architecture baseline rejected: %d findings", len(findings))
	}
	fmt.Println("Architecture baseline: unchanged or reduced; workspace scan passed")
	return nil
}

func scanRevision(base string) ([]architecture.Finding, error) {
	archive, err := exec.Command("git", "archive", base, "backend").Output()
	if err != nil {
		return nil, fmt.Errorf("base sources unavailable")
	}
	dir, err := os.MkdirTemp("", "architecture-base-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	reader := tar.NewReader(bytes.NewReader(archive))
	for {
		h, e := reader.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
		clean := filepath.Clean(filepath.FromSlash(h.Name))
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("unsafe archive path")
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		target := filepath.Join(dir, clean)
		if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return nil, err
		}
		data, e := io.ReadAll(reader)
		if e != nil {
			return nil, e
		}
		if e = os.WriteFile(target, data, 0600); e != nil {
			return nil, e
		}
	}
	return architecture.Scan(filepath.Join(dir, "backend"))
}
