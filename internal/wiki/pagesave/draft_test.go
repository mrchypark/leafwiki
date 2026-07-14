package pagesave

import (
	"testing"

	"github.com/perber/wiki/internal/core/tree"
)

func setDraftForTest(t *testing.T, treeService *tree.TreeService, page *tree.Page, draft bool) *tree.Page {
	t.Helper()
	if err := treeService.UpdateNodeWithDraft("editor", page.ID, page.Title, page.Slug, nil, tree.VersionUnchecked, nil, nil, false, &draft); err != nil {
		t.Fatalf("UpdateNodeWithDraft: %v", err)
	}
	updated, err := treeService.GetPage(page.ID)
	if err != nil {
		t.Fatalf("GetPage: %v", err)
	}
	return updated
}
