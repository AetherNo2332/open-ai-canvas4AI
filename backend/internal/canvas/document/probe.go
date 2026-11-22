package document

import (
	"encoding/json"
	"strings"
)

func LooksLikeDocument(text string) bool {
	return DocumentSyntaxError(text) == nil
}

func DocumentSyntaxError(text string) error {
	escaped, breaks := relaxStringNewlines(text)
	lines := newLineIndex(escaped, breaks)
	decoder := json.NewDecoder(strings.NewReader(escaped))
	decoder.UseNumber()
	var root any
	if err := decoder.Decode(&root); err != nil {
		return syntaxError(err, decoder, lines)
	}
	if _, err := decoder.Token(); err == nil {
		return &Error{Code: CodeSyntax, Line: lines.lineAt(int(decoder.InputOffset())),
			Detail: "文件在第一个 JSON 值之后还有内容：整份文档必须恰好是一个对象"}
	}
	if _, ok := root.(map[string]any); !ok {
		return &Error{Code: CodeSyntax, Line: 1,
			Detail: "整份文档必须是一个 JSON 对象（以 { 开头），形如 {\"type\": \"text\", \"content\": \"…\"}"}
	}
	return nil
}
