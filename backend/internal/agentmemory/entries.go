// Package agentmemory 是用户长期记忆文件条目的纯规则：文本校验、去重指纹、条目 ID、
// 用量与哈希，以及旧版经验（AgentLesson）到条目的映射。
//
// 它只依赖 model 与 kernel：服务层（app）写入记忆和 schema 迁移 v82（database）导入旧经验
// 用的是同一套校验，避免两边各写一份正则后口径漂移。
package agentmemory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
)

const (
	DefaultCapacity = 5000
	EntryMaxRunes   = 400
	EntriesMax      = 80
	// LessonImportSource 标记从旧版经验库导入的条目。
	LessonImportSource = "lesson_import"
)

// 记忆会进入后续每一轮的上下文，写入时机械拦截：链接、ID、密钥和试图改写规则的话一律拒绝。
var blockedPatterns = []struct {
	label   string
	pattern *regexp.Regexp
}{
	{"链接", regexp.MustCompile(`(?i)\b(https?|ftp)://|\bwww\.[a-z0-9-]+\.[a-z]|\bdata:[a-z]+/`)},
	{"接口路径或资源键", regexp.MustCompile(`(?i)/api/|\bresource:`)},
	{"运行或任务 ID", regexp.MustCompile(`\bag[0-9a-f]{12,}\b`)},
	{"长十六进制 ID", regexp.MustCompile(`(?i)\b[0-9a-f]{24,}\b`)},
	{"UUID", regexp.MustCompile(`\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b`)},
	{"节点或连线 ID", regexp.MustCompile(`\b(conn-|agent-edge-|MODEL_)[0-9A-Za-z_-]{4,}\b`)},
	{"密钥或令牌", regexp.MustCompile(`\b(sk|rk|pk)-[A-Za-z0-9_-]{16,}|\bAKIA[0-9A-Z]{16}\b|\bgh[pousr]_[A-Za-z0-9]{20,}|\bxox[abpr]-[A-Za-z0-9-]{10,}|\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}|(?i)\bbearer\s+[A-Za-z0-9._~+/-]{16,}`)},
	{"凭据", regexp.MustCompile(`(?i)(api[ _-]?key|secret|password|passwd|cookie|密码|密钥|口令|令牌)\s*[:：=]\s*\S{4,}`)},
	{"试图改写规则的指令", regexp.MustCompile(`(?i)ignore\s+(all\s+|any\s+|the\s+)?(previous|prior|above|earlier)\s+(instructions|rules|prompts)|disregard\s+(the\s+)?(system|previous)\b|jailbreak|(忽略|无视|不要遵守|不用遵守)(之前|以上|上面|前面|所有|全部|一切|系统)[^，。,.]{0,8}(指令|规则|要求|设定|提示词)|越狱`)},
}

