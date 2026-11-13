package hostupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/goccy/go-yaml"
	"os"
)

type docsComposeContract struct {
	Services map[string]struct {
		Labels map[string]string `yaml:"labels"`
	} `yaml:"services"`
}
type docsReleaseManifest struct {
	SchemaVersion int `json:"schemaVersion"`
	DocsBuild
}

func docsContract(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	var config docsComposeContract
	if err = yaml.Unmarshal(data, &config); err != nil {
		return false, err
	}
	service, exists := config.Services["docs"]
	if exists && service.Labels["tv.yingce.docs.contract"] != "1" {
		return true, errors.New("目标文档合同不受支持，请先安排文档合同升级")
	}
	return exists, nil
}
func (m *Manager) applicationDocs(path, version string) (string, error) {
	exists, err := docsContract(path)
	if err != nil || !exists {
		return "", err
	}
	values, err := readEnvFile(m.envPath())
	if err != nil {
		return "", err
	}
	if current := values["CANVAS_DOCS_IMAGE"]; current != "" {
		ctx, cancel := context.WithTimeout(context.Background(), m.config.StepTimeout)
		defer cancel()
		if _, err = m.inspectDocs(ctx, current); err != nil {
			return "", err
		}
		return current, nil
	}
	endpoint := fmt.Sprintf("https://github.com/%s/releases/download/%s/docs-image.json", m.config.Repository, version)
	data, err := m.downloadReleaseAsset(endpoint, 256<<10)
	if err != nil {
		return "", err
	}
	var manifest docsReleaseManifest
	if err = json.Unmarshal(data, &manifest); err != nil {
		return "", err
	}
	if manifest.SchemaVersion != 1 || manifest.DeploymentContract != 1 {
		return "", errors.New("文档快照合同无效")
	}
	if err = m.validDocsDigest(manifest.Digest); err != nil {
		return "", err
	}
	return manifest.Digest, nil
}
func (m *Manager) startBusiness(path, version string, images deploymentImages) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var config docsComposeContract
	if err = yaml.Unmarshal(data, &config); err != nil {
		return err
	}
	args := []string{"up", "-d", "--remove-orphans", "--wait", "--wait-timeout", "600", "backend", "web"}
	if _, exists := config.Services["agent"]; exists {
		args = append(args, "agent")
	}
	return m.composeWithImages(path, version, images, m.config.StepTimeout, nil, args...)
}
func (m *Manager) syncApplicationDocs() error {
	exists, err := docsContract(m.composePath())
	if err != nil {
		return err
	}
	if !exists {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.docs.Supported = false
		m.docs.Current = nil
		return m.saveDocsLocked()
	}
	values, err := readEnvFile(m.envPath())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), m.config.StepTimeout)
	defer cancel()
	build, err := m.inspectDocs(ctx, values["CANVAS_DOCS_IMAGE"])
	if err != nil {
		return err
	}
	if err = m.recreateDocs(); err != nil {
		return err
	}
	if err = m.verifyDocs(build); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.docs.Current != nil && m.docs.Current.Digest != build.Digest {
		m.docs.Previous = m.docs.Current
	}
	m.docs.Supported = true
	m.docs.Current = &build
	m.docs.Operation = DocsOperation{Phase: PhaseSucceeded}
	return m.saveDocsLocked()
}
