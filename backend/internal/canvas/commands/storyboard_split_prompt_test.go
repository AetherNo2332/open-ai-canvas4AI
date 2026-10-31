package commands

import (
	"strings"
	"testing"
)

func splitPromptDoc(t *testing.T) Document {
	t.Helper()
	return fixture(t, `{"id":"canvas","nodes":[
		{"id":"board","type":"script","title":"分镜","metadata":{"composerContent":"林黛玉在雨夜遇见鲁智深。","storyboard":{"rows":[
			{"id":"r1","durationSeconds":4,"plotDescription":"雨夜街口","continuityOut":"她站在路灯下，伞在左手"},
			{"id":"r2","durationSeconds":4,"plotDescription":"他走近"},
			{"id":"r3","durationSeconds":4,"plotDescription":"对视","locked":true},
			{"id":"r4","durationSeconds":4,"plotDescription":"她转身离开"}
		],"referenceNodeIds":[]}}},
		{"id":"style","type":"text","title":"项目风格","metadata":{"content":"水墨质感，冷色调"}},
		{"id":"requirements","type":"text","title":"用户要求","metadata":{"content":"全程没有旁白"}},
		{"id":"note","type":"text","title":"无关笔记","metadata":{"content":"不要出现在提示词里"}},
		{"id":"card","type":"text","title":"林黛玉","metadata":{"workflowKind":"character","characterAssetId":"asset-1","characterName":"林黛玉","characterPrompt":"白衣长发"}},
		{"id":"scene","type":"image","title":"雨夜小巷","metadata":{"storageKey":"resource:scene","assetCategory":"environment"}}
	],"connections":[
		{"id":"e1","fromNodeId":"style","toNodeId":"board"},
		{"id":"e2","fromNodeId":"requirements","toNodeId":"board"}
	]}`)
}

// 整表拆镜：剧情、连到分镜节点的风格文本、角色卡和可引用素材都由服务端读取；锁定镜头告诉模型不要输出。
func TestStoryboardSplitPromptReadsCanvasContext(t *testing.T) {
	prompt, err := BuildStoryboardSplitPrompt(splitPromptDoc(t), "board", StoryboardSplitScope{}, StoryboardSplitPromptInput{Request: "节奏紧凑", ShotCount: 6})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"林黛玉在雨夜遇见鲁智深", "水墨质感，冷色调", "全程没有旁白", "林黛玉（assetId=asset-1）", "nodeId=scene", "【已锁定的镜头】", "镜头3：对视", "全片共 6 个镜头", "节奏紧凑", "【受保护输出契约】"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt is missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "不要出现在提示词里") {
		t.Fatal("unconnected text nodes must not enter the prompt")
	}
}

// 局部重拆：带上要重拆的原镜头、前后各一镜（只读）、其余镜头摘要和修改意见。
func TestStoryboardSplitPromptResplitCarriesNeighbours(t *testing.T) {
	prompt, err := BuildStoryboardSplitPrompt(splitPromptDoc(t), "board", StoryboardSplitScope{RowIDs: []string{"r2"}}, StoryboardSplitPromptInput{Request: "加一个她回头的反应"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"【前一镜】", "她站在路灯下，伞在左手", "【要重拆的镜头】（镜头2 至 2）", "他走近", "【后一镜】", "对视", "【其余镜头摘要】", "镜头4：她转身离开", "加一个她回头的反应", "镜头数量可以与原来不同"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("resplit prompt is missing %q:\n%s", want, prompt)
		}
	}
}

func TestStoryboardSplitPromptRequiresPlot(t *testing.T) {
	doc := fixture(t, `{"id":"canvas","nodes":[{"id":"board","type":"script","metadata":{"storyboard":{"rows":[],"referenceNodeIds":[]}}}],"connections":[]}`)
	if _, err := BuildStoryboardSplitPrompt(doc, "board", StoryboardSplitScope{}, StoryboardSplitPromptInput{}); err == nil || !strings.Contains(err.Error(), "剧情") {
		t.Fatalf("missing plot must be rejected: %v", err)
	}
}
