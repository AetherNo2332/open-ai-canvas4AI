package capability

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"infinite-canvas/backend/internal/canvas/contract"
)

func generationSpecField() PatchField {
	options := map[string]PatchField{}
	for _, key := range []string{"size", "quality", "resolution", "audioVoice", "audioFormat", "audioSpeed", "audioInstructions"} {
		options[key] = stringField(16000)
	}
	for _, key := range []string{"transparentBackground", "generateAudio", "watermark"} {
		options[key] = PatchField{Kind: patchKindBoolean}
	}
	options["count"] = integerField(1, 100)
	options["durationSeconds"] = integerField(0, 86400)
	selection := objectField(map[string]PatchField{"kind": enumString("logical", "channel"), "logicalModelId": stringField(240), "channelId": stringField(240), "modelKey": stringField(240)}, "kind")
	binding := objectField(map[string]PatchField{"id": stringField(120), "nodeId": stringField(120), "resourceId": stringField(120), "transientId": stringField(120), "mediaType": enumString("image", "video", "audio", "text"), "role": enumString("reference", "source-text", "first-frame", "last-frame", "mask"), "order": integerField(0, 10000), "resolution": enumString("latest", "snapshot")}, "id", "mediaType", "role", "order", "resolution")
	field := objectField(map[string]PatchField{"version": integerField(1, 1), "mode": enumString("image", "video", "audio"), "prompt": stringField(16000), "modelSelection": selection, "options": objectField(options), "referenceBindings": arrayField(binding, 100), "textInputMode": enumString("prompt-only", "append-sources")}, "version", "mode", "prompt", "options", "referenceBindings", "textInputMode")
	field.Format = "generation-spec"
	return field
}

func subtitleEntriesField() PatchField {
	field := arrayField(objectField(map[string]PatchField{"index": integerField(1, 100000), "startMs": integerField(0, 1e12), "endMs": integerField(0, 1e12), "text": stringField(16000)}, "index", "startMs", "endMs", "text"), 10000)
	field.Format = "subtitles"
	return field
}

func subtitleHighlightsField() PatchField {
	field := arrayField(objectField(map[string]PatchField{"entryIndex": integerField(1, 100000), "start": integerField(0, 16000), "end": integerField(0, 16000), "highlightText": stringField(16000), "sourceText": stringField(16000)}, "entryIndex", "start", "end", "highlightText", "sourceText"), 10000)
	field.Format = "subtitle-highlights"
	return field
}

func contentColorField() PatchField {
	field := stringField(80)
	field.Format = "content-color"
	return field
}

func subtitleStyleField() PatchField {
	return objectField(map[string]PatchField{"fontSize": boundedNumber(12, 40), "color": contentColorField(), "position": enumString("top", "bottom", "center"), "highlightEnabled": {Kind: patchKindBoolean}, "highlightBackgroundColor": contentColorField(), "highlightTextColor": contentColorField(), "highlightPaddingX": boundedNumber(0, 100), "highlightPaddingY": boundedNumber(0, 100), "highlightRadius": boundedNumber(0, 100), "highlightAnimation": enumString("pop", "wipe", "none"), "maxCharsPerEntry": integerField(20, 60), "autoResegment": {Kind: patchKindBoolean}}, "fontSize", "color", "position", "highlightEnabled", "highlightBackgroundColor", "highlightTextColor", "highlightPaddingX", "highlightPaddingY", "highlightRadius", "highlightAnimation", "maxCharsPerEntry", "autoResegment")
}

func nullableField(field PatchField) PatchField { field.Nullable = true; return field }

// The editor's Tiptap StarterKit plus TextAlign, Color and Highlight schema.
// Eight block levels permit nested lists/quotes while bounding tool payloads.
func richTextField() PatchField {
	field := richTextNodeField(8)
	field.Properties["type"] = enumString("doc")
	field.Format = "rich-text"
	return field
}

func richTextNodeField(depth int) PatchField {
	attrs := objectField(map[string]PatchField{"textAlign": nullableField(enumString("left", "center", "right", "justify")), "level": integerField(1, 3), "start": integerField(1, 100000), "language": nullableField(stringField(240))})
	markAttrs := objectField(map[string]PatchField{"color": nullableField(contentColorField()), "href": stringField(4000), "target": nullableField(enumString("_blank", "_self", "_parent", "_top")), "rel": nullableField(stringField(240)), "class": nullableField(stringField(240))})
	mark := objectField(map[string]PatchField{"type": enumString("bold", "italic", "underline", "strike", "code", "textStyle", "highlight", "link"), "attrs": markAttrs}, "type")
	properties := map[string]PatchField{"type": enumString("doc", "paragraph", "heading", "blockquote", "bulletList", "orderedList", "listItem", "codeBlock", "horizontalRule", "hardBreak", "text"), "text": stringField(maxAgentDocumentRunes), "attrs": attrs, "marks": arrayField(mark, 16)}
	if depth > 0 {
		properties["content"] = arrayField(richTextNodeField(depth-1), 10000)
	}
	return objectField(properties, "type")
}

