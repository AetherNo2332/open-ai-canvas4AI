package previs

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

func invalid(message string) error { return &ValidationError{Message: message} }
func validateText(value, label string, maximum int, required bool) error {
	if required && strings.TrimSpace(value) == "" {
		return invalid(label + "不能为空")
	}
	if !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return invalid(label + "不能包含首尾空白或无效字符")
	}
	if utf8.RuneCountInString(value) > maximum {
		return &ValidationError{Message: fmt.Sprintf("%s不能超过 %d 个字符", label, maximum), Plain: true}
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return invalid(label + "不能包含控制字符")
		}
	}
	return nil
}

func validateActiveID(value string, policy IDPolicy) error {
	if value == "" {
		return invalid("activeShotId 不能为空")
	}
	if policy == CommandIDs {
		if len(value) > 80 {
			return invalid("activeShotId长度无效")
		}
		for _, ch := range value {
			if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '-' || ch == ':' || ch == '.') {
				return invalid("activeShotId包含非法字符")
			}
		}
		return nil
	}
	if strings.TrimSpace(value) != value || !utf8.ValidString(value) {
		return invalid("activeShotId不能为空、不能包含首尾空白或无效字符")
	}
	if utf8.RuneCountInString(value) > 80 {
		return invalid("activeShotId不能超过 80 个字符")
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return invalid("activeShotId不能包含控制字符")
		}
	}
	return nil
}

func validateColor(value string) error {
	if err := validateText(value, "场景背景色", 32, true); err != nil {
		return err
	}
	if len(value) != 4 && len(value) != 7 && len(value) != 9 {
		return invalid("场景背景色必须是十六进制颜色")
	}
	if value[0] != '#' {
		return invalid("场景背景色必须是十六进制颜色")
	}
	for _, r := range value[1:] {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return invalid("场景背景色必须是十六进制颜色")
		}
	}
	return nil
}
