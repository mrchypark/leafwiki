package pagevisibility

import (
	"testing"

	"github.com/perber/wiki/internal/core/auth"
	"github.com/perber/wiki/internal/core/tree"
)

func TestCanView_DraftRequiresAuthenticatedEditorOrAdmin(t *testing.T) {
	draft := &tree.PageNode{Draft: true}
	for _, tc := range []struct {
		name         string
		user         *auth.User
		authDisabled bool
		want         bool
	}{
		{name: "editor", user: &auth.User{Role: auth.RoleEditor}, want: true},
		{name: "admin", user: &auth.User{Role: auth.RoleAdmin}, want: true},
		{name: "viewer", user: &auth.User{Role: auth.RoleViewer}},
		{name: "anonymous"},
		{name: "auth disabled", user: &auth.User{Role: auth.RoleEditor}, authDisabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := CanView(draft, tc.user, tc.authDisabled); got != tc.want {
				t.Fatalf("CanView() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPrune_RemovesDraftLeafWithoutMutatingTree(t *testing.T) {
	root := &tree.PageNode{ID: "root", Slug: "root"}
	public := &tree.PageNode{ID: "public", Slug: "public", Parent: root}
	draft := &tree.PageNode{ID: "draft", Slug: "draft", Draft: true, Parent: root}
	root.Children = []*tree.PageNode{public, draft}

	visible := Prune(root, nil, false)
	if len(visible.Children) != 1 || visible.Children[0].ID != public.ID {
		t.Fatalf("visible children = %#v", visible.Children)
	}
	if len(root.Children) != 2 || visible == root || visible.Children[0] == public {
		t.Fatal("Prune mutated or reused the live tree")
	}
}
