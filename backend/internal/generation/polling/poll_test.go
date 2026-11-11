package polling

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"testing"
	"time"
)

func fastPolicy() Policy {
	policy := Policy{
		InitialDelay: time.Millisecond, Interval: time.Millisecond,
		MaxNotFoundMisses: 3, MaxMalformedResponses: 3, MaxDownloadTries: 3, RetryTransient: true,
		Sleep: func(context.Context, time.Duration) error { return nil },
	}
	return policy
}

func TestRunRecoversFromTransientHTTPError(t *testing.T) {
	attempts := 0
	result, err := Run(context.Background(), "provider-task-1", fastPolicy(), func(context.Context) (Outcome, error) {
		attempts++
		switch attempts {
		case 1:
			return Outcome{}, &HTTPStatusError{StatusCode: http.StatusBadGateway, Status: "502 Bad Gateway"}
		case 2:
			return Outcome{}, nil
		default:
			return Outcome{Done: true, Result: map[string]interface{}{"mode": "video"}}, nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 3 || result["mode"] != "video" {
		t.Fatalf("attempts = %d, result = %#v", attempts, result)
	}
}

func TestRunWaitsBeforeFirstQueryAndUsesRetryAfter(t *testing.T) {
	var waits []time.Duration
	policy := fastPolicy()
	policy.InitialDelay = 10 * time.Millisecond
	policy.Interval = 10 * time.Millisecond
	policy.Sleep = func(_ context.Context, delay time.Duration) error {
		waits = append(waits, delay)
		return nil
	}
	attempts := 0
	_, err := Run(context.Background(), "provider-task-1", policy, func(context.Context) (Outcome, error) {
		attempts++
		if attempts == 1 {
			return Outcome{}, &HTTPStatusError{StatusCode: http.StatusTooManyRequests, RetryAfter: 20 * time.Millisecond}
		}
		return Outcome{Done: true, Result: map[string]interface{}{"mode": "video"}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond}; !reflect.DeepEqual(waits, want) {
		t.Fatalf("waits = %v, want %v", waits, want)
	}
}

func TestRunStopsImmediatelyOnAuthenticationError(t *testing.T) {
	attempts := 0
	_, err := Run(context.Background(), "provider-task-1", fastPolicy(), func(context.Context) (Outcome, error) {
		attempts++
		return Outcome{}, &HTTPStatusError{StatusCode: http.StatusUnauthorized, Status: "401 Unauthorized"}
	})
	var httpErr *HTTPStatusError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("error = %#v, want 401 provider error", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

func TestRunStopsAfterThreeConsecutiveNotFoundResponses(t *testing.T) {
	attempts := 0
	_, err := Run(context.Background(), "provider-task-1", fastPolicy(), func(context.Context) (Outcome, error) {
		attempts++
		return Outcome{}, &HTTPStatusError{StatusCode: http.StatusNotFound, Status: "404 Not Found"}
	})
	var httpErr *HTTPStatusError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusNotFound {
		t.Fatalf("error = %#v, want 404 provider error", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

// 协议 body 里的 task_not_exist 即使带 400 状态码也按未就绪计数，而不是任务失败。
func TestRunTreatsProtocolTaskNotReadyBodyAsNotFound(t *testing.T) {
	attempts := 0
	_, err := Run(context.Background(), "provider-task-1", fastPolicy(), func(context.Context) (Outcome, error) {
		attempts++
		return Outcome{}, &HTTPStatusError{StatusCode: http.StatusBadRequest, Body: `{"error":{"code":"task_not_exist"}}`}
	})
	var httpErr *HTTPStatusError
	if !errors.As(err, &httpErr) || attempts != 3 {
		t.Fatalf("error = %#v, attempts = %d, want three not-found attempts", err, attempts)
	}
}

func TestRunSuccessfulPendingResponseResetsNotFoundCounter(t *testing.T) {
	attempts := 0
	result, err := Run(context.Background(), "provider-task-1", fastPolicy(), func(context.Context) (Outcome, error) {
		attempts++
		switch attempts {
		case 1, 3:
			return Outcome{}, &HTTPStatusError{StatusCode: http.StatusNotFound, Status: "404 Not Found"}
		case 2:
			return Outcome{}, nil
		default:
			return Outcome{Done: true, Result: map[string]interface{}{"mode": "video"}}, nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 4 || result["mode"] != "video" {
		t.Fatalf("attempts = %d, result = %#v", attempts, result)
	}
}

func TestRunRetriesMalformedResponseTwiceThenSucceeds(t *testing.T) {
	attempts := 0
	result, err := Run(context.Background(), "provider-task-1", fastPolicy(), func(context.Context) (Outcome, error) {
		attempts++
		if attempts <= 2 {
			return Outcome{}, &ResponseDecodeError{Cause: io.ErrUnexpectedEOF}
		}
		return Outcome{Done: true, Result: map[string]interface{}{"mode": "video"}}, nil
	})
	if err != nil || attempts != 3 || result["mode"] != "video" {
		t.Fatalf("result = %#v, error = %v, attempts = %d", result, err, attempts)
	}
}

func TestRunStopsAfterThreeMalformedResponses(t *testing.T) {
	attempts := 0
	_, err := Run(context.Background(), "provider-task-1", fastPolicy(), func(context.Context) (Outcome, error) {
		attempts++
		return Outcome{}, &json.SyntaxError{Offset: int64(attempts)}
	})
	var syntaxError *json.SyntaxError
	if !errors.As(err, &syntaxError) || attempts != 3 {
		t.Fatalf("error = %#v, attempts = %d, want three attempts and syntax error", err, attempts)
	}
}

func TestClassifiesRetryableSignalAndInterruptedReadsAsTransient(t *testing.T) {
	for _, err := range []error{&RetryableSignal{}, io.ErrUnexpectedEOF, io.ErrClosedPipe} {
		retry, notFound := Retryable(context.Background(), err)
		if !retry || notFound {
			t.Fatalf("error %T classified as retry=%v notFound=%v", err, retry, notFound)
		}
	}
}

func TestRunNotifiesOnlyWhenRetryStartsAndRecovers(t *testing.T) {
	var events []Event
	policy := fastPolicy()
	policy.Notify = func(_ context.Context, _ string, event Event, _ error) {
		events = append(events, event)
	}
	attempts := 0
	_, err := Run(context.Background(), "provider-task-1", policy, func(context.Context) (Outcome, error) {
		attempts++
		switch attempts {
		case 1, 2:
			return Outcome{}, &HTTPStatusError{StatusCode: http.StatusBadGateway}
		case 3:
			return Outcome{}, nil
		default:
			return Outcome{Done: true, Result: map[string]interface{}{"mode": "video"}}, nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []Event{Retrying, Recovered}
	if len(events) != len(want) || events[0] != want[0] || events[1] != want[1] {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
}

func TestRunContextCancellationInterruptsInitialWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	_, err := Run(ctx, "provider-task-1", Normalize(Policy{InitialDelay: DefaultInterval}), func(context.Context) (Outcome, error) {
		called = true
		return Outcome{}, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
	if called {
		t.Fatal("query ran after cancellation")
	}
}

func TestRunAppliesFallbackDeadlineToWaitAndQuery(t *testing.T) {
	policy := fastPolicy()
	waitHadDeadline := false
	queryHadDeadline := false
	policy.Sleep = func(ctx context.Context, _ time.Duration) error {
		_, waitHadDeadline = ctx.Deadline()
		return nil
	}
	_, err := Run(context.Background(), "provider-task-1", policy, func(ctx context.Context) (Outcome, error) {
		_, queryHadDeadline = ctx.Deadline()
		return Outcome{Done: true, Result: map[string]interface{}{"mode": "video"}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !waitHadDeadline || !queryHadDeadline {
		t.Fatalf("wait deadline = %v, query deadline = %v", waitHadDeadline, queryHadDeadline)
	}
}

func TestDownloadRetriesTransientFailure(t *testing.T) {
	attempts := 0
	waits := 0
	policy := fastPolicy()
	policy.Sleep = func(context.Context, time.Duration) error {
		waits++
		return nil
	}
	data, mimeType, err := Download(context.Background(), "provider-task-1", policy, func(context.Context) ([]byte, string, error) {
		attempts++
		if attempts == 1 {
			return nil, "", &HTTPStatusError{StatusCode: http.StatusBadGateway, Status: "502 Bad Gateway"}
		}
		return []byte("video"), "video/mp4", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "video" || mimeType != "video/mp4" || attempts != 2 || waits != 1 {
		t.Fatalf("data = %q, mime = %q, attempts = %d, waits = %d", data, mimeType, attempts, waits)
	}
}

func TestDownloadStopsAfterThreeTransientFailures(t *testing.T) {
	attempts := 0
	_, _, err := Download(context.Background(), "provider-task-1", fastPolicy(), func(context.Context) ([]byte, string, error) {
		attempts++
		return nil, "", &HTTPStatusError{StatusCode: http.StatusServiceUnavailable, Status: "503 Service Unavailable"}
	})
	var downloadError DownloadError
	if !errors.As(err, &downloadError) || attempts != 3 {
		t.Fatalf("error = %#v, attempts = %d, want download error after three attempts", err, attempts)
	}
}

func TestRunDoesNotRetryExhaustedDownload(t *testing.T) {
	attempts := 0
	_, err := Run(context.Background(), "provider-task-1", fastPolicy(), func(context.Context) (Outcome, error) {
		attempts++
		return Outcome{}, DownloadError{TaskID: "provider-task-1", Cause: &HTTPStatusError{StatusCode: http.StatusBadGateway}}
	})
	var downloadError DownloadError
	if !errors.As(err, &downloadError) || attempts != 1 {
		t.Fatalf("error = %#v, poll attempts = %d, want one exhausted download", err, attempts)
	}
}

// upstreamStatus 模拟调用方自有的错误类型：只有经过 Translate 适配才能被分类。
type upstreamStatus struct{ Code int }

func (*upstreamStatus) Error() string { return "upstream bad gateway" }

// Translate 适配后的错误与 polling 原生事实走同一分类路径：HTTP 状态码、Retry-After 与计数一致。
func TestRunAppliesPolicyTranslateBeforeClassifying(t *testing.T) {
	policy := fastPolicy()
	policy.Translate = func(err error) error {
		var status *upstreamStatus
		if errors.As(err, &status) && status.Code == http.StatusBadGateway {
			return &HTTPStatusError{StatusCode: http.StatusBadGateway, RetryAfter: 5 * time.Millisecond, Cause: err}
		}
		return err
	}
	var waits []time.Duration
	policy.Sleep = func(_ context.Context, delay time.Duration) error {
		waits = append(waits, delay)
		return nil
	}
	attempts := 0
	_, err := Run(context.Background(), "provider-task-1", policy, func(context.Context) (Outcome, error) {
		attempts++
		if attempts == 1 {
			return Outcome{}, &upstreamStatus{Code: http.StatusBadGateway}
		}
		return Outcome{Done: true, Result: nil}, nil
	})
	if err != nil || attempts != 2 || len(waits) != 2 || waits[1] != 5*time.Millisecond {
		t.Fatalf("err=%v attempts=%d waits=%v", err, attempts, waits)
	}
}
