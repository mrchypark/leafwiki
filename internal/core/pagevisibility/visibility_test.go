package pagevisibility

import (
	"testing"

	"github.com/perber/wiki/internal/core/auth"
	"github.com/perber/wiki/internal/core/tree"
)

func TestCanView_DraftIsLimitedToOwnerOrAdmin(t *testing.T) {
	node := &tree.PageNode{Draft: true, Metadata: tree.PageMetadata{CreatorID: "owner"}}
	if CanView(node, nil, false) {
		t.Fatal("anonymous user can view draft")
	}
	if CanView(node, &auth.User{ID: "other", Role: auth.RoleEditor}, false) {
		t.Fatal("other editor can view draft")
	}
	if !CanView(node, &auth.User{ID: "owner", Role: auth.RoleViewer}, false) {
		t.Fatal("owner cannot view draft")
	}
	if !CanView(node, &auth.User{ID: "admin", Role: auth.RoleAdmin}, false) {
		t.Fatal("admin cannot view draft")
	}
	if !CanView(node, nil, true) {
		t.Fatal("auth-disabled mode changed existing visibility semantics")
	}
}

func TestCanManageDraft_AuthDisabledRejectsToggling(t *testing.T) {
	node := &tree.PageNode{Metadata: tree.PageMetadata{CreatorID: "public-editor"}}
	user := &auth.User{ID: "public-editor", Role: auth.RoleEditor}
	if CanManageDraft(node, user, true) {
		t.Fatal("auth-disabled mode permits toggling draft")
	}
}

func TestCanView_NonDraftDescendantRequiresAccessToDraftAncestor(t *testing.T) {
	parent := &tree.PageNode{Draft: true, Metadata: tree.PageMetadata{CreatorID: "owner"}}
	child := &tree.PageNode{Parent: parent, Metadata: tree.PageMetadata{CreatorID: "other"}}
	if CanView(child, &auth.User{ID: "other", Role: auth.RoleEditor}, false) {
		t.Fatal("descendant bypassed its draft ancestor")
	}
	if !CanView(child, &auth.User{ID: "owner", Role: auth.RoleViewer}, false) {
		t.Fatal("draft ancestor owner cannot view descendant")
	}
}

func TestCanViewSubtree_RejectsVisibleAncestorWithHiddenDraftDescendant(t *testing.T) {
	parent := &tree.PageNode{}
	child := &tree.PageNode{Parent: parent, Draft: true, Metadata: tree.PageMetadata{CreatorID: "owner"}}
	parent.Children = []*tree.PageNode{child}
	if CanViewSubtree(parent, &auth.User{ID: "other", Role: auth.RoleEditor}, false) {
		t.Fatal("visible ancestor concealed a hidden draft descendant")
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
