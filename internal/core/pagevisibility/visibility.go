package pagevisibility

import (
	"github.com/perber/wiki/internal/core/auth"
	"github.com/perber/wiki/internal/core/tree"
)

// CanView reports whether a page is visible to the current request identity.
func CanView(node *tree.PageNode, user *auth.User, authDisabled bool) bool {
	return node != nil && (!IsInDraftSubtree(node) || CanViewDrafts(user, authDisabled))
}

func CanViewDrafts(user *auth.User, authDisabled bool) bool {
	return !authDisabled && user != nil && (user.HasRole(auth.RoleEditor) || user.HasRole(auth.RoleAdmin))
}

func CanManageDraft(node *tree.PageNode, user *auth.User, authDisabled bool) bool {
	return node != nil && CanViewDrafts(user, authDisabled)
}

// CanViewSubtree reports whether every node in the subtree is visible.
func CanViewSubtree(node *tree.PageNode, user *auth.User, authDisabled bool) bool {
	if !CanView(node, user, authDisabled) {
		return false
	}
	for _, child := range node.Children {
		if !CanViewSubtree(child, user, authDisabled) {
			return false
		}
	}
	return true
}

// IsInDraftSubtree reports whether the node or any ancestor owns draft state.
func IsInDraftSubtree(node *tree.PageNode) bool {
	for current := node; current != nil; current = current.Parent {
		if current.Draft {
			return true
		}
	}
	return false
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

// FilterPublishedPageIDs preserves the input order while dropping missing pages
// and pages in draft subtrees.
func FilterPublishedPageIDs(treeService *tree.TreeService, pageIDs []string) []string {
	if treeService == nil {
		return []string{}
	}
	return treeService.FilterPublishedPageIDs(pageIDs)
}

func AllPublishedPageIDs(treeService *tree.TreeService) []string {
	if treeService == nil {
		return []string{}
	}
	return treeService.PublishedPageIDs()
}
