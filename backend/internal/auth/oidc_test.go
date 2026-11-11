package auth

import (
	"errors"
	"net/http"
	"testing"

	"yingce/backend/internal/model"
)

func TestOIDCPrivateSignupEnforcesAgreementAndIdentityOwnership(t *testing.T) {
	for _, tc := range []struct {
		name               string
		accepted, admitted bool
		wantStatus         int
	}{
		{name: "closed registration", accepted: true, wantStatus: http.StatusForbidden},
		{name: "group without agreement", admitted: true, wantStatus: http.StatusBadRequest},
		{name: "group and agreement", accepted: true, admitted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, db := newRegistrationTestService(t)
			owner := model.User{ID: "email-owner", Username: "email-owner", Email: "same@example.com", Role: model.UserRoleAdmin, Status: model.UserStatusActive}
			if err := db.Create(&owner).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&model.SystemSetting{Key: registrationSettingKey, ValueJSON: `{"enabled":false}`}).Error; err != nil {
				t.Fatal(err)
			}
			profile := oauthProfile{provider: oidcProvider, subject: "issuer-scoped-subject", username: "oidc-user", name: "OIDC User", email: owner.Email}
			result, err := svc.finishOAuthLogin(&model.OAuthState{AcceptedTerms: tc.accepted, NextPath: "/create"}, NewLoginLog(oidcProvider, "login", LoginContext{}), profile, tc.admitted)
			if tc.wantStatus != 0 {
				var authErr *AuthError
				if !errors.As(err, &authErr) || authErr.Status != tc.wantStatus {
					t.Fatalf("signup error=%v, want status=%d", err, tc.wantStatus)
				}
				if count, err := svc.repo.UserCount(); err != nil || count != 1 {
					t.Fatalf("rejected signup created user: count=%d error=%v", count, err)
				}
				return
			}
			if err != nil || result == nil || result.Session.Session == "" || result.Next != "/create" {
				t.Fatalf("signup result=%+v error=%v", result, err)
			}
			identity, err := svc.repo.UserIdentity(oidcProvider, profile.subject)
			if err != nil || identity.UserID == owner.ID {
				t.Fatalf("OIDC identity reused email owner: identity=%+v error=%v", identity, err)
			}
			user, err := svc.repo.User(identity.UserID)
			if err != nil || user.Role != model.UserRoleUser || user.Email != "" {
				t.Fatalf("unexpected OIDC user: user=%+v error=%v", user, err)
			}
		})
	}
}
