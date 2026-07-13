package pagevisibility

import (
	"github.com/perber/wiki/internal/core/auth"
	"github.com/perber/wiki/internal/core/tree"
)

// CanView reports whether a page is visible to the current request identity.
func CanView(node *tree.PageNode, user *auth.User, authDisabled bool) bool {
	return node != nil && (!node.Draft || CanViewDrafts(user, authDisabled))
}

func CanViewDrafts(user *auth.User, authDisabled bool) bool {
	return !authDisabled && user != nil && (user.HasRole(auth.RoleEditor) || user.HasRole(auth.RoleAdmin))
}

// Prune returns a detached copy containing only nodes visible to the caller.
func Prune(root *tree.PageNode, user *auth.User, authDisabled bool) *tree.PageNode {
	return cloneVisible(root, cloneAncestors(root), user, authDisabled)
}

func cloneVisible(node, parent *tree.PageNode, user *auth.User, authDisabled bool) *tree.PageNode {
	if !CanView(node, user, authDisabled) {
		return nil
	}
	clone := *node
	clone.Parent = parent
	clone.Children = nil
	for _, child := range node.Children {
		if visible := cloneVisible(child, &clone, user, authDisabled); visible != nil {
			visible.Position = len(clone.Children)
			clone.Children = append(clone.Children, visible)
		}
	}
	return &clone
}

func cloneAncestors(node *tree.PageNode) *tree.PageNode {
	if node == nil || node.Parent == nil {
		return nil
	}
	parent := *node.Parent
	parent.Parent = cloneAncestors(node.Parent)
	parent.Children = nil
	return &parent
}

// FilterPublishedPageIDs preserves the input order while dropping missing and draft pages.
func FilterPublishedPageIDs(treeService *tree.TreeService, pageIDs []string) []string {
	published := make([]string, 0, len(pageIDs))
	if treeService == nil {
		return published
	}
	for _, id := range pageIDs {
		node, err := treeService.FindPageByID(id)
		if err == nil && node != nil && !node.Draft {
			published = append(published, id)
		}
	}
	return published
}

func AllPublishedPageIDs(treeService *tree.TreeService) []string {
	ids := []string{}
	if treeService == nil {
		return ids
	}
	_ = treeService.WalkNodes(func(id string) error {
		node, err := treeService.FindPageByID(id)
		if err == nil && node != nil && !node.Draft {
			ids = append(ids, id)
		}
		return nil
	})
	return ids
}
