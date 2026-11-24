package app

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

const (
	cloudAgentPrevisMaxOperations = 32
	cloudAgentPrevisMaxScenes     = 32
	cloudAgentPrevisMaxShots      = 64
	cloudAgentPrevisMaxObjects    = 128
	cloudAgentPrevisMaxCameras    = 32
	cloudAgentPrevisMaxLights     = 64
	cloudAgentPrevisDocumentLimit = 1 << 20
	cloudAgentPrevisTextLimit     = 160
)

// cloudAgentPrevisAspectRatios 与前端 PREVIS_ASPECT_RATIOS 同序；未设置时按 9:16。
var cloudAgentPrevisAspectRatios = []string{"auto", "2.39:1", "21:9", "16:9", "4:3", "1:1", "3:4", "9:16"}

type cloudAgentPrevisSceneCreateArgs struct {
	CanvasSnapshotHash string `json:"canvasSnapshotHash"`
	SceneID            string `json:"sceneId"`
	Title              string `json:"title"`
	TemplateID         string `json:"templateId"`
}

type cloudAgentPrevisApplyPatchArgs struct {
	SnapshotHash string                           `json:"snapshotHash"`
	SceneID      string                           `json:"sceneId"`
	Operations   []cloudAgentPrevisPatchOperation `json:"operations"`
}

// cloudAgentPrevisPatchOperation is deliberately a closed semantic contract.
// It has no generic path, metadata, URL, storage key or raw scene field.
type cloudAgentPrevisPatchOperation struct {
	Type      string  `json:"type"`
	ID        string  `json:"id"`
	Name      *string `json:"name"`
	Title     *string `json:"title"`
	Kind      string  `json:"kind"`
	Primitive string  `json:"primitive"`
	Archetype string  `json:"archetype"` // adult/child/elderly/man/woman/monster
	Color     *string `json:"color"`
	ParentID  *string `json:"parentId"`
	Visible   *bool   `json:"visible"`
	Pose      string  `json:"pose"`
	Motion    string  `json:"motion"`

	CharacterAssetID   *string `json:"characterAssetId"`
	CharacterVersionID *string `json:"characterVersionId"`
	ReferenceNodeID    *string `json:"referenceNodeId"`
	CharacterName      *string `json:"characterName"`

	Position *[]float64 `json:"position"`
	Rotation *[]float64 `json:"rotation"`
	Scale    *[]float64 `json:"scale"`
	Target   *[]float64 `json:"target"`

	KeyframeID   *string     `json:"keyframeId"`
	Time         *float64    `json:"time"`
	Easing       string      `json:"easing"`
	PathPoints   [][]float64 `json:"pathPoints"`
	PathSpeed    string      `json:"pathSpeed"`
	StartDelay   *float64    `json:"startDelay"`
	OrientToPath *bool       `json:"orientToPath"`
	// object_motion_path_set：pathId 引用用户画的参考轨迹（scene.paths 的标签或 ID）。
	PathID *string `json:"pathId"`

	CameraID    *string  `json:"cameraId"`
	Duration    *float64 `json:"duration"`
	FPS         *int     `json:"fps"`
	ShotSize    string   `json:"shotSize"`
	AspectRatio string   `json:"aspectRatio"`
	CameraMove  string   `json:"cameraMove"`
	Prompt      *string  `json:"prompt"`
	FocalLength *float64 `json:"focalLength"`

	LightType  *string  `json:"lightType"`
	Intensity  *float64 `json:"intensity"`
	Angle      *float64 `json:"angle"`
	Penumbra   *float64 `json:"penumbra"`
	CastShadow *bool    `json:"castShadow"`

	Background           *string  `json:"background"`
	EnvironmentIntensity *float64 `json:"environmentIntensity"`
	GridVisible          *bool    `json:"gridVisible"`
	ActiveShotID         *string  `json:"activeShotId"`

	// kind=prop：目录道具；kind=layout：程序化布景。白名单见 cloud_agent_previs_catalog.go。
	PropID         string   `json:"propId"`
	Variant        *int     `json:"variant"`
	LayoutTemplate string   `json:"layoutTemplate"`
	Seed           *int     `json:"seed"`
	Size           *float64 `json:"size"`
	Density        *float64 `json:"density"`
	Crowd          *bool    `json:"crowd"`
	// 参数化组件参数（building 的 floors/width/style…）；键与范围见 cloud_agent_previs_params.go。
	Params map[string]any `json:"params"`

	// object_array_add：批量摆放同一道具。
	Count   *int     `json:"count"`
	Pattern string   `json:"pattern"`
	Spacing *float64 `json:"spacing"`
	Columns *int     `json:"columns"`
	Facing  string   `json:"facing"`
	Vary    *float64 `json:"vary"`

	// actor_travel：A→B 位移；起终点可用右键标记点标签或 pathPoints。
	TravelStyle string   `json:"travelStyle"`
	FromMarker  *string  `json:"fromMarker"`
	ToMarker    *string  `json:"toMarker"`
	Height      *float64 `json:"height"`

	// camera_move：运镜手法与跟拍 / 环绕主体；参数走 params，白名单见 cloud_agent_previs_camera.go。
	// recipe 与 technique 二选一：按大师手法表一次展开成多段运镜，见 cloud_agent_previs_camera_recipes.go。
	Technique string  `json:"technique"`
	Recipe    string  `json:"recipe"`
	SubjectID *string `json:"subjectId"`

	// world_update：时间、天气、远景、地面范围；worldEnabled=false 关闭世界环境。
	TimeOfDay    *float64 `json:"timeOfDay"`
	Weather      string   `json:"weather"`
	Ground       string   `json:"ground"`
	GroundSize   *int     `json:"groundSize"`
	Horizon      *bool    `json:"horizon"`
	WorldEnabled *bool    `json:"worldEnabled"`
}

