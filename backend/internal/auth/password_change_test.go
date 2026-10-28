package auth

import (
	"errors"
	"testing"
	"time"

	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

func TestChangePasswordKeepsCurrentSessionAndRevokesOthers(t *testing.T) {
	svc, db := newPasswordResetTestService(t)
	user := createPasswordUser(t, db, "user-change", "change@example.com", "old-password")
	sessions := []model.AuthSession{
		{ID: "session-current", UserID: user.ID, TokenHash: "current", ExpiresAt: time.Now().Add(time.Hour)},
		{ID: "session-other", UserID: user.ID, TokenHash: "other", ExpiresAt: time.Now().Add(time.Hour)},
	}
	if err := db.Create(&sessions).Error; err != nil {
		t.Fatal(err)
	}

	if err := svc.ChangePassword(user, ChangePasswordRequest{CurrentPassword: "old-password", NewPassword: "new-password"}, "session-current"); err != nil {
		t.Fatal(err)
	}

	var updated model.User
	if err := db.First(&updated, "id = ?", user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if bcrypt.CompareHashAndPassword([]byte(updated.PasswordHash), []byte("new-password")) != nil {
		t.Fatal("new password hash does not match")
	}
	if bcrypt.CompareHashAndPassword([]byte(updated.PasswordHash), []byte("old-password")) == nil {
		t.Fatal("old password still matches")
	}
	var sessionIDs []string
	if err := db.Model(&model.AuthSession{}).Where("user_id = ?", user.ID).Pluck("id", &sessionIDs).Error; err != nil {
		t.Fatal(err)
	}
	if len(sessionIDs) != 1 || sessionIDs[0] != "session-current" {
		t.Fatalf("remaining sessions = %v, want [session-current]", sessionIDs)
	}
}

func TestChangePasswordRejectsWrongCurrentPassword(t *testing.T) {
	svc, db := newPasswordResetTestService(t)
	user := createPasswordUser(t, db, "user-wrong", "wrong@example.com", "old-password")

	err := svc.ChangePassword(user, ChangePasswordRequest{CurrentPassword: "not-the-password", NewPassword: "new-password"}, "session-1")
	if !isAppErrorMessage(err, 400, "当前密码不正确") {
		t.Fatalf("error = %v", err)
	}
	assertPasswordUnchanged(t, db, user.ID, "old-password")
}

func TestChangePasswordValidatesNewPassword(t *testing.T) {
	svc, db := newPasswordResetTestService(t)
	user := createPasswordUser(t, db, "user-rules", "rules@example.com", "old-password")

	if err := svc.ChangePassword(user, ChangePasswordRequest{CurrentPassword: "old-password", NewPassword: "short"}, "session-1"); !isAppErrorMessage(err, 400, "密码至少 8 位") {
		t.Fatalf("short password error = %v", err)
	}
	if err := svc.ChangePassword(user, ChangePasswordRequest{CurrentPassword: "old-password", NewPassword: "old-password"}, "session-1"); !isAppErrorMessage(err, 400, "新密码不能与当前密码相同") {
		t.Fatalf("same password error = %v", err)
	}
	assertPasswordUnchanged(t, db, user.ID, "old-password")
}

func TestChangePasswordRejectsAccountsWithoutLocalPassword(t *testing.T) {
	svc, db := newPasswordResetTestService(t)
	// 第三方登录创建的账号没有本地密码，只能走邮箱验证码找回。
	oauthUser := model.User{ID: "oauth-user", Username: "oauth-user", Email: "oauth@example.com", DisplayName: "OAuth", Role: model.UserRoleUser, Status: model.UserStatusActive}
	if err := db.Create(&oauthUser).Error; err != nil {
		t.Fatal(err)
	}

	err := svc.ChangePassword(&oauthUser, ChangePasswordRequest{CurrentPassword: "whatever", NewPassword: "new-password"}, "session-1")
	if !isAppErrorMessage(err, 400, "当前账号没有密码，请使用邮箱验证码重置密码后再修改") {
		t.Fatalf("error = %v", err)
	}
}

func TestChangePasswordRejectsDisabledAccount(t *testing.T) {
	svc, db := newPasswordResetTestService(t)
	hash, err := HashPassword("old-password")
	if err != nil {
		t.Fatal(err)
	}
	disabled := model.User{ID: "disabled-user", Username: "disabled-user", Email: "disabled@example.com", DisplayName: "Disabled", Role: model.UserRoleUser, Status: model.UserStatusDisabled, PasswordHash: hash}
	if err := db.Create(&disabled).Error; err != nil {
		t.Fatal(err)
	}

	err = svc.ChangePassword(&disabled, ChangePasswordRequest{CurrentPassword: "old-password", NewPassword: "new-password"}, "session-1")
	if !isAppErrorMessage(err, 403, "该账号已被禁用") {
		t.Fatalf("error = %v", err)
	}
	assertPasswordUnchanged(t, db, disabled.ID, "old-password")
}

func createPasswordUser(t *testing.T, db *gorm.DB, id string, email string, password string) *model.User {
	t.Helper()
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	user := model.User{ID: id, Username: id, Email: email, DisplayName: id, Role: model.UserRoleUser, Status: model.UserStatusActive, PasswordHash: hash}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	return &user
}

func assertPasswordUnchanged(t *testing.T, db *gorm.DB, userID string, password string) {
	t.Helper()
	var user model.User
	if err := db.First(&user, "id = ?", userID).Error; err != nil {
		t.Fatal(err)
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		t.Fatal("password hash changed unexpectedly")
	}
}

func isAppErrorMessage(err error, status int, message string) bool {
	var appErr *kernel.AppError
	return errors.As(err, &appErr) && appErr.Status == status && appErr.Message == message
}
