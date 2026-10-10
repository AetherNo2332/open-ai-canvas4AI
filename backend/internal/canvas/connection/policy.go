// Package connection describes canvas editing, independently of generation permissions.
package connection

import (
	"embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

//go:embed builtin.json
var files embed.FS

type Trait struct {
	InputKind           string   `json:"inputKind,omitempty"`
	Mode                string   `json:"mode,omitempty"`
	MetadataMode        bool     `json:"metadataMode,omitempty"`
	Blocked             bool     `json:"blocked,omitempty"`
	AcceptedInputKinds  []string `json:"acceptedInputKinds,omitempty"`
	AcceptedSourceTypes []string `json:"acceptedSourceTypes,omitempty"`
	MaxInputCount       int      `json:"maxInputCount,omitempty"`
}

var Builtins = func() map[string]Trait {
	raw, err := files.ReadFile("builtin.json")
	if err != nil {
		panic(err)
	}
	var traits map[string]Trait
	if err := json.Unmarshal(raw, &traits); err != nil {
		panic(err)
	}
	return traits
}()

type Node struct{ ID, Type, WorkflowKind, Mode string }
type Edge struct{ From, To, FromHandle, ToHandle string }
type Summary struct{ Text, Image, Video, Audio, Character int }

func Mode(node Node) string {
	trait := Builtins[node.Type]
	if trait.MetadataMode && node.Mode != "" {
		return node.Mode
	}
	return trait.Mode
}
func Inputs(nodes []Node, edges []Edge, candidate Edge) Summary {
	lookup := map[string]Node{}
	for _, n := range nodes {
		lookup[n.ID] = n
	}
	seen := map[string]bool{}
	var result Summary
	for _, e := range append(slices.Clone(edges), candidate) {
		if e.To != candidate.To || seen[e.From] {
			continue
		}
		seen[e.From] = true
		node := lookup[e.From]
		kind := Builtins[node.Type].InputKind
		if kind == "" {
			continue
		}
		if node.WorkflowKind == "character" {
			result.Character++
			continue
		}
		switch kind {
		case "text":
			result.Text++
		case "image":
			result.Image++
		case "video":
			result.Video++
		case "audio":
			result.Audio++
		}
	}
	return result
}
func Validate(nodes []Node, edges []Edge, candidate Edge) error {
	lookup := map[string]Node{}
	for _, n := range nodes {
		lookup[n.ID] = n
	}
	from, fromOK := lookup[candidate.From]
	to, toOK := lookup[candidate.To]
	if !fromOK || !toOK || from.ID == to.ID {
		return fmt.Errorf("连线端点不存在或指向自身")
	}
	source, sourceOK := Builtins[from.Type]
	target, targetOK := Builtins[to.Type]
	if !sourceOK || !targetOK {
		return fmt.Errorf("节点类型未注册连线规则")
	}
	if source.Blocked || target.Blocked || from.Type == "config" && to.Type == "config" {
		return fmt.Errorf("这两个节点不允许连线")
	}
	for _, h := range []struct {
		node   Node
		handle string
	}{{from, candidate.FromHandle}, {to, candidate.ToHandle}} {
		if h.node.Type == "batch-table" && strings.HasPrefix(h.handle, "batch-reference:") {
			continue
		}
		if h.handle != "" && (h.node.Type != "script" || h.handle != "storyboard:context" && !strings.HasPrefix(h.handle, "row:")) {
			return fmt.Errorf("节点 handle 无效")
		}
	}
	for _, e := range edges {
		if e == candidate {
			return fmt.Errorf("连线重复")
		}
	}
	if len(target.AcceptedInputKinds) > 0 {
		if !slices.Contains(target.AcceptedInputKinds, source.InputKind) || len(target.AcceptedSourceTypes) > 0 && !slices.Contains(target.AcceptedSourceTypes, from.Type) {
			return fmt.Errorf("目标节点不接受该来源类型")
		}
		ids := map[string]bool{from.ID: true}
		for _, e := range edges {
			if e.To == to.ID {
				ids[e.From] = true
			}
		}
		if target.MaxInputCount > 0 && len(ids) > target.MaxInputCount {
			return fmt.Errorf("目标节点最多连接 %d 个输入", target.MaxInputCount)
		}
	}
	input := Inputs(nodes, edges, candidate)
	switch Mode(to) {
	case "image":
		if input.Video > 0 || input.Audio > 0 {
			return fmt.Errorf("图片生成节点不能连接参考视频或音频")
		}
	case "text":
		if input.Audio > 0 {
			return fmt.Errorf("文本生成节点不能连接参考音频")
		}
	case "audio":
		if input.Character > 1 || input.Image > 0 || input.Video > 0 || input.Audio > 0 {
			return fmt.Errorf("音频生成节点只接受文本或单个角色卡输入")
		}
	}
	return nil
}
