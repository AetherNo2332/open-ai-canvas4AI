package document

import "strings"

// Normalize applies the only read-side normalization R1: strip CR and BOM and
// ensure a final LF. Empty text remains empty.
func Normalize(text string) string {
	normalized, _ := normalize(text)
	return normalized
}

func normalize(text string) (string, []Repair) {
	var repairs []Repair
	if strings.HasPrefix(text, "\ufeff") {
		text = strings.TrimPrefix(text, "\ufeff")
		repairs = append(repairs, Repair{Kind: RepairBOM, Detail: "已去掉文件开头的 BOM"})
	}
	if strings.Contains(text, "\r") {
		text = strings.ReplaceAll(text, "\r", "")
		repairs = append(repairs, Repair{Kind: RepairCRLF, Detail: "已把 CRLF / CR 统一为 LF"})
	}
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
		repairs = append(repairs, Repair{Kind: RepairMissingFinalNewline, Detail: "已补文件末尾换行"})
	}
	return text, repairs
}
