package pagevisibility

import (
	"github.com/perber/wiki/internal/core/auth"
	"github.com/perber/wiki/internal/core/tree"
)

func CanView(node *tree.PageNode, user *auth.User, authDisabled bool) bool {
	if node == nil {
		return false
	}
	if authDisabled {
		return true
	}
	for current := node; current != nil; current = current.Parent {
		if current.Draft && (user == nil || user.ID != current.Metadata.CreatorID && !user.HasRole(auth.RoleAdmin)) {
			return false
		}
	}
	return true
}

func CanManageDraft(node *tree.PageNode, user *auth.User, authDisabled bool) bool {
	return node != nil && !authDisabled && user != nil && (user.ID == node.Metadata.CreatorID || user.HasRole(auth.RoleAdmin))
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
	if treeService == nil {
		return visible
	}
	for _, pageID := range pageIDs {
		node, err := treeService.FindPageByID(pageID)
		if err == nil && node != nil && !IsInDraftSubtree(node) {
			visible = append(visible, pageID)
		}
	}
	return visible
}

// AllPublishedPageIDs returns every current non-root ID outside draft subtrees.
func AllPublishedPageIDs(treeService *tree.TreeService) []string {
	if treeService == nil {
		return []string{}
	}
	var pageIDs []string
	_ = treeService.WalkNodes(func(id string) error {
		pageIDs = append(pageIDs, id)
		return nil
	})
	return FilterPublishedPageIDs(treeService, pageIDs)
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
