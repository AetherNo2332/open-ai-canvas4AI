package app

import (
	"encoding/json"
	"fmt"
	"maps"
	"strings"

	"yingce/backend/internal/model"
)

// Groups survive process recovery. A comparison is admitted as a unit.
type cloudAgentVisionGroup struct {
	Images []cloudAgentImageInspection `json:"images,omitempty"`
	Issue  string                      `json:"issue,omitempty"`
}

type cloudAgentVisionDelivery struct {
	NodeID    string `json:"nodeId"`
	SHA256    string `json:"sha256"`
	Signature string `json:"signature"`
}

func cloudAgentFlushPendingImages(state *cloudAgentRuntime) bool {
	if state == nil || len(state.PendingImageInspections) == 0 && !state.VisionRequestTail {
		return false
	}
	inspections := state.PendingImageInspections
	state.PendingImageInspections = nil
	if state.VisionRequestTail {
		if state.PiToolBatchTaskID != "" && state.VisionQueuedBatchID == state.PiToolBatchTaskID {
			return false
		}
		state.VisionQueuedBatchID = state.PiToolBatchTaskID
		return state.queueVisionBatch(inspections)
	}
	for _, batch := range planCloudAgentVisionBatches(inspections, state.VisionBatchBudget) {
		state.Canonical.Messages = append(state.Canonical.Messages,
			map[string]any{"role": "user", "content": cloudAgentImageContentParts(batch...)})
	}
	return true
}

func (state *cloudAgentRuntime) queueVisionBatch(inspections []cloudAgentImageInspection) bool {
	var comparison cloudAgentVisionGroup
	var ordinary []cloudAgentVisionGroup
	compared := map[string]bool{}
	seen := map[string]bool{}
	for _, inspection := range inspections {
		nodeID := stringField(inspection.Receipt, "nodeId")
		if inspection.Receipt["purpose"] != "compare" {
			key := nodeID + "\x00" + inspection.ResourceSHA
			if !seen[key] {
				ordinary = append(ordinary, cloudAgentVisionGroup{Images: []cloudAgentImageInspection{inspection}})
				seen[key] = true
			}
			continue
		}
		if !compared[nodeID] {
			comparison.Images = append(comparison.Images, inspection)
			compared[nodeID] = true
		}
	}
	if targets := cloudAgentVisionComparisonTargets(state.Calls); len(targets) > 0 {
		if len(compared) < 2 || len(compared) != len(targets) {
			comparison.Images = nil
			comparison.Issue = "对比未附图：请在同一工具批次为所有待对比节点请求 purpose=compare，且所有图片都须可读取。"
		}
		state.VisionQueue = append(state.VisionQueue, comparison)
	}
	for _, group := range ordinary {
		if comparison.Issue != "" || !compared[stringField(group.Images[0].Receipt, "nodeId")] {
			state.VisionQueue = append(state.VisionQueue, group)
		}
	}
	return len(ordinary) > 0 || len(comparison.Images) > 0 || comparison.Issue != ""
}

func cloudAgentVisionComparisonTargets(calls []cloudAgentCall) map[string]bool {
	targets := map[string]bool{}
	for _, call := range calls {
		if call.Function.Name != "canvas_inspect_image" {
			continue
		}
		var args struct {
			NodeID  string `json:"nodeId"`
			Purpose string `json:"purpose"`
		}
		if json.Unmarshal([]byte(call.Function.Arguments), &args) == nil && args.Purpose == "compare" {
			targets[args.NodeID] = true
		}
	}
	return targets
}

func cloudAgentVisionRefresh(state *cloudAgentRuntime, refresh bool, purpose string) bool {
	return refresh || state != nil && state.VisionRequestTail && purpose == "compare"
}

func cloudAgentReuseDeliveredImage(state *cloudAgentRuntime, cacheKey, nodeID, sha, signature string) bool {
	return state.ImageInspectionReads[cacheKey] > 0 && state.DeliveredImageSHAs[nodeID] == sha &&
		(!state.VisionRequestTail || state.cloudAgentImageObservationFor(nodeID, signature) != "")
}

func (state *cloudAgentRuntime) settleVisionProjection(accepted bool) {
	if accepted {
		state.VisionQueue = state.VisionQueue[min(state.VisionProjectedGroups, len(state.VisionQueue)):]
		if len(state.VisionProjectedImages) > 0 && state.DeliveredImageSHAs == nil {
			state.DeliveredImageSHAs = map[string]string{}
		}
		for _, image := range state.VisionProjectedImages {
			state.countCanvasImageAttachment(image.NodeID)
			state.noteCanvasImageAttachment(image.NodeID, image.Signature)
			state.DeliveredImageSHAs[image.NodeID] = image.SHA256
		}
	}
	state.VisionProjectedGroups = 0
	state.VisionProjectedImages = nil
}

