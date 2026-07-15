package pages

import (
	"context"

	"github.com/perber/wiki/internal/core/auth"
	"github.com/perber/wiki/internal/core/pagevisibility"
	"github.com/perber/wiki/internal/core/tree"
	"github.com/perber/wiki/internal/favorites"
)

type AddFavoriteInput struct {
	UserID       string
	PageID       string
	User         *auth.User
	AuthDisabled bool
}

// AddFavoriteUseCase favorites a page for a given user. Any authenticated
// user may favorite any page they can read — this is a personal bookmark,
// not an editorial action (unlike Pinned Pages).
type AddFavoriteUseCase struct {
	treeService *tree.TreeService
	store       *favorites.FavoritesStore
}

func NewAddFavoriteUseCase(treeService *tree.TreeService, store *favorites.FavoritesStore) *AddFavoriteUseCase {
	return &AddFavoriteUseCase{treeService: treeService, store: store}
}

func (uc *AddFavoriteUseCase) Execute(_ context.Context, in AddFavoriteInput) error {
	node, err := uc.treeService.SnapshotPageNode(in.PageID)
	if err != nil || !pagevisibility.CanView(node, in.User, in.AuthDisabled) {
		return tree.ErrPageNotFound
	}
	if _, err := uc.treeService.GetPage(in.PageID); err != nil {
		return err
	}
	return uc.store.Add(in.UserID, in.PageID)
}
