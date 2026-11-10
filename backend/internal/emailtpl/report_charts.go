package emailtpl

import (
	"fmt"
	"math"
)

func (view *reportView) buildHourBars(hourly [24]int64) int {
	// 小时柱状图：零值保留 2px 底线，便于看出时间轴。
	var hourMax int64
	peak := -1
	for hour, value := range hourly {
		if value > hourMax {
			hourMax, peak = value, hour
		}
	}
	for hour, value := range hourly {
		height := 2
		color := colorLine
		if value > 0 && hourMax > 0 {
			height = max(4, int(math.Round(float64(value)/float64(hourMax)*88)))
			color = "#b9c4ff"
			if hour == peak {
				color = colorAccent
			}
		}
		view.HourBars = append(view.HourBars, bar{Height: height, Color: color, Title: fmt.Sprintf("%02d:00 %s 积分", hour, FormatCredits(value))})
	}
	if peak >= 0 {
		view.PeakHourLabel = fmt.Sprintf("%02d:00–%02d:59", peak, peak)
	}
	return peak
}

func (view *reportView) buildDayBars(days []DayAmount) {
	var dayMax int64
	for _, day := range days {
		dayMax = max(dayMax, day.Micro)
	}
	for index, day := range days {
		last := index == len(days)-1
		height, color := 3, colorLine
		if day.Micro > 0 && dayMax > 0 {
			height = max(4, int(math.Round(float64(day.Micro)/float64(dayMax)*80)))
			color = "#d5dbff"
			if last {
				color = colorAccent
			}
		}
		value := FormatCredits(day.Micro)
		if day.Micro == 0 {
			value = "—"
		}
		label := day.Label
		if last {
			label = "今天"
		}
		view.DayBars = append(view.DayBars, bar{Label: label, Height: height, Color: color, Value: value, Strong: last})
	}
}
