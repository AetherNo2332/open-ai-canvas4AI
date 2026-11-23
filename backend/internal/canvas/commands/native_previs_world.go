package commands

const previsPathEasingRule = "enum:linear,ease_in,ease_out,smooth"
const previsPropRule = "enum:building,tower,chinese_hall,road,wall,ground_patch,stairs,fence_line,room,platform,column,tree_broadleaf,tree_pine,tree_willow,tree_cherry,tree_palm,bamboo,bush,hedge,grass,flowers,flower_bed,potted_plant,lotus_pond,ancient_house,ancient_shop,ancient_courtyard,tower_hall,pagoda,pavilion,city_gate,ancient_wall,paifang,stone_bridge,well,market_stall,lantern_pole,modern_house,villa,apartment,office_tower,skyscraper,shop_front,street_lamp,traffic_light,bench,bus_stop,fence,trash_bin,car,bus,ox_cart,bicycle,rock,boulder,hill,log,sofa_single,sofa,dining_table,chair,bed,bookshelf,desk"
const previsCameraTechniqueRule = "enum:push_in,pull_out,dolly_zoom_out,dolly_zoom_in,zoom_in,zoom_out,pan_left,pan_right,tilt_up,tilt_down,whip_pan_left,whip_pan_right,truck_left,truck_right,roll,pedestal_up,pedestal_down,crane_up,crane_down,orbit_left,orbit_right,follow,look_at,handheld,shake"

var previsPropParamRules = map[string]map[string]string{
	"building":     {"floors": "int:1:80", "width": "range:4:80", "depth": "range:4:60", "floorHeight": "range:2.6:6", "style": "enum:modern,glass,brick,concrete,ancient", "roof": "enum:flat,gable,chinese", "balcony": "enum:none,front"},
	"tower":        {"floors": "int:10:150", "width": "range:12:70", "setbacks": "int:0:3", "crown": "enum:spire,flat,crown"},
	"chinese_hall": {"bays": "int:3:11", "depth": "range:5:20", "tiers": "int:1:5"},
	"road":         {"length": "range:2:600", "width": "range:2:60", "lanes": "int:0:8", "sidewalk": "range:0:10", "surface": "enum:asphalt,stone,dirt"},
	"wall":         {"length": "range:0.5:200", "height": "range:0.3:20", "thickness": "range:0.08:8", "material": "enum:plaster,brick,stone,concrete,wood", "top": "enum:none,tiles,battlement"},
	"ground_patch": {"width": "range:0.5:800", "depth": "range:0.5:800", "surface": "enum:grass,plaza,stone,sand,water,soil,snow"},
	"stairs":       {"width": "range:0.6:40", "steps": "int:1:60", "stepHeight": "range:0.08:0.5", "stepDepth": "range:0.15:1", "material": "enum:stone,concrete,wood"},
	"fence_line":   {"length": "range:1:200", "height": "range:0.4:4", "style": "enum:wood,metal,stone"},
	"room":         {"width": "range:2:40", "depth": "range:2:40", "height": "range:2.2:8", "windows": "int:0:6", "door": "enum:right,none,left,back", "front": "enum:open,closed"},
	"platform":     {"width": "range:0.5:200", "depth": "range:0.5:200", "height": "range:0.05:10", "material": "enum:stone,concrete,wood,plaster"},
	"column":       {"height": "range:0.5:30", "radius": "range:0.05:3", "style": "enum:round,square,chinese"},
}

var previsCameraParamRules = map[string]map[string]string{
	"push_in": {"distance": "range:0.1:200"}, "pull_out": {"distance": "range:0.1:200"},
	"dolly_zoom_out": {"distance": "range:0.1:200", "aim": "range:0:20"}, "dolly_zoom_in": {"distance": "range:0.1:200", "aim": "range:0:20"},
	"zoom_in": {"focalLength": "range:8:300"}, "zoom_out": {"focalLength": "range:8:300"},
	"pan_left": {"angle": "range:1:360"}, "pan_right": {"angle": "range:1:360"},
	"tilt_up": {"angle": "range:1:80"}, "tilt_down": {"angle": "range:1:80"},
	"whip_pan_left": {"angle": "range:15:270"}, "whip_pan_right": {"angle": "range:15:270"},
	"truck_left": {"distance": "range:0.1:200"}, "truck_right": {"distance": "range:0.1:200"}, "roll": {"angle": "range:-180:180"},
	"pedestal_up": {"distance": "range:0.1:100"}, "pedestal_down": {"distance": "range:0.1:100"},
	"crane_up": {"height": "range:0.2:100", "distance": "range:0:200"}, "crane_down": {"height": "range:0.2:100", "distance": "range:0:200"},
	"orbit_left": {"angle": "range:5:720", "rise": "range:-50:50", "aim": "range:0:20"}, "orbit_right": {"angle": "range:5:720", "rise": "range:-50:50", "aim": "range:0:20"},
	"follow":  {"side": "enum:behind,front,left,right,above,shoulder,pov", "shotSize": "enum:extreme_close_up,close_up,medium,full,wide,extreme_wide", "distance": "range:0:60", "height": "range:-5:100", "lag": "range:0:2", "blend": "range:0:10", "aim": "range:0:20"},
	"look_at": {"aim": "range:0:20"}, "handheld": {"intensity": "range:0.1:3"}, "shake": {"intensity": "range:0.1:3"},
}

