package app

import (
	"encoding/base64"
	"encoding/binary"
	"math"
	"net/url"
	"strings"
)

// Native record contract for the pinned tldraw 5.2.5 built-in schema. Resource
// references are hydrated to native media URLs by the frontend before loadSnapshot.
// The shared fixture is generated and validated by that actual engine.
var cloudAgentTLShapeProps = map[string]string{
	"group":     "",
	"geo":       "geo:geo dash:dash url:link w:positive h:positive growY:nonnegative scale:positive labelColor:color color:color fill:fill size:size font:font align:align verticalAlign:vertical richText:richtext",
	"text":      "color:color size:size font:font textAlign:textalign w:positive richText:richtext scale:positive autoSize:bool",
	"note":      "color:color labelColor:color size:size font:font fontSizeAdjustment:nonnegative-null align:align verticalAlign:vertical growY:nonnegative url:link richText:richtext scale:positive textLastEditedBy:string-null",
	"frame":     "w:positive h:positive name:string color:color",
	"embed":     "w:positive h:positive url:string",
	"bookmark":  "w:positive h:positive assetId:asset-null url:link",
	"image":     "w:positive h:positive playing:bool url:link assetId:asset-null crop:crop-null flipX:bool flipY:bool altText:string",
	"video":     "w:positive h:positive time:number playing:bool autoplay:bool url:link assetId:asset-null altText:string",
	"draw":      "color:color fill:fill dash:dash size:size segments:segments isComplete:bool isClosed:bool isPen:bool scale:positive scaleX:nonzero scaleY:nonzero",
	"highlight": "color:color size:size segments:segments isComplete:bool isPen:bool scale:positive scaleX:nonzero scaleY:nonzero",
	"line":      "color:color dash:dash size:size spline:spline points:points scale:positive",
	"arrow":     "kind:arrowkind labelColor:color color:color fill:fill dash:dash size:size arrowheadStart:arrowhead arrowheadEnd:arrowhead font:font start:vec end:vec bend:number richText:richtext labelPosition:number scale:positive elbowMidPoint:number",
}

var cloudAgentTLEnums = map[string]string{
	"color": "black grey light-violet violet blue light-blue yellow orange green light-green light-red red white",
	"dash":  "draw solid dashed dotted none", "fill": "none semi solid pattern fill lined-fill",
	"size": "s m l xl", "font": "draw sans serif mono",
	"align": "start middle end start-legacy end-legacy middle-legacy", "vertical": "start middle end", "textalign": "start middle end",
	"geo":    "cloud rectangle ellipse triangle diamond pentagon hexagon octagon star rhombus rhombus-2 oval trapezoid arrow-right arrow-left arrow-up arrow-down x-box check-box heart",
	"spline": "line cubic", "arrowkind": "arc elbow", "arrowhead": "arrow triangle square dot pipe diamond inverted bar none",
	"terminal": "start end", "snap": "center edge-point edge none", "segmenttype": "free straight",
}

