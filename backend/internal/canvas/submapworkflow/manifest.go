// Package submapworkflow 定义 submap 的创建模板。
//
// workflow 不是画布里的活节点，只是 copy-on-create 的固定子树声明：创建 submap 时
// 按它实例化节点、内部边与初始 prompt，之后模板与实例没有活链接。真源是
// testdata/<id>.json，经 go:embed 进二进制；不放进 backend/builtin——那里按技能
// 目录扫描，缺 SKILL.md 会被当成坏技能。
package submapworkflow

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// SupportedVersion 是当前唯一接受的 manifest 协议版本。不符整份拒绝，不猜兼容
// （与技能配方 presets.json 同型）。
const SupportedVersion = 1

//go:embed testdata/*.json
var embedded embed.FS

// Manifest 是一份 submap workflow。children 与 connections 的数组顺序都是语义：
// children 是固定显示顺序，connections 是槽位顺序（@文本N 按它编号）。
type Manifest struct {
	Version     int          `json:"version"`
	ID          string       `json:"id"`
	Title       string       `json:"title"`
	Children    []Child      `json:"children"`
	Connections []Connection `json:"connections"`
}

// Child 是模板里的一个固定子节点。localId 由 id 去掉 `${scopeId}__` 前缀派生，
// 所以模板里的 localId 必须等于实例化后按类型顺序分配出的本地号。
type Child struct {
	LocalID    string `json:"localId"`
	Type       string `json:"type"`
	Title      string `json:"title"`
	Prompt     string `json:"prompt,omitempty"`
	Seconds    string `json:"seconds,omitempty"`
	StartFrame string `json:"startFrame,omitempty"`
}

// Connection 是一条内部边；from 必须是 text，to 必须是产物节点。
type Connection struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Error 是校验失败：Reason 供调用方分型，Detail 面向模型。
type Error struct {
	Reason string
	Detail string
}

func (e *Error) Error() string { return e.Detail }

// Load 读取并校验一份内置 workflow。
func Load(id string) (Manifest, error) {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return Manifest{}, &Error{Reason: "required", Detail: "workflow id 不能为空"}
	}
	data, err := embedded.ReadFile("testdata/" + trimmed + ".json")
	if err != nil {
		return Manifest{}, &Error{Reason: "not_found", Detail: fmt.Sprintf(
			"未知的 submap workflow %q；可用：%s", trimmed, strings.Join(Available(), "、"))}
	}
	var manifest Manifest
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, &Error{Reason: "invalid", Detail: fmt.Sprintf(
			"workflow %s 的 manifest 无法解析：%s", trimmed, err.Error())}
	}
	if err := Validate(manifest); err != nil {
		return Manifest{}, err
	}
	if manifest.ID != trimmed {
		return Manifest{}, &Error{Reason: "invalid", Detail: fmt.Sprintf(
			"workflow 文件 %s.json 声明了 id %q；文件名与 id 必须一致", trimmed, manifest.ID)}
	}
	return manifest, nil
}

// Available 列出内置 workflow id，供未知 id 的报错指路。
func Available() []string {
	entries, err := embedded.ReadDir("testdata")
	if err != nil {
		return nil
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		ids = append(ids, strings.TrimSuffix(name, ".json"))
	}
	sort.Strings(ids)
	return ids
}
