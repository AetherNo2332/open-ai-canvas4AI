package handler

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"yingce/backend/internal/service"
)

// agentRunEventQueryLimit 是 eventLimit 允许的最大值，与 app 侧的增量上限一致：
// 请求更大的页没有意义（单次响应体积），直接拒绝比静默截断更容易被发现。
const agentRunEventQueryLimit = 500

func agentConversationLimit(c *gin.Context) (int, error) {
	raw, present := c.GetQuery("limit")
	if !present {
		return 100, nil
	}
	limit, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || limit < 1 || limit > 100 {
		return 0, errors.New("limit 必须是 1–100 之间的整数")
	}
	return limit, nil
}

type agentRunViewQuery struct {
	SinceSeq   string `form:"sinceSeq"`
	EventLimit string `form:"eventLimit"`
	FromStart  string `form:"fromStart"`
}

func agentRunViewOptions(c *gin.Context) (service.CloudAgentRunViewOptions, error) {
	options := service.CloudAgentRunViewOptions{}
	if raw, present := c.GetQuery("fromStart"); present {
		value, err := strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			return options, errors.New("fromStart 必须是布尔值")
		}
		options.FromStart = value
	}
	var query agentRunViewQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		return options, err
	}
	for _, item := range []struct {
		name  string
		raw   string
		value *int
		min   int
		max   int
	}{
		{name: "sinceSeq", raw: query.SinceSeq, value: &options.SinceSeq, min: 0, max: math.MaxInt32},
		{name: "eventLimit", raw: query.EventLimit, value: &options.EventLimit, min: 1, max: agentRunEventQueryLimit},
	} {
		raw := strings.TrimSpace(item.raw)
		if raw == "" {
			continue
		}
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < item.min || parsed > item.max {
			return options, fmt.Errorf("%s 必须是 %d–%d 之间的整数", item.name, item.min, item.max)
		}
		*item.value = parsed
	}
	return options, nil
}
