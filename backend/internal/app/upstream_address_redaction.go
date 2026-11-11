package app

import "yingce/backend/internal/taskruntime"

// 上游地址脱敏的归属在 internal/taskruntime（退款原因与计费文本的原生使用方）；
// 任务错误、积分流水备注等 app 侧文本经此转发，保持同一套措辞。
var (
	upstreamAddressLinkPlaceholder = taskruntime.LinkPlaceholder
	upstreamAddressHostPlaceholder = taskruntime.HostPlaceholder
)

func redactUpstreamAddress(text string) string {
	return taskruntime.RedactUpstreamAddress(text)
}
