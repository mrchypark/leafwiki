package pages

import (
	"strings"

	"github.com/perber/wiki/internal/core/revision"
	"github.com/perber/wiki/internal/core/tree"
)

// sanitizeClientVersion rejects the internal VersionUnchecked sentinel so
// external callers cannot bypass optimistic locking by sending the sentinel value.
// Treated as "no version provided" — produces ErrVersionRequired for versioned nodes.
func sanitizeClientVersion(v string) string {
	if v == tree.VersionUnchecked {
		return ""
	}
	return v
}

// collectSubtreeIDs returns all page IDs within a subtree (excluding "root").
func collectSubtreeIDs(node *tree.PageNode) []string {
	ids, _ := collectSubtreeIDsAndTitles(node)
	return ids
}

func collectSubtreeIDsAndTitles(node *tree.PageNode) ([]string, []string) {
	var ids []string
	var titles []string
	seenTitles := make(map[string]struct{})
	var walk func(n *tree.PageNode)
	walk = func(n *tree.PageNode) {
		if n == nil {
			return
		}
		if n.ID != "root" {
			ids = append(ids, n.ID)
			key := strings.ToLower(strings.TrimSpace(n.Title))
			if key != "" {
				if _, ok := seenTitles[key]; !ok {
					seenTitles[key] = struct{}{}
					titles = append(titles, n.Title)
				}
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(node)
	return ids, titles
}

// deleteRevisionData removes all revision data for a list of page IDs.
func deleteRevisionData(svc *revision.Service, pageIDs []string) error {
	if svc == nil {
		return nil
	}
	for _, id := range pageIDs {
		if err := svc.DeletePageData(id); err != nil {
			return err
		}
	}
	return nil
}