type cloudAgentPrevisMutationPlan struct {
	Canvas             *model.CanvasProject
	Document           map[string]any
	BeforeJSON         string
	BeforeSnapshotHash string
	AfterSnapshotHash  string
	ResultSnapshotHash string
	Operation          string
	Preview            cloudAgentApprovalPreview
	SceneID            string
}

func cloudAgentPrevisPatchOperationSchema() map[string]any {
	vec3 := map[string]any{"type": "array", "minItems": 3, "maxItems": 3, "items": map[string]any{"type": "number"}}
	properties := map[string]any{
		"type":               map[string]any{"type": "string", "enum": []string{"scene_update", "world_update", "object_array_add", "actor_travel", "camera_move", "camera_move_remove", "shot_add", "shot_update", "shot_remove", "object_add", "object_update", "object_remove", "object_keyframe_upsert", "object_keyframe_remove", "object_motion_path_set", "object_motion_path_clear", "actor_motion_add", "actor_motion_remove", "camera_add", "camera_update", "camera_remove", "camera_keyframe_upsert", "camera_keyframe_remove", "light_add", "light_update", "light_remove"}},
		"id":                 map[string]any{"type": "string", "maxLength": 80, "description": "目标或新建资源 ID"},
		"name":               map[string]any{"type": "string", "maxLength": cloudAgentPrevisTextLimit},
		"title":              map[string]any{"type": "string", "maxLength": cloudAgentPrevisTextLimit},
		"kind":               map[string]any{"type": "string", "enum": []string{"prop", "layout", "actor", "primitive"}},
		"primitive":          map[string]any{"type": "string", "enum": []string{"box", "sphere", "cylinder", "plane", "character"}},
		"archetype":          map[string]any{"type": "string", "enum": []string{"adult", "child", "elderly", "man", "woman", "monster"}, "description": "演员体型原型"},
		"characterAssetId":   map[string]any{"type": "string", "maxLength": 120, "description": "角色绑定：canvas_get_state 角色卡的 character.assetId；与 characterVersionId、referenceNodeId 三者必须同时提供"},
		"characterVersionId": map[string]any{"type": "string", "maxLength": 120, "description": "角色绑定：同一角色卡的 character.versionId；三者必须同时提供"},
		"referenceNodeId":    map[string]any{"type": "string", "maxLength": 120, "description": "角色绑定：该角色卡节点 id；三者必须同时提供"},
		"characterName":      map[string]any{"type": "string", "maxLength": cloudAgentPrevisTextLimit},
		"pose":               map[string]any{"type": "string", "enum": []string{"stand", "sit", "walk", "run", "wave", "think", "fight", "kick", "throw", "push", "reach", "arms_crossed", "phone", "bow", "kneel_single", "kneel_double", "lean", "squat", "hands_hips", "neutral", "t_pose"}, "description": "演员姿态"},
		"motion":             map[string]any{"type": "string", "enum": cloudAgentPrevisMotionIDs, "description": "actor_motion_add 动作；time=开始秒，duration=时长，intensity=幅度0.2-2；remove 用 keyframeId 传动作 id"},
		"color":              map[string]any{"type": "string", "maxLength": 9, "pattern": `^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$`, "description": "十六进制颜色"},
		"parentId":           map[string]any{"type": "string", "maxLength": 80},
		"visible":            map[string]any{"type": "boolean"},

		"position":             vec3,
		"rotation":             vec3,
		"scale":                vec3,
		"target":               vec3,
		"keyframeId":           map[string]any{"type": "string", "maxLength": 120},
		"time":                 map[string]any{"type": "number", "minimum": 0, "maximum": 60},
		"easing":               map[string]any{"type": "string", "enum": []string{"step", "linear", "smooth", "ease_in", "ease_out"}},
		"pathPoints":           map[string]any{"type": "array", "minItems": 2, "maxItems": 64, "items": vec3},
		"pathSpeed":            map[string]any{"type": "string", "enum": []string{"slow", "walk", "run"}},
		"pathId":               map[string]any{"type": "string", "maxLength": 80},
		"startDelay":           map[string]any{"type": "number", "minimum": 0, "maximum": 60},
		"orientToPath":         map[string]any{"type": "boolean"},
		"cameraId":             map[string]any{"type": "string", "maxLength": 80},
		"duration":             map[string]any{"type": "number", "minimum": 0.1, "maximum": 60},
		"fps":                  map[string]any{"type": "integer", "minimum": 1, "maximum": 60},
		"shotSize":             map[string]any{"type": "string", "enum": []string{"extreme_wide", "wide", "full", "medium", "close_up", "extreme_close_up"}},
		"aspectRatio":          map[string]any{"type": "string", "enum": cloudAgentPrevisAspectRatios},
		"cameraMove":           map[string]any{"type": "string", "enum": []string{"static", "push_in", "pull_out", "pan_left", "pan_right", "tilt_up", "tilt_down", "orbit_left", "orbit_right", "handheld"}},
		"prompt":               map[string]any{"type": "string", "maxLength": 2000},
		"focalLength":          map[string]any{"type": "number", "minimum": 1, "maximum": 300},
		"lightType":            map[string]any{"type": "string", "enum": []string{"directional", "point", "spot", "ambient"}},
		"intensity":            map[string]any{"type": "number", "minimum": 0, "maximum": 100},
		"angle":                map[string]any{"type": "number", "minimum": 0, "maximum": 6.29},
		"penumbra":             map[string]any{"type": "number", "minimum": 0, "maximum": 1},
		"castShadow":           map[string]any{"type": "boolean"},
		"background":           map[string]any{"type": "string", "maxLength": 32},
		"environmentIntensity": map[string]any{"type": "number", "minimum": 0, "maximum": 100},
		"gridVisible":          map[string]any{"type": "boolean"},
		"activeShotId":         map[string]any{"type": "string", "maxLength": 80, "description": "切换当前预览镜头；shot_update 与 scene_update 都支持"},
		"propId":               map[string]any{"type": "string", "maxLength": 40},
		"variant":              map[string]any{"type": "integer", "minimum": 0, "maximum": 7},
		"layoutTemplate":       map[string]any{"type": "string", "enum": cloudAgentPrevisLayoutIDs},
		"seed":                 map[string]any{"type": "integer", "minimum": 0, "maximum": 999999999},
		"size":                 map[string]any{"type": "number", "minimum": 50, "maximum": 1200},
		"density":              map[string]any{"type": "number", "minimum": 0.2, "maximum": 1},
		"crowd":                map[string]any{"type": "boolean"},
		"timeOfDay":            map[string]any{"type": "number", "minimum": 0, "maximum": 24},
		"weather":              map[string]any{"type": "string", "enum": cloudAgentPrevisWeathers},
		"ground":               map[string]any{"type": "string", "enum": cloudAgentPrevisGroundTypes},
		"groundSize":           map[string]any{"type": "integer", "enum": []int{1000, 2000, 4000}},
		"horizon":              map[string]any{"type": "boolean"},
		"worldEnabled":         map[string]any{"type": "boolean"},
		"params":               map[string]any{"type": "object"},
		"count":                map[string]any{"type": "integer", "minimum": 1, "maximum": cloudAgentPrevisArrayMax},
		"pattern":              map[string]any{"type": "string", "enum": []string{"line", "grid", "ring", "scatter"}},
		"spacing":              map[string]any{"type": "number", "minimum": 0.2, "maximum": 500},
		"columns":              map[string]any{"type": "integer", "minimum": 1, "maximum": 30},
		"facing":               map[string]any{"type": "string", "enum": []string{"same", "center", "outward"}},
		"vary":                 map[string]any{"type": "number", "minimum": 0, "maximum": 1},
		"travelStyle":          map[string]any{"type": "string", "enum": cloudAgentPrevisTravelStyles},
		"fromMarker":           map[string]any{"type": "string", "maxLength": 8},
		"toMarker":             map[string]any{"type": "string", "maxLength": 8},
		"height":               map[string]any{"type": "number", "minimum": 0, "maximum": 50},
		"technique":            map[string]any{"type": "string", "maxLength": 24},
		"recipe":               map[string]any{"type": "string", "maxLength": 32},
		"subjectId":            map[string]any{"type": "string", "maxLength": 80},
	}
	return map[string]any{"type": "object", "properties": properties, "required": []string{"type", "id"}, "additionalProperties": false}
}

