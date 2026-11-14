package hostupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var docsDigestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
var docsCommitPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)

func (m *Manager) docsRepository() string {
	return "ghcr.io/" + strings.ToLower(strings.SplitN(m.config.Repository, "/", 2)[0]) + "/open-ai-canvas-docs"
}
func (m *Manager) validDocsDigest(image string) error {
	prefix := m.docsRepository() + "@"
	if !strings.HasPrefix(image, prefix) || !docsDigestPattern.MatchString(strings.TrimPrefix(image, prefix)) {
		return errors.New("文档镜像必须为配置仓库的固定 digest")
	}
	return nil
}

func (m *Manager) docsDocker(ctx context.Context, args ...string) ([]byte, error) {
	var out bytes.Buffer
	// Docker may include credentials in diagnostics: do not persist raw stderr.
	if err := m.runner.Run(ctx, "docker", args, nil, &out, io.Discard); err != nil {
		return nil, fmt.Errorf("文档 Docker 操作失败 (%s)", args[0])
	}
	return out.Bytes(), nil
}

func (m *Manager) inspectDocs(ctx context.Context, image string) (DocsBuild, error) {
	var build DocsBuild
	if err := m.validDocsDigest(image); err != nil {
		return build, err
	}
	data, err := m.docsDocker(ctx, "image", "inspect", image, "--format", "{{json .Config.Labels}}")
	if err != nil {
		return build, err
	}
	var labels struct {
		Build string `json:"tv.yingce.docs.build"`
	}
	if err = json.Unmarshal(data, &labels); err != nil {
		return build, err
	}
	if err = json.Unmarshal([]byte(labels.Build), &build); err != nil {
		return build, errors.New("文档镜像缺少构建身份")
	}
	if build.DeploymentContract != 1 || !docsCommitPattern.MatchString(build.Commit) || build.BuildID != "docs-"+build.Commit {
		return build, errors.New("文档构建身份或部署合同不受支持")
	}
	if _, err = time.Parse(time.RFC3339, build.BuiltAt); err != nil {
		return build, errors.New("文档构建时间无效")
	}
	build.Digest = image
	return build, nil
}

func (m *Manager) recreateDocs() error {
	values, err := readEnvFile(m.envPath())
	if err != nil {
		return err
	}
	return m.compose(m.composePath(), values["CANVAS_IMAGE_TAG"], m.config.StepTimeout, nil,
		"--profile", "docs", "up", "-d", "--no-deps", "--wait", "--wait-timeout", "180", "docs")
}

func (m *Manager) verifyDocs(build DocsBuild) error {
	root, err := url.Parse(m.healthURL())
	if err != nil {
		return err
	}
	root.Path = "/docs"
	root.RawQuery = ""
	root.Fragment = ""
	ctx, cancel := context.WithTimeout(context.Background(), m.config.StepTimeout)
	defer cancel()
	var last error
	for {
		last = m.verifyDocsOnce(ctx, root.String(), build)
		if last == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return last
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func (m *Manager) verifyDocsOnce(ctx context.Context, base string, build DocsBuild) error {
	health, err := m.docsGET(ctx, base+"/api/health")
	if err != nil {
		return err
	}
	var actual DocsBuild
	if err = json.Unmarshal(health, &actual); err != nil {
		return errors.New("文档健康响应不是 JSON")
	}
	if actual.Status != "ok" || actual.BuildID != build.BuildID || actual.Commit != build.Commit || actual.BuiltAt != build.BuiltAt || actual.DeploymentContract != 1 {
		return errors.New("运行的文档构建身份与目标不符")
	}
	body, err := m.docsGET(ctx, base+"/backend/creation-platform-sdk")
	if err != nil {
		return err
	}
	if !bytes.Contains(bytes.ToLower(body), []byte("<html")) {
		return errors.New("文档正文不可用")
	}
	search, err := m.docsGET(ctx, base+"/api/search?query=SDK")
	if err != nil {
		return err
	}
	var results []json.RawMessage
	if json.Unmarshal(search, &results) != nil || len(results) == 0 {
		return errors.New("文档搜索响应无效")
	}
	return nil
}

func (m *Manager) docsGET(ctx context.Context, endpoint string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Cache-Control", "no-cache")
	response, err := m.httpClient.Do(request)
	if err != nil {
		return nil, errors.New("文档服务不可连接")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("文档检查返回 HTTP %d", response.StatusCode)
	}
	return io.ReadAll(io.LimitReader(response.Body, 4<<20))
}
