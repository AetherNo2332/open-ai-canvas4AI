package app

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"gorm.io/gorm"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func canvasCommandFixtureDoc() map[string]any {
	return map[string]any{
		"id": "agent-canvas", "referenceFieldsVersion": 1,
		"nodes": []map[string]any{
			{"id": "submap-1", "type": "submap", "title": "镜头1", "position": map[string]any{"x": 100, "y": 100}, "width": 920, "height": 360,
				"metadata": map[string]any{"status": "idle", "collapsed": false, "workflowId": "storyboard"}},
			{"id": "submap-1__text-1", "type": "text", "title": "文本1", "scopeId": "submap-1", "position": map[string]any{"x": 140, "y": 160}, "width": 240, "height": 120,
				"metadata": map[string]any{"content": "窗边的猫"}},
			{"id": "submap-1__text-2", "type": "text", "title": "文本2", "scopeId": "submap-1", "position": map[string]any{"x": 420, "y": 160}, "width": 240, "height": 120,
				"metadata": map[string]any{"content": "雨夜"}},
			{"id": "submap-1__image-1", "type": "image", "title": "首帧", "scopeId": "submap-1", "position": map[string]any{"x": 420, "y": 320}, "width": 240, "height": 240,
				"metadata": map[string]any{"status": "idle", "composerContent": "一只猫", "referenceNodeIds": []any{"submap-1__text-1", "image-9"}, "referenceTextNodeIds": []any{"submap-1__text-2"}}},
			{"id": "image-9", "type": "image", "title": "外部图", "position": map[string]any{"x": 900, "y": 100}, "width": 240, "height": 240,
				"metadata": map[string]any{"status": "idle", "composerContent": "外部"}},
		},
		"connections": []any{},
		"idCounters":  map[string]any{"submap": 1, "submap-1__text": 2, "submap-1__image": 1},
	}
}

func canvasCommandFixture(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	s, db, _ := agentMediaFixture(t)
	raw, err := json.Marshal(canvasCommandFixtureDoc())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.CanvasProject{}).Where("id = ?", "agent-canvas").Update("payload_json", string(raw)).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.CanvasProject{}).Where("id = ?", "agent-canvas").Update("revision", 1).Error; err != nil {
		t.Fatal(err)
	}
	return s, db
}

func canvasCommandCall(t *testing.T, command, snapshotHash string) cloudAgentCall {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"command": command, "snapshotHash": snapshotHash})
	if err != nil {
		t.Fatal(err)
	}
	call := cloudAgentCall{ID: "canvas-command-call"}
	call.Function.Name = "canvas"
	call.Function.Arguments = string(raw)
	return call
}

func canvasCommandReload(t *testing.T, s *Service) (map[string]any, string) {
	t.Helper()
	canvas, err := s.repo.CanvasProjectForUser("user", "agent-canvas")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		t.Fatal(err)
	}
	return doc, cloudAgentCanvasHash(doc)
}

func canvasCommandTestNode(doc map[string]any, id string) map[string]any {
	for _, node := range creationMaps(doc["nodes"]) {
		if stringValue(node["id"]) == id {
			return node
		}
	}
	return nil
}

