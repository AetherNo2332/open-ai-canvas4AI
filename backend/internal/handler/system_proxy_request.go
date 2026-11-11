package handler

import (
	"bytes"
	"github.com/gin-gonic/gin"
	"net/http"
	"net/url"
	"yingce/backend/internal/model"
	"yingce/backend/internal/service"
)

func buildSystemProxyRequest(c *gin.Context, svc *service.Service, channel *model.ModelChannel, protocol model.ChannelInterfaceType, target string, channelHeaders []service.OutboundHeader, payload []byte) (*http.Request, error) {
	upstreamReq, err := http.NewRequestWithContext(c.Request.Context(), c.Request.Method, target, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	if contentType := c.GetHeader("Content-Type"); contentType != "" {
		upstreamReq.Header.Set("Content-Type", contentType)
	}
	if accept := c.GetHeader("Accept"); accept != "" {
		upstreamReq.Header.Set("Accept", accept)
	}
	service.ApplyOutboundHeaders(upstreamReq, channelHeaders)
	service.ApplyDefaultOutboundHeaders(upstreamReq)
	// 鉴权按插件声明写（bearer / header / anthropic / google-api-key / 签名等），未声明时沿用内置写法。
	if err := svc.ApplySystemProxyAuth(upstreamReq, channel, protocol); err != nil {
		return nil, err
	}
	return upstreamReq, nil
}

func systemProxyTargetURL(baseURL, path string, protocol model.ChannelInterfaceType, query url.Values) string {
	for _, key := range []string{"key", "api_key", "access_token", "token"} {
		query.Del(key)
	}
	target := service.ChannelAPIURLForProtocol(baseURL, path, protocol)
	if encodedQuery := query.Encode(); encodedQuery != "" {
		target += "?" + encodedQuery
	}
	return target
}
