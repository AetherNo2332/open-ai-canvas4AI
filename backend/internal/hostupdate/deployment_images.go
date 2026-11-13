package hostupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type deploymentImages struct {
	backend string
	web     string
	agent   string
	docs    string
}

func immutableImageRefs(repository, version string) deploymentImages {
	owner := strings.ToLower(strings.SplitN(repository, "/", 2)[0])
	tag := strings.TrimPrefix(version, "v")
	return deploymentImages{
		backend: "ghcr.io/" + owner + "/open-ai-canvas-backend:" + tag,
		web:     "ghcr.io/" + owner + "/open-ai-canvas-web:" + tag,
		agent:   "ghcr.io/" + owner + "/open-ai-canvas-agent:" + tag,
	}
}

func (m *Manager) compose(composePath, imageTag string, timeout time.Duration, stdout io.Writer, arguments ...string) error {
	return m.composeWithImages(composePath, imageTag, deploymentImages{}, timeout, stdout, arguments...)
}

func (m *Manager) composeWithImages(composePath, imageTag string, images deploymentImages, timeout time.Duration, stdout io.Writer, arguments ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	envFile := m.envPath()
	if images.backend != "" {
		var err error
		envFile, err = m.writeComposeEnvOverride(images)
		if err != nil {
			return fmt.Errorf("准备目标 Compose 环境：%w", err)
		}
		defer os.Remove(envFile)
	}
	args := []string{"compose", "--project-name", composeProjectName(m.config.InstallDir), "--env-file", envFile, "-f", composePath}
	args = append(args, arguments...)
	var stderr bytes.Buffer
	if stdout == nil {
		stdout = io.Discard
	}
	environment := []string{"CANVAS_IMAGE_TAG=" + strings.TrimPrefix(imageTag, "v")}
	err := m.runner.Run(ctx, "docker", args, environment, stdout, &stderr)
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if len(message) > 1000 {
			message = message[len(message)-1000:]
		}
		if message != "" {
			return fmt.Errorf("docker compose %s：%s", strings.Join(arguments, " "), message)
		}
		return fmt.Errorf("docker compose %s：%w", strings.Join(arguments, " "), err)
	}
	return nil
}

// Older deployments may not declare an agent service yet.
func (m *Manager) stopApplicationServices(version string, timeout time.Duration) error {
	var services bytes.Buffer
	if err := m.compose(m.composePath(), version, timeout, &services, "config", "--services"); err != nil {
		return err
	}
	args := []string{"stop", "web"}
	for _, service := range strings.Fields(services.String()) {
		if service == "agent" {
			args = append(args, "agent")
			break
		}
	}
	args = append(args, "backend")
	return m.compose(m.composePath(), version, timeout, nil, args...)
}

// startApplicationServices 在迁移前失败时恢复已停止的旧服务；只启动编排里存在的 agent 服务。
func (m *Manager) startApplicationServices(version string, timeout time.Duration) error {
	var services bytes.Buffer
	if err := m.compose(m.composePath(), version, timeout, &services, "config", "--services"); err != nil {
		return err
	}
	args := []string{"start", "backend"}
	for _, service := range strings.Fields(services.String()) {
		if service == "agent" {
			args = append(args, "agent")
			break
		}
	}
	args = append(args, "web")
	return m.compose(m.composePath(), version, timeout, nil, args...)
}

func composeProjectName(installDir string) string {
	name := filepath.Base(filepath.Clean(installDir))
	if name == "." || name == string(filepath.Separator) || name == "" {
		return "open-ai-canvas"
	}
	var builder strings.Builder
	for _, char := range strings.ToLower(name) {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' || char == '_' {
			builder.WriteRune(char)
		}
	}
	if result := strings.Trim(builder.String(), "-_ "); result != "" {
		return result
	}
	return "open-ai-canvas"
}

func (m *Manager) writeComposeEnvOverride(images deploymentImages) (string, error) {
	data, err := os.ReadFile(m.envPath())
	if err != nil {
		return "", err
	}
	path := filepath.Join(m.config.StateDir, ".compose-env-override-"+randomID())
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	cleanup := func() { _ = os.Remove(path) }
	if err := setEnvValue(path, "CANVAS_BACKEND_IMAGE", images.backend); err != nil {
		cleanup()
		return "", err
	}
	if err := setEnvValue(path, "CANVAS_WEB_IMAGE", images.web); err != nil {
		cleanup()
		return "", err
	}
	if err := setEnvValue(path, "CANVAS_AGENT_IMAGE", images.agent); err != nil {
		cleanup()
		return "", err
	}
	if images.docs != "" {
		if err := setEnvValue(path, "CANVAS_DOCS_IMAGE", images.docs); err != nil {
			cleanup()
			return "", err
		}
	}
	return path, nil
}

func (m *Manager) verifyImages(targetVersion string) (deploymentImages, error) {
	images := immutableImageRefs(m.config.Repository, targetVersion)
	refs := []string{images.backend, images.web, images.agent}
	digests := make([]string, 0, len(refs))
	for _, image := range refs {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		var output bytes.Buffer
		var stderr bytes.Buffer
		err := m.runner.Run(ctx, "docker", []string{"image", "inspect", image, "--format", "{{json .RepoDigests}}"}, nil, &output, &stderr)
		cancel()
		if err != nil {
			return deploymentImages{}, fmt.Errorf("校验目标镜像摘要失败：%s", strings.TrimSpace(stderr.String()))
		}
		var repositoryDigests []string
		if err := json.Unmarshal(output.Bytes(), &repositoryDigests); err != nil {
			return deploymentImages{}, fmt.Errorf("解析目标镜像摘要失败：%w", err)
		}
		repository := strings.Split(image, ":")[0]
		var digest string
		for _, candidate := range repositoryDigests {
			if strings.HasPrefix(candidate, repository+"@sha256:") {
				digest = candidate
				break
			}
		}
		if digest == "" {
			return deploymentImages{}, fmt.Errorf("目标镜像 %s 未包含仓库摘要", image)
		}
		digests = append(digests, digest)
	}
	return deploymentImages{backend: digests[0], web: digests[1], agent: digests[2]}, nil
}

func setDeploymentImages(path, version string, images deploymentImages) error {
	if err := setEnvValue(path, "CANVAS_IMAGE_TAG", strings.TrimPrefix(version, "v")); err != nil {
		return err
	}
	if err := setEnvValue(path, "CANVAS_BACKEND_IMAGE", images.backend); err != nil {
		return err
	}
	if err := setEnvValue(path, "CANVAS_WEB_IMAGE", images.web); err != nil {
		return err
	}
	if err := setEnvValue(path, "CANVAS_AGENT_IMAGE", images.agent); err != nil {
		return err
	}
	if images.docs != "" {
		return setEnvValue(path, "CANVAS_DOCS_IMAGE", images.docs)
	}
	return nil
}
