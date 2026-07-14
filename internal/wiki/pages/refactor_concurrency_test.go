package pages

import (
	"errors"
	"testing"

	"github.com/perber/wiki/internal/core/tree"
)

func TestContentRewriteUpdate_RejectsStaleLoadedPageWithoutOverwritingConcurrentEdit(t *testing.T) {
	service := tree.NewTreeService(t.TempDir())
	if err := service.LoadTree(); err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	kind := tree.NodeKindPage
	pageID, err := service.CreateNode("editor", nil, "Page", "page", &kind)
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	stale, err := service.GetPage(*pageID)
	if err != nil {
		t.Fatalf("GetPage stale snapshot: %v", err)
	}
	concurrentContent := "concurrent editor content"
	if err := service.UpdateNode("concurrent-editor", stale.ID, stale.Title, stale.Slug, &concurrentContent, stale.Version(), nil, nil, false); err != nil {
		t.Fatalf("concurrent UpdateNode: %v", err)
	}

	errs := service.BulkUpdateContent("refactor", []tree.BulkContentUpdate{
		contentRewriteUpdate(stale, "stale rewrite"),
	})
	if len(errs) != 1 || !errors.Is(errs[0], tree.ErrVersionConflict) {
		t.Fatalf("BulkUpdateContent error = %v, want ErrVersionConflict", errs)
	}
	current, err := service.GetPage(*pageID)
	if err != nil {
		t.Fatalf("GetPage current: %v", err)
	}
	if current.Content != concurrentContent {
		t.Fatalf("current content = %q, want concurrent edit %q", current.Content, concurrentContent)
	}
}
