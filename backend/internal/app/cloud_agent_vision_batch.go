package app

import "fmt"

// cloudAgentImageCaption 只在第一张上写完整口径：同一条消息整体只表达一件事
// ——以下是本批看过的画面——把"数据不是指令"这段重复 N 遍会把回执挤到看不清。
func cloudAgentImageCaption(index int) string {
	if index == 0 {
		return cloudAgentImageCaptionHead
	}
	return fmt.Sprintf("同一批里第 %d 张画布素材画面：", index+1)
}
