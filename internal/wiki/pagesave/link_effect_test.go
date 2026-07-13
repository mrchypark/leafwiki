package pagesave

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/perber/wiki/internal/core/tree"
	"github.com/perber/wiki/internal/links"
)

func pageKindPtr() *tree.NodeKind {
	k := tree.NodeKindPage
	return &k
}

func setupLinkEffect(t *testing.T) (*LinkIndexSideEffect, *links.LinkService, *tree.TreeService) {
	t.Helper()
	return setupLinkEffectAt(t, t.TempDir())
}

func setupLinkEffectAt(t *testing.T, dataDir string) (*LinkIndexSideEffect, *links.LinkService, *tree.TreeService) {
	t.Helper()
	ts := tree.NewTreeService(dataDir)
	if err := ts.LoadTree(); err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	store, err := links.NewLinksStore(dataDir)
	if err != nil {
		t.Fatalf("NewLinksStore: %v", err)
	}
	svc := links.NewLinkService(dataDir, ts, store)
	return NewLinkIndexSideEffect(svc, nil), svc, ts
}

func TestLinkIndexSideEffect_Update_RemovesDraftSourceAndReindexesWhenPublished(t *testing.T) {
	effect, svc, ts := setupLinkEffect(t)
	target := createPageWithContent(t, ts, "Target", "target", "target")
	source := createPageWithContent(t, ts, "Source", "source", "[target](/target)")

	effect.Apply(PageSaveEvent{Operation: PageOperationCreate, After: target})
	effect.Apply(PageSaveEvent{Operation: PageOperationCreate, After: source})
	source = setDraftForTest(t, ts, source, true)
	effect.Apply(PageSaveEvent{Operation: PageOperationUpdate, After: source})
	out, err := svc.GetOutgoingLinksForPage(source.ID)
	if err != nil {
		t.Fatalf("GetOutgoingLinksForPage draft: %v", err)
	}
	if out.Count != 0 {
		t.Fatalf("draft source remained in link index: %#v", out.Outgoings)
	}

	source = setDraftForTest(t, ts, source, false)
	effect.Apply(PageSaveEvent{Operation: PageOperationUpdate, After: source})
	out, err = svc.GetOutgoingLinksForPage(source.ID)
	if err != nil {
		t.Fatalf("GetOutgoingLinksForPage published: %v", err)
	}
	if out.Count != 1 {
		t.Fatalf("published source was not reindexed: %#v", out.Outgoings)
	}
}

func TestLinkIndexSideEffect_StalePublicSaveCannotOverwriteNewerDraftSave(t *testing.T) {
	effect, svc, ts := setupLinkEffect(t)
	target := createPageWithContent(t, ts, "Target", "target", "target")
	source := createPageWithContent(t, ts, "Source", "source", "initial")
	effect.Apply(PageSaveEvent{Operation: PageOperationCreate, After: target})
	effect.Apply(PageSaveEvent{Operation: PageOperationCreate, After: source})

	publicContent := "[target](/target)"
	if err := ts.UpdateNode("editor", source.ID, source.Title, source.Slug, &publicContent, source.Version(), nil, nil, false); err != nil {
		t.Fatalf("public UpdateNode: %v", err)
	}
	stalePublic, err := ts.GetPage(source.ID)
	if err != nil {
		t.Fatalf("GetPage public: %v", err)
	}

	latestDraft := setDraftForTest(t, ts, stalePublic, true)
	// Reproduce two completed tree mutations whose synchronous side effects are
	// scheduled in reverse: the newer draft save runs before the stale public save.
	effect.Apply(PageSaveEvent{
		Operation:     PageOperationUpdate,
		After:         latestDraft,
		DraftChanged:  true,
		AffectedPages: []*tree.Page{latestDraft},
	})
	effect.Apply(PageSaveEvent{
		Operation:      PageOperationUpdate,
		After:          stalePublic,
		ContentChanged: true,
	})

	out, err := svc.GetOutgoingLinksForPage(source.ID)
	if err != nil {
		t.Fatalf("GetOutgoingLinksForPage: %v", err)
	}
	if out.Count != 0 {
		t.Fatalf("stale public save reindexed a draft page: %#v", out.Outgoings)
	}
}