func validateCloudAgentTldrawRecords(records map[string]map[string]any) error {
	pageCount, documentCount := 0, 0
	for id, record := range records {
		kind := stringValue(record["typeName"])
		if stringValue(record["id"]) != id || !strings.HasPrefix(id, kind+":") || len(id) <= len(kind)+1 {
			return BadAuthRequest("tldraw 记录 ID 与类型不一致")
		}
		if err := validateCloudAgentDrawingValue(record, 0); err != nil {
			return err
		}
		var fields string
		switch kind {
		case "document":
			documentCount++
			if id != "document:document" {
				return BadAuthRequest("tldraw 文档 ID 无效")
			}
			fields = "id:string typeName:string gridSize:number name:string meta:jsonobject"
		case "page":
			pageCount++
			fields = "id:string typeName:string name:string index:index meta:jsonobject"
		case "shape":
			fields = "id:string typeName:string type:string x:number y:number rotation:number index:index parentId:string isLocked:bool opacity:opacity props:object meta:jsonobject"
			specs, supported := cloudAgentTLShapeProps[stringValue(record["type"])]
			if !supported {
				return BadAuthRequest("不支持的 tldraw 原生形状类型")
			}
			props, ok := record["props"].(map[string]any)
			if !ok || !cloudAgentTLObject(props, specs) {
				return BadAuthRequest("tldraw 形状 props 不完整或无效")
			}
			parent := records[stringValue(record["parentId"])]
			parentType := stringValue(parent["typeName"])
			if parentType != "page" && parentType != "shape" {
				return BadAuthRequest("tldraw 形状缺少有效父页面或形状")
			}
			if assetID := stringValue(props["assetId"]); assetID != "" {
				asset := records[assetID]
				if stringValue(asset["typeName"]) != "asset" || stringValue(asset["type"]) != stringValue(record["type"]) {
					return BadAuthRequest("tldraw 形状素材不存在或类型不匹配")
				}
			}
		case "asset":
			fields = "id:string typeName:string type:string props:object meta:jsonobject"
			props, ok := record["props"].(map[string]any)
			specs := "w:number h:number name:string isAnimated:bool mimeType:string-null src:resource-null fileSize?:positive pixelRatio?:nonnegative"
			switch stringValue(record["type"]) {
			case "image":
			case "video":
				specs = "w:number h:number name:string isAnimated:bool mimeType:string-null src:resource-null fileSize?:number"
			case "bookmark":
				specs = "title:string description:string image:string favicon:string src:resource-null"
			default:
				return BadAuthRequest("不支持的 tldraw 原生素材类型")
			}
			if !ok || !cloudAgentTLObject(props, specs) {
				return BadAuthRequest("tldraw 素材 props 不完整或无效")
			}
		case "binding":
			fields = "id:string typeName:string type:string fromId:string toId:string props:object meta:jsonobject"
			props, ok := record["props"].(map[string]any)
			if stringValue(record["type"]) != "arrow" || !ok || !cloudAgentTLObject(props, "terminal:terminal normalizedAnchor:vec isExact:bool isPrecise:bool snap:snap") {
				return BadAuthRequest("tldraw 绑定类型或 props 无效")
			}
			source, target := records[stringValue(record["fromId"])], records[stringValue(record["toId"])]
			if stringValue(source["typeName"]) != "shape" || stringValue(source["type"]) != "arrow" || stringValue(target["typeName"]) != "shape" || source == nil || target == nil || source["id"] == target["id"] {
				return BadAuthRequest("tldraw 箭头绑定缺少有效来源或目标形状")
			}
		default:
			return BadAuthRequest("不支持的 tldraw 原生记录类型")
		}
		if !cloudAgentTLObject(record, fields) {
			return BadAuthRequest("tldraw 原生记录字段不完整或无效")
		}
	}
	if pageCount < 1 || documentCount != 1 {
		return BadAuthRequest("tldraw 需要完整文档及至少一个页面")
	}
	// A valid shape ancestry must terminate at a page, never a cycle.
	visited := map[string]bool{}
	for id, record := range records {
		if stringValue(record["typeName"]) != "shape" || visited[id] {
			continue
		}
		path := map[string]bool{}
		current := id
		for stringValue(records[current]["typeName"]) == "shape" && !visited[current] {
			if path[current] {
				return BadAuthRequest("tldraw 形状父级形成循环")
			}
			path[current] = true
			current = stringValue(records[current]["parentId"])
		}
		for item := range path {
			visited[item] = true
		}
	}
	return nil
}

func cloudAgentTLObject(object map[string]any, specs string) bool {
	allowed := map[string]bool{}
	for _, field := range strings.Fields(specs) {
		pair := strings.SplitN(field, ":", 2)
		key := strings.TrimSuffix(pair[0], "?")
		allowed[key] = true
		value, exists := object[key]
		if !exists && strings.HasSuffix(pair[0], "?") {
			continue
		}
		if !exists || !cloudAgentTLValue(value, pair[1]) {
			return false
		}
	}
	for key := range object {
		if !allowed[key] {
			return false
		}
	}
	return true
}

