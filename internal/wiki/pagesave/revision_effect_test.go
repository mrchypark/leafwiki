package pagesave

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/perber/wiki/internal/core/revision"
	"github.com/perber/wiki/internal/core/tree"
)

func revisionBySummary(t *testing.T, revisions []*revision.Revision, summary string) *revision.Revision {
	t.Helper()
	for _, rev := range revisions {
		if rev != nil && rev.Summary == summary {
			return rev
		}
	}
	t.Fatalf("revision with summary %q not found in %#v", summary, revisions)
	return nil
}

func TestRevisionSideEffect_RecordsOnlyPublishedHistory(t *testing.T) {
	dir := t.TempDir()
	treeService := tree.NewTreeService(dir)
	if err := treeService.LoadTree(); err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	sectionKind, pageKind := tree.NodeKindSection, tree.NodeKindPage
	sectionID, err := treeService.CreateNode("owner", nil, "Section", "section", &sectionKind)
	if err != nil {
		t.Fatalf("CreateNode(section): %v", err)
	}
	pageID, err := treeService.CreateNode("owner", sectionID, "Page", "page", &pageKind)
	if err != nil {
		t.Fatalf("CreateNode(page): %v", err)
	}
	draft := true
	if err := treeService.UpdateNodeWithDraft("owner", *sectionID, "Section", "section", nil, tree.VersionUnchecked, nil, nil, false, &draft); err != nil {
		t.Fatalf("mark section draft: %v", err)
	}
	privateContent := "draft-only secret"
	if err := treeService.UpdateNode("owner", *pageID, "Page", "page", &privateContent, tree.VersionUnchecked, nil, nil, false); err != nil {
		t.Fatalf("write draft content: %v", err)
	}

	service := revision.NewService(dir, treeService, nil, revision.ServiceOptions{})
	effect := NewRevisionSideEffect(service, nil)
	draftPage, err := treeService.GetPage(*pageID)
	if err != nil {
		t.Fatalf("GetPage(draft): %v", err)
	}
	effect.Apply(PageSaveEvent{Operation: PageOperationUpdate, UserID: "owner", After: draftPage, ContentChanged: true})
	if revisions, err := service.ListRevisions(*pageID); err != nil || len(revisions) != 0 {
		t.Fatalf("draft revisions = %#v, err = %v", revisions, err)
	}

	draft = false
	if err := treeService.UpdateNodeWithDraft("owner", *sectionID, "Section", "section", nil, tree.VersionUnchecked, nil, nil, false, &draft); err != nil {
		t.Fatalf("publish section: %v", err)
	}
	publicContent := "public content"
	if err := treeService.UpdateNode("owner", *pageID, "Page", "page", &publicContent, tree.VersionUnchecked, nil, nil, false); err != nil {
		t.Fatalf("write public content: %v", err)
	}
	publicPage, err := treeService.GetPage(*pageID)
	if err != nil {
		t.Fatalf("GetPage(public): %v", err)
	}
	effect.Apply(PageSaveEvent{Operation: PageOperationUpdate, UserID: "owner", After: publicPage, ContentChanged: true})
	revisions, err := service.ListRevisions(*pageID)
	if err != nil || len(revisions) != 1 {
		t.Fatalf("public revisions = %#v, err = %v", revisions, err)
	}
	snapshot, err := service.GetRevisionSnapshot(*pageID, revisions[0].ID)
	if err != nil {
		t.Fatalf("GetRevisionSnapshot: %v", err)
	}
	if snapshot.Content != publicContent {
		t.Fatalf("published snapshot content = %q", snapshot.Content)
	}
}

