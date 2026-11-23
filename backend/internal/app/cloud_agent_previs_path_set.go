package app

import (
	"fmt"
	"math"
	"slices"
	"strings"
)

// Resolve the source and keep point height handling separate from timing options.
func cloudAgentPrevisResolveMotionPath(scene, item map[string]any, operation cloudAgentPrevisPatchOperation, duration float64, locked bool) (cloudAgentPrevisPathSpec, bool, error) {
	spec := cloudAgentPrevisPathSpec{Points: operation.PathPoints}
	copied, editing := false, false
	if operation.PathID != nil && strings.TrimSpace(*operation.PathID) != "" {
		if len(operation.PathPoints) > 0 {
			return spec, false, cloudAgentFieldError("pathId", "conflict", "pathPoints 与 pathId 只能给一个")
		}
		source, fromOwner, err := cloudAgentPrevisPathSource(scene, *operation.PathID)
		if err != nil {
			return spec, false, &cloudAgentFieldArgumentError{error: &cloudAgentArgumentError{err}, Field: "pathId", Issue: "invalid_value"}
		}
		spec, copied = source, fromOwner
	} else if previous, ok := item["motionPath"].(map[string]any); ok && len(operation.PathPoints) > 0 {
		spec = cloudAgentPrevisDecodePath(previous)
		if !cloudAgentPrevisKeepsWaypointOrder(spec.Points, operation.PathPoints) {
			spec.Speeds, spec.Holds = nil, nil
		}
		spec.Points, editing = operation.PathPoints, true
	}
	if operation.Height != nil {
		if err := cloudAgentPrevisFiniteInRange(*operation.Height, "height", 0, 50); err != nil {
			return spec, copied, err
		}
	}
	points := make([][]float64, 0, len(spec.Points))
	for _, point := range spec.Points {
		if len(point) != 3 {
			return spec, copied, BadAuthRequest("路径点必须包含 3 个数字")
		}
		next := []float64{point[0], point[1], point[2]}
		switch {
		case operation.Height != nil:
			next[1] = *operation.Height
		case copied && locked:
			next[1] = 0
		}
		points = append(points, next)
	}
	spec.Points = points
	if err := cloudAgentPrevisApplyPathOptions(&spec, operation, duration, locked, editing, copied); err != nil {
		return spec, copied, err
	}
	if locked {
		for index, point := range points {
			if point[1] < 0 {
				return spec, copied, BadAuthRequest(fmt.Sprintf("pathPoints[%d] 的高度不能低于地面 0 米", index))
			}
		}
	}
	return spec, copied, nil
}

func cloudAgentPrevisDecodePath(path map[string]any) cloudAgentPrevisPathSpec {
	spec := cloudAgentPrevisPathSpec{
		Speed: stringValue(path["speed"]), MetersPerSecond: numberValue(path["metersPerSecond"], 0), Duration: numberValue(path["duration"], 0),
		StartDelay: numberValue(path["startDelay"], 0), Easing: stringValue(path["easing"]), Curve: boolValue(path["curve"], false), Orient: boolValue(path["orientToPath"], false),
	}
	for _, raw := range creationAnyList(path["points"]) {
		if point, ok := cloudAgentPrevisNumberVec3(raw); ok {
			spec.Points = append(spec.Points, point)
		}
	}
	for _, raw := range creationAnyList(path["speeds"]) {
		spec.Speeds = append(spec.Speeds, numberValue(raw, 1))
	}
	for _, raw := range creationAnyList(path["holds"]) {
		spec.Holds = append(spec.Holds, numberValue(raw, 0))
	}
	return spec
}

// Path points have no IDs. Keep index-bound timing on same-length coordinate
// edits unless an existing waypoint is recognizably moved to another index.
func cloudAgentPrevisKeepsWaypointOrder(previous, next [][]float64) bool {
	if len(previous) != len(next) {
		return false
	}
	for index, point := range next {
		if len(point) != 3 {
			return false
		}
		if slices.Equal(point, previous[index]) {
			continue
		}
		for oldIndex, old := range previous {
			if oldIndex != index && slices.Equal(old, point) {
				return false
			}
		}
	}
	return true
}

func cloudAgentPrevisApplyPathOptions(spec *cloudAgentPrevisPathSpec, operation cloudAgentPrevisPatchOperation, duration float64, locked, editing, copied bool) error {
	if operation.PathSpeed != "" || spec.Speed == "" {
		spec.Speed = operation.PathSpeed
		if spec.Speed == "" {
			spec.Speed = "walk"
		}
		if operation.PathSpeed != "" {
			spec.MetersPerSecond = 0
		}
	}
	if err := cloudAgentPrevisValidateEnum(spec.Speed, "pathSpeed", "slow", "walk", "run"); err != nil {
		return err
	}
	if operation.Easing != "" {
		spec.Easing = operation.Easing
	}
	if spec.Easing == "" {
		spec.Easing = "linear"
	}
	if err := cloudAgentPrevisValidateEnum(spec.Easing, "路径运动规律 easing", cloudAgentPrevisPathEasings...); err != nil {
		return BadAuthRequest("路径 easing 只能是 linear（匀速）/ease_in（加速）/ease_out（减速）/smooth（缓入缓出）")
	}
	if operation.StartDelay != nil {
		spec.StartDelay = *operation.StartDelay
	} else if !copied && !editing {
		spec.StartDelay = 0
	}
	if err := cloudAgentPrevisFiniteInRange(spec.StartDelay, "startDelay", 0, duration); err != nil {
		return err
	}
	if operation.Duration != nil {
		if err := cloudAgentPrevisFiniteInRange(*operation.Duration, "duration", 0.1, cloudAgentPrevisPathMaxDuration); err != nil {
			return err
		}
		spec.Duration = *operation.Duration
	}
	if !editing {
		spec.Orient = locked
	}
	if operation.OrientToPath != nil {
		spec.Orient = *operation.OrientToPath
	}
	return nil
}

