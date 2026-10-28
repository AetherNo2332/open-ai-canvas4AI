package auth

import (
	"errors"
	"strings"
	"time"

	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"

	"gorm.io/gorm"
)

// ChangePasswordRequest 是登录用户自助改密的入参。
// CurrentPassword 用于证明除会话 Cookie 之外仍持有密码，避免会话被盗后直接改密接管账号。
type ChangePasswordRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

// ChangePassword 校验当前密码后写入新密码，并让当前会话之外的登录会话全部失效。
// 第三方登录创建的账号没有本地密码，只能走邮箱验证码找回流程。
func (s *Service) ChangePassword(user *model.User, req ChangePasswordRequest, keepSessionID string) error {
	if user == nil {
		return kernel.Unauthorized("请先登录")
	}
	if err := ValidatePassword(req.NewPassword); err != nil {
		return err
	}
	current, err := s.repo.User(user.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return kernel.Unauthorized("登录状态已失效")
		}
		return err
	}
	if current.Status != model.UserStatusActive {
		return kernel.Forbidden("该账号已被禁用")
	}
	if strings.TrimSpace(current.PasswordHash) == "" {
		return kernel.BadAuthRequest("当前账号没有密码，请使用邮箱验证码重置密码后再修改")
	}
	if !verifyPassword(req.CurrentPassword, current.PasswordHash) {
		return kernel.BadAuthRequest("当前密码不正确")
	}
	if verifyPassword(req.NewPassword, current.PasswordHash) {
		return kernel.BadAuthRequest("新密码不能与当前密码相同")
	}
	passwordHash, err := HashPassword(req.NewPassword)
	if err != nil {
		return err
	}
	if err := s.repo.ChangeUserPassword(current.ID, passwordHash, keepSessionID, time.Now()); err != nil {
		if errors.Is(err, repository.ErrPasswordChangeRejected) {
			return kernel.Unauthorized("登录状态已失效，请重新登录")
		}
		return err
	}
	return nil
}
