package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// ConfigureShortplayExtractExecutorFromEnv 从部署环境读取提取渠道（TEXT_BASE_URL / TEXT_MODEL /
// TEXT_API_KEY），注入 ShortplayExtractFunc。返回 false 表示没配置完整，调度循环会让在途任务
// 以「提取执行器未配置」失败并写 last_error，而不是静默空转。
//
// 只读部署环境：短剧工作台不向浏览器暴露渠道密钥，也不在前端选择提取渠道。
func (s *Service) ConfigureShortplayExtractExecutorFromEnv() bool {
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("TEXT_BASE_URL")), "/")
	modelName := strings.TrimSpace(os.Getenv("TEXT_MODEL"))
	if baseURL == "" || modelName == "" {
		return false
	}
	apiKey := strings.TrimSpace(os.Getenv("TEXT_API_KEY"))
	timeout := shortplayExtractEnvDuration("TEXT_TIMEOUT_MS", 120*time.Second)
	client := &http.Client{Timeout: timeout}
	s.SetShortplayExtractExecutor(func(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
		return shortplayExtractChat(ctx, client, baseURL, apiKey, modelName, systemPrompt, userPrompt)
	})
	return true
}

// shortplayExtractChat 以 OpenAI 兼容的 chat/completions 形态取回整段文本。
// 提取是内部任务：不流式，直接读完整响应；错误里带响应摘要，收件箱能定位失败原因。
func shortplayExtractChat(ctx context.Context, client *http.Client, baseURL, apiKey, modelName, systemPrompt, userPrompt string) (string, error) {
	payload := map[string]any{
		"model": modelName,
		"messages": []any{
			map[string]any{"role": "system", "content": systemPrompt},
			map[string]any{"role": "user", "content": userPrompt},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if readErr != nil {
		return "", readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("提取模型请求失败：HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var decoded struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil || len(decoded.Choices) == 0 {
		return "", errors.New("提取模型响应无法解析")
	}
	content := strings.TrimSpace(decoded.Choices[0].Message.Content)
	if content == "" {
		return "", errors.New("提取模型返回空内容")
	}
	return content, nil
}

func shortplayExtractEnvDuration(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	ms, err := strconv.Atoi(raw)
	if err != nil || ms <= 0 {
		return fallback
	}
	return time.Duration(ms) * time.Millisecond
}
