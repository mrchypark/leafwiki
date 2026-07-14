package pagesave

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/perber/wiki/internal/core/tree"
	"github.com/perber/wiki/internal/properties"
	"github.com/perber/wiki/internal/search"
	"github.com/perber/wiki/internal/tags"
	"github.com/perber/wiki/internal/test_utils"
)

type projectionTestDeps struct {
	dir        string
	tree       *tree.TreeService
	search     *search.SQLiteIndex
	tags       *tags.TagsService
	properties *properties.PropertiesService
	older      *PageSaveOrchestrator
	newer      *PageSaveOrchestrator
}

func setupProjectionTest(t *testing.T) projectionTestDeps {
	t.Helper()
	dir := t.TempDir()
	treeService := tree.NewTreeService(dir)
	if err := treeService.LoadTree(); err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	searchIndex, err := search.NewSQLiteIndex(dir)
	if err != nil {
		t.Fatalf("NewSQLiteIndex: %v", err)
	}
	t.Cleanup(func() { test_utils.WrapCloseWithErrorCheck(searchIndex.Close, t) })
	tagsStore, err := tags.NewTagsStore(dir)
	if err != nil {
		t.Fatalf("NewTagsStore: %v", err)
	}
	t.Cleanup(func() { test_utils.WrapCloseWithErrorCheck(tagsStore.Close, t) })
	propertiesStore, err := properties.NewPropertiesStore(dir)
	if err != nil {
		t.Fatalf("NewPropertiesStore: %v", err)
	}
	t.Cleanup(func() { test_utils.WrapCloseWithErrorCheck(propertiesStore.Close, t) })

	tagsService := tags.NewTagsService(tagsStore)
	propertiesService := properties.NewPropertiesService(propertiesStore)
	searchEffect := NewSearchIndexSideEffect(searchIndex, treeService, nil)
	tagsEffect := NewTagsSideEffect(tagsService, treeService, nil)
	propertiesEffect := NewPropertiesSideEffect(propertiesService, treeService, nil)
	return projectionTestDeps{
		dir:        dir,
		tree:       treeService,
		search:     searchIndex,
		tags:       tagsService,
		properties: propertiesService,
		older:      NewPageSaveOrchestrator(nil, searchEffect, tagsEffect, propertiesEffect),
		newer:      NewPageSaveOrchestrator(nil, searchEffect, tagsEffect, propertiesEffect),
	}
}

func TestLoadProjectionPages_DistinguishesRemovalFromTransientReadError(t *testing.T) {
	t.Run("missing page is removed", func(t *testing.T) {
		deps := setupProjectionTest(t)
		states := loadProjectionPages(deps.tree, []string{"missing"})
		if len(states) != 1 || !states[0].remove || states[0].err != nil {
			t.Fatalf("missing page state = %#v, want remove", states)
		}
	})

	for _, draft := range []bool{false, true} {
		name := "public read error preserves projection"
		if draft {
			name = "draft read error removes projection"
		}
		t.Run(name, func(t *testing.T) {
			deps := setupProjectionTest(t)
			page := createPageWithFrontmatter(t, deps.tree, "Unreadable", "unreadable", "body")
			if draft {
				page = setDraftForTest(t, deps.tree, page, true)
			}
			if err := os.Remove(filepath.Join(deps.dir, "root", "unreadable.md")); err != nil {
				t.Fatalf("Remove page file: %v", err)
			}
			states := loadProjectionPages(deps.tree, []string{page.ID})
			if draft {
				if len(states) != 1 || !states[0].remove || states[0].err != nil {
					t.Fatalf("draft read-error state = %#v, want remove", states)
				}
			} else if len(states) != 1 || states[0].remove || states[0].err == nil {
				t.Fatalf("public read-error state = %#v, want preserved error", states)
			}
		})
	}
}

