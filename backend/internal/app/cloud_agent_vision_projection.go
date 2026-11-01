package app

import (
	"encoding/json"
	"strings"
)

// Projection never mutates the durable transcript. Deferred originals are
// restored from Pi history on the next request, after current SHAs are summarized.
func projectCloudAgentVisionRequest(canonical canonicalAgentRequest, budget cloudAgentVisionBatchBudget, delivered ...map[string]string) (canonicalAgentRequest, []cloudAgentImageInspection) {
	summaries := cloudAgentVisionSummaries(canonical.Messages)
	items := cloudAgentVisionPendingItems(canonical.Messages, summaries, delivered)
	batches := planCloudAgentVisionBatches(items, budget)
	var batch []cloudAgentImageInspection
	if len(batches) > 0 {
		batch = batches[0]
	}
	admitted := map[string]string{}
	for _, item := range batch {
		admitted[item.ImageURL] = stringValue(item.Receipt["comparisonId"])
	}
	canonical.Messages = append([]map[string]any(nil), canonical.Messages...)
	sent := map[string]bool{}
	for i, message := range canonical.Messages {
		parts, ok := message["content"].([]any)
		if !ok {
			continue
		}
		copy := cloneStringAnyMap(message)
		copy["content"] = cloudAgentVisionKeptParts(parts, admitted, sent)
		canonical.Messages[i] = copy
	}
	if len(items) > len(batch) {
		canonical.Messages = append(canonical.Messages, map[string]any{"role": "user", "content": "本次仅附送当前识图批次。请先为每张已附送图片提交绑定 SHA 的结构化摘要，再继续处理剩余图片，不要提前结束任务。"})
	}
	return canonical, batch
}

// cloudAgentVisionSummaries collects saved vision summaries from tool receipts
// that were returned without an attached image, keyed by nodeId:sha256.
func cloudAgentVisionSummaries(messages []map[string]any) map[string]any {
	summaries := map[string]any{}
	for _, message := range messages {
		if stringValue(message["role"]) != "tool" {
			continue
		}
		var receipt map[string]any
		if json.Unmarshal([]byte(stringValue(message["content"])), &receipt) == nil && receipt["visionCache"] != nil && receipt["imageAttached"] == false {
			summaries[stringValue(receipt["nodeId"])+":"+stringValue(receipt["sha256"])] = receipt["visionCache"]
		}
	}
	return summaries
}

// cloudAgentVisionPendingItems lists the images that still need delivery, one
// per URL (a later receipt for the same URL replaces the earlier one).
func cloudAgentVisionPendingItems(messages []map[string]any, summaries map[string]any, delivered []map[string]string) []cloudAgentImageInspection {
	var items []cloudAgentImageInspection
	itemIndexes := map[string]int{}
	for _, message := range messages {
		parts, _ := message["content"].([]any)
		for index, raw := range parts {
			part, _ := raw.(map[string]any)
			if stringValue(part["type"]) != "image_url" {
				continue
			}
			image, _ := part["image_url"].(map[string]any)
			receipt := map[string]any{}
			if index > 0 {
				caption, _ := parts[index-1].(map[string]any)
				text := stringValue(caption["text"])
				if start := strings.Index(text, "{"); start >= 0 {
					_ = json.Unmarshal([]byte(text[start:]), &receipt)
				}
			}
			if !cloudAgentVisionNeedsDelivery(receipt, summaries, delivered) {
				continue
			}
			item := cloudAgentImageInspection{Receipt: receipt, ResourceSHA: stringValue(receipt["sha256"]), ImageURL: stringValue(image["url"])}
			if previous, exists := itemIndexes[item.ImageURL]; exists {
				items[previous] = item
			} else {
				itemIndexes[item.ImageURL] = len(items)
				items = append(items, item)
			}
		}
	}
	return items
}

// cloudAgentVisionNeedsDelivery skips images that already have a summary or
// were already delivered at this SHA, unless the call forces a fresh look.
func cloudAgentVisionNeedsDelivery(receipt, summaries map[string]any, delivered []map[string]string) bool {
	key := stringValue(receipt["nodeId"]) + ":" + stringValue(receipt["sha256"])
	comparisonID := stringValue(receipt["comparisonId"])
	force := receipt["forceImage"] == true && (receipt["purpose"] == "compare" || receipt["purpose"] == "inspect") && comparisonID != "" && cloudAgentSHA256(stringValue(receipt["sha256"]))
	if force && len(delivered) > 1 && delivered[1][comparisonID] == stringValue(receipt["sha256"]) {
		force = false
	}
	if summaries[key] != nil && !force {
		return false
	}
	// An explicit comparison or refresh bypasses old summaries and SHA
	// delivery only until that exact tool call's image has been sent.
	if !force && len(delivered) > 0 && delivered[0] != nil {
		if sha := delivered[0][stringValue(receipt["nodeId"])]; sha != "" && sha == stringValue(receipt["sha256"]) {
			return false
		}
	}
	return true
}

// cloudAgentVisionKeptParts keeps each admitted image once (for its admitted
// comparison) and replaces every other image with a deferral note.
func cloudAgentVisionKeptParts(parts []any, admitted map[string]string, sent map[string]bool) []any {
	kept := make([]any, 0, len(parts))
	for index, raw := range parts {
		part, _ := raw.(map[string]any)
		if stringValue(part["type"]) != "image_url" {
			kept = append(kept, raw)
			continue
		}
		image, _ := part["image_url"].(map[string]any)
		url := stringValue(image["url"])
		comparisonID := ""
		if index > 0 {
			caption, _ := parts[index-1].(map[string]any)
			comparisonID = stringValue(cloudAgentImageReceipt(stringValue(caption["text"]))["comparisonId"])
		}
		selected, exists := admitted[url]
		if exists && selected == comparisonID && !sent[url] {
			kept = append(kept, raw)
			sent[url] = true
		} else {
			kept = append(kept, map[string]any{"type": "text", "text": "这张原图本次未附送；使用已保存的 SHA 摘要，尚无摘要的图片留待后续批次。"})
		}
	}
	return kept
}