func cloudAgentPrevisValidateText(value, label string, maxRunes int, required bool) error {
	if required && strings.TrimSpace(value) == "" {
		return BadAuthRequest(label + "不能为空")
	}
	if !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return BadAuthRequest(label + "不能包含首尾空白或无效字符")
	}
	if utf8.RuneCountInString(value) > maxRunes {
		return fmt.Errorf("%s不能超过 %d 个字符", label, maxRunes)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return BadAuthRequest(label + "不能包含控制字符")
		}
	}
	return nil
}

// cloudAgentPrevisValidatePose validates pose against the canonical enum.
func cloudAgentPrevisValidatePose(pose string) error {
	valid := map[string]bool{
		"stand": true, "sit": true, "walk": true, "run": true, "wave": true, "think": true,
		"fight": true, "kick": true, "throw": true, "push": true, "reach": true, "arms_crossed": true,
		"phone": true, "bow": true, "kneel_single": true, "kneel_double": true, "lean": true,
		"squat": true, "hands_hips": true, "neutral": true, "t_pose": true,
	}
	if !valid[pose] {
		return BadAuthRequest(fmt.Sprintf("pose 值 %q 不受支持；请使用 stand/sit/walk/run/wave/think/fight 等", pose))
	}
	return nil
}

func cloudAgentPrevisValidateEnum(value, label string, allowed ...string) error {
	for _, candidate := range allowed {
		if value == candidate {
			return nil
		}
	}
	return BadAuthRequest(label + "不受支持")
}