func TestCanvasCommandReadsSubmapPaths(t *testing.T) {
	doc := canvasCommandFixtureDoc()
	fs := newCanvasCommandFS("agent-canvas", doc, 3)
	root, err := canvasCommandList(fs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(root, "./submap-1/") || strings.Contains(root, "submap-1__text-1") {
		t.Fatalf("根目录必须只列顶层节点：%s", root)
	}
	children, err := canvasCommandList(fs, []string{"./submap-1/"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"./submap-1/text-1/", "./submap-1/image-1/"} {
		if !strings.Contains(children, want) {
			t.Fatalf("ls 子画布缺少 %s：%s", want, children)
		}
	}
	prompt, err := canvasCommandCat(fs, []string{"./submap-1/image-1/prompt.txt"}, false)
	if err != nil || prompt != "一只猫" {
		t.Fatalf("prompt.txt 必须读到 composerContent：%q err=%v", prompt, err)
	}
	content, err := canvasCommandCat(fs, []string{"./submap-1/text-1/content.txt"}, true)
	if err != nil || !strings.Contains(content, "窗边的猫") {
		t.Fatalf("content.txt 读取失败：%q err=%v", content, err)
	}
	found, err := canvasCommandFind(fs, []string{"./**/prompt.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(found, "./submap-1/image-1/prompt.txt") || strings.Contains(found, "submap-1__image-1") {
		t.Fatalf("find 必须使用局部路径：%s", found)
	}
	if _, err := canvasCommandCat(fs, []string{"./submap-1/missing-1/content.txt"}, false); err == nil {
		t.Fatal("缺失子节点必须报错")
	}
}

func TestCanvasCommandNewAllocatesScopedIDAndCounter(t *testing.T) {
	s, _ := canvasCommandFixture(t)
	_, hash := canvasCommandReload(t, s)
	call := canvasCommandCall(t, "new --in submap-1 image 首帧2", hash)
	plan, err := prepareCloudAgentCanvasCommandPlan(s.repo, "user", "agent-canvas", call)
	if err != nil {
		t.Fatal(err)
	}
	node := canvasCommandTestNode(plan.Document, "submap-1__image-2")
	if node == nil {
		t.Fatalf("新节点必须按 submap 复合前缀发号：%+v", plan.Document["idCounters"])
	}
	if stringValue(node["scopeId"]) != "submap-1" {
		t.Fatalf("子节点必须写 scopeId：%+v", node)
	}
	if _, parent := node["parentId"]; parent {
		t.Fatalf("submap 子节点不得写 parentId：%+v", node)
	}
	counters, _ := plan.Document["idCounters"].(map[string]any)
	if canvasCommandInt(counters["submap-1__image"]) != 2 {
		t.Fatalf("idCounters 未推进：%+v", counters)
	}
	policy, err := s.RuntimePolicy()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := applyCloudAgentCanvasCommand(s.repo, "user", "agent-canvas", call, policy); err != nil {
		t.Fatal(err)
	}
	persisted, _ := canvasCommandReload(t, s)
	if canvasCommandTestNode(persisted, "submap-1__image-2") == nil {
		t.Fatal("新节点未落库")
	}
}

func TestCanvasCommandEditLockAndStaleSnapshot(t *testing.T) {
	s, db := canvasCommandFixture(t)
	_, hash := canvasCommandReload(t, s)
	policy, err := s.RuntimePolicy()
	if err != nil {
		t.Fatal(err)
	}
	call := canvasCommandCall(t, "edit ./submap-1/text-1/content.txt 新正文", hash)
	if _, err := applyCloudAgentCanvasCommand(s.repo, "user", "agent-canvas", call, policy); err != nil {
		t.Fatal(err)
	}
	doc, hash := canvasCommandReload(t, s)
	node := canvasCommandTestNode(doc, "submap-1__text-1")
	if stringValue(cloudAgentNodeMetadata(node)["content"]) != "新正文" {
		t.Fatalf("字段未写入：%+v", node)
	}
	cloudAgentNodeMetadata(node)["locked"] = true
	raw, _ := json.Marshal(doc)
	if err := db.Model(&model.CanvasProject{}).Where("id = ?", "agent-canvas").Update("payload_json", string(raw)).Error; err != nil {
		t.Fatal(err)
	}
	_, hash = canvasCommandReload(t, s)
	lockedCall := canvasCommandCall(t, "edit ./submap-1/text-1/content.txt 锁定后", hash)
	_, err = prepareCloudAgentCanvasCommandPlan(s.repo, "user", "agent-canvas", lockedCall)
	if err == nil || !strings.Contains(err.Error(), "已锁定") {
		t.Fatalf("锁定节点必须拒绝写入：v=%v", err)
	}
	stale := canvasCommandCall(t, "edit ./submap-1/text-1/content.txt 过期", "stale-hash")
	_, err = prepareCloudAgentCanvasCommandPlan(s.repo, "user", "agent-canvas", stale)
	var fieldErr *cloudAgentFieldArgumentError
	if !errors.As(err, &fieldErr) || fieldErr.Issue != "stale_snapshot" {
		t.Fatalf("过期快照必须返回 stale_snapshot：%v", err)
	}
}

func TestCanvasCommandDupSubmapRemapsInternalReferences(t *testing.T) {
	s, _ := canvasCommandFixture(t)
	_, hash := canvasCommandReload(t, s)
	call := canvasCommandCall(t, "dup ./submap-1 镜头2", hash)
	plan, err := prepareCloudAgentCanvasCommandPlan(s.repo, "user", "agent-canvas", call)
	if err != nil {
		t.Fatal(err)
	}
	if canvasCommandTestNode(plan.Document, "submap-2") == nil {
		t.Fatalf("副本容器未生成：%+v", plan.Document["idCounters"])
	}
	copy := canvasCommandTestNode(plan.Document, "submap-2__image-1")
	if copy == nil || stringValue(copy["scopeId"]) != "submap-2" {
		t.Fatalf("副本子节点作用域错误：%+v", copy)
	}
	meta := cloudAgentNodeMetadata(copy)
	refs := cloudAgentStrings(meta["referenceNodeIds"])
	if !reflect.DeepEqual(refs, []string{"submap-2__text-1"}) {
		t.Fatalf("内部引用必须重映射且外部引用不复制：%+v", refs)
	}
	textRefs := cloudAgentStrings(meta["referenceTextNodeIds"])
	if !reflect.DeepEqual(textRefs, []string{"submap-2__text-2"}) {
		t.Fatalf("文本引用重映射错误：%+v", textRefs)
	}
	policy, err := s.RuntimePolicy()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := applyCloudAgentCanvasCommand(s.repo, "user", "agent-canvas", call, policy); err != nil {
		t.Fatal(err)
	}
	persisted, _ := canvasCommandReload(t, s)
	if canvasCommandTestNode(persisted, "submap-2__text-1") == nil || canvasCommandTestNode(persisted, "submap-2__image-1") == nil {
		t.Fatal("副本子树未落库")
	}
}

func TestCanvasCommandDupChildKeepsScope(t *testing.T) {
	s, _ := canvasCommandFixture(t)
	_, hash := canvasCommandReload(t, s)
	call := canvasCommandCall(t, "dup ./submap-1/text-2 雨夜副本", hash)
	plan, err := prepareCloudAgentCanvasCommandPlan(s.repo, "user", "agent-canvas", call)
	if err != nil {
		t.Fatal(err)
	}
	copy := canvasCommandTestNode(plan.Document, "submap-1__text-3")
	if copy == nil || stringValue(copy["scopeId"]) != "submap-1" {
		t.Fatalf("submap 内复制必须保留作用域：%+v", copy)
	}
	fs := newCanvasCommandFS("agent-canvas", plan.Document, 0)
	text, err := canvasCommandCat(fs, []string{"./submap-1/text-3/content.txt"}, false)
	if err != nil || text != "雨夜" {
		t.Fatalf("副本必须能用子画布局部路径读取：%q err=%v", text, err)
	}
}

// TestCanvasCommandCrossEndEditKeepsUserShot 复现 S09 的跨端例：Agent 先准备改镜头 A，
// 用户从正式保存路径改镜头 B；Agent 的旧快照必须冲突而不是覆盖，重读后才能写入 A，
// 且 B 保持用户版本。随后锁定 A 必须拒绝，重放同一调用不得二次写入。
func TestCanvasCommandCrossEndEditKeepsUserShot(t *testing.T) {
	s, _ := canvasCommandFixture(t)
	_, hash := canvasCommandReload(t, s)
	agentCall := canvasCommandCall(t, "edit ./submap-1/text-1/content.txt Agent 改镜头A", hash)

	canvas, err := s.repo.CanvasProjectForUser("user", "agent-canvas")
	if err != nil {
		t.Fatal(err)
	}
	userDoc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		t.Fatal(err)
	}
	shotB := canvasCommandTestNode(userDoc, "submap-1__text-2")
	if shotB == nil {
		t.Fatal("镜头 B 不存在")
	}
	cloudAgentNodeMetadata(shotB)["content"] = "用户改镜头B"
	raw, err := json.Marshal(userDoc)
	if err != nil {
		t.Fatal(err)
	}
	canvas.PayloadJSON = string(raw)
	if err := s.repo.UpsertCanvasProject(canvas); err != nil {
		t.Fatal(err)
	}

	policy, err := s.RuntimePolicy()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := applyCloudAgentCanvasCommand(s.repo, "user", "agent-canvas", agentCall, policy); err == nil {
		t.Fatal("用户已改镜头 B 后，Agent 旧快照必须冲突")
	}
	freshDoc, freshHash := canvasCommandReload(t, s)
	if got := stringValue(cloudAgentNodeMetadata(canvasCommandTestNode(freshDoc, "submap-1__text-2"))["content"]); got != "用户改镜头B" {
		t.Fatalf("冲突失败不得覆盖用户修改：%q", got)
	}
	if got := stringValue(cloudAgentNodeMetadata(canvasCommandTestNode(freshDoc, "submap-1__text-1"))["content"]); got != "窗边的猫" {
		t.Fatalf("冲突失败不得写入镜头 A：%q", got)
	}

	retry := canvasCommandCall(t, "edit ./submap-1/text-1/content.txt Agent 改镜头A", freshHash)
	if _, err := applyCloudAgentCanvasCommand(s.repo, "user", "agent-canvas", retry, policy); err != nil {
		t.Fatal(err)
	}
	finalDoc, finalHash := canvasCommandReload(t, s)
	if got := stringValue(cloudAgentNodeMetadata(canvasCommandTestNode(finalDoc, "submap-1__text-1"))["content"]); got != "Agent 改镜头A" {
		t.Fatalf("重读后 Agent 必须能写镜头 A：%q", got)
	}
	if got := stringValue(cloudAgentNodeMetadata(canvasCommandTestNode(finalDoc, "submap-1__text-2"))["content"]); got != "用户改镜头B" {
		t.Fatalf("Agent 写入不得覆盖镜头 B：%q", got)
	}
	if _, err := applyCloudAgentCanvasCommand(s.repo, "user", "agent-canvas", retry, policy); err == nil {
		t.Fatal("同一调用重放必须按过期快照拒绝")
	}
	replayedDoc, replayedHash := canvasCommandReload(t, s)
	if replayedHash != finalHash {
		t.Fatal("重放失败不得改变文档")
	}
	if got := stringValue(cloudAgentNodeMetadata(canvasCommandTestNode(replayedDoc, "submap-1__text-1"))["content"]); got != "Agent 改镜头A" {
		t.Fatalf("重放不得二次写入：%q", got)
	}

	canvas, err = s.repo.CanvasProjectForUser("user", "agent-canvas")
	if err != nil {
		t.Fatal(err)
	}
	lockDoc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		t.Fatal(err)
	}
	cloudAgentNodeMetadata(canvasCommandTestNode(lockDoc, "submap-1__text-1"))["locked"] = true
	raw, err = json.Marshal(lockDoc)
	if err != nil {
		t.Fatal(err)
	}
	canvas.PayloadJSON = string(raw)
	if err := s.repo.UpsertCanvasProject(canvas); err != nil {
		t.Fatal(err)
	}
	_, lockedHash := canvasCommandReload(t, s)
	locked := canvasCommandCall(t, "edit ./submap-1/text-1/content.txt 锁定后写入", lockedHash)
	if _, err := prepareCloudAgentCanvasCommandPlan(s.repo, "user", "agent-canvas", locked); err == nil || !strings.Contains(err.Error(), "已锁定") {
		t.Fatalf("锁定镜头必须拒绝 Agent 写入：%v", err)
	}
}

func TestCanvasCommandApprovalReplayAndReadOnlyRejection(t *testing.T) {
	s, _ := canvasCommandFixture(t)
	_, hash := canvasCommandReload(t, s)
	edit := canvasCommandCall(t, "edit ./submap-1/text-1/content.txt 审批写入", "")
	if !cloudAgentWriteCall(edit) || cloudAgentReadCall(edit) {
		t.Fatal("字段写必须按写调用处理")
	}
	read := canvasCommandCall(t, "cat ./submap-1/text-1/content.txt", hash)
	if cloudAgentWriteCall(read) || !cloudAgentReadCall(read) {
		t.Fatal("cat 必须按只读调用处理")
	}
	remove := canvasCommandCall(t, "rm ./submap-1__text-1", hash)
	if !cloudAgentCanvasCommandDestructive(remove) {
		t.Fatal("rm 必须标记为破坏性")
	}
	if cloudAgentCanvasCommandDestructive(edit) {
		t.Fatal("edit 不是破坏性命令")
	}
	resolved, preview, err := resolveCloudAgentCanvasCommandApproval(s.repo, "user", "agent-canvas", edit)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resolved.Function.Arguments, hash) || len(preview.Items) != 1 {
		t.Fatalf("审批必须固化快照并给出明细：args=%s preview=%+v", resolved.Function.Arguments, preview)
	}
	if first, second := cloudAgentApprovalCallHash(resolved), cloudAgentApprovalCallHash(edit); first == second {
		t.Fatal("固化快照后的调用哈希必须变化")
	}
	policy, err := s.RuntimePolicy()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := applyCloudAgentCanvasCommand(s.repo, "user", "agent-canvas", resolved, policy); err != nil {
		t.Fatal(err)
	}
	if _, err := applyCloudAgentCanvasCommand(s.repo, "user", "agent-canvas", resolved, policy); err == nil {
		t.Fatal("同一固化快照重放必须被过期快照拒绝")
	}
	state := &cloudAgentRuntime{Request: CloudAgentRequest{CanvasID: "agent-canvas", PermissionMode: "read_only"}}
	if _, err := canvasCommandHandleRead(s.repo, "user", state, edit); err == nil {
		t.Fatal("只读命令通道必须拒绝写命令")
	}
}

