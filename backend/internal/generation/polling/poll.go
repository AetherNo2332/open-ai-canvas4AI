package polling

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

const (
	// DefaultInterval 是两次轮询之间的默认间隔。
	DefaultInterval = 30 * time.Second
	// DefaultTimeout 是调用方未提供 deadline 时的兜底总时长。
	DefaultTimeout = time.Hour
)

// Event 是轮询过程通知：首次进入重试与从暂时异常中恢复各通知一次。
type Event string

const (
	Retrying  Event = "retrying"
	Recovered Event = "recovered"
)

// Outcome 是单次查询结果。
type Outcome struct {
	Done   bool
	Result map[string]interface{}
}

// Policy 描述一次轮询的行为。query、Sleep、Notify 由调用方注入；
// Translate 把调用方现有错误适配为稳定错误事实，nil 表示原样分类。
type Policy struct {
	InitialDelay          time.Duration
	Interval              time.Duration
	MaxNotFoundMisses     int
	MaxMalformedResponses int
	MaxDownloadTries      int
	RetryTransient        bool
	// TransientRetryLimit 只在 RetryTransient=false（fail-fast 策略）时生效：允许连续多少次
	// 暂时性轮询错误（连接中断、5xx、429 等）后继续查询原任务。0 表示保持 fail-fast。
	TransientRetryLimit int
	Sleep               func(context.Context, time.Duration) error
	Notify              func(context.Context, string, Event, error)
	Translate           func(error) error
}

// Normalize 补齐未设置的策略字段，保持既有默认语义。
func Normalize(policy Policy) Policy {
	if policy.Interval <= 0 {
		policy.Interval = DefaultInterval
	}
	if policy.MaxNotFoundMisses <= 0 {
		policy.MaxNotFoundMisses = 3
	}
	if policy.MaxMalformedResponses <= 0 {
		policy.MaxMalformedResponses = 3
	}
	if policy.MaxDownloadTries <= 0 {
		policy.MaxDownloadTries = 3
	}
	if policy.Sleep == nil {
		policy.Sleep = SleepContext
	}
	if policy.Notify == nil {
		policy.Notify = func(context.Context, string, Event, error) {}
	}
	return policy
}

// SleepContext 在 duration 内等待，ctx 结束时立即返回其错误。
func SleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func pollingDeadline(ctx context.Context) time.Time {
	if deadline, ok := ctx.Deadline(); ok {
		return deadline
	}
	return time.Now().Add(DefaultTimeout)
}

func adapted(policy Policy, err error) error {
	if policy.Translate == nil {
		return err
	}
	return policy.Translate(err)
}

