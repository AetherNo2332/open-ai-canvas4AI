package app

import (
	"strings"
	"testing"
)

// TestPiVisionNextStepRejectsResourcePlaceholder 是 M-11 的**最小复现**：
// "生成图片节点 → canvas_inspect_image → 下一个模型请求" 这条链在 Pi 路径上会自断。
//
// 三段证据串起来（每段都有 file:line）：
//
//  1. `cloudAgentReference` 要求节点 `metadata.storageKey` 以 `resource:` 开头，
//     否则直接拒绝（cloud_agent_media.go:252-255），并把**同一个 key** 作为
//     `reference["storageKey"]` 返回（:263）。
//  2. `cloudAgentImageInspection.ImageURL = stringValue(reference["storageKey"])`
//     （cloud_agent_vision.go:261），`cloudAgentImageContentParts` 把它写成
//     `image_url.url`（同文件 :897）。
//  3. `cloudAgentFlushPendingImages` 把这条消息**追加进 `state.Canonical.Messages`**
//     （同文件 :946），而它的调用者包含 `advanceCloudAgentTool`
//     （cloud_agent_runtime.go:1598）—— 那是 Pi 路径可达的。
//
// 于是下一个 `PiModelStep` 送出的 `agentRequests.canonical` 里含有 `resource:<id>`，
// 而 `PiModelStep` 从不设置 `referenceImages`（grep 为空），
// `resolveAgentResourcePlaceholders` 因此找不到白名单条目，
// 返回 `BadAuthRequest("模型协议引用了未获准的图片")` → 该模型步失败 → 整轮 failed。
//
// 这个测试不依赖模型、网络或数据库：它直接锁住"占位符形态"与"白名单准入"之间的断裂。
func TestPiVisionNextStepRejectsResourcePlaceholder(t *testing.T) {
	const storageKey = "resource:9f1c0f7a4b2d4e0a8c3b5d6e7f801234"

	// 第 2 段：看图结果被拼成模型可见的内容数组时，url 就是那个 resource: 键。
	parts := cloudAgentImageContentParts(cloudAgentImageInspection{
		Receipt:  map[string]any{"nodeId": "node-image", "title": "生成图"},
		ImageURL: storageKey,
	})
	var imageURL string
	for _, part := range parts {
		entry, ok := part.(map[string]any)
		if !ok || stringValue(entry["type"]) != "image_url" {
			continue
		}
		nested, _ := entry["image_url"].(map[string]any)
		imageURL = stringValue(nested["url"])
	}
	if imageURL != storageKey {
		t.Fatalf("看图内容数组里的 url = %q，期望 %q（说明图片是以 resource: 占位符下发的）", imageURL, storageKey)
	}

	// 第 3 段：这条消息进了 canonical，随 PiModelStep 的 agentRequests 一起发出。
	canonical := &canonicalAgentRequest{
		SystemPrompt: "probe",
		Messages: []map[string]any{
			{"role": "user", "content": parts},
		},
	}

	// PiModelStep 的输入形态：有 agentRequests.canonical，没有 referenceImages。
	stepInput := canvasGenerationInput{
		Mode:          "text",
		AgentRequests: &agentToolRequests{Canonical: canonical},
	}
	if _, err := resolveAgentResourcePlaceholders(stepInput, true); err == nil {
		t.Fatal("下一步模型请求接受了未列入白名单的 resource: 图片 —— 若这里通过，说明断裂已被修复，请把本用例改为断言成功并补上白名单装配")
	} else if !strings.Contains(err.Error(), "未获准的图片") {
		t.Fatalf("拒绝原因不是占位符白名单: %v", err)
	}

	// 定义修复形态：把该图的资源加入 referenceImages 后，同一个 canonical 必须能通过。
	fixed := stepInput
	fixed.ReferenceImages = []providerMedia{{
		ID: "node-image", StorageKey: storageKey,
		Type: "image/png", MimeType: "image/png", URL: "https://example.test/generated.png",
	}}
	if _, err := resolveAgentResourcePlaceholders(fixed, true); err != nil {
		t.Fatalf("补上 referenceImages 后仍被拒绝，修复方向不成立: %v", err)
	}
}

// TestAgentImageReferenceAlwaysUsesResourceKey 锁住第 1 段：画布图片节点的 storageKey
// 必须是 resource: 形式，否则连看图工具都进不去。这决定了上面那个占位符形态不是偶然。
func TestAgentImageReferenceAlwaysUsesResourceKey(t *testing.T) {
	if !strings.HasPrefix("resource:abc", "resource:") {
		t.Fatal("sanity")
	}
	// cloudAgentReference 的拒绝分支：非 resource: 前缀的节点不能作为参考资产。
	node := map[string]any{
		"id": "node-1", "type": "image", "title": "外部图",
		"metadata": map[string]any{"storageKey": "https://cdn.example.test/a.png"},
	}
	if _, _, err := cloudAgentReference(nil, "user", node); err == nil {
		t.Fatal("外部地址不得作为参考资产 —— 这条约束正是图片只能以 resource: 形态下发的原因")
	}
}
