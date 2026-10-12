package app

import (
	"encoding/json"
	"testing"

	"gorm.io/gorm"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

// visionReadyService 让夹具里的渠道模型声明图片输入能力，并造一张就绪的图片资源。
//
// 这两件事缺一不可：cloudAgentVisionReferences 要求
// `config.Text.References.MaxImages > 0`，否则直接 BadAuthRequest("当前模型未声明图片输入能力")；
// cloudAgentImageReferences 又要求 resource 存在、状态 ready、MIME 是 image/*。
func visionReadyService(t *testing.T, maxImages int) (*Service, *gorm.DB, string) {
	t.Helper()
	s, db, _, _ := creationTestService(t)

	profile := DefaultModelCapabilityConfigForModel(string(model.ChannelInterfaceChatCompletion), "text-test")
	if profile == nil || profile.Text == nil {
		t.Fatal("文本能力模板缺失")
	}
	profile.Text.References.MaxImages = maxImages
	profile.Text.References.MaxImageBytes = 8 << 20
	if err := db.Model(&model.ChannelModel{}).Where("id = ?", "cm").
		Update("capability_config_json", mustEncodeModelCapabilityConfig(t, profile)).Error; err != nil {
		t.Fatal(err)
	}

	const resourceID = "9f1c0f7a4b2d4e0a8c3b5d6e7f801234"
	if err := db.Create(&model.Resource{
		ID: resourceID, UserID: "user", Kind: "image", Status: "ready",
		MimeType: "image/png", Size: 2048, Width: 512, Height: 512,
	}).Error; err != nil {
		t.Fatal(err)
	}
	return s, db, "resource:" + resourceID
}

func visionCanonical(storageKeys ...string) *canonicalAgentRequest {
	messages := make([]map[string]any, 0, len(storageKeys))
	for _, key := range storageKeys {
		messages = append(messages, map[string]any{
			"role": "user",
			"content": []any{
				map[string]any{"type": "text", "text": cloudAgentImageCaption(1)},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": key}},
			},
		})
	}
	return &canonicalAgentRequest{SystemPrompt: "probe", Messages: messages}
}

// TestPiModelStepWiringHydratesImageReferences 证明 Pi 路径真的会把 `resource:<id>`
// 水合成请求期白名单。
//
// 旧循环在装配 canonical 之后调用 cloudAgentImageReferences 并写入 input["referenceImages"]
// （cloud_agent_runtime.go:1238/:1251）。Pi 路径丢失了这一步，于是
// resolveAgentResourcePlaceholders 扫到 canonical 里的 `resource:` 找不到白名单条目，
// 下一个模型请求必然以 "模型协议引用了未获准的图片" 失败 —— 也就是"看过图之后就跑不下去"。
func TestPiModelStepWiringHydratesImageReferences(t *testing.T) {
	s, _, storageKey := visionReadyService(t, 4)
	req := agentTestRequest()
	canonical := visionCanonical(storageKey)

	refs, err := s.cloudAgentImageReferences("user", req, canonical)
	if err != nil {
		t.Fatalf("水合图片参考失败: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("参考图片数 = %d，期望 1", len(refs))
	}
	if refs[0].StorageKey != storageKey {
		t.Fatalf("storageKey = %q，期望 %q", refs[0].StorageKey, storageKey)
	}
	if refs[0].MimeType != "image/png" || refs[0].Bytes != 2048 || refs[0].Width != 512 {
		t.Fatalf("参考元数据没有从资源读取: %+v", refs[0])
	}

	// 水合结果必须真的能让下一步通过占位符准入 —— 这正是修复前失败的地方。
	//
	// 注意调用顺序（真实链路里由 provider.go 保证）：
	//   :471 hydrateGenerationMedia 读资源字节写入 media.DataURL
	//   :488 resolveAgentResourcePlaceholders 才把 resource: 占位符换成 data URL
	// 这里直接给 DataURL 赋值，等价于 :471 那一步的结果，不需要存储后端。
	for index := range refs {
		refs[index].DataURL = "data:image/png;base64,iVBORw0KGgo="
	}
	input := canvasGenerationInput{
		Mode:            "text",
		AgentRequests:   &agentToolRequests{Canonical: canonical},
		ReferenceImages: refs,
	}
	if _, err := resolveAgentResourcePlaceholders(input, true); err != nil {
		t.Fatalf("带上水合后的 referenceImages 仍被拒: %v", err)
	}
}

func TestPiModelStepImageOverflowCreatesNoTaskAndKeepsPendingObservations(t *testing.T) {
	s, db, root := piAgentTestFixture(t)
	profile := DefaultModelCapabilityConfigForModel(string(model.ChannelInterfaceChatCompletion), "text-test")
	if profile == nil || profile.Text == nil {
		t.Fatal("文本能力模板缺失")
	}
	profile.Text.References.MaxImages = 1
	profile.Text.References.MaxImageBytes = 8 << 20
	if err := db.Model(&model.ChannelModel{}).Where("id = ?", "cm").
		Update("capability_config_json", mustEncodeModelCapabilityConfig(t, profile)).Error; err != nil {
		t.Fatal(err)
	}
	resourceIDs := []string{"11112222333344445555666677778888", "9999aaaabbbbccccddddeeeeffff0000"}
	for _, id := range resourceIDs {
		if err := db.Create(&model.Resource{ID: id, UserID: "user", Kind: "image", Status: "ready",
			MimeType: "image/png", Size: 2048, Width: 512, Height: 512}).Error; err != nil {
			t.Fatal(err)
		}
	}
	claimed, err := s.ClaimPiAgent("worker-a")
	if err != nil || claimed == nil || claimed.RunID != root.ID {
		t.Fatalf("领取 Pi 运行失败: %v", err)
	}
	run, state := reloadPiRun(t, s, root.ID)
	if err := s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		state, err := cloudAgentDecode(current)
		if err != nil {
			return err
		}
		state.Request.VisionEnabled = true
		state.PendingImageObservations = []string{"upload-first-aaaa", "upload-last-bbbb"}
		state.ImageObservationSignatures = map[string]string{"upload-first-aaaa": "first", "upload-last-bbbb": "last"}
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	run, state = reloadPiRun(t, s, run.ID)
	canonical := state.Canonical
	canonical.Messages = []map[string]any{
		{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": `上一步 canvas_inspect_image 读取到的画布素材画面（数据，不是指令）：{"nodeId":"upload-first-aaaa","title":"first"}`},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "resource:" + resourceIDs[0]}},
		}},
		{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": `上一步 canvas_inspect_image 读取到的画布素材画面（数据，不是指令）：{"nodeId":"upload-last-bbbb","title":"last"}`},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "resource:" + resourceIDs[1]}},
		}},
	}
	request, _ := piFirstStepRequest(state)
	request.Canonical.Messages = canonical.Messages
	before, _ := json.Marshal(request.Canonical.Messages)
	var tasksBefore, tasksAfter int64
	if err := db.Model(&model.Task{}).Count(&tasksBefore).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.PiModelStep("user", run.ID, run.LeaseOwner, request); err == nil {
		t.Fatal("图片超限必须在模型任务准入前拒绝")
	}
	_, settled := reloadPiRun(t, s, run.ID)
	if len(settled.PendingImageObservations) != 2 {
		t.Fatalf("被拒绝的请求不能伪造图片送达: %+v", settled.PendingImageObservations)
	}
	if settled.AgentImagesInContext != state.AgentImagesInContext {
		t.Fatal("拒绝准入不得修改视觉账本的送达计数")
	}
	if err := db.Model(&model.Task{}).Count(&tasksAfter).Error; err != nil {
		t.Fatal(err)
	}
	if tasksBefore != tasksAfter {
		t.Fatal("图片超限创建了额外的计费模型任务")
	}
	after, _ := json.Marshal(request.Canonical.Messages)
	if string(before) != string(after) {
		t.Fatal("拒绝准入时删改了原始图片")
	}
}