// Model-facing attachments come from server-owned tool results. Recheck the
// current node binding because a user can replace or unlink it while a run waits.
func (s *Service) piProjectVisionTail(run *model.CloudAgentExecution, state *cloudAgentRuntime, canonical *canonicalAgentRequest) ([]providerMedia, error) {
	if len(state.VisionQueue) == 0 && len(cloudAgentCanonicalImageKeys(canonical)) == 0 {
		return nil, nil
	}
	if len(state.VisionQueue) == 0 {
		return s.piLegacyModelStepImageReferences(run, state, canonical)
	}
	canvas, err := s.repo.CanvasProjectForUser(run.UserID, state.Request.CanvasID)
	if err != nil {
		return nil, err
	}
	nodes, err := cloudAgentVisionNodes(canvas.PayloadJSON)
	if err != nil {
		return nil, err
	}
	budget, err := s.cloudAgentVisionBatchBudget(state.Request)
	if err != nil {
		return nil, err
	}
	// Old histories can still contain images. Their authorization and limits
	// apply independently of queued tool images.
	legacy, err := s.piLegacyModelStepImageReferences(run, state, canonical)
	if err != nil {
		return nil, err
	}
	legacyCost := 0
	for _, image := range legacy {
		legacyCost += cloudAgentVisionImageCost(cloudAgentImageInspection{Receipt: map[string]any{"bytes": image.Bytes, "width": image.Width, "height": image.Height}})
	}
	limits, err := s.cloudAgentVisionReferences(state.Request)
	if err != nil {
		return nil, err
	}
	batch, notes, consumed := selectPiVisionTail(state.VisionQueue, budget, len(cloudAgentCanonicalImageKeys(canonical)), legacyCost, func(group cloudAgentVisionGroup) string {
		return s.verifyPiVisionGroup(run.UserID, canvas.ProjectID, group, nodes, limits.MaxImageBytes, state)
	})
	state.VisionProjectedGroups = consumed
	state.VisionProjectedImages = nil
	if len(notes) > 0 {
		canonical.Messages = appendVisionRuntimeData(canonical.Messages, "【本次图片交付】"+strings.Join(notes, "\n"))
	}
	if len(batch) > 0 {
		for index, item := range batch {
			state.VisionProjectedImages = append(state.VisionProjectedImages, cloudAgentVisionDelivery{
				NodeID: stringField(item.Receipt, "nodeId"), SHA256: item.ResourceSHA, Signature: stringField(item.Receipt, "contentSignature"),
			})
			batch[index].Receipt = maps.Clone(item.Receipt)
			delete(batch[index].Receipt, "imagePending")
			batch[index].Receipt["imageAttached"] = true
			batch[index].Receipt["note"] = "本次已附送此图。观察后可提交 nodeId、sha256 和 summary 保存摘要。"
		}
		canonical.Messages = appendVisionRuntimeData(canonical.Messages, cloudAgentImageContentParts(batch...))
	}
	return s.cloudAgentAuthorizedImageReferences(run.UserID, state.Request, canonical, nil)
}

func cloudAgentVisionNodes(payload string) (map[string]map[string]any, error) {
	doc, err := creationDocument(payload)
	if err != nil {
		return nil, BadAuthRequest("服务端画布内容无法解析，请先重新同步")
	}
	nodes := map[string]map[string]any{}
	for _, node := range creationMaps(doc["nodes"]) {
		nodes[stringField(node, "id")] = node
	}
	return nodes, nil
}

func appendVisionRuntimeData(messages []map[string]any, content any) []map[string]any {
	return append(messages[:len(messages):len(messages)], map[string]any{
		"role": "user", "content": content, cloudAgentContextSourceKey: "runtime",
	})
}

func (s *Service) verifyPiVisionGroup(userID, projectID string, group cloudAgentVisionGroup, nodes map[string]map[string]any, maxBytes int64, state *cloudAgentRuntime) string {
	for _, item := range group.Images {
		nodeID := stringField(item.Receipt, "nodeId")
		if state.cloudAgentImageInspectionCount(nodeID) >= cloudAgentMaxImageInspectionsPerRun {
			return fmt.Sprintf("本组未附图：节点 %s 已达到本轮附图上限。", nodeID)
		}
		ref, _, err := cloudAgentMediaReference(s.repo, userID, projectID, nodes[nodeID])
		if err != nil || stringField(ref, "storageKey") != item.ImageURL {
			return fmt.Sprintf("本组未附图：节点 %s 的图片已更换、解绑或不可用。需要观察时请重新读取目标节点。", nodeID)
		}
		if size, ok := ref["bytes"].(int64); !ok || validateCloudAgentImageResourceSize(size, maxBytes) != nil {
			return fmt.Sprintf("本组未附图：节点 %s 超出当前渠道图片大小限制或大小不可用。", nodeID)
		}
	}
	return group.Issue
}

func selectPiVisionTail(groups []cloudAgentVisionGroup, budget cloudAgentVisionBatchBudget, usedImages, usedCost int, validate func(cloudAgentVisionGroup) string) (batch []cloudAgentImageInspection, notes []string, consumed int) {
	for _, group := range groups {
		cost := 0
		for _, item := range group.Images {
			cost += cloudAgentVisionImageCost(item)
		}
		issue := validate(group)
		switch {
		case issue != "":
			notes = append(notes, issue)
		case len(group.Images) > budget.MaxImages || cost > budget.MaxCost:
			notes = append(notes, fmt.Sprintf("本组未附图：需同批发送 %d 张、成本 %d，当前单次最多 %d 张、成本 %d。请说明限制或改用支持该批次的渠道。",
				len(group.Images), cost, budget.MaxImages, budget.MaxCost))
		case usedImages+len(group.Images) > budget.MaxImages || usedCost+cost > budget.MaxCost:
			if len(batch) > 0 {
				return batch, append(notes, "还有图片待后续批次附送，本次仅依据实际随请求提供的图片观察。"), consumed
			}
			notes = append(notes, "本组未附图：请求中的其他图片占用了预算，无法同时发送本组全部图片。")
		default:
			batch = append(batch, group.Images...)
			usedImages += len(group.Images)
			usedCost += cost
		}
		consumed++
	}
	return batch, notes, consumed
}
