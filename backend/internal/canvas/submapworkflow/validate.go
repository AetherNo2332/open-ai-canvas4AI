package submapworkflow

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	workflowIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	localIDPattern    = regexp.MustCompile(`^(text|image|video)-(\d+)$`)
	promptMention     = regexp.MustCompile(`@文本(\d+)`)
)

// Validate 校验一份 manifest。任何一条不满足都整份拒绝，不做静默修正。
// 这里只校验结构性不变量（ID/类型/序号/边/@文本N 槽位/startFrame）；
// "哪段文本填什么、怎么组合"是业务语义，交给 Agent 与语义化 skill。
func Validate(manifest Manifest) error {
	if err := validateHeader(manifest); err != nil {
		return err
	}
	children, images, videos, err := validateChildren(manifest.ID, manifest.Children)
	if err != nil {
		return err
	}
	slots, err := validateConnections(manifest.Connections, children)
	if err != nil {
		return err
	}
	if err := validateChildReferences(manifest.Children, children, slots); err != nil {
		return err
	}
	// storyboard 是替代旧 row 的默认模板：恰好一个 image、一个 video。
	if manifest.ID == "storyboard" && (images != 1 || videos != 1) {
		return invalid(fmt.Sprintf("storyboard workflow 必须恰好一个 image 与一个 video，收到 %d 个 image、%d 个 video", images, videos))
	}
	return nil
}

func validateHeader(manifest Manifest) error {
	if manifest.Version != SupportedVersion {
		return invalid(fmt.Sprintf(
			"workflow %s 的 version 是 %d，当前只接受 %d；版本不符整份拒绝，不做兼容猜测",
			manifest.ID, manifest.Version, SupportedVersion))
	}
	if !workflowIDPattern.MatchString(manifest.ID) {
		return invalid(fmt.Sprintf("workflow id %q 无效；只能是小写字母、数字与连字符", manifest.ID))
	}
	if strings.TrimSpace(manifest.Title) == "" {
		return invalid(fmt.Sprintf("workflow %s 缺少 title", manifest.ID))
	}
	if len(manifest.Children) == 0 {
		return invalid(fmt.Sprintf("workflow %s 没有 children", manifest.ID))
	}
	return nil
}

func validateChildren(manifestID string, manifestChildren []Child) (map[string]Child, int, int, error) {
	children := make(map[string]Child, len(manifestChildren))
	nextIndex := map[string]int{}
	images, videos := 0, 0
	for _, child := range manifestChildren {
		if err := validateChild(child, children, nextIndex, &images, &videos); err != nil {
			return nil, 0, 0, err
		}
		children[child.LocalID] = child
	}
	if images > 1 || videos > 1 {
		return nil, 0, 0, invalid(fmt.Sprintf("workflow %s 最多各有一个 image 与 video，收到 %d 个 image、%d 个 video", manifestID, images, videos))
	}
	if images+videos == 0 {
		return nil, 0, 0, invalid(fmt.Sprintf("workflow %s 至少要有一个产物节点（image 或 video）", manifestID))
	}
	return children, images, videos, nil
}

func validateChild(child Child, children map[string]Child, nextIndex map[string]int, images, videos *int) error {
	if _, duplicated := children[child.LocalID]; duplicated {
		return invalid(fmt.Sprintf("localId %q 重复；每个子节点必须有唯一 localId", child.LocalID))
	}
	match := localIDPattern.FindStringSubmatch(child.LocalID)
	if match == nil || match[1] != child.Type {
		return invalid(fmt.Sprintf(
			"localId %q 与类型 %q 不匹配；必须写成 <type>-<N>，例如 image-1", child.LocalID, child.Type))
	}
	index, _ := strconv.Atoi(match[2])
	nextIndex[child.Type]++
	if index != nextIndex[child.Type] {
		return invalid(fmt.Sprintf(
			"localId %q 的序号必须按 children 顺序从 1 连续编号；%s 类第 %d 个应为 %s-%d",
			child.LocalID, child.Type, nextIndex[child.Type], child.Type, nextIndex[child.Type]))
	}
	if strings.TrimSpace(child.Title) == "" {
		return invalid(fmt.Sprintf("子节点 %s 缺少 title", child.LocalID))
	}
	return validateChildShape(child, images, videos)
}

