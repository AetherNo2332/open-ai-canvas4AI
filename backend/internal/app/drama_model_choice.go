package app

// requireDramaModelOption 只接受当前目录中可由服务端执行的模型，防止提交任意渠道标识。
func (s *Service) requireDramaModelOption(userID string, capability string, choice DramaModelChoice) error {
	options, unavailable, err := s.dramaModelOptions(userID, capability)
	if err != nil {
		return err
	}
	for _, item := range unavailable {
		if dramaSameChoice(item.Choice, choice) {
			return BadAuthRequest("所选模型不能用于短剧：" + item.Reason)
		}
	}
	for _, option := range options {
		if option.Choice.LogicalModelID != "" && option.Choice.LogicalModelID == choice.LogicalModelID {
			return nil
		}
		if option.Choice.ChannelID != "" && option.Choice.ChannelID == choice.ChannelID && option.Choice.ModelKey == choice.ModelKey {
			return nil
		}
	}
	return BadAuthRequest("所选" + map[string]string{"text": "文本", "image": "图片", "video": "视频"}[capability] + "模型不可用，请重新选择")
}

func dramaSameChoice(a, b DramaModelChoice) bool {
	if a.LogicalModelID != "" || b.LogicalModelID != "" {
		return a.LogicalModelID == b.LogicalModelID
	}
	return a.ChannelID == b.ChannelID && a.ModelKey == b.ModelKey
}
