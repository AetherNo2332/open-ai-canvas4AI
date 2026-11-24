package app

import (
	"strings"
	"testing"

	builtinAssets "yingce/backend/builtin"
)

func previsCameraTestScene(t *testing.T) map[string]any {
	t.Helper()
	scene, err := cloudAgentPrevisSceneCreateTemplate(cloudAgentPrevisSceneCreateArgs{SceneID: "s", Title: "运镜", TemplateID: "monologue"})
	if err != nil {
		t.Fatal(err)
	}
	if cloudAgentPrevisFindByID(creationMaps(scene["objects"]), "s-actor-1") == nil {
		t.Fatalf("monologue template must contain s-actor-1: %#v", scene["objects"])
	}
	return scene
}

func previsCameraMoves(t *testing.T, scene map[string]any) []map[string]any {
	t.Helper()
	return creationMaps(cloudAgentPrevisFindByID(creationMaps(scene["cameras"]), "s-camera-1")["moves"])
}

func previsMultiShotCameraScene(t *testing.T) map[string]any {
	t.Helper()
	scene := previsCameraTestScene(t)
	base := cloudAgentPrevisFindByID(creationMaps(scene["cameras"]), "s-camera-1")
	if base == nil {
		t.Fatal("template must contain s-camera-1")
	}
	cameras := creationMaps(scene["cameras"])
	for _, extra := range []struct{ id, name string }{
		{"s-camera-2", "B机"},
		{"s-camera-3", "C机"},
	} {
		camera := make(map[string]any, len(base))
		for key, value := range base {
			camera[key] = value
		}
		camera["id"], camera["name"] = extra.id, extra.name
		delete(camera, "moves")
		cameras = append(cameras, camera)
	}
	scene["cameras"] = cameras
	shot := func(id, name, cameraID string, duration float64) map[string]any {
		return map[string]any{"id": id, "name": name, "cameraId": cameraID, "duration": duration, "fps": 24, "shotSize": "medium", "cameraMove": "static", "prompt": "", "cuts": []any{}}
	}
	scene["shots"] = []any{
		shot("s-shot-1", "镜头 1", "s-camera-1", 3),
		shot("s-shot-2", "镜头 2", "s-camera-2", 4),
		shot("s-shot-3", "镜头 3", "s-camera-3", 3),
	}
	scene["activeShotId"] = "s-shot-1"
	if err := cloudAgentPrevisValidateScene(scene); err != nil {
		t.Fatal(err)
	}
	return scene
}

