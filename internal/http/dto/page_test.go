package dto

import (
	"testing"

	"github.com/perber/wiki/internal/core/pagevisibility"
	"github.com/perber/wiki/internal/core/tree"
)

func TestToAPIPageMappers_PreserveNestedPageAndNodePaths(t *testing.T) {
	root := &tree.PageNode{ID: "root", Slug: "root"}
	a := &tree.PageNode{ID: "a", Slug: "a", Parent: root}
	b := &tree.PageNode{ID: "b", Slug: "b", Parent: a}
	c := &tree.PageNode{ID: "c", Slug: "c", Parent: b}
	b.Children = []*tree.PageNode{c}

	input := &tree.Page{PageNode: pagevisibility.Prune(b, nil, false)}
	tests := []struct {
		name    string
		mapPage func(*tree.Page) *Page
	}{
		{name: "plain", mapPage: func(page *tree.Page) *Page { return ToAPIPage(page, nil) }},
		{name: "depth limited", mapPage: func(page *tree.Page) *Page { return ToAPIPageWithDepth(page, nil, 1) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page := tt.mapPage(input)
			if page.Path != "a/b" || page.Node.Path != "a/b" {
				t.Fatalf("page paths = Page.Path %q, Node.Path %q", page.Path, page.Node.Path)
			}
			if len(page.Children) != 1 || page.Children[0].Path != "a/b/c" {
				t.Fatalf("child paths = %#v", page.Children)
			}
		})
	}
}

func TestToAPINode_ReportsDirectAndInheritedDraftSeparately(t *testing.T) {
	root := &tree.PageNode{ID: "root", Slug: "root"}
	draftParent := &tree.PageNode{ID: "parent", Slug: "parent", Draft: true, Parent: root}
	inheritedChild := &tree.PageNode{ID: "child", Slug: "child", Parent: draftParent}
	directChild := &tree.PageNode{ID: "direct", Slug: "direct", Draft: true, Parent: inheritedChild}

	inherited := ToAPINode(inheritedChild, "parent", nil)
	if inherited.Draft || !inherited.EffectiveDraft {
		t.Fatalf("inherited draft flags = draft %v, effectiveDraft %v", inherited.Draft, inherited.EffectiveDraft)
	}

	direct := ToAPINode(directChild, "parent/child", nil)
	if !direct.Draft || !direct.EffectiveDraft {
		t.Fatalf("direct draft flags = draft %v, effectiveDraft %v", direct.Draft, direct.EffectiveDraft)
	}
}
