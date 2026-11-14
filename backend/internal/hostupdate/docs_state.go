package hostupdate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

func writeDocsJSON(path string, value DocsStatus) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".docs-*.json")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, path)
}

func (m *Manager) docsPath(name string) string { return filepath.Join(m.config.StateDir, name) }
func (m *Manager) saveDocsLocked() error {
	// Journal first: a crash between journal and snapshot still has an authoritative record.
	if err := writeDocsJSON(m.docsPath("docs-operation.json"), m.docs); err != nil {
		return err
	}
	if err := writeDocsJSON(m.docsPath("docs-state.json"), m.docs); err != nil {
		return err
	}
	if !m.docsBusyLocked() {
		return os.Remove(m.docsPath("docs-operation.json"))
	}
	return nil
}

func (m *Manager) loadDocsState() error {
	m.docs.Operation.Phase = PhaseIdle
	for _, name := range []string{"docs-state.json", "docs-operation.json"} {
		data, err := os.ReadFile(m.docsPath(name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if err = json.Unmarshal(data, &m.docs); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) docsBusyLocked() bool {
	return m.docs.Operation.Phase.Active() || m.docs.Operation.Phase == PhaseChecking || m.docs.Operation.Phase == PhaseManualIntervention
}

func cloneDocsStatus(value DocsStatus) DocsStatus {
	clone := func(build *DocsBuild) *DocsBuild {
		if build == nil {
			return nil
		}
		copy := *build
		return &copy
	}
	value.Current = clone(value.Current)
	value.Previous = clone(value.Previous)
	value.Candidate = clone(value.Candidate)
	value.Operation.Source = clone(value.Operation.Source)
	value.Operation.Target = clone(value.Operation.Target)
	return value
}

func (m *Manager) DocsSnapshot() DocsStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	status := cloneDocsStatus(m.docs)
	values, err := readEnvFile(m.envPath())
	exists, contractErr := docsContract(m.composePath())
	status.Supported = exists && contractErr == nil && err == nil && m.validDocsDigest(values["CANVAS_DOCS_IMAGE"]) == nil
	if status.Supported && (status.Current == nil || status.Current.Digest != values["CANVAS_DOCS_IMAGE"]) {
		status.Current = &DocsBuild{Digest: values["CANVAS_DOCS_IMAGE"]}
	}
	return status
}
