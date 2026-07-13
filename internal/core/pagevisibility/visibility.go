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
	visible := make([]string, 0, len(pageIDs))
	published := make(map[string]struct{})
	for _, pageID := range AllPublishedPageIDs(treeService) {
		published[pageID] = struct{}{}
	}
	for _, pageID := range pageIDs {
		if _, ok := published[pageID]; ok {
			visible = append(visible, pageID)
		}
	}
	return visible
}

// AllPublishedPageIDs returns every current non-root ID outside draft subtrees.
func AllPublishedPageIDs(treeService *tree.TreeService) []string {
	pageIDs := []string{}
	if treeService == nil {
		return pageIDs
	}
	root := treeService.SnapshotTree()
	if root == nil || root.Draft {
		return pageIDs
	}

	var collect func(*tree.PageNode)
	collect = func(node *tree.PageNode) {
		if node == nil || node.Draft {
			return
		}
		pageIDs = append(pageIDs, node.ID)
		for _, child := range node.Children {
			collect(child)
		}
	}
	for _, child := range root.Children {
		collect(child)
	}
	return pageIDs
}

// Prune returns a detached visible copy. A hidden draft also hides its subtree.
func Prune(root *tree.PageNode, user *auth.User, authDisabled bool) *tree.PageNode {
	return cloneVisible(root, nil, user, authDisabled)
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