func TestLinkIndexSideEffect_StalePublicSaveCannotHealNewerDraftTarget(t *testing.T) {
	effect, svc, ts := setupLinkEffect(t)
	target := createPageWithContent(t, ts, "Target", "target", "target")
	source := createPageWithContent(t, ts, "Source", "source", "[target](/target)")
	effect.Apply(PageSaveEvent{Operation: PageOperationCreate, After: target})
	effect.Apply(PageSaveEvent{Operation: PageOperationCreate, After: source})
	stalePublic := target

	latestDraft := setDraftForTest(t, ts, target, true)
	effect.Apply(PageSaveEvent{
		Operation:     PageOperationUpdate,
		After:         latestDraft,
		DraftChanged:  true,
		AffectedPages: []*tree.Page{latestDraft},
	})
	effect.Apply(PageSaveEvent{
		Operation: PageOperationUpdate,
		After:     stalePublic,
	})

	out, err := svc.GetOutgoingLinksForPage(source.ID)
	if err != nil {
		t.Fatalf("GetOutgoingLinksForPage: %v", err)
	}
	if out.Count != 1 || !out.Outgoings[0].Broken || out.Outgoings[0].ToPageID != "" {
		t.Fatalf("stale public save healed a draft target: %#v", out.Outgoings)
	}
}

func TestLinkIndexSideEffect_LateRenameDoesNotBreakReusedOldPath(t *testing.T) {
	effect, svc, ts := setupLinkEffect(t)
	source := createPageWithContent(t, ts, "Source", "source", "[old](/old)")
	effect.Apply(PageSaveEvent{Operation: PageOperationCreate, After: source})

	target := createPageWithContent(t, ts, "Old Target", "old", "target")
	if err := ts.UpdateNode("editor", target.ID, target.Title, "new", nil, target.Version(), nil, nil, false); err != nil {
		t.Fatalf("rename old target: %v", err)
	}
	renamed, err := ts.GetPage(target.ID)
	if err != nil {
		t.Fatalf("GetPage renamed target: %v", err)
	}
	lateRename := PageSaveEvent{
		Operation:     PageOperationUpdate,
		After:         renamed,
		OldPath:       "/old",
		SlugChanged:   true,
		AffectedPages: []*tree.Page{renamed},
	}

	replacement := createPageWithContent(t, ts, "Replacement", "old", "replacement")
	effect.Apply(PageSaveEvent{Operation: PageOperationCreate, After: replacement})
	before, err := svc.GetOutgoingLinksForPage(source.ID)
	if err != nil {
		t.Fatalf("GetOutgoingLinksForPage before late rename: %v", err)
	}
	if before.Count != 1 || before.Outgoings[0].Broken || before.Outgoings[0].ToPageID != replacement.ID {
		t.Fatalf("replacement did not heal reused path: %#v", before.Outgoings)
	}

	effect.Apply(lateRename)

	after, err := svc.GetOutgoingLinksForPage(source.ID)
	if err != nil {
		t.Fatalf("GetOutgoingLinksForPage after late rename: %v", err)
	}
	if after.Count != 1 || after.Outgoings[0].Broken || after.Outgoings[0].ToPageID != replacement.ID {
		t.Fatalf("late rename broke reused old path: %#v", after.Outgoings)
	}
}

