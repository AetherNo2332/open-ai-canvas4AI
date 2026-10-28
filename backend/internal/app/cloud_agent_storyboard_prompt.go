package app

import (
	"fmt"

	"yingce/backend/internal/canvas/contract"
	"yingce/backend/internal/canvas/references"
)

func cloudAgentStoryboardProviderPrompt(prompt string, bindings []contract.ReferenceBinding, refs map[string]any) (string, error) {
	slots := map[string]string{}
	for _, kind := range []struct{ field, label string }{{"referenceImages", "图片"}, {"referenceVideos", "视频"}, {"referenceAudios", "音频"}} {
		for i, ref := range creationMaps(refs[kind.field]) {
			id := stringValue(ref["id"])
			if slots[id] == "" {
				slots[id] = fmt.Sprintf("@%s%d", kind.label, i+1)
			}
		}
	}
	resolved, err := references.ResolvePromptHandles(prompt, bindings, slots)
	if err != nil {
		return "", cloudAgentReferenceError(err)
	}
	return resolved, nil
}
