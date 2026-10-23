package hostupdate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDeploymentImagesIncludeAgent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("CANVAS_AGENT_INTERNAL_TOKEN=preserved-test-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	images := immutableImageRefs("AetherNo2332/open-ai-canvas4AI", "v1.6.0")
	if err := setDeploymentImages(path, "v1.6.0", images); err != nil {
		t.Fatal(err)
	}
	manager := &Manager{config: Config{InstallDir: dir, EnvFile: ".env", StateDir: dir}}
	override, err := manager.writeComposeEnvOverride(images)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(override)
	for _, file := range []string{path, override} {
		values, err := readEnvFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if got := values["CANVAS_AGENT_IMAGE"]; got != "ghcr.io/aetherno2332/open-ai-canvas-agent:1.6.0" {
			t.Errorf("agent image = %q", got)
		}
		if values["CANVAS_AGENT_INTERNAL_TOKEN"] != "preserved-test-token" {
			t.Error("internal token changed")
		}
	}
}

type agentDeploymentRunner struct {
	services   string
	failConfig bool
	calls      []string
}

func (r *agentDeploymentRunner) Run(_ context.Context, _ string, args, _ []string, stdout, _ io.Writer) error {
	call := strings.Join(args, " ")
	r.calls = append(r.calls, call)
	if strings.HasSuffix(call, "config --services") {
		if r.failConfig {
			return errors.New("invalid deployment config")
		}
		_, err := io.WriteString(stdout, r.services)
		return err
	}
	if len(args) > 2 && args[0] == "image" && args[1] == "inspect" {
		_, err := fmt.Fprintf(stdout, `["%s@sha256:%s"]`, strings.Split(args[2], ":")[0], strings.Repeat("a", 64))
		return err
	}
	return nil
}

func TestStopApplicationServicesIncludesAvailableAgent(t *testing.T) {
	for _, tc := range []struct {
		name, services, want string
		fail                 bool
	}{
		{"agent", "postgres\nredis\nbackend\nagent\nweb\n", "stop web agent backend", false},
		{"legacy", "postgres\nredis\nbackend\nweb\n", "stop web backend", false},
		{"invalid-config", "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &agentDeploymentRunner{services: tc.services, failConfig: tc.fail}
			manager := &Manager{config: Config{InstallDir: t.TempDir(), EnvFile: ".env"}, runner: runner}
			err := manager.stopApplicationServices("v1.6.0", time.Second)
			if tc.fail {
				if err == nil || len(runner.calls) != 1 {
					t.Fatalf("invalid config did not stop safely: %v, %v", err, runner.calls)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(runner.calls) != 2 || !strings.HasSuffix(runner.calls[1], tc.want) {
				t.Fatalf("unexpected service stop: %v", runner.calls)
			}
		})
	}
}

func TestVerifyDeploymentPinsAgentImage(t *testing.T) {
	runner := &agentDeploymentRunner{}
	manager := &Manager{config: Config{Repository: "AetherNo2332/open-ai-canvas4AI"}, runner: runner}
	images, err := manager.verifyImages("v1.6.0")
	if err != nil {
		t.Fatal(err)
	}
	if images.agent != "ghcr.io/aetherno2332/open-ai-canvas-agent@sha256:"+strings.Repeat("a", 64) {
		t.Fatalf("agent image was not pinned: %q", images.agent)
	}
}
