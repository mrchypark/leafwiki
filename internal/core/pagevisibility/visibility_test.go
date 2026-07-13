package pagevisibility

import (
	"testing"

	"github.com/perber/wiki/internal/core/auth"
	"github.com/perber/wiki/internal/core/tree"
)

func TestCanView_DraftIsVisibleOnlyToEditorsAndAdmins(t *testing.T) {
	node := &tree.PageNode{Draft: true, Metadata: tree.PageMetadata{CreatorID: "owner"}}
	tests := []struct {
		name string
		user *auth.User
		want bool
	}{
		{name: "anonymous"},
		{name: "viewer who created the page", user: &auth.User{ID: "owner", Role: auth.RoleViewer}},
		{name: "editor who did not create the page", user: &auth.User{ID: "other", Role: auth.RoleEditor}, want: true},
		{name: "admin", user: &auth.User{ID: "admin", Role: auth.RoleAdmin}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanView(node, tt.user, false); got != tt.want {
				t.Fatalf("CanView() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCanManageDraft_OnlyEditorsAndAdminsCanChangeDraftState(t *testing.T) {
	node := &tree.PageNode{Metadata: tree.PageMetadata{CreatorID: "owner"}}
	tests := []struct {
		name         string
		user         *auth.User
		authDisabled bool
		want         bool
	}{
		{name: "anonymous"},
		{name: "viewer who created the page", user: &auth.User{ID: "owner", Role: auth.RoleViewer}},
		{name: "editor", user: &auth.User{ID: "editor", Role: auth.RoleEditor}, want: true},
		{name: "admin", user: &auth.User{ID: "admin", Role: auth.RoleAdmin}, want: true},
		{name: "auth disabled editor", user: &auth.User{ID: "editor", Role: auth.RoleEditor}, authDisabled: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanManageDraft(node, tt.user, tt.authDisabled); got != tt.want {
				t.Fatalf("CanManageDraft() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCanView_NonDraftDescendantInheritsDraftAncestorVisibility(t *testing.T) {
	parent := &tree.PageNode{Draft: true, Metadata: tree.PageMetadata{CreatorID: "owner"}}
	child := &tree.PageNode{Parent: parent, Metadata: tree.PageMetadata{CreatorID: "other"}}
	if CanView(child, &auth.User{ID: "owner", Role: auth.RoleViewer}, false) {
		t.Fatal("viewer can access descendant through draft ancestor")
	}
	if !CanView(child, &auth.User{ID: "other", Role: auth.RoleEditor}, false) {
		t.Fatal("editor cannot access descendant through draft ancestor")
	}
}

func TestCanViewSubtree_RejectsVisibleAncestorWithHiddenDraftDescendant(t *testing.T) {
	parent := &tree.PageNode{}
	child := &tree.PageNode{Parent: parent, Draft: true, Metadata: tree.PageMetadata{CreatorID: "owner"}}
	parent.Children = []*tree.PageNode{child}
	if CanViewSubtree(parent, &auth.User{ID: "owner", Role: auth.RoleViewer}, false) {
		t.Fatal("visible ancestor concealed a hidden draft descendant")
	}
}

func TestCanView_AuthDisabledHidesDraftSubtreesButKeepsPublishedPagesVisible(t *testing.T) {
	published := &tree.PageNode{}
	draft := &tree.PageNode{Draft: true}
	draftChild := &tree.PageNode{Parent: draft}
	editor := &auth.User{Role: auth.RoleEditor}

	if !CanView(published, nil, true) {
		t.Fatal("auth-disabled mode hides published page")
	}
	if CanView(draft, editor, true) {
		t.Fatal("auth-disabled mode exposes draft page")
	}
	if CanView(draftChild, editor, true) {
		t.Fatal("auth-disabled mode exposes draft descendant")
	}
}

func TestIsInDraftSubtree_IncludesNonDraftDescendant(t *testing.T) {
	parent := &tree.PageNode{Draft: true}
	child := &tree.PageNode{Parent: parent}
	if !IsInDraftSubtree(child) {
		t.Fatal("non-draft descendant was not recognized as part of draft subtree")
	}
}

func TestPrune_DropsHiddenDraftSubtreeWithoutMutatingSource(t *testing.T) {
	root := &tree.PageNode{ID: "root", Children: []*tree.PageNode{
		{ID: "first", Position: 4},
		{ID: "draft", Position: 5, Draft: true, Metadata: tree.PageMetadata{CreatorID: "owner"}, Children: []*tree.PageNode{{ID: "child"}}},
		{ID: "last", Position: 9},
	}}
	for _, child := range root.Children {
		child.Parent = root
	}
	root.Children[1].Children[0].Parent = root.Children[1]

	got := Prune(root, nil, false)
	if got == root || len(got.Children) != 2 || got.Children[0].ID != "first" || got.Children[1].ID != "last" {
		t.Fatalf("unexpected pruned tree: %#v", got)
	}
	if len(root.Children) != 3 || len(root.Children[1].Children) != 1 {
		t.Fatal("source tree was mutated")
	}
	if got.Children[0].Parent != got {
		t.Fatal("cloned parent pointer was not repaired")
	}
	if got.Children[0].Position != 0 || got.Children[1].Position != 1 {
		t.Fatalf("visible positions were not compacted: %d, %d", got.Children[0].Position, got.Children[1].Position)
	}
}

func TestFilterPublishedPageIDs_DropsDraftSubtreesAndMissingPages(t *testing.T) {
	dir := t.TempDir()
	treeService := tree.NewTreeService(dir)
	if err := treeService.LoadTree(); err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	kind := tree.NodeKindPage
	publicID, err := treeService.CreateNode("owner", nil, "Public", "public", &kind)
	if err != nil {
		t.Fatalf("CreateNode public: %v", err)
	}
	draftKind := tree.NodeKindSection
	draftID, err := treeService.CreateNode("owner", nil, "Draft", "draft", &draftKind)
	if err != nil {
		t.Fatalf("CreateNode draft: %v", err)
	}
	draft, err := treeService.FindPageByID(*draftID)
	if err != nil {
		t.Fatalf("FindPageByID: %v", err)
	}
	draft.Draft = true
	draftChildID, err := treeService.CreateNode("owner", draftID, "Nested Published Bit", "nested-published-bit", &kind)
	if err != nil {
		t.Fatalf("CreateNode draft child: %v", err)
	}

	got := FilterPublishedPageIDs(treeService, []string{*draftID, *draftChildID, "missing", *publicID})
	if len(got) != 1 || got[0] != *publicID {
		t.Fatalf("published IDs = %v", got)
	}
	all := AllPublishedPageIDs(treeService)
	if len(all) != 1 || all[0] != *publicID {
		t.Fatalf("all published IDs = %v", all)
	}
}

func TestAllPublishedPageIDs_DraftRootHidesDescendants(t *testing.T) {
	treeService := tree.NewTreeService(t.TempDir())
	if err := treeService.LoadTree(); err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	kind := tree.NodeKindPage
	pageID, err := treeService.CreateNode("owner", nil, "Child", "child", &kind)
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	root := treeService.GetTree()
	root.Draft = true
	child, err := treeService.FindPageByID(*pageID)
	if err != nil {
		t.Fatalf("FindPageByID: %v", err)
	}
	if !IsInDraftSubtree(child) {
		t.Fatal("child does not inherit draft visibility from root")
	}

	pageIDs := AllPublishedPageIDs(treeService)
	if pageIDs == nil || len(pageIDs) != 0 {
		t.Fatalf("published IDs under draft root = %#v, want non-nil empty slice", pageIDs)
	}
}