// 多镜头共用一条表演时间线：共享表演按最长镜头校验，单台摄影机的运镜按它自己的镜头校验。
func TestCloudAgentPrevisMultiShotTimelineDurations(t *testing.T) {
	scene := previsMultiShotCameraScene(t)
	apply := func(operation cloudAgentPrevisPatchOperation) (string, error) {
		item, err := cloudAgentPrevisApplyPatchOperation(scene, operation, 0)
		return item.Summary, err
	}
	subject := "s-actor-1"
	start, length := 1.8, 2.2
	if _, err := apply(cloudAgentPrevisPatchOperation{Type: "camera_move", ID: "s-camera-2", Technique: "follow", SubjectID: &subject, Time: &start, Duration: &length, Params: map[string]any{"side": "front"}}); err != nil {
		t.Fatalf("camera used by the 4s shot must accept a 4s move while shot 1 is active: %v", err)
	}
	start3, length3 := 1.8, 1.2
	if _, err := apply(cloudAgentPrevisPatchOperation{Type: "camera_move", ID: "s-camera-3", Technique: "look_at", SubjectID: &subject, Time: &start3, Duration: &length3}); err != nil {
		t.Fatalf("camera used by the 3s shot must accept a 3s move: %v", err)
	}
	if _, err := apply(cloudAgentPrevisPatchOperation{Type: "camera_move", ID: "s-camera-1", Technique: "push_in", Time: &start, Duration: &length}); err == nil || !strings.Contains(err.Error(), "超出镜头时长 3.0s") {
		t.Fatalf("camera used only by the 3s active shot must reject a 4s move, got %v", err)
	}
	if _, err := apply(cloudAgentPrevisPatchOperation{Type: "camera_move", ID: "s-camera-2", Recipe: "emotional_push", Time: &start, Duration: &length}); err != nil {
		t.Fatalf("camera recipe must use the camera's 4s shot: %v", err)
	}
	late := 3.9
	if _, err := apply(cloudAgentPrevisPatchOperation{Type: "camera_keyframe_upsert", ID: "s-camera-2", Time: &late}); err != nil {
		t.Fatalf("camera keyframe at 3.9s must fit the camera's 4s shot: %v", err)
	}
	if _, err := apply(cloudAgentPrevisPatchOperation{Type: "camera_keyframe_upsert", ID: "s-camera-1", Time: &late}); err == nil || !strings.Contains(err.Error(), "关键帧 time") {
		t.Fatalf("camera keyframe at 3.9s must not fit a 3s shot, got %v", err)
	}
	motionTime, motionLength := 3.2, 0.7
	if _, err := apply(cloudAgentPrevisPatchOperation{Type: "actor_motion_add", ID: subject, Motion: "nod", Time: &motionTime, Duration: &motionLength}); err != nil {
		t.Fatalf("shared actor performance must use the longest 4s shot: %v", err)
	}
	overTime, overLength := 3.8, 0.5
	if _, err := apply(cloudAgentPrevisPatchOperation{Type: "actor_motion_add", ID: subject, Motion: "nod", Time: &overTime, Duration: &overLength}); err == nil || !strings.Contains(err.Error(), "共享表演时间线 4.0s") {
		t.Fatalf("actor motion may not run past the longest shot, got %v", err)
	}
	delay := 3.1
	if _, err := apply(cloudAgentPrevisPatchOperation{Type: "object_motion_path_set", ID: "s-camera-3", PathPoints: [][]float64{{0, 1.6, 0}, {2, 1.6, 0}}, StartDelay: &delay}); err == nil || !strings.Contains(err.Error(), "startDelay") {
		t.Fatalf("camera path startDelay must respect its 3s shot, got %v", err)
	}
	if _, err := apply(cloudAgentPrevisPatchOperation{Type: "object_motion_path_set", ID: subject, PathPoints: [][]float64{{0, 0, 0}, {2, 0, 0}}, StartDelay: &delay}); err != nil {
		t.Fatalf("shared actor path must use the longest 4s shot: %v", err)
	}
	if err := cloudAgentPrevisValidateScene(scene); err != nil {
		t.Fatalf("scene after multi-shot edits invalid: %v", err)
	}
}

func TestCloudAgentPrevisShotUpdateActiveShotID(t *testing.T) {
	scene := previsMultiShotCameraScene(t)
	apply := func(operation cloudAgentPrevisPatchOperation) (string, error) {
		item, err := cloudAgentPrevisApplyPatchOperation(scene, operation, 0)
		return item.Summary, err
	}
	name, shot2 := "镜头 2 改名", "s-shot-2"
	summary, err := apply(cloudAgentPrevisPatchOperation{Type: "shot_update", ID: shot2, Name: &name, ActiveShotID: &shot2})
	if err != nil {
		t.Fatal(err)
	}
	if stringValue(scene["activeShotId"]) != shot2 || !strings.Contains(summary, "切到") {
		t.Fatalf("shot_update must switch the active shot: active=%s summary=%s", stringValue(scene["activeShotId"]), summary)
	}
	name = "镜头 1 改名"
	if _, err := apply(cloudAgentPrevisPatchOperation{Type: "shot_update", ID: "s-shot-1", Name: &name}); err != nil {
		t.Fatal(err)
	}
	if stringValue(scene["activeShotId"]) != shot2 {
		t.Fatalf("shot_update without activeShotId must keep the active shot, got %s", stringValue(scene["activeShotId"]))
	}
	ghost := "missing-shot"
	if _, err := apply(cloudAgentPrevisPatchOperation{Type: "shot_update", ID: "s-shot-1", ActiveShotID: &ghost}); err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("unknown activeShotId must be rejected, got %v", err)
	}
	if stringValue(scene["activeShotId"]) != shot2 {
		t.Fatalf("invalid activeShotId must not change the active shot, got %s", stringValue(scene["activeShotId"]))
	}
}

