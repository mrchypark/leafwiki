package tree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/perber/wiki/internal/core/markdown"
)

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

func TestTreeService_PublicLookupResultsRetainTheirVisibilitySnapshot(t *testing.T) {
	service := NewTreeService(t.TempDir())
	if err := service.LoadTree(); err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	kind := NodeKindPage
	pageID, err := service.CreateNodeWithDraft("editor", nil, "Secret", "secret", &kind, true)
	if err != nil {
		t.Fatalf("CreateNodeWithDraft: %v", err)
	}
	content := "draft-only content"
	if err := service.UpdateNode("editor", *pageID, "Secret", "secret", &content, VersionUnchecked, nil, nil, false); err != nil {
		t.Fatalf("UpdateNode content: %v", err)
	}

	page, err := service.FindPageByRoutePath("secret")
	if err != nil {
		t.Fatalf("FindPageByRoutePath: %v", err)
	}
	lookup, err := service.LookupPagePath("secret")
	if err != nil {
		t.Fatalf("LookupPagePath: %v", err)
	}
	target, err := service.ResolvePermalinkTarget(*pageID)
	if err != nil {
		t.Fatalf("ResolvePermalinkTarget: %v", err)
	}

	published := false
	if err := service.UpdateNodeWithDraft("editor", *pageID, "Public", "public", nil, VersionUnchecked, nil, nil, false, &published); err != nil {
		t.Fatalf("publish and rename: %v", err)
	}

	if page.Title != "Secret" || page.Slug != "secret" || page.Content != content || !page.Draft {
		t.Fatalf("route lookup changed after publish: %#v", page)
	}
	segment := lookup.Segments[0]
	if segment.Title == nil || *segment.Title != "Secret" || segment.VisibilityNode == nil || !segment.VisibilityNode.Draft {
		t.Fatalf("path lookup changed after publish: %#v", segment)
	}
	if target.Slug != "secret" || target.VisibilityNode == nil || !target.VisibilityNode.Draft {
		t.Fatalf("permalink changed after publish: %#v", target)
	}
}

func TestTreeService_DraftContentWriteRemainsHiddenWhenRenameFails(t *testing.T) {
	dir := t.TempDir()
	service := NewTreeService(dir)
	if err := service.LoadTree(); err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	kind := NodeKindPage
	pageID, err := service.CreateNode("editor", nil, "Public", "public", &kind)
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}

	// This path is not represented in the tree, so slug validation succeeds but
	// the filesystem rename fails after the content write.
	if err := os.WriteFile(filepath.Join(dir, "root", "blocked.md"), []byte("occupied"), 0o644); err != nil {
		t.Fatalf("create rename blocker: %v", err)
	}
	draft := true
	secret := "new secret body"
	err = service.UpdateNodeWithDraft("editor", *pageID, "Draft", "blocked", &secret, VersionUnchecked, nil, nil, false, &draft)
	if err == nil {
		t.Fatal("UpdateNodeWithDraft unexpectedly succeeded")
	}

	node, findErr := service.SnapshotPageNode(*pageID)
	if findErr != nil {
		t.Fatalf("SnapshotPageNode: %v", findErr)
	}
	if !node.Draft {
		t.Fatal("failed draft save left the live page publicly visible")
	}
	raw, readErr := os.ReadFile(filepath.Join(dir, "root", "public.md"))
	if readErr != nil {
		t.Fatalf("read persisted page: %v", readErr)
	}
	if !strings.Contains(string(raw), "\ndraft: true\n") || !strings.Contains(string(raw), secret) {
		t.Fatalf("new content was written without fail-closed draft frontmatter:\n%s", raw)
	}
}

