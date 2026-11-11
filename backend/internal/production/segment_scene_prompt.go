package production

import "strings"

func writeSegmentScenePrompt(b *strings.Builder, in SegmentPromptInput, withRef func(string) string) {
	b.WriteString("\n【场景布局、人物站位与机位轴线】\n")
	scenes := []string{}
	for _, ref := range in.References {
		if ref.Kind == "scene" {
			line := withRef(ref.Name)
			if detail := strings.TrimSpace(ref.Detail); detail != "" {
				line += "：" + detail
			}
			scenes = append(scenes, line)
		}
	}
	if len(scenes) == 0 {
		seen := map[string]bool{}
		for _, shot := range in.Shots {
			if name := strings.TrimSpace(shot.Scene); name != "" && !seen[name] {
				seen[name] = true
				scenes = append(scenes, name)
			}
		}
	}
	if len(scenes) > 0 {
		b.WriteString("空间锁定：" + strings.Join(scenes, "；") + "。\n")
	}
	b.WriteString("切镜保持轴线与画面左右关系，不越轴，不穿模；人物按剧情走位。\n")

}
