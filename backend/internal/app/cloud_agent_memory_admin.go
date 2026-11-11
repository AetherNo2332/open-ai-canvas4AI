package app

// 管理员跨用户聚合留 app：跨用户列表与用户信息拼装是跨域编排职责；
// 单份文档渲染归 internal/agentmemory/document。

// AdminAgentMemoryDocuments 供管理员按用户查看记忆文件正文（只读，不替用户创建或修改）。
func (s *Service) AdminAgentMemoryDocuments(userID string, limit int) ([]AgentMemoryDocumentAdminView, error) {
	rows, err := s.repo.AdminAgentMemoryDocuments(userID, limit)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.UserID)
	}
	users, err := s.repo.UsersByIDs(ids)
	if err != nil {
		return nil, err
	}
	views := make([]AgentMemoryDocumentAdminView, 0, len(rows))
	for index := range rows {
		view, err := s.memoryDocuments().ViewOf(&rows[index])
		if err != nil {
			return nil, err
		}
		admin := AgentMemoryDocumentAdminView{UserID: rows[index].UserID, View: view}
		if user, ok := users[rows[index].UserID]; ok {
			admin.Username, admin.DisplayName = user.Username, user.DisplayName
		}
		views = append(views, admin)
	}
	return views, nil
}
