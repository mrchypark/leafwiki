package tree

import "testing"

func TestTreeService_SnapshotsAreDetachedAndRetainRelevantRelationships(t *testing.T) {
	service := NewTreeService(t.TempDir())
	if err := service.LoadTree(); err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	sectionKind := NodeKindSection
	sectionID, err := service.CreateNodeWithDraft("editor", nil, "Draft Section", "draft-section", &sectionKind, true)
	if err != nil {
		t.Fatalf("CreateNodeWithDraft: %v", err)
	}
	pageKind := NodeKindPage
	pageID, err := service.CreateNode("editor", sectionID, "Page", "page", &pageKind)
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}

	treeSnapshot := service.SnapshotTree()
	if treeSnapshot == nil || len(treeSnapshot.Children) != 1 {
		t.Fatalf("SnapshotTree = %#v", treeSnapshot)
	}
	treeSnapshot.Children[0].Title = "changed snapshot"
	treeSnapshot.Children = nil

	section, err := service.FindPageByID(*sectionID)
	if err != nil {
		t.Fatalf("FindPageByID(section): %v", err)
	}
	if section.Title != "Draft Section" || len(service.GetTree().Children) != 1 {
		t.Fatalf("live tree changed through snapshot: section=%#v root=%#v", section, service.GetTree())
	}

	pageSnapshot, err := service.SnapshotPageNode(*pageID)
	if err != nil {
		t.Fatalf("SnapshotPageNode: %v", err)
	}
	if pageSnapshot.Parent == nil || !pageSnapshot.Parent.Draft {
		t.Fatalf("draft ancestor was not retained: %#v", pageSnapshot.Parent)
	}
	if got := pageSnapshot.CalculatePath(); got != "/draft-section/page" {
		t.Fatalf("CalculatePath = %q", got)
	}
	pageSnapshot.Title = "changed page snapshot"
	pageSnapshot.Parent.Draft = false

	page, err := service.FindPageByID(*pageID)
	if err != nil {
		t.Fatalf("FindPageByID(page): %v", err)
	}
	if page.Title != "Page" || !page.Parent.Draft {
		t.Fatalf("live page changed through snapshot: %#v", page)
	}
}
