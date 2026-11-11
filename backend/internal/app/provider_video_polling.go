// 上游异步任务轮询的 app 适配层：循环、错误分类计数与下载重试已迁
// internal/generation/polling；这里保留默认策略构造、现有 HTTP 错误到
// polling 稳定事实的转换、日志通知，以及调用点使用的类型别名。

package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"yingce/backend/internal/generation/polling"
)

const defaultVideoPollInterval = polling.DefaultInterval

// 轮询的行为与类型归 polling 所有；别名保持既有调用点与测试不变。
type (
	videoPollPolicy    = polling.Policy
	videoPollEvent     = polling.Event
	videoPollOutcome   = polling.Outcome
	videoDownloadError = polling.DownloadError
)

const (
	videoPollEventRetrying  = polling.Retrying
	videoPollEventRecovered = polling.Recovered
)

// defaultVideoPollPolicy 是视频/协议轮询的默认策略：日志通知走任务事件，
// HTTP 客户端错误经 translateVideoPollError 适配为 polling 的稳定事实。
func defaultVideoPollPolicy() videoPollPolicy {
	return videoPollPolicy{
		InitialDelay:          defaultVideoPollInterval,
		Interval:              defaultVideoPollInterval,
		MaxNotFoundMisses:     3,
		MaxMalformedResponses: 3,
		MaxDownloadTries:      3,
		RetryTransient:        true,
		Sleep:                 sleepContext,
		Notify:                notifyTaskVideoPollEvent,
		Translate:             translateVideoPollError,
	}
}

// translateVideoPollError 把现有 HTTP/解码/熔断/槽位错误转换为 polling 认识的
// 稳定错误事实；未知错误原样返回，由 polling 按传输层与标准库错误分类。
func translateVideoPollError(err error) error {
	var httpErr providerHTTPError
	if errors.As(err, &httpErr) {
		return &polling.HTTPStatusError{StatusCode: httpErr.StatusCode, Status: httpErr.Status, Body: httpErr.Body, RetryAfter: httpErr.RetryAfter, Cause: err}
	}
	var decode providerResponseDecodeError
	if errors.As(err, &decode) {
		return &polling.ResponseDecodeError{Cause: err}
	}
	var circuit providerCircuitOpenError
	if errors.As(err, &circuit) {
		return &polling.RetryableSignal{Cause: err}
	}
	if code, _ := ChannelSlotFailureDetails(err); code != "" {
		return &polling.RetryableSignal{Cause: err}
	}
	var recovery *mediaRecoveryError
	if errors.As(err, &recovery) {
		// 媒体恢复已接管的结果错误不由轮询重试；包装后传输层启发式不再穿透。
		return &polling.UnretryableSignal{Cause: err}
	}
	return err
}

// runVideoPollLoop 转发轮询循环；deadline 缺省时由 polling 以 DefaultTimeout 兜底。
func runVideoPollLoop(ctx context.Context, taskID string, policy videoPollPolicy, query func(context.Context) (videoPollOutcome, error)) (map[string]interface{}, error) {
	return polling.Run(ctx, taskID, policy, query)
}

var runVideoDownload = polling.Download

// providerRetryAfter 读取现有 HTTP 错误上的 Retry-After；等待间隔由 polling 统一取用。
func providerRetryAfter(err error) time.Duration {
	var httpErr providerHTTPError
	if errors.As(err, &httpErr) && httpErr.RetryAfter > 0 {
		return httpErr.RetryAfter
	}
	return 0
}

// isTransientTransportError 转发 polling 的连接中断识别，供协议异步轮询使用。
func isTransientTransportError(err error) bool {
	return polling.IsTransientTransport(err)
}

func notifyTaskVideoPollEvent(ctx context.Context, _ string, event videoPollEvent, eventErr error) {
	metadata, ok := ctx.Value(providerAnalyticsKey{}).(providerAnalyticsContext)
	if !ok || metadata.Service == nil || metadata.Service.repo == nil || metadata.TaskID == "" {
		return
	}
	message := "上游视频查询暂时异常，将继续轮询原任务"
	level := "warn"
	payload := ""
	if eventErr != nil {
		payload = eventErr.Error()
	}
	if event == videoPollEventRecovered {
		message = "上游视频查询已恢复"
		level = "info"
	}
	_ = metadata.Service.log(metadata.UserID, metadata.TaskID, level, message, payload)
}

// isProviderTaskNotReadyError 供 HTTP 客户端层判断响应是否代表任务尚未就绪；
// 协议 body 的判定事实已同步到 polling.taskNotReady，这里的实现保持原语义。
func isProviderTaskNotReadyError(httpErr providerHTTPError) bool {
	if httpErr.StatusCode != 400 && httpErr.StatusCode != 404 {
		return false
	}
	var payload map[string]any
	if json.Unmarshal([]byte(httpErr.Body), &payload) != nil {
		return false
	}
	code, message := providerFailureDetails(payload)
	for _, value := range []string{code, message} {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "task_not_exist", "task_not_found", "task not exist", "task not found":
			return true
		}
	}
	return false
}
