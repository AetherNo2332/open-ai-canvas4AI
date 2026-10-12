package desktop

import (
	"context"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/repository"
)

func BootstrapOwner(ctx context.Context, repo *repository.Repository) (identity.Principal, error) {
	_, user, err := repo.BootstrapDesktopOwner(ctx)
	if err != nil {
		return identity.Principal{}, err
	}
	return identity.Principal{ID: user.ID, OwnerID: user.ID, Role: "owner", Scopes: identity.OwnerScopes()}, nil
}
