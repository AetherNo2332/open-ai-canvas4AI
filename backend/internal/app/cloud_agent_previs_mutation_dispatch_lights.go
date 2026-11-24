package app

import (
	"fmt"
	"math"
)

func cloudAgentPrevisLightOperation(scene map[string]any, operation cloudAgentPrevisPatchOperation, lights []map[string]any) (cloudAgentApprovalPreviewItem, error) {
	if operation.Type == "light_add" {
		return cloudAgentPrevisAddLight(scene, operation, lights)
	}
	light := cloudAgentPrevisFindByID(lights, operation.ID)
	if light == nil {
		return cloudAgentApprovalPreviewItem{}, BadAuthRequest("目标灯光不存在")
	}
	if operation.Type == "light_remove" {
		next := make([]map[string]any, 0, len(lights)-1)
		for _, item := range lights {
			if stringValue(item["id"]) != operation.ID {
				next = append(next, item)
			}
		}
		scene["lights"] = next
		return cloudAgentApprovalPreviewItem{Operation: operation.Type, NodeID: operation.ID, NodeTitle: stringValue(light["name"]), Summary: fmt.Sprintf("删除灯光《%s》", stringValue(light["name"]))}, nil
	}
	return cloudAgentPrevisUpdateLight(light, operation)
}

func cloudAgentPrevisAddLight(scene map[string]any, operation cloudAgentPrevisPatchOperation, lights []map[string]any) (cloudAgentApprovalPreviewItem, error) {
	if cloudAgentPrevisFindByID(lights, operation.ID) != nil {
		return cloudAgentApprovalPreviewItem{}, BadAuthRequest("新增灯光 ID 已存在")
	}
	name := "新灯光"
	if operation.Name != nil {
		name = *operation.Name
	}
	if err := cloudAgentPrevisValidateText(name, "灯光名称", cloudAgentPrevisTextLimit, true); err != nil {
		return cloudAgentApprovalPreviewItem{}, err
	}
	lightType := "point"
	if operation.LightType != nil {
		lightType = *operation.LightType
	}
	if err := cloudAgentPrevisValidateEnum(lightType, "灯光类型", "directional", "point", "spot", "ambient"); err != nil {
		return cloudAgentApprovalPreviewItem{}, err
	}
	position, rotation, scale := []float64{0, 3, 0}, []float64{0, 0, 0}, []float64{1, 1, 1}
	if operation.Position != nil {
		position = *operation.Position
	}
	if operation.Rotation != nil {
		rotation = *operation.Rotation
	}
	if operation.Scale != nil {
		scale = *operation.Scale
	}
	if err := cloudAgentPrevisValidateVec3(position, "position"); err != nil {
		return cloudAgentApprovalPreviewItem{}, err
	}
	if err := cloudAgentPrevisValidateVec3(rotation, "rotation"); err != nil {
		return cloudAgentApprovalPreviewItem{}, err
	}
	if err := cloudAgentPrevisValidateVec3(scale, "scale"); err != nil {
		return cloudAgentApprovalPreviewItem{}, err
	}
	intensity := 1.0
	if operation.Intensity != nil {
		intensity = *operation.Intensity
	}
	if err := cloudAgentPrevisFiniteInRange(intensity, "灯光强度", 0, 100); err != nil {
		return cloudAgentApprovalPreviewItem{}, err
	}
	angle, penumbra := math.Pi/4, 0.35
	if operation.Angle != nil {
		angle = *operation.Angle
	}
	if operation.Penumbra != nil {
		penumbra = *operation.Penumbra
	}
	if err := cloudAgentPrevisFiniteInRange(angle, "灯光角度", 0, 6.29); err != nil {
		return cloudAgentApprovalPreviewItem{}, err
	}
	if err := cloudAgentPrevisFiniteInRange(penumbra, "灯光 penumbra", 0, 1); err != nil {
		return cloudAgentApprovalPreviewItem{}, err
	}
	color, castShadow, err := cloudAgentPrevisNewLightAppearance(operation, lightType)
	if err != nil {
		return cloudAgentApprovalPreviewItem{}, err
	}
	light := map[string]any{"id": operation.ID, "name": name, "type": lightType, "transform": map[string]any{"position": cloudAgentPrevisVec3(position), "rotation": cloudAgentPrevisVec3(rotation), "scale": cloudAgentPrevisVec3(scale)}, "color": color, "intensity": intensity, "angle": angle, "penumbra": penumbra, "castShadow": castShadow}
	scene["lights"] = append(lights, light)
	return cloudAgentApprovalPreviewItem{Operation: operation.Type, NodeID: operation.ID, NodeTitle: name, Summary: fmt.Sprintf("新增灯光《%s》", name)}, nil
}

func cloudAgentPrevisNewLightAppearance(operation cloudAgentPrevisPatchOperation, lightType string) (string, bool, error) {
	color := "#ffffff"
	if operation.Color != nil {
		if err := cloudAgentPrevisValidateColor(*operation.Color, "灯光颜色"); err != nil {
			return "", false, err
		}
		color = *operation.Color
	}
	castShadow := lightType != "ambient"
	if operation.CastShadow != nil {
		castShadow = *operation.CastShadow
	}
	return color, castShadow, nil
}

func cloudAgentPrevisUpdateLight(light map[string]any, operation cloudAgentPrevisPatchOperation) (cloudAgentApprovalPreviewItem, error) {
	if err := cloudAgentPrevisApplyName(light, operation.Name, "灯光名称"); err != nil {
		return cloudAgentApprovalPreviewItem{}, err
	}
	if operation.LightType != nil {
		if err := cloudAgentPrevisValidateEnum(*operation.LightType, "灯光类型", "directional", "point", "spot", "ambient"); err != nil {
			return cloudAgentApprovalPreviewItem{}, err
		}
		light["type"] = *operation.LightType
	}
	if operation.Intensity != nil {
		if err := cloudAgentPrevisFiniteInRange(*operation.Intensity, "灯光强度", 0, 100); err != nil {
			return cloudAgentApprovalPreviewItem{}, err
		}
		light["intensity"] = *operation.Intensity
	}
	if operation.Color != nil {
		if err := cloudAgentPrevisValidateColor(*operation.Color, "灯光颜色"); err != nil {
			return cloudAgentApprovalPreviewItem{}, err
		}
		light["color"] = *operation.Color
	}
	if operation.Angle != nil {
		if err := cloudAgentPrevisFiniteInRange(*operation.Angle, "灯光角度", 0, 6.29); err != nil {
			return cloudAgentApprovalPreviewItem{}, err
		}
		light["angle"] = *operation.Angle
	}
	if operation.Penumbra != nil {
		if err := cloudAgentPrevisFiniteInRange(*operation.Penumbra, "灯光 penumbra", 0, 1); err != nil {
			return cloudAgentApprovalPreviewItem{}, err
		}
		light["penumbra"] = *operation.Penumbra
	}
	if operation.CastShadow != nil {
		light["castShadow"] = *operation.CastShadow
	}
	if err := cloudAgentPrevisApplyTransform(light, operation); err != nil {
		return cloudAgentApprovalPreviewItem{}, err
	}
	return cloudAgentApprovalPreviewItem{Operation: operation.Type, NodeID: operation.ID, NodeTitle: stringValue(light["name"]), Summary: fmt.Sprintf("修改灯光《%s》", stringValue(light["name"]))}, nil
}