func TestLinkIndexSideEffect_LateRenameReassignsResolvedOldPathToReplacement(t *testing.T) {
	dataDir := t.TempDir()
	effect, svc, ts := setupLinkEffectAt(t, dataDir)
	target := createPageWithContent(t, ts, "Old Target", "old", "target")
	source := createPageWithContent(t, ts, "Source", "source", "[old](/old)")
	effect.Apply(PageSaveEvent{Operation: PageOperationCreate, After: source})

	if err := ts.UpdateNode("editor", target.ID, target.Title, "new", nil, target.Version(), nil, nil, false); err != nil {
		t.Fatalf("rename old target: %v", err)
	}
	renamed, err := ts.GetPage(target.ID)
	if err != nil {
		t.Fatalf("GetPage renamed target: %v", err)
	}
	lateRename := PageSaveEvent{
		Operation:       PageOperationUpdate,
		After:           renamed,
		SlugChanged:     true,
		AffectedPages:   []*tree.Page{renamed},
		AffectedPageIDs: []string{renamed.ID},
	}

	replacement := createPageWithContent(t, ts, "Replacement", "old", "replacement")
	effect.Apply(PageSaveEvent{Operation: PageOperationCreate, After: replacement})
	if err := os.Remove(filepath.Join(dataDir, "root", "old.md")); err != nil {
		t.Fatalf("Remove replacement file: %v", err)
	}
	before, err := svc.GetOutgoingLinksForPage(source.ID)
	if err != nil {
		t.Fatalf("GetOutgoingLinksForPage before late rename: %v", err)
	}
	if before.Count != 1 || before.Outgoings[0].Broken || before.Outgoings[0].ToPageID != target.ID {
		t.Fatalf("precondition link = %#v, want stale resolved target %q", before.Outgoings, target.ID)
	}

	effect.Apply(lateRename)

	after, err := svc.GetOutgoingLinksForPage(source.ID)
	if err != nil {
		t.Fatalf("GetOutgoingLinksForPage after late rename: %v", err)
	}
	if after.Count != 1 || after.Outgoings[0].Broken || after.Outgoings[0].ToPageID != replacement.ID {
		t.Fatalf("late rename did not reassign reused old path: %#v", after.Outgoings)
	}
}

func TestLinkIndexSideEffect_AffectedIDRemovesUnreadableDraftDescendant(t *testing.T) {
	dataDir := t.TempDir()
	ts := tree.NewTreeService(dataDir)
	if err := ts.LoadTree(); err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	store, err := links.NewLinksStore(dataDir)
	if err != nil {
		t.Fatalf("NewLinksStore: %v", err)
	}
	svc := links.NewLinkService(dataDir, ts, store)
	effect := NewLinkIndexSideEffect(svc, nil)

	sectionKind := tree.NodeKindSection
	sectionID, err := ts.CreateNode("editor", nil, "Section", "section", &sectionKind)
	if err != nil {
		t.Fatalf("CreateNode section: %v", err)
	}
	childID, err := ts.CreateNode("editor", sectionID, "Shared", "child", pageKindPtr())
	if err != nil {
		t.Fatalf("CreateNode child: %v", err)
	}
	target := createPageWithContent(t, ts, "Target", "target", "target")
	child, err := ts.GetPage(*childID)
	if err != nil {
		t.Fatalf("GetPage child: %v", err)
	}
	content := "[target](/target)"
	if err := ts.UpdateNode("editor", child.ID, child.Title, child.Slug, &content, child.Version(), nil, nil, false); err != nil {
		t.Fatalf("UpdateNode child: %v", err)
	}
	child, err = ts.GetPage(*childID)
	if err != nil {
		t.Fatalf("GetPage updated child: %v", err)
	}
	source := createPageWithContent(t, ts, "Source", "source", "[child](/section/child)")
	keeper := createPageWithContent(t, ts, "Shared", "keeper", "keeper")
	wikiSource := createPageWithContent(t, ts, "Wiki Source", "wiki-source", "[[Shared]]")
	effect.Apply(PageSaveEvent{Operation: PageOperationCreate, After: target})
	effect.Apply(PageSaveEvent{Operation: PageOperationCreate, After: child})
	effect.Apply(PageSaveEvent{Operation: PageOperationCreate, After: source})
	effect.Apply(PageSaveEvent{Operation: PageOperationCreate, After: wikiSource})
	assertWikilinkTarget(t, svc, wikiSource.ID, "", true)

	section, err := ts.GetPage(*sectionID)
	if err != nil {
		t.Fatalf("GetPage section: %v", err)
	}
	draft := true
	if err := ts.UpdateNodeWithDraft("editor", section.ID, section.Title, section.Slug, nil, section.Version(), nil, nil, false, &draft); err != nil {
		t.Fatalf("UpdateNodeWithDraft section: %v", err)
	}
	if err := os.Remove(filepath.Join(dataDir, "root", "section", "child.md")); err != nil {
		t.Fatalf("Remove child file: %v", err)
	}

	effect.Apply(PageSaveEvent{
		Operation:       PageOperationUpdate,
		DraftChanged:    true,
		AffectedPageIDs: []string{*childID},
		AffectedTitles:  []string{"Shared", "shared"},
	})

	outgoing, err := svc.GetOutgoingLinksForPage(*childID)
	if err != nil {
		t.Fatalf("GetOutgoingLinksForPage child: %v", err)
	}
	if outgoing.Count != 0 {
		t.Fatalf("unreadable draft descendant retained outgoing links: %#v", outgoing.Outgoings)
	}
	sourceLinks, err := svc.GetOutgoingLinksForPage(source.ID)
	if err != nil {
		t.Fatalf("GetOutgoingLinksForPage source: %v", err)
	}
	if sourceLinks.Count != 1 || !sourceLinks.Outgoings[0].Broken || sourceLinks.Outgoings[0].ToPageID != "" {
		t.Fatalf("unreadable draft descendant retained incoming link: %#v", sourceLinks.Outgoings)
	}
	assertWikilinkTarget(t, svc, wikiSource.ID, keeper.ID, false)
}

