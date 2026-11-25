package capability

import (
	"fmt"
	"strings"
)

const wrapClosingMarks = "」』”’）】》〉〕］｝\"'"

// SentenceWrapDetail is the shared receipt text for sentence wrapping.
func SentenceWrapDetail(path string, count int) string {
	return fmt.Sprintf("字段 %s 已按句断行 %d 处（句末标点后插入换行，断点处的行内空白一并收掉）", path, count)
}

// WrapSentences inserts a newline after sentence-ending punctuation. The
// transformation is idempotent so a second write reports zero insertions.
func WrapSentences(text string) (string, int) {
	if text == "" {
		return text, 0
	}
	var out strings.Builder
	out.Grow(len(text) + 16)
	inserted := 0
	inFence := false
	for start := 0; start < len(text); {
		end := strings.IndexByte(text[start:], '\n')
		line := text[start:]
		hasNewline := end >= 0
		if hasNewline {
			line = text[start : start+end]
			end = start + end
		} else {
			end = len(text)
		}
		trimmed := strings.TrimLeft(line, " \t")
		if isFenceLine(trimmed) {
			inFence = !inFence
			out.WriteString(line)
		} else if inFence || skipWrapLine(trimmed) {
			out.WriteString(line)
		} else {
			wrapped, count := wrapLineSentences(line)
			inserted += count
			out.WriteString(wrapped)
		}
		if hasNewline {
			out.WriteByte('\n')
		}
		start = end + 1
	}
	return out.String(), inserted
}

func isFenceLine(trimmed string) bool {
	return strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")
}

func skipWrapLine(trimmed string) bool {
	if trimmed == "" {
		return true
	}
	if strings.HasPrefix(trimmed, "|") {
		return true
	}
	if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
		return true
	}
	return isOrderedListMarker(trimmed)
}

func isOrderedListMarker(trimmed string) bool {
	index := 0
	for index < len(trimmed) && trimmed[index] >= '0' && trimmed[index] <= '9' {
		index++
	}
	if index == 0 || index >= len(trimmed) {
		return false
	}
	if trimmed[index] != '.' && trimmed[index] != ')' {
		return false
	}
	index++
	return index < len(trimmed) && (trimmed[index] == ' ' || trimmed[index] == '\t')
}

func wrapLineSentences(line string) (string, int) {
	if line == "" {
		return line, 0
	}
	var out strings.Builder
	out.Grow(len(line) + 8)
	inserted := 0
	rest := line
	for {
		runes := []rune(rest)
		end := firstSentenceEnd(runes)
		if end < 0 {
			out.WriteString(rest)
			break
		}
		out.WriteString(string(runes[:end]))
		out.WriteByte('\n')
		inserted++
		rest = strings.TrimLeft(string(runes[end:]), " \t")
		if rest == "" {
			break
		}
		if skipWrapLine(rest) {
			out.WriteString(rest)
			break
		}
	}
	return out.String(), inserted
}

func firstSentenceEnd(runes []rune) int {
	for index := 0; index < len(runes); index++ {
		if !isSentenceEndRune(runes[index]) {
			continue
		}
		punctuation := runes[index]
		end := index + 1
		for end < len(runes) && isSentenceEndRune(runes[end]) {
			end++
		}
		for end < len(runes) && isClosingMark(runes[end]) {
			end++
		}
		next := end
		for next < len(runes) && (runes[next] == ' ' || runes[next] == '\t') {
			next++
		}
		if next >= len(runes) {
			continue
		}
		if isASCIISentenceEnd(punctuation) && next == end {
			continue
		}
		return end
	}
	return -1
}

func isSentenceEndRune(r rune) bool {
	switch r {
	case '。', '！', '？', '；', '…':
		return true
	case '.', '!', '?', ';':
		return true
	}
	return false
}

func isASCIISentenceEnd(r rune) bool {
	switch r {
	case '.', '!', '?', ';':
		return true
	}
	return false
}

func isClosingMark(r rune) bool {
	return strings.ContainsRune(wrapClosingMarks, r)
}
