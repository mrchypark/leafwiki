package pagevisibility

import (
	"github.com/perber/wiki/internal/core/auth"
	"github.com/perber/wiki/internal/core/tree"
)

func CanView(node *tree.PageNode, user *auth.User, authDisabled bool) bool {
	return node != nil && (!IsInDraftSubtree(node) || canAccessDraft(user, authDisabled))
}

func CanManageDraft(node *tree.PageNode, user *auth.User, authDisabled bool) bool {
	return node != nil && canAccessDraft(user, authDisabled)
}

func canAccessDraft(user *auth.User, authDisabled bool) bool {
	return !authDisabled && user != nil && (user.HasRole(auth.RoleEditor) || user.HasRole(auth.RoleAdmin))
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

func IsInDraftSubtree(node *tree.PageNode) bool {
	for current := node; current != nil; current = current.Parent {
		if current.Draft {
			return true
		}
	}
	return false
}

// FilterPublishedPageIDs keeps IDs that currently resolve outside a draft subtree.
func FilterPublishedPageIDs(treeService *tree.TreeService, pageIDs []string) []string {
	if treeService == nil {
		return []string{}
	}
	return treeService.FilterPublishedPageIDs(pageIDs)
}

// AllPublishedPageIDs returns every current non-root ID outside draft subtrees.
func AllPublishedPageIDs(treeService *tree.TreeService) []string {
	if treeService == nil {
		return []string{}
	}
	return treeService.PublishedPageIDs()
}

// Prune returns a detached visible copy. A hidden draft also hides its subtree.
func Prune(root *tree.PageNode, user *auth.User, authDisabled bool) *tree.PageNode {
	if root == nil {
		return nil
	}
	return cloneVisible(root, cloneAncestors(root.Parent), user, authDisabled)
}

func cloneAncestors(node *tree.PageNode) *tree.PageNode {
	if node == nil {
		return nil
	}
	clone := *node
	clone.Parent = cloneAncestors(node.Parent)
	clone.Children = nil
	return &clone
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
