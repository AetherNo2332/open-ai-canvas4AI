// Package taskruntime 是任务链路的计费与终态归属：计费状态迁移与核对策略、
// 终态业务策略、任务执行诊断与退款原因脱敏。执行编排（worker/lifecycle）仍由
// app 组合；本包不 import app，也不感知 CloudAgent runtime。
//
// 兑换码、充值和管理员人工核对仍属于 finance service；这里收敛的是任务
// 从排队、上游调用到终态这一条链路，避免 worker、provider 和终态协调器
// 各自决定如何退款或进入待核对。
package taskruntime

import (
	"regexp"
	"strings"
)

// 用户可见文本（任务错误、退款原因、积分流水备注）会随 API 回传到浏览器，
// 不能出现上游地址。原始内容仍写入请求日志和任务诊断，管理员按需排查。
const (
	// LinkPlaceholder 是错误文本中隐藏链接的占位符，app 侧错误文案复用同一措辞。
	LinkPlaceholder = "[链接已隐藏]"
	// HostPlaceholder 是错误文本中隐藏主机地址的占位符。
	HostPlaceholder = "[地址已隐藏]"
)

var (
	// providerErrorURL 匹配上游错误文本中的 URL；与请求明细日志同一套措辞。
	providerErrorURL = regexp.MustCompile(`(?i)https?://[^\s<>"'，。；）]+`)
	// 网络错误的地址常以 dial/read/write tcp <addr> 形式出现，保留动词前缀，
	// 前端据此继续把这条失败归类成网络异常。
	upstreamNetworkOperation = regexp.MustCompile(`(?i)\b(?:dial|read|write|proxyconnect)\s+tcp\s+\S+`)
	upstreamIPv4Address      = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}(?::\d+)?\b`)
)

// RedactUpstreamAddress 隐藏文本里的上游 URL、IP 和网络操作地址。
// URL 复用请求明细的 [链接已隐藏] 约定，保持用户可见文案与后台日志同一套措辞。
func RedactUpstreamAddress(text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	text = providerErrorURL.ReplaceAllString(text, LinkPlaceholder)
	text = upstreamNetworkOperation.ReplaceAllStringFunc(text, func(match string) string {
		fields := strings.Fields(match)
		if len(fields) >= 2 {
			return fields[0] + " " + fields[1] + " " + HostPlaceholder + ":"
		}
		return HostPlaceholder
	})
	return upstreamIPv4Address.ReplaceAllString(text, HostPlaceholder)
}