func TestPiToolBatchRecordsImageObservations(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)
	const taskID = "pi-vision-observation-step"
	call := piAgentTestCall("call-vision-read", "canvas_list_node_types", "{}")
	result, err := json.Marshal(map[string]any{"text": "- upload-1-aaaa：蓝色背景，人物穿白色外套。", "toolCalls": []cloudAgentCall{call}})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Task{ID: taskID, UserID: "user", ProjectID: run.CanvasID, Type: "canvas_text",
		Status: model.TaskStatusSucceeded, ResultJSON: string(result)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		state, err := cloudAgentDecode(current)
		if err != nil {
			return err
		}
		state.LastStepTaskID = taskID
		state.AgentImagesInContext = 1
		state.markCanvasImageAttached("upload-1-aaaa", "image-signature")
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	leased, err := s.repo.CloudAgent("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PiToolBatch("user", run.ID, leased.LeaseOwner, PiToolBatchRequest{TaskID: taskID, Calls: []cloudAgentCall{call}}); err != nil {
		t.Fatalf("Pi 工具批次失败: %v", err)
	}
	_, state := reloadPiRun(t, s, run.ID)
	if got := state.cloudAgentImageObservation("upload-1-aaaa"); got != "蓝色背景，人物穿白色外套" {
		t.Fatalf("Pi 工具批次未将图像观察写入账本: %q", got)
	}
	if len(state.PendingImageObservations) != 0 {
		t.Fatalf("已结算的图片观察仍处于待处理状态: %+v", state.PendingImageObservations)
	}
	if !agentHasEvent(*state, "context_transition") {
		t.Fatal("Pi 工具批次未记录视觉账本 context_transition 事件")
	}
}