// Validate the world contract on stored entities while preserving opaque
// extension fields. New writes still pass through the closed previsFields.
func (p *planner) validatePrevisWorldScene(scene map[string]any) error {
	if err := p.validatePrevisSelectedFields(scene, "settings", "world", "markers", "paths"); err != nil {
		return err
	}
	for _, object := range maps(scene["objects"]) {
		if err := p.validatePrevisSelectedFields(object, "object", "propId", "variant", "params", "layout", "setId", "motions", "travels", "motionPath"); err != nil {
			return err
		}
		if err := validatePrevisWorldObject(object); err != nil {
			return err
		}
		if err := p.validatePrevisFrameLifts(object); err != nil {
			return err
		}
	}
	for _, camera := range maps(scene["cameras"]) {
		if err := p.validatePrevisSelectedFields(camera, "camera", "moves", "motionPath"); err != nil {
			return err
		}
	}
	return nil
}

func (p *planner) validatePrevisSelectedFields(item map[string]any, kind string, fields ...string) error {
	for _, field := range fields {
		if value, exists := item[field]; exists {
			if err := p.validatePrevisValue(value, previsFields[kind][field]); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *planner) validatePrevisFrameLifts(object map[string]any) error {
	for _, frame := range maps(object["keyframes"]) {
		if err := p.validatePrevisSelectedFields(frame, "keyframe", "lift"); err != nil {
			return err
		}
	}
	return nil
}

func validatePrevisWorldRecord(item map[string]any, kind string) error {
	switch kind {
	case "object":
		return validatePrevisWorldObject(item)
	case "layout":
		return validatePrevisLayoutSize(item)
	case "motionPath":
		return validatePrevisPathTiming(item)
	case "cameraMove":
		return validatePrevisCameraMove(item)
	}
	return nil
}

func validatePrevisWorldObject(item map[string]any) error {
	prop := text(item["propId"])
	if text(item["kind"]) == "prop" && prop == "" {
		return BadAuthRequest("目录道具必须指定 propId")
	}
	if text(item["kind"]) == "layout" && item["layout"] == nil {
		return BadAuthRequest("程序化布景必须指定 layout")
	}
	if params, exists := item["params"]; exists {
		return (&planner{}).validatePrevisRecord(params, "propParams", previsPropParamRules[prop])
	}
	return nil
}

func validatePrevisLayoutSize(item map[string]any) error {
	rules := map[string]string{
		"ancient_city": "range:160:1200", "ancient_street": "range:80:600", "modern_city": "range:200:1200", "modern_street": "range:80:600",
		"village": "range:100:900", "garden": "range:50:300", "forest": "range:80:1000",
	}
	rule, ok := rules[text(item["template"])]
	if !ok {
		return BadAuthRequest("布景模板不受支持")
	}
	return validatePrevisNumber(item["size"], rule)
}

func validatePrevisPathTiming(item map[string]any) error {
	points, ok := item["points"].([]any)
	if !ok {
		return BadAuthRequest("路径点必须是数组")
	}
	lengths := map[string]int{"speeds": len(points) - 1, "holds": len(points)}
	for field, length := range lengths {
		if raw, exists := item[field]; exists {
			values, ok := raw.([]any)
			if !ok || len(values) != length {
				return BadAuthRequest("路径 " + field + " 数量与途经点不匹配")
			}
		}
	}
	return nil
}

func validatePrevisCameraMove(item map[string]any) error {
	technique, subject := text(item["technique"]), text(item["subjectId"])
	if (technique == "follow" || technique == "look_at") && subject == "" {
		return BadAuthRequest("跟拍与跟摇必须指定 subjectId")
	}
	if params, exists := item["params"]; exists {
		return (&planner{}).validatePrevisRecord(params, "cameraParams", previsCameraParamRules[technique])
	}
	return nil
}
