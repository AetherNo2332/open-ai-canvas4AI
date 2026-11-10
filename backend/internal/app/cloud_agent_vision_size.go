package app

func validateCloudAgentImageResourceSize(size, limit int64) error {
	if size < 0 {
		return BadAuthRequest("图片资源大小信息无效")
	}
	// 模型单图上限在发送时压缩后核（hydration）；这里只拒绝连读入内存都做不到的图。
	if textModelImageRejectable(limit) && size > textModelImageCompressionMaxInput {
		return BadAuthRequest(textModelImageOversizedMessage())
	}
	return nil
}
