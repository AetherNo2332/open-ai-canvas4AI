// 声明式协议插件的结果处理：输出提取、媒体下载重试与错误归类。

package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"yingce/backend/internal/generation"
	"yingce/backend/internal/protocol"
)

func appendProtocolQuery(rawURL string, values map[string][]string) (string, error) {
	return protocol.AppendQuery(rawURL, values)
}

func finishProtocolResult(ctx context.Context, config providerConfig, mode string, taskID string, result *protocol.Result, pollPolicy videoPollPolicy) (map[string]interface{}, error) {
	if result == nil {
		return nil, errors.New("声明式协议已完成但没有返回结果")
	}
	if mode == "text" {
		return protocol.TextResultOutput(result), nil
	}
	references, err := protocol.ResultMediaReferences(mode, result)
	if err != nil {
		return nil, err
	}
	if output, handled, err := recoverProtocolMedia(ctx, config, mode, references); handled {
		if err == nil {
			protocol.AttachResultUsage(output, mode, result)
		}
		return output, err
	}
	items := make([]protocol.DownloadedMedia, 0, len(references))
	for _, reference := range references {
		var data []byte
		var mimeType string
		var downloadErr error
		if mode == "video" {
			data, mimeType, downloadErr = runVideoDownload(ctx, taskID, pollPolicy, func(ctx context.Context) ([]byte, string, error) {
				return protocolMediaBytesOnce(ctx, config, reference)
			})
		} else {
			data, mimeType, downloadErr = protocolMediaBytes(ctx, config, reference)
		}
		if downloadErr != nil {
			return nil, downloadErr
		}
		items = append(items, protocol.DownloadedMedia{Data: data, MIMEType: mimeType})
	}
	return protocol.MediaResultOutput(mode, result, items)
}

// finishProtocolAdapterResult 优先消费 create/poll 已返回的内联或 URL 结果，只有插件明确声明独立结果端点时才下载。
// 空响应或下载失败必须向上失败，不能生成伪素材；application/octet-stream 仅表示传输层未知类型，
// 不会把未知内容伪装成具体图片、视频或音频 MIME。
func finishProtocolAdapterResult(ctx context.Context, input canvasGenerationInput, adapter protocol.Adapter, request protocol.GenerationRequest, taskID string, result *protocol.Result, pollPolicy videoPollPolicy) (map[string]interface{}, error) {
	if protocol.ResultHasOutput(input.Mode, result) {
		return finishProtocolResult(ctx, input.Config, input.Mode, taskID, result, pollPolicy)
	}
	resultAdapter, ok := protocol.ResultEndpoint(adapter)
	if !ok {
		return finishProtocolResult(ctx, input.Config, input.Mode, taskID, result, pollPolicy)
	}
	spec, err := resultAdapter.BuildResult(ctx, protocol.PollContext{BaseURL: input.Config.BaseURL, Model: request.Model, Request: request, TaskID: taskID})
	if err != nil {
		return nil, err
	}
	download := func(ctx context.Context) ([]byte, string, error) {
		return executeProtocolBinaryRequest(withProviderRequestKind(ctx, "download"), input.Config, spec)
	}
	var data []byte
	var mimeType string
	if input.Mode == "video" {
		data, mimeType, err = runVideoDownload(ctx, taskID, pollPolicy, download)
	} else {
		data, mimeType, err = download(ctx)
	}
	if err != nil {
		return nil, fmt.Errorf("声明式协议结果下载失败：%w", err)
	}
	if len(data) == 0 {
		return nil, errors.New("声明式协议结果下载返回空内容")
	}
	mimeType = strings.TrimSpace(strings.Split(mimeType, ";")[0])
	if mimeType == "application/json" || strings.HasSuffix(mimeType, "+json") {
		state, parseErr := adapter.ParsePoll(ctx, protocol.PollContext{BaseURL: input.Config.BaseURL, Model: request.Model, Request: request, TaskID: taskID}, data)
		if parseErr != nil {
			return nil, fmt.Errorf("解析声明式协议结果清单失败：%w", parseErr)
		}
		if state.Status == protocol.StatusFailed || state.Status == protocol.StatusCancelled {
			return nil, protocolTaskFailure(ctx, state.Status, state.Message, taskID)
		}
		if !protocol.ResultHasOutput(input.Mode, state.Result) {
			return nil, errProtocolResultNotReady
		}
		if err := resolveProtocolResultMediaURLs(input.Config.BaseURL, spec, state.Result); err != nil {
			return nil, err
		}
		return finishProtocolResult(ctx, input.Config, input.Mode, taskID, state.Result, pollPolicy)
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	reference := protocol.MediaReference{DataURL: dataURL(mimeType, data), MIMEType: mimeType}
	downloaded := &protocol.Result{}
	switch input.Mode {
	case "image":
		downloaded.Images = []protocol.MediaReference{reference}
	case "video":
		downloaded.Videos = []protocol.MediaReference{reference}
	case "audio":
		downloaded.Audios = []protocol.MediaReference{reference}
	default:
		return nil, fmt.Errorf("声明式协议结果下载不支持生成模式 %s", input.Mode)
	}
	return finishProtocolResult(ctx, input.Config, input.Mode, taskID, downloaded, pollPolicy)
}

// errProtocolResultNotReady 表示上游已报告完成，但独立结果接口返回的清单里还没有媒体。
// 轮询方把它和“完成但没有媒体地址”同样处理：宽限期内继续查原任务，过后按此错误失败。
var errProtocolResultNotReady = errors.New("声明式协议结果清单没有返回媒体地址")

// 有些上游先报告完成、稍后才给出媒体地址。宽限期按查询次数和时长取先到者：
// 视频每 30 秒查一次约等 2 分钟，图片每 2.5 秒查一次约等 1 分钟；超过后按“没有媒体地址”失败，
// 不等到轮询总时限（视频 1 小时、分离式 48 小时）。
const (
	protocolLateResultGrace    = protocol.LateResultGrace
	protocolLateResultMaxPolls = protocol.LateResultMaxPolls
	protocolLateResultStage    = protocol.LateResultStage
)

type protocolLateResultWait struct {
	protocol.LateResultWait
}

// keepWaiting 记录一次“已完成但没有媒体”的查询，返回是否仍在宽限期内。
func (w *protocolLateResultWait) keepWaiting(now time.Time) bool {
	return w.LateResultWait.KeepWaiting(now)
}

// protocolResultReady 判断“已完成”的结果能否收尾：媒体任务要么已带媒体，要么有独立结果接口可下载。
func protocolResultReady(mode string, result *protocol.Result, adapter protocol.Adapter) bool {
	return protocol.ResultReady(mode, result, adapter)
}

func protocolMediaBytes(ctx context.Context, config providerConfig, reference protocol.MediaReference) ([]byte, string, error) {
	var data []byte
	var mimeType string
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		data, mimeType, err = protocolMediaBytesOnce(ctx, config, reference)
		if err == nil {
			return data, mimeType, nil
		}
		if attempt == 2 || !retryableProtocolMediaDownload(err) {
			break
		}
		if waitErr := sleepContext(ctx, time.Duration(attempt+1)*time.Second); waitErr != nil {
			return nil, "", fmt.Errorf("声明式协议媒体结果下载失败：%w", waitErr)
		}
	}
	return nil, "", fmt.Errorf("声明式协议媒体结果下载失败：%w", err)
}