func TestRevisionSideEffect_PublishRecordsDirectPageOnce(t *testing.T) {
	dir := t.TempDir()
	treeService := tree.NewTreeService(dir)
	if err := treeService.LoadTree(); err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	kind := tree.NodeKindPage
	pageID, err := treeService.CreateNode("editor", nil, "Original", "original", &kind)
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	content := "unchanged content"
	if err := treeService.UpdateNode("editor", *pageID, "Original", "original", &content, tree.VersionUnchecked, nil, nil, false); err != nil {
		t.Fatalf("UpdateNode(public): %v", err)
	}

	service := revision.NewService(dir, treeService, nil, revision.ServiceOptions{})
	if _, created, err := service.RecordContentUpdate(*pageID, "editor", "original public"); err != nil || !created {
		t.Fatalf("RecordContentUpdate: created=%v err=%v", created, err)
	}
	draft := true
	if err := treeService.UpdateNodeWithDraft("editor", *pageID, "Original", "original", nil, tree.VersionUnchecked, nil, nil, false, &draft); err != nil {
		t.Fatalf("mark draft: %v", err)
	}
	if err := treeService.UpdateNode("editor", *pageID, "Published", "published", nil, tree.VersionUnchecked, nil, nil, false); err != nil {
		t.Fatalf("rename draft: %v", err)
	}
	draft = false
	if err := treeService.UpdateNodeWithDraft("editor", *pageID, "Published", "published", nil, tree.VersionUnchecked, nil, nil, false, &draft); err != nil {
		t.Fatalf("publish page: %v", err)
	}
	page, err := treeService.GetPage(*pageID)
	if err != nil {
		t.Fatalf("GetPage: %v", err)
	}

	effect := NewRevisionSideEffect(service, nil)
	effect.Apply(PageSaveEvent{
		Operation:      PageOperationUpdate,
		UserID:         "editor",
		After:          page,
		AffectedPages:  []*tree.Page{page},
		ContentChanged: true,
		SlugChanged:    true,
		TitleChanged:   true,
		DraftChanged:   true,
		Summary:        "published",
	})

	revisions, err := service.ListRevisions(*pageID)
	if err != nil || len(revisions) != 2 {
		t.Fatalf("published revisions = %#v, err = %v", revisions, err)
	}
	originalRevision := revisionBySummary(t, revisions, "original public")
	if originalRevision.Title != "Original" || originalRevision.Slug != "original" {
		t.Fatalf("original revision was mutated: %#v", originalRevision)
	}
	publishedRevision := revisionBySummary(t, revisions, "published")
	snapshot, err := service.GetRevisionSnapshot(*pageID, publishedRevision.ID)
	if err != nil {
		t.Fatalf("GetRevisionSnapshot: %v", err)
	}
	if snapshot.Content != content || snapshot.Revision.Title != "Published" || snapshot.Revision.Slug != "published" {
		t.Fatalf("published snapshot = %#v", snapshot)
	}
}

