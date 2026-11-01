package app

import (
	"strings"

	"yingce/backend/internal/canvas/layout"
)

func cloudAgentArrangeModeLabel(mode layout.Mode) string {
	switch mode {
	case layout.ModeFlow:
		return "按依赖分层"
	case layout.ModeByLane:
		return "按媒体类型分区"
	case layout.ModeRow:
		return "排成一行"
	case layout.ModeColumn:
		return "排成一列"
	case layout.ModeGrid:
		return "排成网格"
	default:
		return "自动整理"
	}
}

func cloudAgentArrangeAlignLabel(align layout.AlignMode) string {
	switch align {
	case layout.AlignLeft:
		return "左对齐"
	case layout.AlignCenterX:
		return "水平居中"
	case layout.AlignRight:
		return "右对齐"
	case layout.AlignTop:
		return "顶对齐"
	case layout.AlignCenterY:
		return "垂直居中"
	case layout.AlignBottom:
		return "底对齐"
	case layout.AlignDistributeX:
		return "水平等距"
	case layout.AlignDistributeY:
		return "垂直等距"
	default:
		return ""
	}
}

func cloudAgentArrangeMode(value string) (layout.Mode, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "auto":
		return layout.ModeAuto, nil
	case "flow":
		return layout.ModeFlow, nil
	case "bytype", "by_type", "lane", "lanes":
		return layout.ModeByLane, nil
	case "row":
		return layout.ModeRow, nil
	case "column":
		return layout.ModeColumn, nil
	case "grid":
		return layout.ModeGrid, nil
	default:
		return "", cloudAgentFieldError("mode", "invalid_value", "整理模式无效：只能是 auto、flow、byType、row、column 或 grid")
	}
}

func cloudAgentArrangeAlign(value string) (layout.AlignMode, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return "", nil
	case "left", "centerx", "right", "top", "centery", "bottom", "distributex", "distributey":
		return layout.AlignMode(strings.ToLower(strings.TrimSpace(value))), nil
	default:
		return "", cloudAgentFieldError("align", "invalid_value", "对齐模式无效：只能是 left、centerX、right、top、centerY、bottom、distributeX 或 distributeY")
	}
}
