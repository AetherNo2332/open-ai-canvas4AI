package capability

// submapDescriptor is the canvas-internal scope container. Child nodes remain
// top-level nodes and belong to the container through their scopeId field. Business
// meaning (shot, scene, chapter) belongs to the workflow, not to this type.
func submapDescriptor() Descriptor {
	return Descriptor{
		Type: "submap", Version: "1", Label: "子画布", DefaultWidth: 920, DefaultHeight: 360,
		Purpose:     "画布内的一层作用域容器与路径命名空间：把一组节点收在同一个子作用域里，按 submap-N/type-M 寻址。",
		GoodFor:     []string{"按业务分组组织一组节点（镜头、场景、章节等由使用方定义）", "承载 workflow 模板一次性实例化出来的固定子树", "让 Agent 按路径批量读写一组节点"},
		NotIdealFor: []string{"承载已经生成的结果（应使用图片、视频或音频节点）", "只需要视觉分区、不需要作用域归属（应使用背板）", "跨作用域共享同一份提示词"},
		Tradeoffs:   []string{"容器本身不产出媒体，生成仍逐产物节点进行", "子节点在画布顶层存储，靠 scopeId 归属", "workflow 模板只在创建时使用，之后与实例没有活链接", "v1 不支持嵌套 submap"},
		CanUpdate:   true,
		PatchFields: map[string]PatchField{
			"title": {Path: "title", Kind: patchKindString, Label: "节点名称", Order: 10, Description: "子画布标题", MaxRunes: maxAgentNodeTitleRunes},
		},
		Fields: withLockedField(Fields{
			"title": titleField(),
			"collapsed": {
				Path: "metadata.collapsed", Kind: KindBoolean, Label: "已折叠", Order: 20,
				Projected: true, Summary: true,
				Description: "只读：折叠时子节点在画布上隐藏，作用域归属不变",
			},
			"workflowId": {
				Path: "metadata.workflowId", Kind: KindString, Label: "来源 workflow", Order: 30,
				MaxRunes: 80, Projected: true, Summary: true,
				Description: "只读：创建时使用的 workflow id，不是活链接；改模板不影响已创建的 submap",
			},
			"workflowVersion": {
				Path: "metadata.workflowVersion", Kind: KindNumber, Label: "workflow 版本", Order: 40,
				Projected: true, Summary: true,
				Description: "只读：创建时使用的 workflow 版本号",
			},
		}),
		CreateMetadata: func(string) map[string]any {
			return map[string]any{"status": "idle", "collapsed": false}
		},
	}
}