// Build frames before writing either field, so a rejected path leaves the item intact.
func cloudAgentPrevisApplyMotionPath(item map[string]any, spec cloudAgentPrevisPathSpec, operation cloudAgentPrevisPatchOperation, duration float64, locked, copied bool) (string, error) {
	transform := cloudAgentPrevisMapTransform(item)
	position, ok := cloudAgentPrevisNumberVec3(transform["position"])
	if !ok {
		position = []float64{0, 0, 0}
	}
	rotation, ok := cloudAgentPrevisNumberVec3(transform["rotation"])
	if !ok {
		rotation = []float64{0, 0, 0}
	}
	scale, ok := cloudAgentPrevisNumberVec3(transform["scale"])
	if !ok {
		scale = []float64{1, 1, 1}
	}
	keyframes, err := cloudAgentPrevisBuildPathKeyframes(stringValue(item["id"]), position, rotation, scale, spec, duration, locked)
	if err != nil {
		return "", err
	}
	motionPath, top := cloudAgentPrevisStoredMotionPath(spec)
	item["motionPath"] = motionPath
	// 与 UI 画走位一致：整条路径接管 transform 轨道，避免走完后跳回旧手工帧。
	item["keyframes"] = keyframes
	plan, _ := cloudAgentPrevisPlanPath(spec, duration)
	summary := fmt.Sprintf("《%s》沿 %d 个途经点的路径移动 %.1f–%.1fs，约 %.1f 米，%s", truncateRunes(stringValue(item["name"]), 40), len(spec.Points), plan.start, plan.end, plan.length, spec.Easing)
	if top > 0.05 {
		summary += fmt.Sprintf("，最高离地 %.1f 米", top)
	}
	if copied {
		summary += "，路线复用自 " + truncateRunes(strings.TrimSpace(*operation.PathID), 40)
	}
	if plan.compressed {
		summary += "；镜头时长不够，已整体加速压缩"
	}
	return summary, nil
}

func cloudAgentPrevisStoredMotionPath(spec cloudAgentPrevisPathSpec) (map[string]any, float64) {
	stored := make([]any, 0, len(spec.Points))
	top := 0.0
	for _, point := range spec.Points {
		stored = append(stored, cloudAgentPrevisVec3(point))
		top = math.Max(top, point[1])
	}
	motionPath := map[string]any{"points": stored, "speed": spec.Speed, "startDelay": spec.StartDelay, "orientToPath": spec.Orient}
	if spec.Easing != "linear" {
		motionPath["easing"] = spec.Easing
	}
	if spec.Duration > 0 {
		motionPath["duration"] = spec.Duration
	}
	if spec.MetersPerSecond > 0 {
		motionPath["metersPerSecond"] = spec.MetersPerSecond
	}
	if spec.Curve && len(spec.Points) > 2 {
		motionPath["curve"] = true
	}
	if len(spec.Speeds) == len(spec.Points)-1 {
		motionPath["speeds"] = cloudAgentPrevisFloatList(spec.Speeds)
	}
	if len(spec.Holds) == len(spec.Points) {
		motionPath["holds"] = cloudAgentPrevisFloatList(spec.Holds)
	}
	return motionPath, top
}

// Sample eased legs against cumulative edge time; linear legs keep their original vertices.
func cloudAgentPrevisSamplePathLeg(from, to int, vertices [][]float64, cumulative []float64, legTime, rate, current float64, easing string,
	heading func([]float64, []float64) float64, vertexYaw func(int) float64, push func(float64, []float64, float64, string)) {
	samples := max(2, int(math.Ceil(legTime*rate)))
	edge := 0
	for step := 1; step <= samples; step++ {
		w := float64(step) / float64(samples)
		target := cloudAgentPrevisPathEase(easing, w) * legTime
		for edge < to-from-1 && cumulative[edge+1] < target {
			edge++
		}
		span := cumulative[edge+1] - cumulative[edge]
		k := 1.0
		if span > 1e-9 {
			k = cloudAgentPrevisClamp((target-cumulative[edge])/span, 0, 1)
		}
		a, b := vertices[from+edge], vertices[from+edge+1]
		point := []float64{a[0] + (b[0]-a[0])*k, a[1] + (b[1]-a[1])*k, a[2] + (b[2]-a[2])*k}
		var yaw float64
		if step == samples {
			yaw = vertexYaw(from + edge + 1)
		} else {
			yaw = heading(a, b)
		}
		push(current+w*legTime, point, yaw, "")
	}
}