func TestLinkIndexSideEffect_MoveIntoDraftRemovesOutgoingLinks(t *testing.T) {
	effect, svc, ts := setupLinkEffect(t)
	target := createPageWithContent(t, ts, "Target", "target", "target")
	source := createPageWithContent(t, ts, "Moved Source", "moved-source", "[target](/target)")
	effect.Apply(PageSaveEvent{Operation: PageOperationCreate, After: target})
	effect.Apply(PageSaveEvent{Operation: PageOperationCreate, After: source})
	draftParentID, err := ts.CreateNodeWithDraft("editor", nil, "Draft Parent", "draft-parent", pageKindPtr(), true)
	if err != nil {
		t.Fatalf("CreateNodeWithDraft: %v", err)
	}
	if err := ts.MoveNode("editor", source.ID, *draftParentID, source.Version()); err != nil {
		t.Fatalf("MoveNode into draft: %v", err)
	}
	source, err = ts.GetPage(source.ID)
	if err != nil {
		t.Fatalf("GetPage after move: %v", err)
	}
	effect.Apply(PageSaveEvent{Operation: PageOperationMove, OldPath: "/moved-source", AffectedPages: []*tree.Page{source}})

	out, err := svc.GetOutgoingLinksForPage(source.ID)
	if err != nil {
		t.Fatalf("GetOutgoingLinksForPage: %v", err)
	}
	if out.Count != 0 {
		t.Fatalf("page moved into draft retained outgoing links: %#v", out.Outgoings)
	}
}

func TestLinkIndexSideEffect_Update_BreaksIncomingLinksWhenTargetBecomesDraft(t *testing.T) {
	effect, svc, ts := setupLinkEffect(t)
	target := createPageWithContent(t, ts, "Target", "target", "target")
	source := createPageWithContent(t, ts, "Source", "source", "[target](/target)")
	effect.Apply(PageSaveEvent{Operation: PageOperationCreate, After: target})
	effect.Apply(PageSaveEvent{Operation: PageOperationCreate, After: source})

	target = setDraftForTest(t, ts, target, true)
	effect.Apply(PageSaveEvent{Operation: PageOperationUpdate, After: target})
	backlinks, err := svc.GetBacklinksForPage(target.ID)
	if err != nil {
		t.Fatalf("GetBacklinksForPage: %v", err)
	}
	if backlinks.Count != 0 {
		t.Fatalf("draft target retained incoming links: %#v", backlinks.Backlinks)
	}
}

func TestLinkIndexSideEffect_DraftChange_HealsWikilinkWhenPublishedTitleBecomesUnique(t *testing.T) {
	effect, svc, ts := setupLinkEffect(t)
	target := createPageWithContent(t, ts, "Shared", "target", "target")
	keeper := createPageWithContent(t, ts, "Shared", "keeper", "keeper")
	source := createPageWithContent(t, ts, "Source", "source", "[[Shared]]")
	effect.Apply(PageSaveEvent{Operation: PageOperationCreate, After: source})

	assertWikilinkTarget(t, svc, source.ID, "", true)
	target = setDraftForTest(t, ts, target, true)
	effect.Apply(PageSaveEvent{
		Operation:     PageOperationUpdate,
		After:         target,
		OldTitle:      "Shared",
		DraftChanged:  true,
		AffectedPages: []*tree.Page{target},
	})

	assertWikilinkTarget(t, svc, source.ID, keeper.ID, false)
}

