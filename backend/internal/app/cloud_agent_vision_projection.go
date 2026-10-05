package app

import (
	"encoding/json"
	"strings"
)

// Projection never mutates the durable transcript. Deferred originals are
// restored from Pi history on the next request, after current SHAs are summarized.
func projectCloudAgentVisionRequest(canonical canonicalAgentRequest, budget cloudAgentVisionBatchBudget, delivered ...map[string]string) (canonicalAgentRequest, []cloudAgentImageInspection) {
	summaries := map[string]any{}
	for _, message := range canonical.Messages {
		if stringValue(message["role"]) != "tool" {
			continue
		}
		var receipt map[string]any
		if json.Unmarshal([]byte(stringValue(message["content"])), &receipt) == nil && receipt["visionCache"] != nil && receipt["imageAttached"] == false {
			summaries[stringValue(receipt["nodeId"])+":"+stringValue(receipt["sha256"])] = receipt["visionCache"]
		}
	}
	var items []cloudAgentImageInspection
	for _, message := range canonical.Messages {
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
			key := stringValue(receipt["nodeId"]) + ":" + stringValue(receipt["sha256"])
			if summaries[key] != nil {
				continue
			}
			// 送达账本按节点 ID 记录实际附图的 sha256。变参 delivered 是"零个或一个 map"：
			// len(delivered) 恒为 1，不能当 map 是否有数据用；且对 nil map 取值返回空串，
			// 会把"收据缺 sha256"误判成"已送达空 SHA"而把图永久藏住。只有账本里存在
			// 非空 SHA 且与收据一致时才认定已送达。
			if len(delivered) > 0 && delivered[0] != nil {
				if sha := delivered[0][stringValue(receipt["nodeId"])]; sha != "" && sha == stringValue(receipt["sha256"]) {
					continue
				}
			}
			items = append(items, cloudAgentImageInspection{Receipt: receipt, ResourceSHA: stringValue(receipt["sha256"]), ImageURL: stringValue(image["url"])})
		}
	}
	batches := planCloudAgentVisionBatches(items, budget)
	var batch []cloudAgentImageInspection
	if len(batches) > 0 {
		batch = batches[0]
	}
	admitted := map[string]bool{}
	for _, item := range batch {
		admitted[item.ImageURL] = true
	}
	canonical.Messages = append([]map[string]any(nil), canonical.Messages...)
	sent := map[string]bool{}
	for i, message := range canonical.Messages {
		parts, ok := message["content"].([]any)
		if !ok {
			continue
		}
		kept := make([]any, 0, len(parts))
		for _, raw := range parts {
			part, _ := raw.(map[string]any)
			if stringValue(part["type"]) != "image_url" {
				kept = append(kept, raw)
				continue
			}
			image, _ := part["image_url"].(map[string]any)
			url := stringValue(image["url"])
			if admitted[url] && !sent[url] {
				kept = append(kept, raw)
				sent[url] = true
			} else {
				kept = append(kept, map[string]any{"type": "text", "text": "这张原图本次未附送；使用已保存的 SHA 摘要，尚无摘要的图片留待后续批次。"})
			}
		}
		copy := cloneStringAnyMap(message)
		copy["content"] = kept
		canonical.Messages[i] = copy
	}
	if len(items) > len(batch) {
		canonical.Messages = append(canonical.Messages, map[string]any{"role": "user", "content": "本次仅附送当前识图批次。请先为每张已附送图片提交绑定 SHA 的结构化摘要，再继续处理剩余图片，不要提前结束任务。"})
	}
	return canonical, batch
}
