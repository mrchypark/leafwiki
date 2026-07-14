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

func TestCanView_NonDraftChildInheritsDraftAncestor(t *testing.T) {
	parent := &tree.PageNode{Draft: true}
	child := &tree.PageNode{Parent: parent}

	if CanView(child, nil, false) {
		t.Fatal("anonymous user can view a child of a draft ancestor")
	}
	if !CanView(child, &auth.User{Role: auth.RoleEditor}, false) {
		t.Fatal("editor cannot view a child of a draft ancestor")
	}
}

func TestPrune_RemovesEntireDraftSubtreeWithoutMutatingTree(t *testing.T) {
	root := &tree.PageNode{ID: "root", Slug: "root"}
	draft := &tree.PageNode{ID: "draft", Slug: "draft", Draft: true, Parent: root}
	child := &tree.PageNode{ID: "child", Slug: "child", Parent: draft}
	draft.Children = []*tree.PageNode{child}
	root.Children = []*tree.PageNode{draft}

	visible := Prune(root, nil, false)
	if visible == nil || len(visible.Children) != 0 {
		t.Fatalf("visible tree retained draft subtree: %#v", visible)
	}
	if len(root.Children) != 1 || len(draft.Children) != 1 {
		t.Fatal("Prune mutated the live tree")
	}
}

func TestFilterPublishedPageIDs_RemovesDraftDescendants(t *testing.T) {
	svc := tree.NewTreeService(t.TempDir())
	if err := svc.LoadTree(); err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	sectionKind := tree.NodeKindSection
	pageKind := tree.NodeKindPage
	draftID, err := svc.CreateNodeWithDraft("editor", nil, "Draft", "draft", &sectionKind, true)
	if err != nil {
		t.Fatalf("CreateNodeWithDraft: %v", err)
	}
	childID, err := svc.CreateNode("editor", draftID, "Child", "child", &pageKind)
	if err != nil {
		t.Fatalf("CreateNode child: %v", err)
	}
	publicID, err := svc.CreateNode("editor", nil, "Public", "public", &pageKind)
	if err != nil {
		t.Fatalf("CreateNode public: %v", err)
	}

	got := FilterPublishedPageIDs(svc, []string{*draftID, *childID, *publicID})
	if len(got) != 1 || got[0] != *publicID {
		t.Fatalf("published IDs = %v", got)
	}
}