func TestTreeService_ManagedFieldsRemainConsistentWhenCombinedUpdateRenameFails(t *testing.T) {
	dir := t.TempDir()
	service := NewTreeService(dir)
	if err := service.LoadTree(); err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	kind := NodeKindPage
	pageID, err := service.CreateNode("editor", nil, "Old Title", "old-slug", &kind)
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	before, err := service.GetPage(*pageID)
	if err != nil {
		t.Fatalf("GetPage before update: %v", err)
	}
	beforeFM, _, _, err := markdown.ParseFrontmatter(before.RawContent)
	if err != nil {
		t.Fatalf("ParseFrontmatter before update: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "root", "blocked.md"), []byte("occupied"), 0o644); err != nil {
		t.Fatalf("create rename blocker: %v", err)
	}
	newBody := "body written before rename failure"
	err = service.UpdateNode("editor", *pageID, "New Title", "blocked", &newBody, VersionUnchecked, nil, nil, false)
	if err == nil {
		t.Fatal("UpdateNode unexpectedly succeeded")
	}

	after, findErr := service.GetPage(*pageID)
	if findErr != nil {
		t.Fatalf("GetPage after failed update: %v", findErr)
	}
	if after.Title != before.Title || after.Slug != before.Slug || after.Version() != before.Version() {
		t.Fatalf("live managed state changed: before=%#v after=%#v", before.PageNode, after.PageNode)
	}
	afterFM, body, _, parseErr := markdown.ParseFrontmatter(after.RawContent)
	if parseErr != nil {
		t.Fatalf("ParseFrontmatter after failed update: %v", parseErr)
	}
	if afterFM.LeafWikiTitle != beforeFM.LeafWikiTitle ||
		afterFM.LeafWikiUpdatedAt != beforeFM.LeafWikiUpdatedAt ||
		afterFM.LeafWikiLastAuthorID != beforeFM.LeafWikiLastAuthorID {
		t.Fatalf("persisted managed fields changed: before=%#v after=%#v", beforeFM, afterFM)
	}
	// A markdown body write and a path rename are separate filesystem operations;
	// the body can be durable even though the later rename fails.
	if body != newBody {
		t.Fatalf("persisted body = %q, want %q", body, newBody)
	}
	oldMatches := service.SnapshotPagesByTitle("Old Title")
	newMatches := service.SnapshotPagesByTitle("New Title")
	if len(oldMatches) != 1 || oldMatches[0].ID != *pageID || len(newMatches) != 0 {
		t.Fatalf("title index split: old=%#v new=%#v", oldMatches, newMatches)
	}
}

func TestTreeService_SetPinnedReturnsDetachedPageWithRawContent(t *testing.T) {
	service := NewTreeService(t.TempDir())
	if err := service.LoadTree(); err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	sectionKind := NodeKindSection
	sectionID, err := service.CreateNode("editor", nil, "Section", "section", &sectionKind)
	if err != nil {
		t.Fatalf("CreateNode section: %v", err)
	}
	pageKind := NodeKindPage
	childID, err := service.CreateNode("editor", sectionID, "Child", "child", &pageKind)
	if err != nil {
		t.Fatalf("CreateNode child: %v", err)
	}
	raw := "---\ntags:\n  - retained\nstatus: active\n---\nBody"
	if err := service.UpdateNode("editor", *sectionID, "Section", "section", &raw, VersionUnchecked, nil, nil, true); err != nil {
		t.Fatalf("UpdateNode section content: %v", err)
	}
	before, err := service.GetPage(*sectionID)
	if err != nil {
		t.Fatalf("GetPage before pin: %v", err)
	}

	pinned, err := service.SetPinned(*sectionID, before.Version(), true)
	if err != nil {
		t.Fatalf("SetPinned: %v", err)
	}
	if !pinned.Pinned || pinned.RawContent == "" || !strings.Contains(pinned.RawContent, "status: active") {
		t.Fatalf("pinned page missing final raw state: %#v raw=%q", pinned.PageNode, pinned.RawContent)
	}
	if len(pinned.Children) != 1 || pinned.Children[0].ID != *childID || pinned.Children[0].Parent != pinned.PageNode {
		t.Fatalf("pinned subtree snapshot = %#v", pinned.Children)
	}

	pinned.Title = "mutated snapshot"
	pinned.Children[0].Title = "mutated child"
	liveSection, err := service.SnapshotPageNode(*sectionID)
	if err != nil {
		t.Fatalf("SnapshotPageNode section: %v", err)
	}
	liveChild, err := service.SnapshotPageNode(*childID)
	if err != nil {
		t.Fatalf("SnapshotPageNode child: %v", err)
	}
	if liveSection.Title != "Section" || liveChild.Title != "Child" {
		t.Fatalf("live tree changed through pinned result: section=%#v child=%#v", liveSection, liveChild)
	}
}

func TestTreeService_PublishedPageIDsAndFilterSkipDraftSubtrees(t *testing.T) {
	service := NewTreeService(t.TempDir())
	if err := service.LoadTree(); err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	sectionKind := NodeKindSection
	draftID, err := service.CreateNodeWithDraft("editor", nil, "Draft", "draft", &sectionKind, true)
	if err != nil {
		t.Fatalf("CreateNodeWithDraft: %v", err)
	}
	pageKind := NodeKindPage
	draftChildID, err := service.CreateNode("editor", draftID, "Draft child", "child", &pageKind)
	if err != nil {
		t.Fatalf("CreateNode(draft child): %v", err)
	}
	firstID, err := service.CreateNode("editor", nil, "First", "first", &pageKind)
	if err != nil {
		t.Fatalf("CreateNode(first): %v", err)
	}
	secondID, err := service.CreateNode("editor", nil, "Second", "second", &pageKind)
	if err != nil {
		t.Fatalf("CreateNode(second): %v", err)
	}

	if got := service.PublishedPageIDs(); len(got) != 2 || got[0] != *firstID || got[1] != *secondID {
		t.Fatalf("PublishedPageIDs = %v", got)
	}
	got := service.FilterPublishedPageIDs([]string{*secondID, *draftID, "root", "missing", *firstID, *draftChildID})
	if len(got) != 2 || got[0] != *secondID || got[1] != *firstID {
		t.Fatalf("FilterPublishedPageIDs = %v", got)
	}
}