// 「镜头跟随御剑飞行的人物，后半段环绕，全程手持」：Agent 按手法写运镜段，后端校验白名单、时长与主体。
func TestCloudAgentPrevisCameraMoveWritesSemanticSegments(t *testing.T) {
	scene := previsCameraTestScene(t)
	shotDuration := cloudAgentPrevisShotDuration(scene)
	apply := func(operation cloudAgentPrevisPatchOperation) (string, error) {
		item, err := cloudAgentPrevisApplyPatchOperation(scene, operation, 0)
		return item.Summary, err
	}

	summary, err := apply(cloudAgentPrevisPatchOperation{Type: "camera_move", ID: "s-camera-1", Technique: "push_in"})
	if err != nil {
		t.Fatal(err)
	}
	moves := previsCameraMoves(t, scene)
	if len(moves) != 1 || stringValue(moves[0]["technique"]) != "push_in" || numberValue(moves[0]["start"], -1) != 0 || numberValue(moves[0]["duration"], 0) != shotDuration {
		t.Fatalf("push_in must default to the whole shot: %#v", moves)
	}
	if !strings.Contains(summary, "push_in") || !strings.Contains(summary, "主摄影机") {
		t.Fatalf("summary must name camera and technique: %s", summary)
	}

	subject := "s-actor-1"
	start, length := 1.0, 3.0
	if _, err := apply(cloudAgentPrevisPatchOperation{Type: "camera_move", ID: "s-camera-1", Technique: "follow", Time: &start, Duration: &length}); err == nil || !strings.Contains(err.Error(), "subjectId") {
		t.Fatalf("follow without subject must be rejected, got %v", err)
	}
	summary, err = apply(cloudAgentPrevisPatchOperation{Type: "camera_move", ID: "s-camera-1", Technique: "follow", SubjectID: &subject, Time: &start, Duration: &length, Easing: "linear", Params: map[string]any{"side": "behind", "distance": 6.0, "lag": 0.35}})
	if err != nil {
		t.Fatal(err)
	}
	moves = previsCameraMoves(t, scene)
	follow := moves[1]
	if len(moves) != 2 || stringValue(follow["technique"]) != "follow" {
		t.Fatalf("moves must stay sorted by start: %#v", moves)
	}
	params, _ := follow["params"].(map[string]any)
	if stringValue(follow["subjectId"]) != subject || stringValue(follow["easing"]) != "linear" || stringValue(params["side"]) != "behind" || numberValue(params["distance"], 0) != 6 || numberValue(follow["start"], 0) != 1 {
		t.Fatalf("follow segment wrong: %#v", follow)
	}
	if !strings.Contains(summary, "主体《") || strings.Contains(summary, "（随演员") {
		t.Fatalf("summary must name the followed subject and keep only the short technique name: %s", summary)
	}
	if _, err := apply(cloudAgentPrevisPatchOperation{Type: "camera_move", ID: "s-camera-1", Technique: "handheld", Params: map[string]any{"intensity": 1.4}}); err != nil {
		t.Fatal(err)
	}
	// 叠加层默认时长：shake 0.6 秒，不铺满整个镜头。
	shakeStart := 2.0
	if _, err := apply(cloudAgentPrevisPatchOperation{Type: "camera_move", ID: "s-camera-1", Technique: "shake", Time: &shakeStart}); err != nil {
		t.Fatal(err)
	}
	if shake := previsCameraMoves(t, scene)[3]; stringValue(shake["technique"]) != "shake" || numberValue(shake["duration"], 0) != 0.6 {
		t.Fatalf("shake must default to 0.6s: %#v", previsCameraMoves(t, scene))
	}

	bad := []struct {
		name      string
		operation cloudAgentPrevisPatchOperation
		want      string
	}{
		{"unknown technique", cloudAgentPrevisPatchOperation{Type: "camera_move", ID: "s-camera-1", Technique: "teleport_cam"}, "references/moves.md"},
		{"unknown param", cloudAgentPrevisPatchOperation{Type: "camera_move", ID: "s-camera-1", Technique: "pan_left", Params: map[string]any{"speed": 2.0}}, "不支持参数"},
		{"param out of range", cloudAgentPrevisPatchOperation{Type: "camera_move", ID: "s-camera-1", Technique: "follow", SubjectID: &subject, Params: map[string]any{"distance": 500.0}}, "distance"},
		{"bad side", cloudAgentPrevisPatchOperation{Type: "camera_move", ID: "s-camera-1", Technique: "follow", SubjectID: &subject, Params: map[string]any{"side": "under"}}, "side"},
		{"string number", cloudAgentPrevisPatchOperation{Type: "camera_move", ID: "s-camera-1", Technique: "push_in", Params: map[string]any{"distance": "2"}}, "distance"},
		{"subject on plain move", cloudAgentPrevisPatchOperation{Type: "camera_move", ID: "s-camera-1", Technique: "pan_left", SubjectID: &subject}, "不需要 subjectId"},
		{"missing subject object", cloudAgentPrevisPatchOperation{Type: "camera_move", ID: "s-camera-1", Technique: "look_at", SubjectID: previsStringPtr("ghost")}, "不存在"},
		{"step easing", cloudAgentPrevisPatchOperation{Type: "camera_move", ID: "s-camera-1", Technique: "pan_left", Easing: "step"}, "easing"},
		{"past shot end", cloudAgentPrevisPatchOperation{Type: "camera_move", ID: "s-camera-1", Technique: "orbit_left", Time: float64Ptr(shotDuration - 1), Duration: float64Ptr(3)}, "超出镜头时长"},
		{"unknown camera", cloudAgentPrevisPatchOperation{Type: "camera_move", ID: "ghost-camera", Technique: "push_in"}, "摄影机"},
	}
	for _, item := range bad {
		if _, err := apply(item.operation); err == nil || !strings.Contains(err.Error(), item.want) {
			t.Fatalf("%s: want error containing %q, got %v", item.name, item.want, err)
		}
	}

	// 同一 keyframeId 覆盖原段，不新增。
	firstID := stringValue(previsCameraMoves(t, scene)[0]["id"])
	if _, err := apply(cloudAgentPrevisPatchOperation{Type: "camera_move", ID: "s-camera-1", Technique: "pull_out", KeyframeID: &firstID}); err != nil {
		t.Fatal(err)
	}
	if moves := previsCameraMoves(t, scene); len(moves) != 4 || stringValue(moves[0]["technique"]) != "pull_out" || stringValue(moves[0]["id"]) != firstID {
		t.Fatalf("keyframeId must replace the existing move: %#v", moves)
	}
	if err := cloudAgentPrevisValidateScene(scene); err != nil {
		t.Fatalf("scene with camera moves invalid: %v", err)
	}

	// 读场景时把运镜段交给 Agent，才能在已有运镜上继续或按 id 删除。
	var projected []map[string]any
	for _, camera := range projectPrevisScene(scene, "", nil)["cameras"].([]map[string]any) {
		if camera["id"] == "s-camera-1" {
			projected, _ = camera["moves"].([]map[string]any)
		}
	}
	projectedFollow := false
	for _, move := range projected {
		if move["technique"] == "follow" && move["subjectId"] == subject {
			projectedFollow = true
		}
	}
	if len(projected) != 4 || !projectedFollow {
		t.Fatalf("camera moves must be projected: %#v", projected)
	}

	if _, err := apply(cloudAgentPrevisPatchOperation{Type: "camera_move_remove", ID: "s-camera-1", KeyframeID: previsStringPtr("missing")}); err == nil {
		t.Fatal("removing an unknown move must fail")
	}
	if _, err := apply(cloudAgentPrevisPatchOperation{Type: "camera_move_remove", ID: "s-camera-1", KeyframeID: &firstID}); err != nil {
		t.Fatal(err)
	}
	if moves := previsCameraMoves(t, scene); len(moves) != 3 {
		t.Fatalf("remove by id must drop one move: %#v", moves)
	}
	if _, err := apply(cloudAgentPrevisPatchOperation{Type: "camera_move_remove", ID: "s-camera-1"}); err != nil {
		t.Fatal(err)
	}
	if _, exists := cloudAgentPrevisFindByID(creationMaps(scene["cameras"]), "s-camera-1")["moves"]; exists {
		t.Fatal("remove without keyframeId must clear all moves")
	}

	for index := 0; index < cloudAgentPrevisMaxCameraMoves; index++ {
		if _, err := apply(cloudAgentPrevisPatchOperation{Type: "camera_move", ID: "s-camera-1", Technique: "handheld"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := apply(cloudAgentPrevisPatchOperation{Type: "camera_move", ID: "s-camera-1", Technique: "handheld"}); err == nil {
		t.Fatal("moves beyond the limit must be rejected")
	}
}

// 整场景校验：浏览器写入的未知手法要指明摄影机与手法，主体被删除不阻断无关补丁。
func TestCloudAgentPrevisValidateSceneChecksCameraMoves(t *testing.T) {
	scene := previsCameraTestScene(t)
	camera := cloudAgentPrevisFindByID(creationMaps(scene["cameras"]), "s-camera-1")
	camera["moves"] = []any{map[string]any{"id": "m1", "technique": "follow", "start": 0.0, "duration": 2.0, "subjectId": "deleted-actor", "params": map[string]any{"side": "left"}}}
	if err := cloudAgentPrevisValidateScene(scene); err != nil {
		t.Fatalf("a move whose subject was deleted must not block validation: %v", err)
	}
	camera["moves"] = []any{map[string]any{"id": "m1", "technique": "hyperlapse", "start": 0.0, "duration": 2.0}}
	err := cloudAgentPrevisValidateScene(scene)
	if err == nil || !strings.Contains(err.Error(), "s-camera-1") || !strings.Contains(err.Error(), "hyperlapse") {
		t.Fatalf("unknown technique must name camera and technique, got %v", err)
	}
	camera["moves"] = []any{map[string]any{"id": "m1", "technique": "push_in", "start": 0.0, "duration": 2.0, "params": map[string]any{"distance": 900.0}}}
	if err := cloudAgentPrevisValidateScene(scene); err == nil {
		t.Fatal("stored params out of range must be rejected")
	}
}

func TestCloudAgentPrevisCameraMoveContract(t *testing.T) {
	moves, err := builtinAssets.FS.ReadFile("previs-world-guide/references/moves.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, technique := range cloudAgentPrevisCameraTechniques {
		if !strings.Contains(string(moves), technique.ID) {
			t.Fatalf("Skill moves.md missing technique %q", technique.ID)
		}
	}
	if !strings.Contains(string(moves), "side=behind/front/left/right/above/shoulder/pov") || !strings.Contains(string(moves), "camera_move_remove") {
		t.Fatal("Skill moves.md is missing camera move layering or the follow-side contract")
	}
	recipes, err := builtinAssets.FS.ReadFile("previs-world-guide/references/recipes.md")
	if err != nil {
		t.Fatal(err)
	}
	actionRecipes, err := builtinAssets.FS.ReadFile("previs-world-guide/references/recipes_action.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(recipes), "希区柯克") || !strings.Contains(string(actionRecipes), "御剑飞行") {
		t.Fatal("Skill recipe references are missing master recipes")
	}
	schema := cloudAgentPrevisPatchOperationSchema()["properties"].(map[string]any)
	types := schema["type"].(map[string]any)["enum"].([]string)
	if !strings.Contains(strings.Join(types, ","), "camera_move,camera_move_remove") {
		t.Fatalf("schema type enum missing camera moves: %v", types)
	}
	for _, key := range []string{"technique", "recipe", "subjectId"} {
		if _, ok := schema[key]; !ok {
			t.Fatalf("schema missing %s", key)
		}
	}
	if !strings.Contains(string(moves), "shotSize=extreme_close_up/close_up/medium/full/wide/extreme_wide") || !strings.Contains(string(moves), "blend 0~10") {
		t.Fatalf("Skill moves.md must list follow shot sizes and blend: %s", moves)
	}
	shots, err := builtinAssets.FS.ReadFile("previs-world-guide/references/shots.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(shots), "recipes_action.md") || !strings.Contains(string(shots), "review") {
		t.Fatalf("Skill shots.md must point to complete recipes and review: %s", shots)
	}
}

// 走完整的审批写入路径：JSON 严格解码能识别 technique / subjectId / params，结果落到画布文档。
func TestCloudAgentPrevisCameraMoveThroughApplyPatch(t *testing.T) {
	s, canvas := previsMutationFixture(t)
	policy, err := s.RuntimePolicy()
	if err != nil {
		t.Fatal(err)
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		t.Fatal(err)
	}
	created, err := applyCloudAgentPrevisMutation(s.repo, "user", canvas.ID, previsMutationCall(t, "create", "previs_scene_create", map[string]any{"canvasSnapshotHash": cloudAgentCanvasHash(doc), "sceneId": "s", "title": "御剑", "templateId": "monologue"}), policy)
	if err != nil {
		t.Fatal(err)
	}
	patch := previsMutationCall(t, "patch", "previs_apply_patch", map[string]any{
		"snapshotHash": previsResultString(t, created, "snapshotHash"),
		"sceneId":      "s",
		"operations": []map[string]any{
			{"type": "actor_travel", "id": "s-actor-1", "travelStyle": "sword_fly", "pathPoints": [][]float64{{0, 0, 0}, {20, 0, -10}}, "time": 0.5, "duration": 4},
			{"type": "camera_move", "id": "s-camera-1", "technique": "follow", "subjectId": "s-actor-1", "params": map[string]any{"side": "behind", "distance": 6}},
			{"type": "camera_move", "id": "s-camera-1", "technique": "orbit_left", "subjectId": "s-actor-1", "time": 2.5, "duration": 2.5, "params": map[string]any{"angle": 120}},
		},
	})
	if _, err := applyCloudAgentPrevisMutation(s.repo, "user", canvas.ID, patch, policy); err != nil {
		t.Fatal(err)
	}
	stored, err := s.repo.CanvasProjectForUser("user", canvas.ID)
	if err != nil {
		t.Fatal(err)
	}
	storedDoc, err := creationDocument(stored.PayloadJSON)
	if err != nil {
		t.Fatal(err)
	}
	scene, ok := findPrevisScene(creationMaps(storedDoc["previsScenes"]), "s")
	if !ok {
		t.Fatal("scene missing after patch")
	}
	moves := previsCameraMoves(t, scene)
	if len(moves) != 2 || stringValue(moves[0]["technique"]) != "follow" || stringValue(moves[1]["technique"]) != "orbit_left" || numberValue(moves[1]["start"], 0) != 2.5 {
		t.Fatalf("stored camera moves wrong: %#v", moves)
	}
	if err := cloudAgentPrevisValidateScene(scene); err != nil {
		t.Fatal(err)
	}
}
