package app

import (
	"testing"

	"infinite-canvas/backend/internal/model"
)

// TestResolveProviderConfigAttachesAuthoritativeCapability pins the trust boundary
// for channel-model capabilities.
//
// `task_creation.go` deliberately strips the client-supplied `capabilityConfig`
// (the right call), so by the time a task executes the config carries no
// capability at all. If `resolveProviderConfig` does not put the *selected channel
// model's* capability back, the execution path sees nil, and `provider.go`'s
// `supportsStream` treats "not declared" as "streaming supported" — a channel model
// explicitly configured with `streaming: false` would still be sent `stream=true`.
// The model's physical output ceiling is lost the same way.
func TestResolveProviderConfigAttachesAuthoritativeCapability(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	// 夹具渠道默认没有 base URL，而 resolveProviderConfig 会先校验出站地址。
	// 与仓库既有用例一致：显式放行回环主机，避免测试依赖真实 DNS。
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	if err := db.Model(&model.ModelChannel{}).Where("id = ?", "channel").
		Update("base_url", "http://127.0.0.1:8080").Error; err != nil {
		t.Fatal(err)
	}
	streaming := false
	profile := DefaultModelCapabilityConfigForModel(string(model.ChannelInterfaceChatCompletion), "text-test")
	profile.Text.Streaming = &streaming
	profile.Text.MaxOutputTokens = 5_000
	if err := db.Model(&model.ChannelModel{}).Where("id = ?", "cm").
		Update("capability_config_json", mustEncodeModelCapabilityConfig(t, profile)).Error; err != nil {
		t.Fatal(err)
	}

	// 客户端只给渠道与模型键：与 task_creation 剔除 capabilityConfig 之后的形态一致。
	resolved, err := s.resolveProviderConfig(providerConfig{ChannelID: "channel", ChannelModelKey: "text-test"})
	if err != nil {
		t.Fatalf("resolveProviderConfig() error = %v", err)
	}
	if resolved.CapabilityConfig == nil || resolved.CapabilityConfig.Text == nil {
		t.Fatal("authoritative capability was not attached to the resolved config")
	}
	if resolved.CapabilityConfig.Text.Streaming == nil || *resolved.CapabilityConfig.Text.Streaming {
		t.Fatalf("streaming=false declared by the channel model was lost: %#v",
			resolved.CapabilityConfig.Text.Streaming)
	}
	if resolved.CapabilityConfig.Text.MaxOutputTokens != 5_000 {
		t.Fatalf("MaxOutputTokens = %d, want 5000", resolved.CapabilityConfig.Text.MaxOutputTokens)
	}

	// 同一权威能力必须能驱动 SSE 放行判据：streaming=false 时不得开启流式。
	requestedStream := true
	supportsStream := resolved.CapabilityConfig == nil ||
		resolved.CapabilityConfig.Text == nil ||
		resolved.CapabilityConfig.Text.Streaming == nil ||
		*resolved.CapabilityConfig.Text.Streaming
	if requestedStream && supportsStream {
		t.Fatal("a channel model with streaming=false must not be treated as stream-capable")
	}
}

// TestResolveProviderConfigFailsClosedOnBrokenCapability: a corrupt capability
// config must not degrade into "no capability declared", which would silently
// re-open the SSE fail-open above. Same policy as task_creation.go.
func TestResolveProviderConfigFailsClosedOnBrokenCapability(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	if err := db.Model(&model.ModelChannel{}).Where("id = ?", "channel").
		Update("base_url", "http://127.0.0.1:8080").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.ChannelModel{}).Where("id = ?", "cm").
		Update("capability_config_json", "{").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.resolveProviderConfig(providerConfig{ChannelID: "channel", ChannelModelKey: "text-test"}); err == nil {
		t.Fatal("a corrupt capability config must fail closed, not degrade to nil")
	}
}