func TestProjectionSideEffects_Apply_ConvergesLateEventsToCurrentPage(t *testing.T) {
	t.Run("public after newer draft", func(t *testing.T) {
		deps := setupProjectionTest(t)
		raw := "---\ntags:\n  - secret\nstatus: secret\n---\n\nsecretsearchterm"
		public := createPageWithFrontmatter(t, deps.tree, "Secret", "secret", raw)
		deps.older.Run(PageSaveEvent{Operation: PageOperationCreate, After: public})
		draft := setDraftForTest(t, deps.tree, public, true)
		deps.newer.Run(PageSaveEvent{Operation: PageOperationUpdate, After: draft, DraftChanged: true, AffectedPages: []*tree.Page{draft}})
		deps.older.Run(PageSaveEvent{Operation: PageOperationUpdate, After: public})

		searchResult, err := deps.search.Search("secretsearchterm", nil, 0, 10)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		tagIDs, err := deps.tags.GetPageIDsByTags([]string{"secret"})
		if err != nil {
			t.Fatalf("GetPageIDsByTags: %v", err)
		}
		propertyIDs, err := deps.properties.GetPageIDsByProperty("status", "secret")
		if err != nil {
			t.Fatalf("GetPageIDsByProperty: %v", err)
		}
		if searchResult.Count != 0 || len(tagIDs) != 0 || len(propertyIDs) != 0 {
			t.Fatalf("late public event exposed draft projections: search=%#v tags=%v properties=%v", searchResult.Items, tagIDs, propertyIDs)
		}
	})

	tests := []struct {
		name      string
		lateEvent func(stale, current *tree.Page) PageSaveEvent
	}{
		{
			name: "old public content after newer public content",
			lateEvent: func(stale, _ *tree.Page) PageSaveEvent {
				return PageSaveEvent{Operation: PageOperationUpdate, After: stale}
			},
		},
		{
			name: "draft after newer publish",
			lateEvent: func(stale, _ *tree.Page) PageSaveEvent {
				return PageSaveEvent{Operation: PageOperationUpdate, After: stale, DraftChanged: true, AffectedPages: []*tree.Page{stale}}
			},
		},
		{
			name: "delete after newer restored page",
			lateEvent: func(stale, _ *tree.Page) PageSaveEvent {
				return PageSaveEvent{Operation: PageOperationDelete, Before: stale, AffectedPages: []*tree.Page{stale}}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps := setupProjectionTest(t)
			oldRaw := "---\ntags:\n  - oldtag\nstatus: old\n---\n\noldsearchterm"
			stale := createPageWithFrontmatter(t, deps.tree, "Projection", "projection", oldRaw)
			if tt.name == "draft after newer publish" {
				stale = setDraftForTest(t, deps.tree, stale, true)
			}

			newRaw := "---\ntags:\n  - newtag\nstatus: new\n---\n\nnewsearchterm"
			if err := deps.tree.UpdateNodeWithDraft("editor", stale.ID, stale.Title, stale.Slug, &newRaw, tree.VersionUnchecked, nil, nil, true, boolPointer(false)); err != nil {
				t.Fatalf("UpdateNodeWithDraft: %v", err)
			}
			current, err := deps.tree.GetPage(stale.ID)
			if err != nil {
				t.Fatalf("GetPage current: %v", err)
			}
			deps.newer.Run(PageSaveEvent{Operation: PageOperationUpdate, After: current})
			deps.older.Run(tt.lateEvent(stale, current))

			assertCurrentProjection(t, deps, current.ID)
		})
	}
}

func TestProjectionPageIDs_IncludesAuthoritativeIDsMissingSnapshots(t *testing.T) {
	page := &tree.Page{PageNode: &tree.PageNode{ID: "loaded"}}
	event := PageSaveEvent{
		Operation:       PageOperationMove,
		AffectedPages:   []*tree.Page{page},
		AffectedPageIDs: []string{"loaded", "read-error", "read-error", ""},
	}
	ids := projectionPageIDs(event, true)
	if len(ids) != 2 || ids[0] != "loaded" || ids[1] != "read-error" {
		t.Fatalf("projection IDs = %v, want authoritative deduplicated IDs", ids)
	}
}

func TestProjectionPageIDs_MoveSelectionMatchesProjectionNeeds(t *testing.T) {
	page := &tree.Page{PageNode: &tree.PageNode{ID: "moved"}}
	tests := []struct {
		name          string
		draftChanged  bool
		pathSensitive bool
		wantCount     int
	}{
		{name: "metadata ignores public move", wantCount: 0},
		{name: "search reindexes public move", pathSensitive: true, wantCount: 1},
		{name: "metadata reconciles visibility move", draftChanged: true, wantCount: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := PageSaveEvent{
				Operation:       PageOperationMove,
				DraftChanged:    tt.draftChanged,
				AffectedPages:   []*tree.Page{page},
				AffectedPageIDs: []string{page.ID},
			}
			if got := len(projectionPageIDs(event, tt.pathSensitive)); got != tt.wantCount {
				t.Fatalf("projection page count = %d, want %d", got, tt.wantCount)
			}
		})
	}
}

func assertCurrentProjection(t *testing.T, deps projectionTestDeps, pageID string) {
	t.Helper()
	oldSearch, err := deps.search.Search("oldsearchterm", nil, 0, 10)
	if err != nil {
		t.Fatalf("Search old: %v", err)
	}
	newSearch, err := deps.search.Search("newsearchterm", nil, 0, 10)
	if err != nil {
		t.Fatalf("Search new: %v", err)
	}
	if oldSearch.Count != 0 || newSearch.Count != 1 || newSearch.Items[0].PageID != pageID {
		t.Fatalf("search projection is stale: old=%#v new=%#v", oldSearch.Items, newSearch.Items)
	}
	oldTags, err := deps.tags.GetPageIDsByTags([]string{"oldtag"})
	if err != nil {
		t.Fatalf("GetPageIDsByTags old: %v", err)
	}
	newTags, err := deps.tags.GetPageIDsByTags([]string{"newtag"})
	if err != nil {
		t.Fatalf("GetPageIDsByTags new: %v", err)
	}
	if len(oldTags) != 0 || len(newTags) != 1 || newTags[0] != pageID {
		t.Fatalf("tag projection is stale: old=%v new=%v", oldTags, newTags)
	}
	oldProperties, err := deps.properties.GetPageIDsByProperty("status", "old")
	if err != nil {
		t.Fatalf("GetPageIDsByProperty old: %v", err)
	}
	newProperties, err := deps.properties.GetPageIDsByProperty("status", "new")
	if err != nil {
		t.Fatalf("GetPageIDsByProperty new: %v", err)
	}
	if len(oldProperties) != 0 || len(newProperties) != 1 || newProperties[0] != pageID {
		t.Fatalf("property projection is stale: old=%v new=%v", oldProperties, newProperties)
	}
}

func boolPointer(value bool) *bool {
	return &value
}