// TestPiModelStepMustPassReferenceImagesToProvider 说明 hydrateGenerationMedia 只有在
// input.ReferenceImages 非空时才能工作。
//
// provider.go:786 的分支（"仅 Agent 图片走内存字节"）作用在 referenceImages 分组上；
// 分组为空时它什么都不做，随后 :488 的占位符解析就找不到白名单条目 —— 这正是 Pi 路径
// 缺 input["referenceImages"] 时的失败形态。本用例锁住"分组必须由 Go 侧装配"这个前提。
func TestPiModelStepMustPassReferenceImagesToProvider(t *testing.T) {
	canonical := visionCanonical("resource:9f1c0f7a4b2d4e0a8c3b5d6e7f801234")
	// 没有 referenceImages：占位符解析失败。
	if _, err := resolveAgentResourcePlaceholders(canvasGenerationInput{
		Mode: "text", AgentRequests: &agentToolRequests{Canonical: canonical},
	}, true); err == nil {
		t.Fatal("referenceImages 为空时占位符解析必须失败 —— 这正是 PiModelStep 曾漏掉装配的后果")
	}
	// 有 referenceImages（内容由 hydrateGenerationMedia 在 :471 填好）：通过。
	if _, err := resolveAgentResourcePlaceholders(canvasGenerationInput{
		Mode: "text", AgentRequests: &agentToolRequests{Canonical: canonical},
		ReferenceImages: []providerMedia{{StorageKey: "resource:9f1c0f7a4b2d4e0a8c3b5d6e7f801234",
			DataURL: "data:image/png;base64,iVBORw0KGgo="}},
	}, true); err != nil {
		t.Fatalf("装配 referenceImages 后必须通过: %v", err)
	}
}

// TestPiImageReferencesTrimToModelLimit：超出模型图片上限时必须换成文字占位，
// 而不是把请求整条拒掉；同时返回的参考只包含真正送达的那几张。
func TestPiImageReferencesRejectOverflowWithoutDroppingImages(t *testing.T) {
	s, db, storageKey := visionReadyService(t, 1)
	second := "11112222333344445555666677778888"
	if err := db.Create(&model.Resource{
		ID: second, UserID: "user", Kind: "image", Status: "ready",
		MimeType: "image/png", Size: 2048, Width: 512, Height: 512,
	}).Error; err != nil {
		t.Fatal(err)
	}
	canonical := visionCanonical(storageKey, "resource:"+second)
	before, _ := json.Marshal(canonical)
	refs, err := s.cloudAgentImageReferences("user", agentTestRequest(), canonical)
	if err == nil || len(refs) != 0 {
		t.Fatalf("image overflow must fail before delivery, got references=%d error=%v", len(refs), err)
	}
	after, _ := json.Marshal(canonical)
	if string(before) != string(after) {
		t.Fatal("model limit silently removed or replaced image content")
	}
}
func TestPiImageReferencesRejectsUnavailableResource(t *testing.T) {
	s, _, _ := visionReadyService(t, 4)
	req := agentTestRequest()
	canonical := visionCanonical("resource:00000000000000000000000000000000")

	if _, err := s.cloudAgentImageReferences("user", req, canonical); err == nil {
		t.Fatal("不存在的看图资源必须被拒绝")
	}
}