func NormalizeText(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func invisibleRune(r rune) bool {
	switch {
	case r >= 0x200B && r <= 0x200F, r >= 0x202A && r <= 0x202E, r >= 0x2060 && r <= 0x2064, r >= 0x2066 && r <= 0x2069, r == 0xFEFF:
		return true
	}
	return unicode.IsControl(r)
}

// ValidateText 规范空白并做长度、隐形字符和泄露检查；label 用于定位是哪一条出错。
func ValidateText(raw, label string) (string, error) {
	if !utf8.ValidString(raw) {
		return "", kernel.BadAuthRequest(label + " 必须是有效的 UTF-8 文本")
	}
	text := NormalizeText(raw)
	if text == "" {
		return "", kernel.BadAuthRequest(label + " 不能为空")
	}
	if count := utf8.RuneCountInString(text); count > EntryMaxRunes {
		return "", kernel.BadAuthRequest(fmt.Sprintf("%s 有 %d 字，超过单条 %d 字上限；请写短一点，或拆成两条", label, count, EntryMaxRunes))
	}
	for _, r := range text {
		if invisibleRune(r) {
			return "", kernel.BadAuthRequest(label + " 含不可见或控制字符")
		}
	}
	for _, entry := range blockedPatterns {
		if entry.pattern.MatchString(text) {
			return "", kernel.BadAuthRequest(fmt.Sprintf("%s 里不能写%s。记忆只保存跨会话仍成立的偏好、约定和做法；链接、资源键、节点或任务 ID、密钥都不要写进去，请改写成通用说法", label, entry.label))
		}
	}
	return text, nil
}

// Fingerprint 忽略大小写、空白和常见标点，用于识别重复条目。
func Fingerprint(text string) string {
	const dropped = " \t\r\n，。！？；：、,.!?;:-'\"“”‘’（）()【】[]《》<>·…"
	var b strings.Builder
	for _, r := range strings.ToLower(text) {
		if !strings.ContainsRune(dropped, r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func Duplicate(entries []model.AgentMemoryEntry, text, exceptID string) bool {
	want := Fingerprint(text)
	for _, entry := range entries {
		if entry.ID != exceptID && Fingerprint(entry.Text) == want {
			return true
		}
	}
	return false
}

func CharCount(entries []model.AgentMemoryEntry) int {
	total := 0
	for _, entry := range entries {
		total += utf8.RuneCountInString(entry.Text)
	}
	return total
}

func Hash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func EntryID(section string, seq int64) string {
	prefix := "n"
	if section == model.AgentMemorySectionUser {
		prefix = "u"
	}
	return prefix + strconv.FormatInt(seq, 10)
}

// NextSeq 取文档计数与现有 ID 最大序号中较大者，旧数据或手工修复后也不会复用 ID。
func NextSeq(docSeq int64, entries []model.AgentMemoryEntry) int64 {
	next := int64(1)
	if docSeq > next {
		next = docSeq
	}
	for _, entry := range entries {
		if len(entry.ID) < 2 {
			continue
		}
		if seq, err := strconv.ParseInt(entry.ID[1:], 10, 64); err == nil && seq >= next {
			next = seq + 1
		}
	}
	return next
}

// TruncateRunes 规范空白后截到 limit 个字符，超长时以 … 结尾。
func TruncateRunes(text string, limit int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if limit <= 0 || utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return strings.TrimSpace(string(runes[:limit])) + "…"
}

// LessonText 把一条旧经验写成一条记忆：「适用场景：做法」；没有做法时用步骤动作串起来。
func LessonText(lesson model.AgentLesson) string {
	text := strings.TrimSpace(lesson.Situation)
	body := strings.TrimSpace(lesson.Lesson)
	if body == "" && strings.TrimSpace(lesson.StepsJSON) != "" {
		var steps []model.AgentLessonStep
		if json.Unmarshal([]byte(lesson.StepsJSON), &steps) == nil {
			actions := make([]string, 0, len(steps))
			for _, step := range steps {
				if action := strings.TrimSpace(step.Action); action != "" {
					actions = append(actions, action)
				}
			}
			body = strings.Join(actions, " → ")
		}
	}
	if body == "" {
		return text
	}
	if text == "" {
		return body
	}
	return text + "：" + body
}

// LessonSection：用户偏好类经验进「关于用户」，其余进「工作笔记」。
func LessonSection(lesson model.AgentLesson) string {
	if strings.TrimSpace(lesson.Source) == "user_preference" {
		return model.AgentMemorySectionUser
	}
	return model.AgentMemorySectionNotes
}

// AppendLessons 把已批准经验按给定顺序追加到 existing 之后，返回新增条目与下一个序号。
// 超长的截断到单条上限；无法通过校验、与已有条目重复、或会超出容量/条数上限的跳过。
// 调用方负责只传入已批准经验，并按优先级（命中次数）排序。
func AppendLessons(existing []model.AgentMemoryEntry, nextSeq int64, lessons []model.AgentLesson, capacity int, now time.Time) ([]model.AgentMemoryEntry, int64) {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	all := append([]model.AgentMemoryEntry(nil), existing...)
	used := CharCount(existing)
	added := []model.AgentMemoryEntry{}
	for _, lesson := range lessons {
		text, err := ValidateText(TruncateRunes(LessonText(lesson), EntryMaxRunes-1), "导入的经验")
		if err != nil || Duplicate(all, text, "") {
			continue
		}
		size := utf8.RuneCountInString(text)
		if used+size > capacity || len(all) >= EntriesMax {
			continue
		}
		section := LessonSection(lesson)
		entry := model.AgentMemoryEntry{ID: EntryID(section, nextSeq), Section: section, Text: text, Source: LessonImportSource, CreatedAt: now, UpdatedAt: now}
		nextSeq++
		used += size
		all = append(all, entry)
		added = append(added, entry)
	}
	return added, nextSeq
}
