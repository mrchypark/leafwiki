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
	sectionNodeSnapshot, err := service.SnapshotPageNode(*sectionID)
	if err != nil {
		t.Fatalf("SnapshotPageNode(section): %v", err)
	}
	if len(sectionNodeSnapshot.Children) != 0 {
		t.Fatalf("SnapshotPageNode retained children: %#v", sectionNodeSnapshot.Children)
	}
	sectionSubtreeSnapshot, err := service.SnapshotPageSubtree(*sectionID)
	if err != nil {
		t.Fatalf("SnapshotPageSubtree: %v", err)
	}
	if len(sectionSubtreeSnapshot.Children) != 1 || sectionSubtreeSnapshot.Children[0].Parent != sectionSubtreeSnapshot {
		t.Fatalf("SnapshotPageSubtree relationships = %#v", sectionSubtreeSnapshot)
	}
	sectionSubtreeSnapshot.Children[0].Title = "changed subtree snapshot"
	titleSnapshots := service.SnapshotPagesByTitle(" page ")
	if len(titleSnapshots) != 1 || titleSnapshots[0].ID != *pageID || titleSnapshots[0].Parent == nil || !titleSnapshots[0].Parent.Draft || len(titleSnapshots[0].Children) != 0 {
		t.Fatalf("SnapshotPagesByTitle = %#v", titleSnapshots)
	}
	titleSnapshots[0].Title = "changed title snapshot"
	titleSnapshots[0].Parent.Draft = false

	page, err := service.FindPageByID(*pageID)
	if err != nil {
		t.Fatalf("FindPageByID(page): %v", err)
	}
	if page.Title != "Page" || !page.Parent.Draft {
		t.Fatalf("live page changed through snapshot: %#v", page)
	}
}

func TestTreeService_GetPageResultsAreDetached(t *testing.T) {
	service := NewTreeService(t.TempDir())
	if err := service.LoadTree(); err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	sectionKind := NodeKindSection
	sectionID, err := service.CreateNodeWithDraft("editor", nil, "Section", "section", &sectionKind, true)
	if err != nil {
		t.Fatalf("CreateNodeWithDraft: %v", err)
	}
	pageKind := NodeKindPage
	pageID, err := service.CreateNode("editor", sectionID, "Page", "page", &pageKind)
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}

	section, err := service.GetPage(*sectionID)
	if err != nil || len(section.Children) != 1 {
		t.Fatalf("GetPage(section) = %#v, %v", section, err)
	}
	section.Title = "changed"
	section.Children[0].Title = "changed child"

	pages, errs := service.GetPages([]string{*pageID})
	if errs[0] != nil || pages[0].Parent == nil || !pages[0].Parent.Draft {
		t.Fatalf("GetPages(page) = %#v, %#v", pages, errs)
	}
	pages[0].Title = "changed batch page"
	pages[0].Parent.Draft = false

	liveSection, err := service.FindPageByID(*sectionID)
	if err != nil {
		t.Fatalf("FindPageByID(section): %v", err)
	}
	livePage, err := service.FindPageByID(*pageID)
	if err != nil {
		t.Fatalf("FindPageByID(page): %v", err)
	}
	if liveSection.Title != "Section" || !liveSection.Draft || livePage.Title != "Page" {
		t.Fatalf("live tree changed through GetPage results: section=%#v page=%#v", liveSection, livePage)
	}
}
