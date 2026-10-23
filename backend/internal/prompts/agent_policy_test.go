package prompts

import (
	"strings"
	"testing"
)

func TestLoadAgentPoliciesUsesDocumentMetadata(t *testing.T) {
	system, media, err := LoadAgentPolicies()
	if err != nil {
		t.Fatal(err)
	}
	// 版本号是策略文件的公开合同：工具披露规则改变时一同更新。
	if system.ID != "cloud-agent-system" || system.Version != 21 || media.ID != "cloud-agent-media" || media.Version != 5 {
		t.Fatalf("unexpected policy metadata: system=%s@%d media=%s@%d", system.ID, system.Version, media.ID, media.Version)
	}
	if strings.Contains(system.Text, "id: cloud-agent-system") || !strings.HasPrefix(system.Text, "# ArrowFX Cloud Agent") ||
		strings.Contains(media.Text, "id: cloud-agent-media") || !strings.HasPrefix(media.Text, "#") {
		t.Fatal("policy metadata leaked into a compiled policy body")
	}
	for _, phrase := range []string{
		"auto 由服务端完成模型、价格、预算和资源准入后提交",
		"成功自动采用，但用户期间手动选择过版本或输出归属曾改变时只进入历史",
		"request_approval 必须等待界面独立审批",
		"authorizedChargeMicrocredits / chargeLimitMicrocredits 是预授权或上限，不是实际消费",
	} {
		if !strings.Contains(media.Text, phrase) {
			t.Fatalf("media policy lost required contract phrase %q", phrase)
		}
	}
}

func TestParsePolicyDocumentRejectsInvalidMetadata(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  string
	}{
		{"missing header", "# policy"},
		{"unclosed header", "---\nid: policy\nversion: 1\n# body"},
		{"missing id", "---\nversion: 1\n---\n# body"},
		{"invalid version", "---\nid: policy\nversion: latest\n---\n# body"},
		{"duplicate field", "---\nid: policy\nid: other\nversion: 1\n---\n# body"},
		{"unknown field", "---\nid: policy\nversion: 1\nowner: app\n---\n# body"},
		{"empty body", "---\nid: policy\nversion: 1\n---"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, _, _, err := parsePolicyDocument(test.raw); err == nil {
				t.Fatal("invalid policy document was accepted")
			}
		})
	}
}