// Run 执行轮询循环直到查询完成、错误不可重试或计数耗尽；deadline 取自 ctx，
// 未设置时用 DefaultTimeout 兜底。
func Run(ctx context.Context, taskID string, policy Policy, query func(context.Context) (Outcome, error)) (map[string]interface{}, error) {
	policy = Normalize(policy)
	deadline := pollingDeadline(ctx)
	pollContext, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	ctx = pollContext
	nextDelay := policy.InitialDelay
	notFoundMisses := 0
	malformedResponses := 0
	retrying := false
	transientFailures := 0
	for time.Now().Before(deadline) {
		if nextDelay > 0 {
			if err := policy.Sleep(ctx, nextDelay); err != nil {
				return nil, err
			}
		}
		outcome, err := query(ctx)
		if err != nil {
			err = adapted(policy, err)
			if !policy.RetryTransient {
				retry, notFound := Retryable(ctx, err)
				if !retry || notFound || isMalformed(err) || transientFailures >= policy.TransientRetryLimit {
					return nil, err
				}
				transientFailures++
				nextDelay = max(min(policy.Interval<<(transientFailures-1), 4*policy.Interval), retryAfterOf(err))
				continue
			}
			retry, notFound := Retryable(ctx, err)
			if !retry {
				return nil, err
			}
			if isMalformed(err) {
				malformedResponses++
				notFoundMisses = 0
				if malformedResponses >= policy.MaxMalformedResponses {
					return nil, err
				}
			} else if notFound {
				malformedResponses = 0
				notFoundMisses++
				if notFoundMisses >= policy.MaxNotFoundMisses {
					return nil, err
				}
			} else {
				malformedResponses = 0
				notFoundMisses = 0
			}
			if !retrying {
				retrying = true
				policy.Notify(ctx, taskID, Retrying, err)
			}
			nextDelay = max(policy.Interval, retryAfterOf(err))
			continue
		}
		notFoundMisses = 0
		malformedResponses = 0
		transientFailures = 0
		nextDelay = policy.Interval
		if retrying {
			retrying = false
			policy.Notify(ctx, taskID, Recovered, nil)
		}
		if outcome.Done {
			return outcome.Result, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, context.DeadlineExceeded
}

// Download 重试结果下载，最多 MaxDownloadTries 次；耗尽后返回 DownloadError。
func Download(ctx context.Context, taskID string, policy Policy, download func(context.Context) ([]byte, string, error)) ([]byte, string, error) {
	policy = Normalize(policy)
	var lastErr error
	for attempt := 1; attempt <= policy.MaxDownloadTries; attempt++ {
		data, mimeType, err := download(ctx)
		if err == nil {
			return data, mimeType, nil
		}
		err = adapted(policy, err)
		lastErr = err
		retry, _ := Retryable(ctx, err)
		if !retry || attempt == policy.MaxDownloadTries {
			return nil, "", DownloadError{TaskID: taskID, Cause: err}
		}
		delay := max(policy.Interval, retryAfterOf(err))
		if err := policy.Sleep(ctx, delay); err != nil {
			return nil, "", err
		}
	}
	return nil, "", DownloadError{TaskID: taskID, Cause: lastErr}
}

// Retryable 分类轮询错误：retry 表示可以继续查询原任务；notFound 表示任务尚未就绪
// （计入未命中上限）。取消与下载耗尽不重试。
func Retryable(ctx context.Context, err error) (retry bool, notFound bool) {
	if err == nil || ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return false, false
	}
	var downloadError DownloadError
	if errors.As(err, &downloadError) {
		return false, false
	}
	var terminal *UnretryableSignal
	if errors.As(err, &terminal) {
		return false, false
	}
	var signal *RetryableSignal
	if errors.As(err, &signal) {
		return true, false
	}
	if isMalformed(err) {
		return true, false
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.ErrClosedPipe) {
		return true, false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true, false
	}
	var httpErr *HTTPStatusError
	if errors.As(err, &httpErr) {
		if taskNotReady(httpErr.Body) {
			return true, true
		}
		if httpErr.StatusCode == http.StatusNotFound {
			return true, true
		}
		switch httpErr.StatusCode {
		case http.StatusRequestTimeout, http.StatusConflict, http.StatusTooEarly, http.StatusTooManyRequests:
			return true, false
		default:
			return httpErr.StatusCode >= http.StatusInternalServerError, false
		}
	}
	if IsTransientTransport(err) {
		return true, false
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		return networkError.Timeout() || networkError.Temporary(), false
	}
	return false, false
}

func isMalformed(err error) bool {
	var decodeError *ResponseDecodeError
	if errors.As(err, &decodeError) {
		return true
	}
	var syntaxError *json.SyntaxError
	return errors.As(err, &syntaxError)
}

func retryAfterOf(err error) time.Duration {
	var httpErr *HTTPStatusError
	if errors.As(err, &httpErr) && httpErr.RetryAfter > 0 {
		return httpErr.RetryAfter
	}
	return 0
}

// taskNotReady 识别协议 body 中的任务不存在类错误码；这类响应按未命中计数而不是失败。
func taskNotReady(body string) bool {
	if strings.TrimSpace(body) == "" {
		return false
	}
	var payload map[string]any
	if json.Unmarshal([]byte(body), &payload) != nil {
		return false
	}
	for _, value := range protocolErrorValues(payload) {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "task_not_exist", "task_not_found", "task not exist", "task not found":
			return true
		}
	}
	return false
}

func protocolErrorValues(payload map[string]any) []string {
	values := []string{}
	candidates := make([]map[string]any, 0, 3)
	for _, key := range []string{"error", "data"} {
		if nested, ok := payload[key].(map[string]any); ok {
			candidates = append(candidates, nested)
		}
	}
	candidates = append(candidates, payload)
	for _, candidate := range candidates {
		for _, key := range []string{"code", "message", "msg", "fail_reason"} {
			if raw, ok := candidate[key].(string); ok {
				values = append(values, raw)
			}
		}
	}
	return values
}

// IsTransientTransport 识别读写阶段的连接中断。Go 对读写阶段的 ECONNRESET 等错误
// 返回 Temporary()=false，只看 net.Error 会把已提交任务的一次网络抖动当成永久失败。
func IsTransientTransport(err error) bool {
	var urlError *url.Error
	var opError *net.OpError
	if !errors.As(err, &urlError) && !errors.As(err, &opError) {
		return false
	}
	for _, target := range []error{io.EOF, syscall.ECONNRESET, syscall.ECONNABORTED, syscall.EPIPE} {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}