func TestLinkIndexSideEffect_DraftChange_BreaksWikilinkWhenPublishedTitleBecomesAmbiguous(t *testing.T) {
	effect, svc, ts := setupLinkEffect(t)
	keeper := createPageWithContent(t, ts, "Shared", "keeper", "keeper")
	target := createPageWithContent(t, ts, "Shared", "target", "target")
	target = setDraftForTest(t, ts, target, true)
	source := createPageWithContent(t, ts, "Source", "source", "[[Shared]]")
	effect.Apply(PageSaveEvent{Operation: PageOperationCreate, After: source})

	assertWikilinkTarget(t, svc, source.ID, keeper.ID, false)
	target = setDraftForTest(t, ts, target, false)
	effect.Apply(PageSaveEvent{
		Operation:     PageOperationUpdate,
		After:         target,
		OldTitle:      "Shared",
		DraftChanged:  true,
		AffectedPages: []*tree.Page{target},
	})

	assertWikilinkTarget(t, svc, source.ID, "", true)
}

func TestLinkIndexSideEffect_DraftAndTitleChange_ReconcilesOldAndNewTitles(t *testing.T) {
	effect, svc, ts := setupLinkEffect(t)
	oldKeeper := createPageWithContent(t, ts, "Old", "old-keeper", "old keeper")
	_ = createPageWithContent(t, ts, "New", "new-keeper", "new keeper")
	target := createPageWithContent(t, ts, "Old", "target", "target")
	target = setDraftForTest(t, ts, target, true)
	source := createPageWithContent(t, ts, "Source", "source", "[[Old]] [[New]]")
	effect.Apply(PageSaveEvent{Operation: PageOperationCreate, After: source})

	draft := false
	if err := ts.UpdateNodeWithDraft("editor", target.ID, "New", target.Slug, nil, tree.VersionUnchecked, nil, nil, false, &draft); err != nil {
		t.Fatalf("UpdateNodeWithDraft: %v", err)
	}
	target, err := ts.GetPage(target.ID)
	if err != nil {
		t.Fatalf("GetPage: %v", err)
	}
	effect.Apply(PageSaveEvent{
		Operation:     PageOperationUpdate,
		After:         target,
		OldTitle:      "Old",
		TitleChanged:  true,
		DraftChanged:  true,
		AffectedPages: []*tree.Page{target},
	})

	out, err := svc.GetOutgoingLinksForPage(source.ID)
	if err != nil {
		t.Fatalf("GetOutgoingLinksForPage: %v", err)
	}
	if out.Count != 2 {
		t.Fatalf("outgoing count = %d, want 2: %#v", out.Count, out.Outgoings)
	}
	for _, link := range out.Outgoings {
		switch link.ToPath {
		case "Old":
			if link.Broken || link.ToPageID != oldKeeper.ID {
				t.Fatalf("old-title wikilink = %#v, want healed keeper %q", link, oldKeeper.ID)
			}
		case "New":
			if !link.Broken || link.ToPageID != "" {
				t.Fatalf("new-title wikilink = %#v, want ambiguous broken sentinel", link)
			}
		default:
			t.Fatalf("unexpected outgoing link: %#v", link)
		}
	}
}

func TestLinkIndexSideEffect_TitleChange_HealsOldTitleWithoutSlugChange(t *testing.T) {
	effect, svc, ts := setupLinkEffect(t)
	target := createPageWithContent(t, ts, "Old", "target", "target")
	keeper := createPageWithContent(t, ts, "Old", "keeper", "keeper")
	source := createPageWithContent(t, ts, "Source", "source", "[[Old]]")
	effect.Apply(PageSaveEvent{Operation: PageOperationCreate, After: source})
	assertWikilinkTarget(t, svc, source.ID, "", true)

	if err := ts.UpdateNode("editor", target.ID, "New", target.Slug, nil, tree.VersionUnchecked, nil, nil, false); err != nil {
		t.Fatalf("UpdateNode: %v", err)
	}
	target, err := ts.GetPage(target.ID)
	if err != nil {
		t.Fatalf("GetPage: %v", err)
	}
	effect.Apply(PageSaveEvent{
		Operation:    PageOperationUpdate,
		After:        target,
		OldTitle:     "Old",
		TitleChanged: true,
	})

	assertWikilinkTarget(t, svc, source.ID, keeper.ID, false)
}

