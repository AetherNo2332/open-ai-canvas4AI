package production

import (
	"fmt"
	"unicode/utf8"
)

const minSegmentPromptBodyRunes = 300

// SegmentPromptBodyBudget 返回视频提示词正文可用的字符上限：总上限减去提交时加在开头的参考图说明，
// 再留两个换行分隔正文。返回 0 表示模型未声明上限；剩余空间连最小正文都放不下时直接报错，
// 不提交一段注定会被视频模型拒绝的提示词。
func SegmentPromptBodyBudget(promptMaxChars int, header string) (int, error) {
	if promptMaxChars <= 0 {
		return 0, nil
	}
	headerRunes := utf8.RuneCountInString(header)
	budget := promptMaxChars - headerRunes - 2
	if budget < minSegmentPromptBodyRunes {
		return 0, fmt.Errorf("开头的参考图说明占用 %d 个字符，所选视频模型的提示词上限只有 %d 个字符，放不下本段正文；请减少本段参考图，或换用提示词上限更大的视频模型", headerRunes, promptMaxChars)
	}
	return budget, nil
}
