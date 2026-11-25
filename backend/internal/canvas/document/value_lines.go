package document

import "strings"

func ValueLines(value string) []string {
	value = stripCarriageReturns(value)
	if value == "" {
		return nil
	}
	if strings.HasSuffix(value, "\n") {
		value = value[:len(value)-1]
	}
	return strings.Split(value, "\n")
}

func ValueLinesTrailingLF(value string) bool {
	return strings.HasSuffix(stripCarriageReturns(value), "\n")
}

func JoinValueLines(lines []string, trailingLF bool) string {
	if len(lines) == 0 {
		return ""
	}
	joined := strings.Join(lines, "\n")
	if trailingLF {
		joined += "\n"
	}
	return joined
}