func TestRevisionSideEffect_PublishCapturesAssetsAddedWhileDraft(t *testing.T) {
	dir := t.TempDir()
	treeService := tree.NewTreeService(dir)
	if err := treeService.LoadTree(); err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	kind := tree.NodeKindPage
	pageID, err := treeService.CreateNode("editor", nil, "Page", "page", &kind)
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	service := revision.NewService(dir, treeService, nil, revision.ServiceOptions{})
	if _, created, err := service.RecordContentUpdate(*pageID, "editor", "original public"); err != nil || !created {
		t.Fatalf("RecordContentUpdate: created=%v err=%v", created, err)
	}
	draft := true
	if err := treeService.UpdateNodeWithDraft("editor", *pageID, "Page", "page", nil, tree.VersionUnchecked, nil, nil, false, &draft); err != nil {
		t.Fatalf("mark draft: %v", err)
	}
	assetDir := filepath.Join(dir, "assets", *pageID)
	if err := os.MkdirAll(assetDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(assetDir, "draft.txt"), []byte("draft asset"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	draft = false
	if err := treeService.UpdateNodeWithDraft("editor", *pageID, "Page", "page", nil, tree.VersionUnchecked, nil, nil, false, &draft); err != nil {
		t.Fatalf("publish page: %v", err)
	}
	page, err := treeService.GetPage(*pageID)
	if err != nil {
		t.Fatalf("GetPage: %v", err)
	}
	NewRevisionSideEffect(service, nil).Apply(PageSaveEvent{
		Operation: PageOperationUpdate, UserID: "editor", After: page,
		AffectedPages: []*tree.Page{page}, DraftChanged: true, Summary: "published",
	})

	revisions, err := service.ListRevisions(*pageID)
	if err != nil || len(revisions) != 2 {
		t.Fatalf("published revisions = %#v, err = %v", revisions, err)
	}
	originalRevision := revisionBySummary(t, revisions, "original public")
	oldSnapshot, err := service.GetRevisionSnapshot(*pageID, originalRevision.ID)
	if err != nil {
		t.Fatalf("GetRevisionSnapshot(old): %v", err)
	}
	if len(oldSnapshot.Assets) != 0 {
		t.Fatalf("original revision assets = %#v", oldSnapshot.Assets)
	}
	publishedRevision := revisionBySummary(t, revisions, "published")
	latest, err := service.GetRevisionSnapshot(*pageID, publishedRevision.ID)
	if err != nil {
		t.Fatalf("GetRevisionSnapshot(latest): %v", err)
	}
	if len(latest.Assets) != 1 || latest.Assets[0].Name != "draft.txt" {
		t.Fatalf("published assets = %#v", latest.Assets)
	}
}

func TestRevisionSideEffect_PublishBypassesCoalescing(t *testing.T) {
	dir := t.TempDir()
	treeService := tree.NewTreeService(dir)
	if err := treeService.LoadTree(); err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	kind := tree.NodeKindPage
	pageID, err := treeService.CreateNode("editor", nil, "Page", "page", &kind)
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	originalContent := "original public content"
	if err := treeService.UpdateNode("editor", *pageID, "Page", "page", &originalContent, tree.VersionUnchecked, nil, nil, false); err != nil {
		t.Fatalf("UpdateNode(public): %v", err)
	}
	service := revision.NewService(dir, treeService, nil, revision.ServiceOptions{CoalesceWindow: time.Hour})
	if _, created, err := service.RecordContentUpdate(*pageID, "editor", "original public"); err != nil || !created {
		t.Fatalf("RecordContentUpdate: created=%v err=%v", created, err)
	}
	draft := true
	if err := treeService.UpdateNodeWithDraft("editor", *pageID, "Page", "page", nil, tree.VersionUnchecked, nil, nil, false, &draft); err != nil {
		t.Fatalf("mark draft: %v", err)
	}
	publishedContent := "content prepared while draft"
	if err := treeService.UpdateNode("editor", *pageID, "Page", "page", &publishedContent, tree.VersionUnchecked, nil, nil, false); err != nil {
		t.Fatalf("UpdateNode(draft): %v", err)
	}
	draft = false
	if err := treeService.UpdateNodeWithDraft("editor", *pageID, "Page", "page", nil, tree.VersionUnchecked, nil, nil, false, &draft); err != nil {
		t.Fatalf("publish page: %v", err)
	}
	page, err := treeService.GetPage(*pageID)
	if err != nil {
		t.Fatalf("GetPage: %v", err)
	}
	NewRevisionSideEffect(service, nil).Apply(PageSaveEvent{
		Operation: PageOperationUpdate, UserID: "editor", After: page,
		AffectedPages: []*tree.Page{page}, ContentChanged: true, DraftChanged: true, Summary: "published",
	})

	revisions, err := service.ListRevisions(*pageID)
	if err != nil || len(revisions) != 2 {
		t.Fatalf("published revisions = %#v, err = %v", revisions, err)
	}
	originalRevision := revisionBySummary(t, revisions, "original public")
	oldSnapshot, err := service.GetRevisionSnapshot(*pageID, originalRevision.ID)
	if err != nil {
		t.Fatalf("GetRevisionSnapshot(old): %v", err)
	}
	if oldSnapshot.Content != originalContent {
		t.Fatalf("original revision content = %q", oldSnapshot.Content)
	}
	publishedRevision := revisionBySummary(t, revisions, "published")
	latest, err := service.GetRevisionSnapshot(*pageID, publishedRevision.ID)
	if err != nil {
		t.Fatalf("GetRevisionSnapshot(latest): %v", err)
	}
	if latest.Content != publishedContent {
		t.Fatalf("published revision content = %q", latest.Content)
	}
}

func TestRevisionSideEffect_PublishSectionRecordsVisibleDescendants(t *testing.T) {
	dir := t.TempDir()
	treeService := tree.NewTreeService(dir)
	if err := treeService.LoadTree(); err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	sectionKind := tree.NodeKindSection
	sectionID, err := treeService.CreateNodeWithDraft("editor", nil, "Draft Section", "draft-section", &sectionKind, true)
	if err != nil {
		t.Fatalf("CreateNodeWithDraft(section): %v", err)
	}
	pageKind := tree.NodeKindPage
	childID, err := treeService.CreateNode("editor", sectionID, "Child", "child", &pageKind)
	if err != nil {
		t.Fatalf("CreateNode(child): %v", err)
	}
	childContent := "child content prepared while ancestor was draft"
	if err := treeService.UpdateNode("editor", *childID, "Child", "child", &childContent, tree.VersionUnchecked, nil, nil, false); err != nil {
		t.Fatalf("UpdateNode(child): %v", err)
	}
	draft := false
	if err := treeService.UpdateNodeWithDraft("editor", *sectionID, "Draft Section", "draft-section", nil, tree.VersionUnchecked, nil, nil, false, &draft); err != nil {
		t.Fatalf("publish section: %v", err)
	}
	pages, errs := treeService.GetPages([]string{*sectionID, *childID})
	for i, getErr := range errs {
		if getErr != nil {
			t.Fatalf("GetPages[%d]: %v", i, getErr)
		}
	}

	service := revision.NewService(dir, treeService, nil, revision.ServiceOptions{})
	effect := NewRevisionSideEffect(service, nil)
	effect.Apply(PageSaveEvent{
		Operation:     PageOperationUpdate,
		UserID:        "editor",
		After:         pages[0],
		AffectedPages: pages,
		DraftChanged:  true,
		Summary:       "published",
	})

	for _, pageID := range []string{*sectionID, *childID} {
		revisions, err := service.ListRevisions(pageID)
		if err != nil || len(revisions) != 1 {
			t.Fatalf("page %s revisions = %#v, err = %v", pageID, revisions, err)
		}
	}
	childRevisions, err := service.ListRevisions(*childID)
	if err != nil {
		t.Fatalf("ListRevisions(child): %v", err)
	}
	snapshot, err := service.GetRevisionSnapshot(*childID, childRevisions[0].ID)
	if err != nil {
		t.Fatalf("GetRevisionSnapshot(child): %v", err)
	}
	if snapshot.Content != childContent {
		t.Fatalf("published child snapshot content = %q", snapshot.Content)
	}
}

func TestRevisionSideEffect_DoesNotMutateAffectedPagesBackingArray(t *testing.T) {
	dir := t.TempDir()
	treeService := tree.NewTreeService(dir)
	if err := treeService.LoadTree(); err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	kind := tree.NodeKindPage
	pageID, err := treeService.CreateNode("editor", nil, "Page", "page", &kind)
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	page, err := treeService.GetPage(*pageID)
	if err != nil {
		t.Fatalf("GetPage: %v", err)
	}
	sentinel := &tree.Page{PageNode: &tree.PageNode{ID: "sentinel"}}
	backing := []*tree.Page{page, sentinel}

	NewRevisionSideEffect(revision.NewService(dir, treeService, nil, revision.ServiceOptions{}), nil).Apply(PageSaveEvent{
		Operation: PageOperationUpdate, UserID: "editor", After: page,
		AffectedPages: backing[:1], DraftChanged: true,
	})

	if backing[1] != sentinel {
		t.Fatalf("AffectedPages backing array was mutated: got %#v", backing[1])
	}
}
