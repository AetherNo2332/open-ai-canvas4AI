// Package submappreset defines the persisted shape and generic validation for
// user-owned submap presets. A preset is a localized snapshot of ordinary
// canvas nodes, not the restricted storyboard manifest.
package submappreset

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"yingce/backend/internal/canvas/capability"
)

const SupportedVersion = 1

const (
	MaxNodes       = 200
	MaxSnapshotLen = 512 << 10
	MaxNameRunes   = 80
)

var (
	refArrayFields  = []string{"referenceNodeIds", "referenceTextNodeIds"}
	refScalarFields = []string{"videoStartFrameNodeId", "videoEndFrameNodeId"}
	localIDPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
)

// Snapshot stores the reusable part of a submap. The container is recreated on
// insertion; only the title and localized child nodes live in the snapshot.
type Snapshot struct {
	Version int    `json:"version"`
	ID      string `json:"id"`
	Title   string `json:"title"`
	Nodes   []Node `json:"nodes"`
}

// Node is a canvas node in local-id form. Metadata is intentionally retained
// without a content allowlist so user-authored prompts, media references and
// other ordinary node fields round-trip unchanged.
type Node struct {
	LocalID  string         `json:"localId"`
	Type     string         `json:"type"`
	Title    string         `json:"title"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// Validate accepts any registered canvas node type and checks only snapshot
// self-containment: unique local ids and references that point inside the set.
func Validate(snapshot Snapshot) error {
	if err := validateHeader(snapshot); err != nil {
		return err
	}
	ids, err := validateNodes(snapshot.Nodes)
	if err != nil {
		return err
	}
	return validateReferences(snapshot.Nodes, ids)
}

func validateHeader(snapshot Snapshot) error {
	if snapshot.Version != SupportedVersion {
		return fmt.Errorf("预制版本是 %d，当前只接受 %d；版本不符整份拒绝", snapshot.Version, SupportedVersion)
	}
	if strings.TrimSpace(snapshot.ID) == "" {
		return errors.New("预制缺少 id")
	}
	if strings.TrimSpace(snapshot.Title) == "" {
		return errors.New("预制缺少 title")
	}
	if len([]rune(snapshot.Title)) > MaxNameRunes {
		return fmt.Errorf("预制名称最多 %d 个字", MaxNameRunes)
	}
	if len(snapshot.Nodes) == 0 {
		return errors.New("预制没有任何节点")
	}
	if len(snapshot.Nodes) > MaxNodes {
		return fmt.Errorf("预制节点数 %d 超过上限 %d", len(snapshot.Nodes), MaxNodes)
	}
	return nil
}

func validateNodes(nodes []Node) (map[string]struct{}, error) {
	registry := capability.BuiltinRegistry()
	ids := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		if !localIDPattern.MatchString(node.LocalID) {
			return nil, fmt.Errorf("节点 localId %q 无效；只能含小写字母、数字、连字符与下划线", node.LocalID)
		}
		if _, exists := ids[node.LocalID]; exists {
			return nil, fmt.Errorf("节点 localId %q 重复", node.LocalID)
		}
		ids[node.LocalID] = struct{}{}
		if _, ok := registry.Resolve(node.Type); !ok {
			return nil, fmt.Errorf("节点 %s 的类型 %q 不是当前画布支持的节点类型", node.LocalID, node.Type)
		}
		if strings.TrimSpace(node.Title) == "" {
			return nil, fmt.Errorf("节点 %s 缺少 title", node.LocalID)
		}
	}
	return ids, nil
}

func validateReferences(nodes []Node, ids map[string]struct{}) error {
	for _, node := range nodes {
		for _, field := range refArrayFields {
			refs, err := stringArray(node.Metadata[field])
			if err != nil {
				return fmt.Errorf("节点 %s 的 %s 必须是字符串数组", node.LocalID, field)
			}
			for _, ref := range refs {
				if ref != "" {
					if _, exists := ids[ref]; !exists {
						return fmt.Errorf("节点 %s 的 %s 引用了不存在的节点 %q（预制必须自包含）", node.LocalID, field, ref)
					}
				}
			}
		}
		for _, field := range refScalarFields {
			value, exists := node.Metadata[field]
			if !exists || value == nil {
				continue
			}
			ref, ok := value.(string)
			if !ok {
				return fmt.Errorf("节点 %s 的 %s 必须是字符串", node.LocalID, field)
			}
			if ref != "" {
				if _, exists := ids[ref]; !exists {
					return fmt.Errorf("节点 %s 的 %s 引用了不存在的节点 %q（预制必须自包含）", node.LocalID, field, ref)
				}
			}
		}
		if err := validateBindingReferences(node, ids); err != nil {
			return err
		}
	}
	return nil
}

func validateBindingReferences(node Node, ids map[string]struct{}) error {
	generationSpec, exists := node.Metadata["generationSpec"]
	if exists && generationSpec != nil {
		spec, ok := generationSpec.(map[string]any)
		if !ok {
			return fmt.Errorf("节点 %s 的 generationSpec 必须是对象", node.LocalID)
		}
		if err := validateBindingList(node, "generationSpec.referenceBindings", spec["referenceBindings"], ids); err != nil {
			return err
		}
	}
	return validateBindingList(node, "inputBindings", node.Metadata["inputBindings"], ids)
}

func validateBindingList(node Node, field string, value any, ids map[string]struct{}) error {
	if value == nil {
		return nil
	}
	bindings, ok := value.([]any)
	if !ok {
		return fmt.Errorf("节点 %s 的 %s 必须是数组", node.LocalID, field)
	}
	for index, value := range bindings {
		binding, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("节点 %s 的 %s[%d] 必须是对象", node.LocalID, field, index)
		}
		ref, exists := binding["nodeId"]
		if !exists {
			continue
		}
		nodeID, ok := ref.(string)
		if !ok || nodeID == "" {
			return fmt.Errorf("节点 %s 的 %s[%d].nodeId 必须是非空字符串", node.LocalID, field, index)
		}
		if _, exists := ids[nodeID]; !exists {
			return fmt.Errorf("节点 %s 的 %s[%d].nodeId 引用了不存在的节点 %q（预制必须自包含）", node.LocalID, field, index, nodeID)
		}
	}
	return nil
}

func stringArray(value any) ([]string, error) {
	switch items := value.(type) {
	case nil:
		return nil, nil
	case []string:
		return items, nil
	case []any:
		result := make([]string, 0, len(items))
		for _, item := range items {
			value, ok := item.(string)
			if !ok {
				return nil, errors.New("not a string array")
			}
			if value != "" {
				result = append(result, value)
			}
		}
		return result, nil
	default:
		return nil, errors.New("not a string array")
	}
}
