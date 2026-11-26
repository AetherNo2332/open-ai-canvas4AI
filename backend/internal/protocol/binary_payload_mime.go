package protocol

import (
	"net/http"
	"strings"
)

// detectBinaryPayloadMime 识别同步二进制响应的类型：先按字节嗅探，再按协议声明的
// resultKind 兜底。标准库嗅探器不认识 MP3（没有 ID3 时也没有独立魔数），而 OpenAI
// 兼容语音接口默认直接返回 MP3 流；只靠嗅探会把它降级成 application/octet-stream，
// 后续媒体保存再按类型前缀拒绝。
func detectBinaryPayloadMime(resultKind string, body []byte) string {
	detected := strings.ToLower(strings.TrimSpace(strings.Split(http.DetectContentType(body), ";")[0]))
	if strings.HasPrefix(detected, "audio/") {
		return detected
	}
	if resultKind == "audio" && looksLikeMPEGAudio(body) {
		return "audio/mpeg"
	}
	return detected
}

// looksLikeMPEGAudio 按 ID3 头或 MPEG-1/2 帧同步认定 MPEG 音频，
// 与音频结果校验使用同一判定，避免把任意字节当成音频。
func looksLikeMPEGAudio(body []byte) bool {
	if len(body) >= 3 && body[0] == 'I' && body[1] == 'D' && body[2] == '3' {
		return true
	}
	if len(body) < 4 || body[0] != 0xff || body[1]&0xe0 != 0xe0 {
		return false
	}
	layer, bitrate, sampleRate := (body[1]>>1)&0x03, (body[2]>>4)&0x0f, (body[2]>>2)&0x03
	return layer != 0 && bitrate != 0x0f && sampleRate != 0x03
}