func TestCanvasCommandExposedByToolPolicy(t *testing.T) {
	readOnly := agentSchemasByName(compileCloudAgentTools(CloudAgentRequest{PermissionMode: "read_only", ContextScope: []string{"canvas"}}))
	auto := agentSchemasByName(compileCloudAgentTools(CloudAgentRequest{PermissionMode: "auto", ContextScope: []string{"canvas"}}))
	if readOnly["canvas"] == nil || auto["canvas"] == nil {
		t.Fatal("canvas 工具必须在只读与写入模式都披露")
	}
	tool := builtinAgentToolPolicy("canvas")
	if tool.writeOnly || cloudAgentWrite("canvas") {
		t.Fatal("canvas 是命令级权限工具，不能整体标记为写入")
	}
	if !cloudAgentToolAllowed(agentTestRequest(), "canvas") {
		t.Fatal("canvas 工具未获业务授权")
	}
}

func TestCanvasCommandPresetReadCannotDeleteUserTemplate(t *testing.T) {
	s, db := canvasCommandFixture(t)
	if err := db.AutoMigrate(&model.UserSubmapPreset{}); err != nil {
		t.Fatal(err)
	}
	preset := &model.UserSubmapPreset{ID: "preset-read-boundary", UserID: "user", Name: "保留模板", ManifestJSON: "{}", Revision: 1}
	if err := s.repo.CreateUserSubmapPreset(preset); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"read_only", "auto"} {
		state := &cloudAgentRuntime{Request: CloudAgentRequest{CanvasID: "agent-canvas", PermissionMode: mode}}
		call := canvasCommandCall(t, "preset delete "+preset.ID, "")
		if _, err := canvasCommandHandleRead(s.repo, "user", state, call); err == nil {
			t.Errorf("%s: read channel must reject template deletion", mode)
		}
		if _, err := s.repo.UserSubmapPresetForUser("user", preset.ID); err != nil {
			t.Fatalf("%s: read channel changed template: %v", mode, err)
		}
	}
	_, hash := canvasCommandReload(t, s)
	call := canvasCommandCall(t, "preset delete "+preset.ID+"; edit ./submap-1/text-1/content.txt 不应落库", hash)
	if _, err := prepareCloudAgentCanvasCommandPlan(s.repo, "user", "agent-canvas", call); err == nil {
		t.Fatal("write preview must reject unsupported template deletion")
	}
	if _, err := s.repo.UserSubmapPresetForUser("user", preset.ID); err != nil {
		t.Fatalf("approval preparation changed template: %v", err)
	}
}

func TestCanvasCommandRollsBackWhenRecorderFails(t *testing.T) {
	s, _ := canvasCommandFixture(t)
	_, hash := canvasCommandReload(t, s)
	policy, err := s.RuntimePolicy()
	if err != nil {
		t.Fatal(err)
	}
	root, err := s.CreateCloudAgentRun("user", agentTestRequest(), "")
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	before := canvasPayloadJSON(t, s)
	call := canvasCommandCall(t, "new text 原子失败", hash)
	recorderErr := errors.New("injected command recorder failure")
	failRecorder := func(*repository.Repository, cloudAgentMutationInput) error { return recorderErr }
	err = s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(_ *model.CloudAgentExecution, repo *repository.Repository) error {
		_, writeErr := applyCloudAgentCanvasCommand(repo, "user", "agent-canvas", call, policy, failRecorder)
		return writeErr
	})
	if !errors.Is(err, recorderErr) {
		t.Fatalf("recorder 错误必须向上返回：%v", err)
	}
	if after := canvasPayloadJSON(t, s); after != before {
		t.Fatal("事务回滚后画布不得保留部分写入")
	}
}

func canvasCommandInt(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case int64:
		return int(typed)
	default:
		return -1
	}
}
