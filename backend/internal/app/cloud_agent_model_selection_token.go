package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
)

// selectionId：把"模型选择"从三个可被模型改写的字段收敛成服务端签发的一个不透明凭证。
//
// 为什么要它：`logicalModelId` 与 `channelId`/`channelModelKey` 是"复制粘贴"契约——模型要
// 逐字抄对三个字段，抄错、混用、漏字段都会变成一次失败的工具调用（handoff 工作项 B 取证：
// 66 条 tool_failed 里 18 条是 selection 字段错误）。改成一个由服务端签发、模型只需原样回传的
// 字符串，把"抄写"退化面降到最低。
//
// 凭证里绑定签发用户：渠道模型可能是用户自己的，跨用户复用别人的 selectionId 必须无效。
// 签名用与匿名资源链接同一把服务端密钥（`settingsEncryptionKey`），不引入新的密钥管理。
// 旧的三字段契约保留兼容期（同时提供两种时按冲突拒绝）。

// cloudAgentSelection 是一次模型选择的服务端表示。
type cloudAgentSelection struct {
	LogicalModelID  string `json:"l,omitempty"`
	ChannelID       string `json:"c,omitempty"`
	ChannelModelKey string `json:"k,omitempty"`
}

type cloudAgentSelectionClaims struct {
	UserID string `json:"u"`
	cloudAgentSelection
}

const cloudAgentSelectionSignatureDomain = "cloud-agent-selection"

// issueCloudAgentSelectionID 为 model_list 的某一种选择签发凭证。
func (s *Service) issueCloudAgentSelectionID(userID string, selection cloudAgentSelection) (string, error) {
	if s == nil || strings.TrimSpace(userID) == "" {
		return "", BadAuthRequest("模型选择凭证缺少用户身份")
	}
	claims, err := json.Marshal(cloudAgentSelectionClaims{UserID: strings.TrimSpace(userID), cloudAgentSelection: selection})
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(claims)
	signature, err := s.signCloudAgentSelection(payload)
	if err != nil {
		return "", err
	}
	return payload + "." + signature, nil
}

// resolveCloudAgentSelectionID 校验并解开凭证：签名不符、被改写过、或不属于当前用户都无效。
func (s *Service) resolveCloudAgentSelectionID(userID, token string) (cloudAgentSelection, error) {
	payload, signature, ok := strings.Cut(strings.TrimSpace(token), ".")
	if !ok || payload == "" || signature == "" {
		return cloudAgentSelection{}, cloudAgentFieldError("selectionId", "invalid_value", "selectionId 格式无效：请原样复制 model_list 返回的 selectionId")
	}
	expected, err := s.signCloudAgentSelection(payload)
	if err != nil {
		return cloudAgentSelection{}, err
	}
	if !hmac.Equal([]byte(expected), []byte(signature)) {
		return cloudAgentSelection{}, cloudAgentFieldError("selectionId", "invalid_value", "selectionId 校验失败（被改写或不是本服务签发）：请重新调用 model_list 取新的 selectionId")
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return cloudAgentSelection{}, cloudAgentFieldError("selectionId", "invalid_value", "selectionId 内容无法解析：请重新调用 model_list")
	}
	var claims cloudAgentSelectionClaims
	if err := json.Unmarshal(raw, &claims); err != nil {
		return cloudAgentSelection{}, cloudAgentFieldError("selectionId", "invalid_value", "selectionId 内容无法解析：请重新调用 model_list")
	}
	if strings.TrimSpace(claims.UserID) != strings.TrimSpace(userID) {
		return cloudAgentSelection{}, cloudAgentFieldError("selectionId", "invalid_value", "selectionId 不属于当前用户：请用本账号重新调用 model_list")
	}
	if claims.LogicalModelID == "" && (claims.ChannelID == "" || claims.ChannelModelKey == "") {
		return cloudAgentSelection{}, cloudAgentFieldError("selectionId", "invalid_value", "selectionId 内容不完整：请重新调用 model_list")
	}
	return claims.cloudAgentSelection, nil
}

func (s *Service) signCloudAgentSelection(payload string) (string, error) {
	key, err := s.settingsEncryptionKey()
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(cloudAgentSelectionSignatureDomain + "\n" + payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}
