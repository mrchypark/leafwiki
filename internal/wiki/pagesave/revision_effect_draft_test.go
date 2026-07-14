package pagesave

import (
	"testing"

	"github.com/perber/wiki/internal/core/revision"
	"github.com/perber/wiki/internal/core/tree"
)

func TestRevisionSideEffect_RecordsFirstRevisionWhenDraftIsPublished(t *testing.T) {
	dir := t.TempDir()
	treeService := tree.NewTreeService(dir)
	if err := treeService.LoadTree(); err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	kind := tree.NodeKindPage
	id, err := treeService.CreateNodeWithDraft("editor", nil, "Draft", "draft", &kind, true)
	if err != nil {
		t.Fatalf("CreateNodeWithDraft: %v", err)
	}
	draft, err := treeService.GetPage(*id)
	if err != nil {
		t.Fatalf("GetPage draft: %v", err)
	}
	revisions := revision.NewService(dir, treeService, nil, revision.ServiceOptions{})
	effect := NewRevisionSideEffect(revisions, nil, nil)
	effect.Apply(PageSaveEvent{Operation: PageOperationCreate, UserID: "editor", After: draft})
	if latest, err := revisions.GetLatestRevision(*id); err != nil || latest != nil {
		t.Fatalf("draft latest revision = %#v, err = %v", latest, err)
	}

	beforeNode := *draft.PageNode
	before := &tree.Page{PageNode: &beforeNode, Content: draft.Content}
	if err := treeService.SetDraft("editor", *id, false, draft.Version()); err != nil {
		t.Fatalf("SetDraft: %v", err)
	}
	published, err := treeService.GetPage(*id)
	if err != nil {
		t.Fatalf("GetPage published: %v", err)
	}
	effect.Apply(PageSaveEvent{Operation: PageOperationUpdate, UserID: "editor", Before: before, After: published})
	latest, err := revisions.GetLatestRevision(*id)
	if err != nil || latest == nil || latest.Summary != "page published" || latest.Type != revision.RevisionTypeContentUpdate {
		t.Fatalf("published latest revision = %#v, err = %v", latest, err)
	}
	all, err := revisions.ListRevisions(*id)
	if err != nil || len(all) != 1 {
		t.Fatalf("published revisions = %#v, err = %v", all, err)
	}
}