func TestLinkIndexSideEffect_TitleChange_BreaksNewTitleWhenItBecomesAmbiguous(t *testing.T) {
	effect, svc, ts := setupLinkEffect(t)
	target := createPageWithContent(t, ts, "Old", "target", "target")
	keeper := createPageWithContent(t, ts, "New", "keeper", "keeper")
	source := createPageWithContent(t, ts, "Source", "source", "[[New]]")
	effect.Apply(PageSaveEvent{Operation: PageOperationCreate, After: source})
	assertWikilinkTarget(t, svc, source.ID, keeper.ID, false)

	if err := ts.UpdateNode("editor", target.ID, "New", target.Slug, nil, tree.VersionUnchecked, nil, nil, false); err != nil {
		t.Fatalf("UpdateNode: %v", err)
	}
	target, err := ts.GetPage(target.ID)
	if err != nil {
		t.Fatalf("GetPage: %v", err)
	}
	effect.Apply(PageSaveEvent{
		Operation:    PageOperationUpdate,
		After:        target,
		OldTitle:     "Old",
		TitleChanged: true,
	})

	assertWikilinkTarget(t, svc, source.ID, "", true)
}

func TestLinkIndexSideEffect_MoveIntoDraft_ReconcilesUnchangedDescendantTitles(t *testing.T) {
	effect, svc, ts := setupLinkEffect(t)
	sectionKind := tree.NodeKindSection
	draftParentID, err := ts.CreateNodeWithDraft("editor", nil, "Draft Parent", "draft-parent", &sectionKind, true)
	if err != nil {
		t.Fatalf("CreateNodeWithDraft parent: %v", err)
	}
	sectionID, err := ts.CreateNode("editor", nil, "Section", "section", &sectionKind)
	if err != nil {
		t.Fatalf("CreateNode section: %v", err)
	}
	childID, err := ts.CreateNode("editor", sectionID, "Shared", "child", pageKindPtr())
	if err != nil {
		t.Fatalf("CreateNode child: %v", err)
	}
	child, err := ts.GetPage(*childID)
	if err != nil {
		t.Fatalf("GetPage child: %v", err)
	}
	keeper := createPageWithContent(t, ts, "Shared", "keeper", "keeper")
	source := createPageWithContent(t, ts, "Source", "source", "[[Shared]]")
	effect.Apply(PageSaveEvent{Operation: PageOperationCreate, After: source})
	assertWikilinkTarget(t, svc, source.ID, "", true)

	section, err := ts.GetPage(*sectionID)
	if err != nil {
		t.Fatalf("GetPage section: %v", err)
	}
	if err := ts.MoveNode("editor", section.ID, *draftParentID, section.Version()); err != nil {
		t.Fatalf("MoveNode: %v", err)
	}
	section, err = ts.GetPage(*sectionID)
	if err != nil {
		t.Fatalf("GetPage section after move: %v", err)
	}
	child, err = ts.GetPage(*childID)
	if err != nil {
		t.Fatalf("GetPage child after draft: %v", err)
	}
	effect.Apply(PageSaveEvent{
		Operation:     PageOperationMove,
		OldPath:       "/section",
		DraftChanged:  true,
		AffectedPages: []*tree.Page{section, child},
	})

	assertWikilinkTarget(t, svc, source.ID, keeper.ID, false)
}

func assertWikilinkTarget(t *testing.T, svc *links.LinkService, sourceID, targetID string, broken bool) {
	t.Helper()
	out, err := svc.GetOutgoingLinksForPage(sourceID)
	if err != nil {
		t.Fatalf("GetOutgoingLinksForPage: %v", err)
	}
	if out.Count != 1 {
		t.Fatalf("outgoing count = %d, want 1: %#v", out.Count, out.Outgoings)
	}
	if got := out.Outgoings[0]; got.ToPageID != targetID || got.Broken != broken {
		t.Fatalf("wikilink = %#v, want target %q broken=%v", got, targetID, broken)
	}
}