var contentColorPattern = regexp.MustCompile(`^(#[0-9a-fA-F]{3,8}|[a-zA-Z]{1,30}|(?:rgb|rgba|hsl|hsla)\([0-9.% ,+-]+\))$`)

func validateFieldFormat(format string, value any) error {
	switch format {
	case "":
		return nil
	case "content-color":
		if !contentColorPattern.MatchString(value.(string)) {
			return fmt.Errorf("颜色必须是 CSS 颜色值")
		}
	case "generation-spec":
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		_, err = contract.Decode(encoded)
		return err
	case "subtitles":
		seen := map[float64]bool{}
		for _, item := range value.([]any) {
			row := item.(map[string]any)
			index := row["index"].(float64)
			if seen[index] || row["endMs"].(float64) < row["startMs"].(float64) {
				return fmt.Errorf("字幕序号必须唯一且时间范围有效")
			}
			seen[index] = true
		}
	case "subtitle-highlights":
		for _, item := range value.([]any) {
			row := item.(map[string]any)
			start, end := int(row["start"].(float64)), int(row["end"].(float64))
			source := utf16.Encode([]rune(row["sourceText"].(string)))
			if end < start || end > len(source) {
				return fmt.Errorf("字幕重点范围无效")
			}
		}
	case "rich-text":
		budget := maxAgentDocumentRunes
		nodes := 10000
		return validateRichText(value.(map[string]any), true, 0, &budget, &nodes)
	default:
		return fmt.Errorf("未知字段格式")
	}
	return nil
}

func validateRichText(node map[string]any, root bool, depth int, budget, nodes *int) error {
	*nodes--
	if *nodes < 0 || depth > 8 {
		return fmt.Errorf("富文本超过嵌套或节点数量限制")
	}
	typ, _ := node["type"].(string)
	if (root && typ != "doc") || (!root && typ == "doc") {
		return fmt.Errorf("富文本文档类型无效")
	}
	text, hasText := node["text"].(string)
	if hasText {
		if typ != "text" {
			return fmt.Errorf("只有文字节点允许 text")
		}
		*budget -= utf8.RuneCountInString(text)
		if *budget < 0 {
			return fmt.Errorf("富文本超过内容限制")
		}
	} else if typ == "text" {
		return fmt.Errorf("文字节点缺少 text")
	}
	allowedAttrs := map[string]bool{}
	switch typ {
	case "paragraph":
		allowedAttrs["textAlign"] = true
	case "heading":
		allowedAttrs["textAlign"], allowedAttrs["level"] = true, true
	case "orderedList":
		allowedAttrs["start"] = true
	case "codeBlock":
		allowedAttrs["language"] = true
	}
	if attrs, ok := node["attrs"].(map[string]any); ok {
		for key := range attrs {
			if !allowedAttrs[key] {
				return fmt.Errorf("节点 %s 不允许属性 %s", typ, key)
			}
		}
	}
	if marks, ok := node["marks"].([]any); ok {
		if typ != "text" && typ != "hardBreak" {
			return fmt.Errorf("此节点不允许文字标记")
		}
		for _, item := range marks {
			mark := item.(map[string]any)
			kind := mark["type"].(string)
			attrs, _ := mark["attrs"].(map[string]any)
			for key := range attrs {
				allowed := (key == "color" && (kind == "textStyle" || kind == "highlight")) || (kind == "link" && (key == "href" || key == "target" || key == "rel" || key == "class"))
				if !allowed {
					return fmt.Errorf("文字标记属性无效")
				}
			}
			if kind == "link" {
				href, _ := attrs["href"].(string)
				parsed, err := url.Parse(strings.TrimSpace(href))
				if err != nil || strings.TrimSpace(href) == "" || parsed.User != nil {
					return fmt.Errorf("链接无效")
				}
				switch strings.ToLower(parsed.Scheme) {
				case "", "http", "https", "mailto", "tel":
				default:
					return fmt.Errorf("不允许的链接协议")
				}
			}
		}
	}
	if children, ok := node["content"].([]any); ok {
		if typ == "text" || typ == "hardBreak" || typ == "horizontalRule" {
			return fmt.Errorf("叶节点不能有 content")
		}
		for _, item := range children {
			if err := validateRichText(item.(map[string]any), false, depth+1, budget, nodes); err != nil {
				return err
			}
		}
	}
	return nil
}

func richTextPlainText(node map[string]any) string {
	typ, _ := node["type"].(string)
	if typ == "text" {
		text, _ := node["text"].(string)
		return text
	}
	if typ == "hardBreak" {
		return "\n"
	}
	parts := []string{}
	children, _ := node["content"].([]any)
	for _, item := range children {
		parts = append(parts, richTextPlainText(item.(map[string]any)))
	}
	separator := ""
	switch typ {
	case "doc", "blockquote", "bulletList", "orderedList", "listItem":
		separator = "\n"
	}
	return strings.TrimRight(strings.Join(parts, separator), "\n")
}