func cloudAgentTLValue(value any, rule string) bool {
	if strings.HasSuffix(rule, "-null") {
		if value == nil {
			return true
		}
		rule = strings.TrimSuffix(rule, "-null")
	}
	if values, ok := cloudAgentTLEnums[rule]; ok {
		text, valid := value.(string)
		if !valid {
			return false
		}
		for _, candidate := range strings.Fields(values) {
			if candidate == text {
				return true
			}
		}
		return false
	}
	switch rule {
	case "string":
		_, ok := value.(string)
		return ok
	case "bool":
		_, ok := value.(bool)
		return ok
	case "object", "jsonobject":
		_, ok := value.(map[string]any)
		return ok
	case "number", "positive", "nonnegative", "nonzero", "opacity":
		n, ok := value.(float64)
		if !ok || math.IsNaN(n) || math.IsInf(n, 0) {
			return false
		}
		switch rule {
		case "positive":
			return n > 0
		case "nonnegative":
			return n >= 0
		case "nonzero":
			return n != 0
		case "opacity":
			return n >= 0 && n <= 1
		}
		return true
	case "index":
		text, ok := value.(string)
		if !ok || len(text) < 2 {
			return false
		}
		integerLength := 0
		if text[0] >= 'a' && text[0] <= 'z' {
			integerLength = int(text[0]-'a') + 2
		}
		if text[0] >= 'A' && text[0] <= 'Z' {
			integerLength = int('Z'-text[0]) + 2
		}
		return integerLength > 0 && len(text) >= integerLength && text != "A"+strings.Repeat("0", 26) && !(len(text) > integerLength && strings.HasSuffix(text, "0"))
	case "asset":
		text, ok := value.(string)
		return ok && strings.HasPrefix(text, "asset:") && len(text) > 6
	case "resource":
		text, ok := value.(string)
		return ok && strings.HasPrefix(text, "resource:") && len(text) > 9
	case "link":
		text, ok := value.(string)
		if !ok {
			return false
		}
		if text == "" {
			return true
		}
		parsed, err := url.Parse(text)
		return err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http" || parsed.Scheme == "mailto")
	case "vec":
		object, ok := value.(map[string]any)
		return ok && cloudAgentTLObject(object, "x:number y:number z?:number")
	case "crop":
		object, ok := value.(map[string]any)
		return ok && cloudAgentTLObject(object, "topLeft:vec bottomRight:vec isCircle?:bool")
	case "richtext":
		object, ok := value.(map[string]any)
		return ok && cloudAgentTLObject(object, "type:string content:array attrs?:any")
	case "array":
		_, ok := value.([]any)
		return ok
	case "any":
		return true
	case "segments":
		segments, ok := value.([]any)
		if !ok {
			return false
		}
		for _, item := range segments {
			segment, ok := item.(map[string]any)
			if !ok || !cloudAgentTLObject(segment, "type:segmenttype path:string dim?:dimension") {
				return false
			}
			if !cloudAgentTLPath(segment) {
				return false
			}
		}
		return true
	case "dimension":
		n, ok := value.(float64)
		return ok && (n == 2 || n == 3)
	case "points":
		points, ok := value.(map[string]any)
		if !ok {
			return false
		}
		for _, item := range points {
			point, ok := item.(map[string]any)
			if !ok || !cloudAgentTLObject(point, "id:string index:index x:number y:number") {
				return false
			}
		}
		return true
	}
	return false
}

// The native schema only declares path:string; the renderer then decodes it as
// Float32 first coordinates plus Float16 deltas. Reject strings that loadSnapshot
// would accept but that would throw or introduce NaN while rendering a stroke.
func cloudAgentTLPath(segment map[string]any) bool {
	path := stringValue(segment["path"])
	if path == "" {
		return true
	}
	dim := 3
	if segment["dim"] == float64(2) {
		dim = 2
	}
	data, err := base64.StdEncoding.Strict().DecodeString(path)
	if err != nil || len(data) < dim*4 || (len(data)-dim*4)%(dim*2) != 0 {
		return false
	}
	for offset := 0; offset < dim*4; offset += 4 {
		n := float64(math.Float32frombits(binary.LittleEndian.Uint32(data[offset : offset+4])))
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return false
		}
	}
	for offset := dim * 4; offset < len(data); offset += 2 {
		if binary.LittleEndian.Uint16(data[offset:offset+2])&0x7c00 == 0x7c00 {
			return false
		}
	}
	return true
}