// TestLinkIndexSideEffect_Rename_HealsPreexistingBrokenWikilinks verifies that
// renaming page "Alpha" → "Beta" heals broken [[Alpha]] sentinels that already
// existed in the store (e.g. from when the title was ambiguous) by re-routing
// them to the one remaining page still titled "Alpha".
func TestLinkIndexSideEffect_Rename_HealsPreexistingBrokenWikilinks(t *testing.T) {
	effect, svc, ts := setupLinkEffect(t)

	// "Alpha" will be renamed; "Keeper" retains the title so [[Alpha]] becomes unambiguous.
	alphaIDPtr, err := ts.CreateNode("system", nil, "Alpha", "alpha", pageKindPtr())
	if err != nil {
		t.Fatalf("CreateNode alpha: %v", err)
	}
	alphaID := *alphaIDPtr

	keeperIDPtr, err := ts.CreateNode("system", nil, "Alpha", "keeper", pageKindPtr())
	if err != nil {
		t.Fatalf("CreateNode keeper: %v", err)
	}
	keeperID := *keeperIDPtr

	linkerIDPtr, err := ts.CreateNode("system", nil, "Linker", "linker", pageKindPtr())
	if err != nil {
		t.Fatalf("CreateNode linker: %v", err)
	}
	linkerID := *linkerIDPtr

	// Write [[Alpha]] wikilink into "Linker" and index it.
	// With two pages titled "Alpha" the sentinel is ambiguous → stored broken.
	linkerPage, err := ts.GetPage(linkerID)
	if err != nil {
		t.Fatalf("GetPage linker: %v", err)
	}
	content := "See [[Alpha]] for details."
	if err := ts.UpdateNode("system", linkerPage.ID, linkerPage.Title, linkerPage.Slug, &content, tree.VersionUnchecked, nil, nil, false); err != nil {
		t.Fatalf("UpdateNode linker: %v", err)
	}
	linkerPage, err = ts.GetPage(linkerID)
	if err != nil {
		t.Fatalf("GetPage linker (after update): %v", err)
	}
	if err := svc.UpdateLinksForPage(linkerPage, linkerPage.Content); err != nil {
		t.Fatalf("UpdateLinksForPage: %v", err)
	}

	// Precondition: sentinel must be broken (ambiguous).
	out, err := svc.GetOutgoingLinksForPage(linkerID)
	if err != nil {
		t.Fatalf("GetOutgoingLinksForPage: %v", err)
	}
	if out.Count != 1 || !out.Outgoings[0].Broken {
		t.Fatalf("precondition failed: expected 1 broken [[Alpha]] sentinel, got %+v", out.Outgoings)
	}

	// Rename "Alpha" → "Beta" in the tree (UpdateNode mutates the live node).
	if err := ts.UpdateNode("system", alphaID, "Beta", "beta", nil, tree.VersionUnchecked, nil, nil, false); err != nil {
		t.Fatalf("UpdateNode rename alpha→beta: %v", err)
	}
	afterPage, err := ts.GetPage(alphaID)
	if err != nil {
		t.Fatalf("GetPage after rename: %v", err)
	}

	// Apply the link effect with the event that the use case would emit.
	effect.Apply(PageSaveEvent{
		Operation:     PageOperationUpdate,
		UserID:        "system",
		After:         afterPage,
		OldPath:       "/alpha",
		OldTitle:      "Alpha",
		TitleChanged:  true,
		SlugChanged:   true,
		AffectedPages: []*tree.Page{afterPage},
	})

	// The broken [[Alpha]] sentinel must now be healed to point to "Keeper" —
	// the only page still titled "Alpha".
	out2, err := svc.GetOutgoingLinksForPage(linkerID)
	if err != nil {
		t.Fatalf("GetOutgoingLinksForPage (after heal): %v", err)
	}
	if out2.Count != 1 {
		t.Fatalf("expected 1 outgoing after heal, got %d: %+v", out2.Count, out2.Outgoings)
	}
	if out2.Outgoings[0].Broken {
		t.Fatalf("expected [[Alpha]] sentinel to be healed, still broken: %+v", out2.Outgoings[0])
	}
	if out2.Outgoings[0].ToPageID != keeperID {
		t.Fatalf("expected sentinel to point to keeper (%q), got %q", keeperID, out2.Outgoings[0].ToPageID)
	}
}
