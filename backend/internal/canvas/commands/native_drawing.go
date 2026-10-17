package commands

import "encoding/json"

// Initial synchronization carries only the empty engine envelope/schema; all
// actual shapes, files, pages and bindings use the existing native record planner.
func (p *planner) applyNativeDrawing(doc Document, op Operation) error {
	node := entity(doc, "node:"+op.ID)
	if node == nil || text(node["type"]) != "drawing" {
		return BadAuthRequest("目标不是绘图节点")
	}
	if op.Engine != "tldraw" && op.Engine != "excalidraw" {
		return BadAuthRequest("绘图引擎无效")
	}
	meta := p.canvasNodeMetadata(node)
	if op.DrawingID != "" {
		if err := validateCanvasID(op.DrawingID, "绘图文档 ID", 128); err != nil {
			return err
		}
		if existing := text(meta["drawingId"]); existing != "" && existing != op.DrawingID {
			return BadAuthRequest("绘图文档身份已变化")
		}
	}
	if op.DrawingSnapshot != nil {
		if meta["drawingDocument"] != nil {
			return BadAuthRequest("绘图原生文档已初始化")
		}
		if op.DrawingID == "" {
			return BadAuthRequest("初始化绘图需要文档 ID")
		}
		if existing := text(meta["drawingEngine"]); existing != "" && existing != op.Engine {
			return BadAuthRequest("绘图引擎不一致")
		}
		if err := p.validateCanvasDrawingValue(op.DrawingSnapshot, 0); err != nil {
			return err
		}
		wrapper := map[string]any{"version": 2, "engine": op.Engine, "snapshot": op.DrawingSnapshot, "revision": 0}
		records, err := p.canvasDrawingRecords(wrapper)
		if err != nil {
			return err
		}
		if len(records) != 0 {
			return BadAuthRequest("初始化信封不能包含绘图记录，请使用原生记录操作")
		}
		meta["drawingDocument"], meta["drawingEngine"] = wrapper, op.Engine
	} else if len(op.DrawingOperations) == 0 && op.DrawingElementIDs == nil && len(op.DrawingOptions) == 0 {
		return BadAuthRequest("绘图命令需要原生记录修改或层排序")
	}
	if err := p.editCanvasDrawingDocument(doc, canvasDrawingArgs{NodeID: op.ID, Engine: op.Engine, Operations: op.DrawingOperations}); err != nil {
		return err
	}
	meta = p.canvasNodeMetadata(entity(doc, "node:"+op.ID))
	if len(op.DrawingOptions) > 0 {
		if op.Engine != "excalidraw" {
			return BadAuthRequest("绘图选项仅用于 Excalidraw 原生文档")
		}
		wrapper, _ := meta["drawingDocument"].(map[string]any)
		snapshot, _ := wrapper["snapshot"].(map[string]any)
		state, _ := snapshot["appState"].(map[string]any)
		if state == nil {
			state = map[string]any{}
		}
		for key, value := range op.DrawingOptions {
			switch key {
			case "viewBackgroundColor":
				if !canvasColor.MatchString(text(value)) && text(value) != "transparent" {
					return BadAuthRequest("绘图背景颜色无效")
				}
			case "gridModeEnabled":
				if _, ok := value.(bool); !ok {
					return BadAuthRequest("绘图网格开关无效")
				}
			case "gridSize", "gridStep":
				if value != nil {
					if n, ok := value.(float64); !ok || n <= 0 || n > 10000 {
						return BadAuthRequest("绘图网格大小无效")
					}
				}
			default:
				return BadAuthRequest("不支持的共享绘图选项")
			}
			state[key] = value
		}
		snapshot["appState"] = state
	}
	if op.DrawingID != "" {
		meta["drawingId"] = op.DrawingID
	}
	if op.DrawingElementIDs != nil {
		if op.Engine != "excalidraw" {
			return BadAuthRequest("tldraw 层排序由原生记录 index 决定")
		}
		wrapper, _ := meta["drawingDocument"].(map[string]any)
		snapshot, _ := wrapper["snapshot"].(map[string]any)
		ordered, err := p.canvasReorderItems(maps(snapshot["elements"]), op.DrawingElementIDs)
		if err != nil {
			return err
		}
		snapshot["elements"] = p.mapsAsAny(ordered)
	}
	raw, err := json.Marshal(meta["drawingDocument"])
	if err != nil {
		return err
	}
	if len(raw) > 4<<20 {
		return BadAuthRequest("绘图原生文档过大")
	}
	return nil
}
