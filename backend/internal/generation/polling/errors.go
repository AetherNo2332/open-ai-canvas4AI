// Package polling 是上游异步任务的轮询归属：等待循环、错误分类计数、下载重试与
// Retry-After 处理。服务层通过 query/Sleep/Notify 回调接入，并把现有 HTTP 客户端
// 错误适配为这里的稳定错误事实；本包不读取 CloudAgent runtime 或完整 Service。
package polling

import (
	"fmt"
	"strings"
	"time"
)

// HTTPStatusError 是分类所需的稳定 HTTP 事实：状态码、响应体与 Retry-After。
// polling 只读取协议字段判定任务是否尚未就绪，不解析展示文案恢复业务类别。
type HTTPStatusError struct {
	StatusCode int
	Status     string
	Body       string
	RetryAfter time.Duration
	Cause      error
}

func (e *HTTPStatusError) Error() string {
	if e.Cause != nil {
		return e.Cause.Error()
	}
	if strings.TrimSpace(e.Status) != "" {
		return e.Status
	}
	return fmt.Sprintf("provider HTTP %d", e.StatusCode)
}

func (e *HTTPStatusError) Unwrap() error { return e.Cause }

// ResponseDecodeError 表示响应体损坏（非法 JSON、中途截断）；连续超限后轮询放弃。
type ResponseDecodeError struct {
	Cause error
}

func (e *ResponseDecodeError) Error() string {
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return "provider response decode failed"
}

func (e *ResponseDecodeError) Unwrap() error { return e.Cause }

// RetryableSignal 表示调用方已断定该错误可重试（渠道熔断打开、槽位临时降级等）。
type RetryableSignal struct {
	Cause error
}

func (e *RetryableSignal) Error() string {
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return "transient provider failure"
}

func (e *RetryableSignal) Unwrap() error { return e.Cause }

// UnretryableSignal 表示调用方已断定该错误不可重试（如媒体恢复已接管），优先于
// 传输层启发式分类。
type UnretryableSignal struct {
	Cause error
}

func (e *UnretryableSignal) Error() string {
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return "permanent provider failure"
}

func (e *UnretryableSignal) Unwrap() error { return e.Cause }

// DownloadError 是结果下载耗尽重试后的错误；轮询查询遇到它不再重试。
type DownloadError struct {
	TaskID string
	Cause  error
}

func (e DownloadError) Error() string {
	if strings.TrimSpace(e.TaskID) == "" {
		return fmt.Sprintf("视频结果下载失败：%v", e.Cause)
	}
	return fmt.Sprintf("视频结果下载失败（任务 %s）：%v", e.TaskID, e.Cause)
}

func (e DownloadError) Unwrap() error { return e.Cause }
