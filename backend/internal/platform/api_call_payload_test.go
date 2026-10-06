package platform

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestAPICallPayloadTruncationPreservesUTF8(t *testing.T) {
	for _, prefix := range []int{0, 1, 2} {
		value := strings.Repeat("a", prefix) + strings.Repeat("时", maxAPICallPayloadBytes/3+1)
		got := SanitizeAPICallPayload([]byte(value), "text/plain")
		if !utf8.ValidString(got) {
			t.Fatalf("prefix=%d: truncated log contains a partial UTF-8 character", prefix)
		}
		if !strings.Contains(got, "报文已截断") {
			t.Fatal("missing truncation notice")
		}
	}
}

func TestAPICallPayloadSanitizesInvalidText(t *testing.T) {
	got := SanitizeAPICallPayload([]byte{'a', 0xe9, 0x97, '\n', 0, 'b'}, "text/plain")
	if !utf8.ValidString(got) || strings.ContainsRune(got, 0) {
		t.Fatalf("log payload is not valid PostgreSQL text: %q", got)
	}
}