func cloudAgentPrevisIsHexColor(value string) bool {
	if len(value) != 4 && len(value) != 7 && len(value) != 9 || value[0] != '#' {
		return false
	}
	for _, r := range value[1:] {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return true
}

func cloudAgentPrevisNormalizeLegacyColor(value string) string {
	trimmed := strings.TrimSpace(value)
	if cloudAgentPrevisIsHexColor(trimmed) {
		return strings.ToLower(trimmed)
	}
	lower := strings.ToLower(trimmed)
	var raw string
	var rgba bool
	switch {
	case strings.HasPrefix(lower, "rgb(") && strings.HasSuffix(lower, ")"):
		raw = trimmed[4 : len(trimmed)-1]
	case strings.HasPrefix(lower, "rgba(") && strings.HasSuffix(lower, ")"):
		raw = trimmed[5 : len(trimmed)-1]
		rgba = true
	default:
		return value
	}
	parts := strings.Split(raw, ",")
	if len(parts) != 3 && len(parts) != 4 {
		return value
	}
	channels := make([]int, 3)
	for index := range channels {
		channel, err := strconv.Atoi(strings.TrimSpace(parts[index]))
		if err != nil || channel < 0 || channel > 255 {
			return value
		}
		channels[index] = channel
	}
	if !rgba && len(parts) == 4 {
		return value
	}
	result := fmt.Sprintf("#%02x%02x%02x", channels[0], channels[1], channels[2])
	if rgba && len(parts) == 4 {
		alpha, err := strconv.ParseFloat(strings.TrimSpace(parts[3]), 64)
		if err != nil {
			return value
		}
		if alpha <= 1 {
			alpha = math.Round(alpha * 255)
		}
		if alpha < 0 || alpha > 255 {
			return value
		}
		result += fmt.Sprintf("%02x", int(alpha))
	}
	return result
}

// Legacy scenes can contain CSS rgb/rgba colors from the browser-era editor.
// Normalize those values before strict validation so a harmless scene read or
// patch is not blocked by historical presentation data.
func cloudAgentPrevisNormalizeSceneColor(value, fallback string) string {
	normalized := cloudAgentPrevisNormalizeLegacyColor(value)
	if cloudAgentPrevisIsHexColor(normalized) {
		return normalized
	}
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func cloudAgentPrevisValidateColor(value, label string) error {
	if err := cloudAgentPrevisValidateText(value, label, 32, true); err != nil {
		return err
	}
	if !cloudAgentPrevisIsHexColor(value) {
		return BadAuthRequest(label + "必须是十六进制颜色")
	}
	return nil
}

func cloudAgentPrevisFiniteInRange(value float64, label string, minimum, maximum float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < minimum || value > maximum {
		return BadAuthRequest(label + "超出限制")
	}
	return nil
}

func cloudAgentPrevisValidateTemplateID(value string) error {
	valid := map[string]bool{"empty": true, "monologue": true, "dialogue": true, "blocking": true, "product": true, "action_chase": true, "interior": true, "crowd": true}
	if !valid[value] {
		return BadAuthRequest("templateId 不受支持；有效值：empty/monologue/dialogue/blocking/product/action_chase/interior/crowd")
	}
	return nil
}

func cloudAgentPrevisValidateVec3(value []float64, label string) error {
	if len(value) != 3 {
		return BadAuthRequest(label + "必须是包含3个数字的数组")
	}
	for _, number := range value {
		if math.IsNaN(number) || math.IsInf(number, 0) || math.Abs(number) > 100000 {
			return BadAuthRequest(label + "包含无效或超出范围的数字")
		}
	}
	return nil
}

func cloudAgentPrevisVec3(value []float64) []any {
	return []any{value[0], value[1], value[2]}
}

func cloudAgentPrevisNumberVec3(value any) ([]float64, bool) {
	result := make([]float64, 3)
	switch items := value.(type) {
	case []float64:
		if len(items) != 3 {
			return nil, false
		}
		copy(result, items)
	case []any:
		if len(items) != 3 {
			return nil, false
		}
		for index, item := range items {
			result[index] = numberValue(item, math.NaN())
		}
	default:
		return nil, false
	}
	for _, number := range result {
		if math.IsNaN(number) || math.IsInf(number, 0) {
			return nil, false
		}
	}
	return result, true
}

func cloudAgentPrevisOptionalVec3(value *[]float64, label string) error {
	if value == nil {
		return nil
	}
	return cloudAgentPrevisValidateVec3(*value, label)
}

func cloudAgentPrevisTextPointer(value *string, label string, maxRunes int) error {
	if value == nil {
		return nil
	}
	return cloudAgentPrevisValidateText(*value, label, maxRunes, false)
}

func cloudAgentPrevisCanvas(repo *repository.Repository, userID, canvasID string) (*model.CanvasProject, map[string]any, error) {
	canvas, err := repo.CanvasProjectForUser(userID, canvasID)
	if err != nil {
		return nil, nil, err
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		return nil, nil, err
	}
	return canvas, doc, nil
}

func cloudAgentPrevisScenesSize(scenes []map[string]any) error {
	encoded, err := json.Marshal(scenes)
	if err != nil || len(encoded) > cloudAgentPrevisDocumentLimit {
		return BadAuthRequest("预演台场景数据超过限制")
	}
	return nil
}

func cloudAgentPrevisIDSet(items []map[string]any, label string) (map[string]map[string]any, error) {
	result := make(map[string]map[string]any, len(items))
	for _, item := range items {
		id := stringValue(item["id"])
		if err := validateCloudAgentID(id, label, 80); err != nil {
			return nil, err
		}
		if _, exists := result[id]; exists {
			return nil, BadAuthRequest(label + "重复")
		}
		result[id] = item
	}
	return result, nil
}

func cloudAgentPrevisFinite(value any) bool {
	number := numberValue(value, math.NaN())
	return !math.IsNaN(number) && !math.IsInf(number, 0)
}

func cloudAgentPrevisValidateTransform(value any, label string) error {
	transform, ok := value.(map[string]any)
	if !ok {
		return BadAuthRequest(label + "缺少 transform")
	}
	for _, key := range []string{"position", "rotation", "scale"} {
		items, ok := transform[key].([]any)
		if !ok || len(items) != 3 {
			return BadAuthRequest(label + "的 transform." + key + " 必须包含3个数字")
		}
		for _, item := range items {
			if !cloudAgentPrevisFinite(item) {
				return BadAuthRequest(label + "的 transform 包含无效数字")
			}
		}
	}
	return nil
}
