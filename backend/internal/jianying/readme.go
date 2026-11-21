package jianying

import (
	"fmt"
	"strings"
)

// Readme 返回压缩包内的剪映导入说明，列出草稿目录、字幕限制和未导出的剧情段。
func Readme(draft Draft, skipped []string) string {
	var builder strings.Builder
	builder.WriteString("影策短剧 · 剪映草稿\n\n")
	builder.WriteString("使用方法：\n")
	builder.WriteString("1. 关闭剪映专业版。\n")
	fmt.Fprintf(&builder, "2. 把压缩包解压到剪映草稿目录，确保文件夹「%s」直接位于：\n   %s\n", draft.Name, draft.Root)
	fmt.Fprintf(&builder, "   解压后的完整路径应为：%s\n", draft.FolderPath())
	builder.WriteString("3. 打开剪映，在首页草稿列表里找到同名草稿。\n\n")
	builder.WriteString("注意：素材按上面的完整路径写入草稿；解压到别处或改了文件夹名，剪映会提示素材丢失，可在剪映里重新链接 materials 目录下的视频。\n")
	if len(draft.Captions) > 0 {
		builder.WriteString("台词字幕按分镜时长估算，与配音可能有偏差；需要精确对齐时可删除字幕轨，用剪映「识别字幕」重新生成。台词字幕.srt 可单独导入。\n")
	}
	if len(skipped) > 0 {
		builder.WriteString("\n以下剧情段没有导出：\n")
		for _, item := range skipped {
			builder.WriteString("- " + item + "\n")
		}
	}
	return builder.String()
}
