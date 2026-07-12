package pagesave

import (
	"testing"

	"github.com/perber/wiki/internal/core/revision"
	"github.com/perber/wiki/internal/core/tree"
)

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
