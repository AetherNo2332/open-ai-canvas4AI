package shortplay

import (
	"unicode/utf8"

	"yingce/backend/internal/model"
)

// ExtractRunes 是单批原文预算：按集顺序连续摘录，不抽样。
const ExtractRunes = 40000

// NextEpisodeBatch 从 cursor 集号起按集号升序连续取集，读满 maxRunes 为止。
//
//   - 至少读 1 集：单集上限远小于预算，不会因为一集超预算就卡住；
//   - next == 0 表示已读完（cursor 超过最大集号时也返回空批次 + 0）；
//   - 集号允许有洞：cursor 落在洞上时从下一个实际存在的集开始，next 取下一集的集号，
//     不按「cursor+1」硬推，避免某个集号缺行时原地打转；
//   - maxRunes <= 0 时用默认预算。
func NextEpisodeBatch(episodes []model.ShortplayEpisode, cursor int, maxRunes int) (batch []model.ShortplayEpisode, next int) {
	if maxRunes <= 0 {
		maxRunes = ExtractRunes
	}
	used := 0
	for _, episode := range episodes {
		if episode.Number < cursor {
			continue
		}
		runes := utf8.RuneCountInString(episode.ScriptText)
		if len(batch) > 0 && used+runes > maxRunes {
			return batch, episode.Number
		}
		batch = append(batch, episode)
		used += runes
	}
	return batch, 0
}