func validateChildShape(child Child, images, videos *int) error {
	switch child.Type {
	case "text":
		if child.Prompt != "" {
			return invalid(fmt.Sprintf("文本节点 %s 不接受 prompt；正文在实例化后由用户或 Agent 写入", child.LocalID))
		}
		if child.Seconds != "" || child.StartFrame != "" {
			return invalid(fmt.Sprintf("文本节点 %s 不接受 seconds / startFrame", child.LocalID))
		}
	case "image":
		*images++
		if child.Seconds != "" || child.StartFrame != "" {
			return invalid(fmt.Sprintf("图片节点 %s 不接受 seconds / startFrame", child.LocalID))
		}
	case "video":
		*videos++
		if child.Seconds != "" {
			seconds, err := strconv.ParseFloat(child.Seconds, 64)
			if err != nil || seconds <= 0 {
				return invalid(fmt.Sprintf("视频节点 %s 的 seconds %q 不是正数", child.LocalID, child.Seconds))
			}
		}
	default:
		return invalid(fmt.Sprintf(
			"子节点 %s 的类型 %q 不受支持；模板只允许 text / image / video", child.LocalID, child.Type))
	}
	return nil
}

func validateConnections(manifestConnections []Connection, children map[string]Child) (map[string]int, error) {
	// 连接顺序即槽位顺序：按数组顺序累计每个目标的文本入边，@文本N 的 N 就是序号。
	slot := map[string]int{}
	edges := map[string]bool{}
	for index, connection := range manifestConnections {
		from, fromOK := children[connection.From]
		to, toOK := children[connection.To]
		if !fromOK {
			return nil, invalid(fmt.Sprintf("connections[%d] 的 from %q 不是已声明的子节点", index, connection.From))
		}
		if !toOK {
			return nil, invalid(fmt.Sprintf("connections[%d] 的 to %q 不是已声明的子节点", index, connection.To))
		}
		if from.Type != "text" {
			return nil, invalid(fmt.Sprintf("connections[%d] 的来源 %s 不是文本节点；模板边只能从文本连到产物", index, connection.From))
		}
		if to.Type == "text" {
			return nil, invalid(fmt.Sprintf("connections[%d] 的目标 %s 是文本节点；模板边只能从文本连到产物", index, connection.To))
		}
		key := connection.From + "\x00" + connection.To
		if edges[key] {
			return nil, invalid(fmt.Sprintf("connections[%d] 重复：%s → %s", index, connection.From, connection.To))
		}
		edges[key] = true
		slot[connection.To]++
	}
	return slot, nil
}

func validateChildReferences(manifestChildren []Child, children map[string]Child, slot map[string]int) error {
	for _, child := range manifestChildren {
		if child.Type == "text" {
			continue
		}
		for _, match := range promptMention.FindAllStringSubmatch(child.Prompt, -1) {
			number, _ := strconv.Atoi(match[1])
			if number < 1 || number > slot[child.LocalID] {
				return invalid(fmt.Sprintf(
					"%s 的 prompt 引用了 %s，但它只有 %d 条文本入边；@文本N 按连接顺序编号",
					child.LocalID, match[0], slot[child.LocalID]))
			}
		}
		if child.StartFrame != "" {
			frame, ok := children[child.StartFrame]
			if !ok {
				return invalid(fmt.Sprintf("%s 的 startFrame %q 不是已声明的子节点", child.LocalID, child.StartFrame))
			}
			if frame.Type != "image" {
				return invalid(fmt.Sprintf("%s 的 startFrame 必须指向图片节点，收到 %s（%s）", child.LocalID, frame.LocalID, frame.Type))
			}
		}
	}
	return nil
}

func invalid(detail string) error {
	return &Error{Reason: "invalid", Detail: detail}
}
