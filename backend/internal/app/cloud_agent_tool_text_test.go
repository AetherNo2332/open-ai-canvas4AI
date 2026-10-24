package app

import (
	"reflect"
	"strings"
	"testing"
)

func TestCloudAgentToolTextNormalizesCheckoutLineEndings(t *testing.T) {
	const markdown = "# Tools\n\n## first\nFirst line.\nSecond line.\n\nFinal paragraph.\n\n## second\nOther description.\n"
	want := map[string]string{
		"first":  "First line.\nSecond line.\n\nFinal paragraph.",
		"second": "Other description.",
	}
	for name, input := range map[string]string{
		"lf":    markdown,
		"crlf":  strings.ReplaceAll(markdown, "\n", "\r\n"),
		"mixed": strings.Replace(markdown, "First line.\n", "First line.\r\n", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if got := parseCloudAgentToolText(input); !reflect.DeepEqual(got, want) {
				t.Fatalf("checkout line endings changed tool descriptions: got %#v, want %#v", got, want)
			}
		})
	}
}