func protocolMediaBytesOnce(ctx context.Context, config providerConfig, reference protocol.MediaReference) ([]byte, string, error) {
	if strings.TrimSpace(reference.DataURL) != "" {
		mimeType, data, err := decodeProviderDataURL(reference.DataURL)
		return data, mimeType, err
	}
	value := strings.TrimSpace(reference.URL)
	if value == "" {
		return nil, "", errors.New("声明式协议媒体结果地址为空")
	}
	data, mimeType, err := getProviderExternalBinary(withProviderRequestKind(ctx, "download"), config, value)
	return data, normalizedMediaMimeType(mimeType, data), err
}

func retryableProtocolMediaDownload(err error) bool {
	return protocol.RetryableMediaDownload(err)
}

// providerTaskTerminalError 表示协议适配器已从上游响应解析出明确的失败或取消终态。
// 它和超时、断线不同：上游已确认任务结束，调用日志与账单都按失败收尾，不进入待核对。
// 稳定错误事实归 internal/generation 所有；这里保留别名维持协议调用点。
type providerTaskTerminalError = generation.TerminalError

// protocolTaskFailure 构造终态错误，并把同一个终态写回本次上游任务的计费根日志。
// 调用日志在适配器解析响应之前就已落库，日志侧只能按通用字段猜状态；manifest 的 status 表达式、
// statusPaths 和 errorPaths 它都看不到。不回写时，失败任务的根日志仍是 succeeded，账单随之进入待核对。
func protocolTaskFailure(ctx context.Context, status protocol.Status, message, taskID string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		message = "上游返回失败状态"
	}
	failure := providerTaskTerminalError{TaskID: taskID, Status: status, Message: message}
	recordProviderTaskTerminalFailure(ctx, failure)
	return failure
}

func resolveProtocolResultMediaURLs(baseURL string, spec protocol.RequestSpec, result *protocol.Result) error {
	manifestURL, parseErr := protocolRequestURL(baseURL, spec)
	if parseErr != nil {
		return parseErr
	}
	return protocol.ResolveResultMediaURLs(manifestURL, result)
}
