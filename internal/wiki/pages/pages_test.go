package pages_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/perber/wiki/internal/core/assets"
	"github.com/perber/wiki/internal/core/auth"
	"github.com/perber/wiki/internal/core/revision"
	sharederrors "github.com/perber/wiki/internal/core/shared/errors"
	"github.com/perber/wiki/internal/core/tree"
	"github.com/perber/wiki/internal/favorites"
	httpmetrics "github.com/perber/wiki/internal/http/metrics"
	"github.com/perber/wiki/internal/links"
	"github.com/perber/wiki/internal/search"
	"github.com/perber/wiki/internal/test_utils"
	wikiassets "github.com/perber/wiki/internal/wiki/assets"
	"github.com/perber/wiki/internal/wiki/pages"
	"github.com/perber/wiki/internal/wiki/pagesave"
	wikirevisions "github.com/perber/wiki/internal/wiki/revisions"
)

// testDeps holds real services backed by a temporary directory.
type testDeps struct {
	storageDir string
	tree       *tree.TreeService
	slug       *tree.SlugService
	revision   *revision.Service
	links      *links.LinkService
	assets     *assets.AssetService
	favorites  *favorites.FavoritesStore
}

func newTestDeps(t *testing.T) *testDeps {
	t.Helper()
	storageDir := t.TempDir()

	treeService := tree.NewTreeService(storageDir)
	if err := treeService.LoadTree(); err != nil {
		t.Fatalf("failed to load tree: %v", err)
	}

	slugService := tree.NewSlugService()
	assetService := assets.NewAssetService(storageDir, slugService)

	linksStore, err := links.NewLinksStore(storageDir)
	if err != nil {
		t.Fatalf("failed to create links store: %v", err)
	}
	linkService := links.NewLinkService(storageDir, treeService, linksStore)

	revService := revision.NewService(
		storageDir, treeService, slog.Default(),
		revision.ServiceOptions{},
	)

	favoritesStore, err := favorites.NewFavoritesStore(storageDir)
	if err != nil {
		t.Fatalf("failed to create favorites store: %v", err)
	}
	t.Cleanup(func() {
		if err := favoritesStore.Close(); err != nil {
			t.Errorf("failed to close favorites store: %v", err)
		}
	})

	return &testDeps{
		storageDir: storageDir,
		tree:       treeService,
		slug:       slugService,
		revision:   revService,
		links:      linkService,
		assets:     assetService,
		favorites:  favoritesStore,
	}
}

func (d *testDeps) orchestrator() *pagesave.PageSaveOrchestrator {
	return pagesave.NewPageSaveOrchestrator(nil,
		pagesave.NewLinkIndexSideEffect(d.links, slog.Default(), nil),
		pagesave.NewRevisionSideEffect(d.revision, slog.Default(), nil),
	)
}

func (d *testDeps) searchOrchestrator(t *testing.T) (*search.SQLiteIndex, *pagesave.PageSaveOrchestrator) {
	t.Helper()
	index, err := search.NewSQLiteIndex(d.storageDir)
	if err != nil {
		t.Fatalf("NewSQLiteIndex: %v", err)
	}
	t.Cleanup(func() { _ = index.Close() })
	return index, pagesave.NewPageSaveOrchestrator(
		nil,
		pagesave.NewSearchIndexSideEffect(index, d.tree, slog.Default()),
		pagesave.NewLinkIndexSideEffect(d.links, slog.Default()),
		pagesave.NewRevisionSideEffect(d.revision, slog.Default()),
	)
}

type captureEffect struct {
	events []pagesave.PageSaveEvent
}

func (e *captureEffect) Apply(event pagesave.PageSaveEvent) {
	e.events = append(e.events, event)
}

type editChildOnRenameEffect struct {
	tree    *tree.TreeService
	pageID  string
	content string
	err     error
	done    bool
}

func (e *editChildOnRenameEffect) Apply(event pagesave.PageSaveEvent) {
	if e.done || event.Operation != pagesave.PageOperationUpdate || !event.SlugChanged {
		return
	}
	e.done = true
	page, err := e.tree.GetPage(e.pageID)
	if err != nil {
		e.err = err
		return
	}
	e.err = e.tree.UpdateNode("concurrent-editor", page.ID, page.Title, page.Slug, &e.content, page.Version(), nil, nil, false)
}

func pageKind() *tree.NodeKind {
	k := tree.NodeKindPage
	return &k
}

func metricsBody(t *testing.T, metrics *httpmetrics.HTTPMetrics) string {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	metrics.HTTPHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected metrics endpoint to return 200, got %d", rec.Code)
	}
	return rec.Body.String()
}

func sectionKind() *tree.NodeKind {
	k := tree.NodeKindSection
	return &k
}

// ─────────────────────────────────────────────────────────────────────────────
// CreatePageUseCase
// ─────────────────────────────────────────────────────────────────────────────

func TestCreatePageUseCase_HappyPath_Root(t *testing.T) {
	deps := newTestDeps(t)
	uc := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)

	out, err := uc.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1",
		Title:  "Home",
		Slug:   "home",
		Kind:   pageKind(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Page.Title != "Home" {
		t.Errorf("expected title 'Home', got %q", out.Page.Title)
	}
	if out.Page.Slug != "home" {
		t.Errorf("expected slug 'home', got %q", out.Page.Slug)
	}
}

func TestCreatePageUseCase_PersistsDraftBeforePostSaveEffects(t *testing.T) {
	deps := newTestDeps(t)
	effect := &captureEffect{}
	uc := pages.NewCreatePageUseCase(
		deps.tree,
		deps.slug,
		pagesave.NewPageSaveOrchestrator(nil, effect),
		slog.Default(),
	)

	out, err := uc.Execute(context.Background(), pages.CreatePageInput{
		UserID: "editor", Title: "Private Draft", Slug: "private-draft", Kind: pageKind(), Draft: true, DraftAllowed: true,
	})
	if err != nil {
		t.Fatalf("Create draft: %v", err)
	}
	if !out.Page.Draft || !strings.Contains(out.Page.RawContent, "\ndraft: true\n") {
		t.Fatalf("draft was not persisted on initial create: draft=%v raw=%q", out.Page.Draft, out.Page.RawContent)
	}
	if len(effect.events) != 1 || effect.events[0].After == nil || !effect.events[0].After.Draft {
		t.Fatalf("post-save effect observed a published page: %#v", effect.events)
	}
}

func TestCreatePageUseCase_HappyPath_WithParent(t *testing.T) {
	deps := newTestDeps(t)
	uc := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)

	parent, err := uc.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1",
		Title:  "Docs",
		Slug:   "docs",
		Kind:   pageKind(),
	})
	if err != nil {
		t.Fatalf("unexpected error creating parent: %v", err)
	}

	child, err := uc.Execute(context.Background(), pages.CreatePageInput{
		UserID:   "user1",
		ParentID: &parent.Page.ID,
		Title:    "Reference",
		Slug:     "reference",
		Kind:     pageKind(),
	})
	if err != nil {
		t.Fatalf("unexpected error creating child: %v", err)
	}
	if child.Page.Parent == nil || child.Page.Parent.ID != parent.Page.ID {
		t.Errorf("expected parent ID %q, got %v", parent.Page.ID, child.Page.Parent)
	}
}

func TestCreatePageUseCase_EmptyTitle_ReturnsValidationError(t *testing.T) {
	deps := newTestDeps(t)
	uc := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)

	_, err := uc.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1",
		Title:  "",
		Slug:   "home",
		Kind:   pageKind(),
	})
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	var ve *sharederrors.ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationErrors, got %T: %v", err, err)
	}
}

func TestCreatePageUseCase_ReservedSlug_ReturnsValidationError(t *testing.T) {
	deps := newTestDeps(t)
	uc := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)

	_, err := uc.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1",
		Title:  "Reserved",
		Slug:   "e", // too short / reserved
		Kind:   pageKind(),
	})
	if err == nil {
		t.Fatal("expected error for reserved slug, got nil")
	}
	var ve *sharederrors.ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationErrors, got %T: %v", err, err)
	}
}

func TestCreatePageUseCase_NilKind_ReturnsValidationError(t *testing.T) {
	deps := newTestDeps(t)
	uc := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)

	_, err := uc.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1",
		Title:  "Test",
		Slug:   "test",
		Kind:   nil,
	})
	if err == nil {
		t.Fatal("expected error for nil kind, got nil")
	}
}

func TestCreatePageUseCase_Section_HappyPath(t *testing.T) {
	deps := newTestDeps(t)
	uc := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)

	out, err := uc.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1",
		Title:  "Section",
		Slug:   "section",
		Kind:   sectionKind(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Page.Kind != tree.NodeKindSection {
		t.Errorf("expected kind %q, got %q", tree.NodeKindSection, out.Page.Kind)
	}
}

func TestCreatePageUseCase_DraftRequiresAuthenticatedMode(t *testing.T) {
	tests := []struct {
		name         string
		kind         *tree.NodeKind
		draftAllowed bool
	}{
		{name: "auth disabled", kind: pageKind(), draftAllowed: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps := newTestDeps(t)
			uc := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
			_, err := uc.Execute(context.Background(), pages.CreatePageInput{
				UserID: "editor", Title: "Draft", Slug: "draft", Kind: tt.kind,
				Draft: true, DraftAllowed: tt.draftAllowed,
			})
			if err == nil {
				t.Fatal("expected draft creation to be rejected")
			}
		})
	}
}

func TestCreatePageUseCase_AllowsDraftSection(t *testing.T) {
	deps := newTestDeps(t)
	uc := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	out, err := uc.Execute(context.Background(), pages.CreatePageInput{
		UserID: "editor", Title: "Draft section", Slug: "draft-section", Kind: sectionKind(), Draft: true, DraftAllowed: true,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !out.Page.Draft || out.Page.Kind != tree.NodeKindSection {
		t.Fatalf("created section = kind %q draft %v", out.Page.Kind, out.Page.Draft)
	}
}

func TestCreatePageUseCase_CreatesPersistentDraftWhenAllowed(t *testing.T) {
	deps := newTestDeps(t)
	uc := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	out, err := uc.Execute(context.Background(), pages.CreatePageInput{
		UserID: "editor", Title: "Draft", Slug: "draft", Kind: pageKind(),
		Draft: true, DraftAllowed: true,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !out.Page.Draft {
		t.Fatal("created page is not a draft")
	}
	raw, err := os.ReadFile(filepath.Join(deps.storageDir, "root", "draft.md"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(raw), "draft: true") {
		t.Fatalf("draft frontmatter missing from %q", raw)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// UpdatePageUseCase
// ─────────────────────────────────────────────────────────────────────────────

func TestUpdatePageUseCase_HappyPath(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)

	created, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Old Title", Slug: "old-title", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("unexpected error creating page: %v", err)
	}

	content := "updated content"
	out, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID:  "user1",
		ID:      created.Page.ID,
		Version: created.Page.Version(),
		Title:   "New Title",
		Slug:    "new-title",
		Content: &content})
	if err != nil {
		t.Fatalf("unexpected error updating page: %v", err)
	}
	if out.Page.Title != "New Title" {
		t.Errorf("expected title 'New Title', got %q", out.Page.Title)
	}
	if out.Page.Slug != "new-title" {
		t.Errorf("expected slug 'new-title', got %q", out.Page.Slug)
	}
}

func TestUpdatePageUseCase_DraftTransitionListsEntireSubtree(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default())
	section, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "owner", Title: "Section", Slug: "section", Kind: sectionKind(),
	})
	if err != nil {
		t.Fatalf("create section: %v", err)
	}
	if _, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "owner", ParentID: &section.Page.ID, Title: "Child", Slug: "child", Kind: pageKind(),
	}); err != nil {
		t.Fatalf("create child: %v", err)
	}
	effect := &captureEffect{}
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, pagesave.NewPageSaveOrchestrator(nil, effect), slog.Default())
	draft := true
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "owner", ID: section.Page.ID, Version: section.Page.Version(), Title: section.Page.Title, Slug: section.Page.Slug, Draft: &draft, DraftAllowed: true,
	}); err != nil {
		t.Fatalf("mark section draft: %v", err)
	}
	if len(effect.events) != 1 || !effect.events[0].DraftChanged || len(effect.events[0].AffectedPages) != 2 {
		t.Fatalf("draft event = %#v", effect.events)
	}
	if got := strings.Join(effect.events[0].AffectedTitles, ","); got != "Section,Child" {
		t.Fatalf("affected titles = %q, want Section,Child", got)
	}
	afterCount := 0
	for _, affected := range effect.events[0].AffectedPages {
		if affected != nil && effect.events[0].After != nil && affected.ID == effect.events[0].After.ID {
			afterCount++
		}
	}
	if afterCount != 1 {
		t.Fatalf("updated page appears %d times in affected pages, want exactly once: %#v", afterCount, effect.events[0])
	}
}

func TestUpdatePageUseCase_FailedDraftRenameReconcilesCurrentDraftState(t *testing.T) {
	deps := newTestDeps(t)
	index, err := search.NewSQLiteIndex(deps.storageDir)
	if err != nil {
		t.Fatalf("NewSQLiteIndex: %v", err)
	}
	t.Cleanup(func() { _ = index.Close() })
	capture := &captureEffect{}
	searchEffect := pagesave.NewSearchIndexSideEffect(index, deps.tree, slog.Default())
	orchestrator := pagesave.NewPageSaveOrchestrator(
		nil,
		searchEffect,
		pagesave.NewLinkIndexSideEffect(deps.links, slog.Default()),
		pagesave.NewRevisionSideEffect(deps.revision, slog.Default()),
		capture,
	)
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, orchestrator, slog.Default())

	targetID, err := deps.tree.CreateNode("user1", nil, "Target", "target", pageKind())
	if err != nil {
		t.Fatalf("CreateNode(target): %v", err)
	}
	sourceID, err := deps.tree.CreateNode("user1", nil, "Source", "source", pageKind())
	if err != nil {
		t.Fatalf("CreateNode(source): %v", err)
	}
	referrerID, err := deps.tree.CreateNode("user1", nil, "Referrer", "referrer", pageKind())
	if err != nil {
		t.Fatalf("CreateNode(referrer): %v", err)
	}
	content := "publiccompensationtoken [Target](/target)"
	if err := deps.tree.UpdateNode("user1", *sourceID, "Source", "source", &content, tree.VersionUnchecked, nil, nil, false); err != nil {
		t.Fatalf("UpdateNode(source): %v", err)
	}
	referrerContent := "[Source](/source)"
	if err := deps.tree.UpdateNode("user1", *referrerID, "Referrer", "referrer", &referrerContent, tree.VersionUnchecked, nil, nil, false); err != nil {
		t.Fatalf("UpdateNode(referrer): %v", err)
	}
	if err := searchEffect.IndexAllPages(); err != nil {
		t.Fatalf("IndexAllPages(search): %v", err)
	}
	if err := deps.links.IndexAllPages(); err != nil {
		t.Fatalf("IndexAllPages(links): %v", err)
	}
	if _, created, err := deps.revision.RecordContentUpdate(*sourceID, "user1", "initial"); err != nil || !created {
		t.Fatalf("RecordContentUpdate: created=%v err=%v", created, err)
	}
	beforeRevision, err := deps.revision.GetLatestRevision(*sourceID)
	if err != nil || beforeRevision == nil {
		t.Fatalf("GetLatestRevision(before): %#v, %v", beforeRevision, err)
	}
	source, err := deps.tree.GetPage(*sourceID)
	if err != nil {
		t.Fatalf("GetPage(source): %v", err)
	}
	if _, err := deps.tree.GetPage(*targetID); err != nil {
		t.Fatalf("GetPage(target): %v", err)
	}
	if err := os.WriteFile(filepath.Join(deps.storageDir, "root", "blocked.md"), []byte("occupied"), 0o644); err != nil {
		t.Fatalf("WriteFile(blocker): %v", err)
	}

	draft := true
	secret := "private compensation body"
	_, updateErr := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "user1", ID: source.ID, Version: source.Version(), Title: "Draft Source", Slug: "blocked", Content: &secret, Draft: &draft, DraftAllowed: true,
	})
	if !errors.Is(updateErr, tree.ErrPageAlreadyExists) {
		t.Fatalf("update error = %v, want ErrPageAlreadyExists", updateErr)
	}
	if len(capture.events) != 1 {
		t.Fatalf("events = %#v, want exactly one", capture.events)
	}
	event := capture.events[0]
	if !event.ReconciliationOnly || event.Operation != pagesave.PageOperationUpdate || !event.DraftChanged || event.UserID != "user1" || event.OldPath != "/source" || event.OldTitle != "Source" {
		t.Fatalf("reconciliation event = %#v", event)
	}
	if len(event.AffectedPageIDs) != 1 || event.AffectedPageIDs[0] != source.ID || len(event.AffectedTitles) != 1 || event.AffectedTitles[0] != "Source" {
		t.Fatalf("affected event fields = %#v", event)
	}
	if event.After == nil || !event.After.Draft || len(event.AffectedPages) != 1 || !event.AffectedPages[0].Draft {
		t.Fatalf("current draft snapshots = %#v", event)
	}
	searchResult, err := index.Search("publiccompensationtoken", nil, 0, 20)
	if err != nil || searchResult.Count != 0 {
		t.Fatalf("stale search result = %#v, %v", searchResult, err)
	}
	outgoing, err := deps.links.GetOutgoingLinksForPage(source.ID)
	if err != nil || outgoing.Count != 0 {
		t.Fatalf("stale outgoing links = %#v, %v", outgoing, err)
	}
	incoming, err := deps.links.GetOutgoingLinksForPage(*referrerID)
	if err != nil || incoming.Count != 1 || !incoming.Outgoings[0].Broken || incoming.Outgoings[0].ToPageID != "" {
		t.Fatalf("incoming target was not broken = %#v, %v", incoming, err)
	}
	afterRevision, err := deps.revision.GetLatestRevision(source.ID)
	if err != nil || afterRevision == nil || afterRevision.ID != beforeRevision.ID {
		t.Fatalf("revision changed: before=%#v after=%#v err=%v", beforeRevision, afterRevision, err)
	}
}

func TestUpdatePageUseCase_FailedDraftRenameDoesNotReconcileWhenPageRemainsPublic(t *testing.T) {
	deps := newTestDeps(t)
	capture := &captureEffect{}
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, pagesave.NewPageSaveOrchestrator(nil, capture), slog.Default())

	sourceID, err := deps.tree.CreateNode("user1", nil, "Source", "source", pageKind())
	if err != nil {
		t.Fatalf("CreateNode(source): %v", err)
	}
	if _, err := deps.tree.CreateNode("user1", nil, "Blocked", "blocked", pageKind()); err != nil {
		t.Fatalf("CreateNode(blocked): %v", err)
	}
	source, err := deps.tree.GetPage(*sourceID)
	if err != nil {
		t.Fatalf("GetPage(source): %v", err)
	}
	draft := true
	secret := "must not be written"
	_, updateErr := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "user1", ID: source.ID, Version: source.Version(), Title: source.Title, Slug: "blocked", Content: &secret, Draft: &draft, DraftAllowed: true,
	})
	if !errors.Is(updateErr, tree.ErrPageAlreadyExists) {
		t.Fatalf("update error = %v, want ErrPageAlreadyExists", updateErr)
	}
	if len(capture.events) != 0 {
		t.Fatalf("unexpected reconciliation events = %#v", capture.events)
	}
	current, err := deps.tree.GetPage(source.ID)
	if err != nil || current.Draft {
		t.Fatalf("current page = %#v, err=%v", current, err)
	}
}

func TestUpdatePageUseCase_PreserveFrontmatterCannotChangeSemanticDraft(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default())
	created, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "owner", Title: "Page", Slug: "page", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("create page: %v", err)
	}
	effect := &captureEffect{}
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, pagesave.NewPageSaveOrchestrator(nil, effect), slog.Default())
	raw := "---\ndraft: true\ncustom: value\n---\nBody"
	out, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "owner", ID: created.Page.ID, Version: created.Page.Version(), Title: created.Page.Title, Slug: created.Page.Slug,
		Content: &raw, PreserveFrontmatter: true,
	})
	if err != nil {
		t.Fatalf("preserve-frontmatter update: %v", err)
	}
	if out.Page.Draft || len(effect.events) != 1 || effect.events[0].DraftChanged {
		t.Fatalf("incoming raw draft changed semantics: page=%v events=%#v", out.Page.Draft, effect.events)
	}
}

func TestUpdatePageUseCase_VersionConflict_ReturnsVersionConflictError(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)

	created, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Old Title", Slug: "old-title", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("unexpected error creating page: %v", err)
	}
	staleVersion := created.Page.Version()

	firstContent := "first update"
	updated, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID:  "user1",
		ID:      created.Page.ID,
		Version: staleVersion,
		Title:   "New Title",
		Slug:    "new-title",
		Content: &firstContent})
	if err != nil {
		t.Fatalf("unexpected error applying first update: %v", err)
	}

	secondContent := "second update"
	_, err = updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID:  "user2",
		ID:      created.Page.ID,
		Version: staleVersion,
		Title:   updated.Page.Title,
		Slug:    updated.Page.Slug,
		Content: &secondContent})
	if err == nil {
		t.Fatal("expected version conflict, got nil")
	}
	if !errors.Is(err, tree.ErrVersionConflict) {
		t.Fatalf("expected tree.ErrVersionConflict, got %T: %v", err, err)
	}
}

func TestUpdatePageUseCase_VersionUncheckedSentinel_TreatedAsVersionRequired(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)

	created, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Page", Slug: "page", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("unexpected error creating page: %v", err)
	}

	content := "new content"
	_, err = updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID:  "user1",
		ID:      created.Page.ID,
		Version: tree.VersionUnchecked,
		Title:   "Page",
		Slug:    "page",
		Content: &content})
	if !errors.Is(err, tree.ErrVersionRequired) {
		t.Fatalf("expected ErrVersionRequired when sending VersionUnchecked sentinel, got %v", err)
	}
}

func TestUpdatePageUseCase_EmptyTitle_ReturnsValidationError(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)

	created, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Page", Slug: "page", Kind: pageKind(),
	})

	_, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "user1", ID: created.Page.ID, Version: created.Page.Version(), Title: "", Slug: "page"})
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
}

func TestUpdatePageUseCase_DraftTransitionMustBeAuthorized(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	effect := &captureEffect{}
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, pagesave.NewPageSaveOrchestrator(nil, effect), slog.Default(), nil)
	created, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "editor", Title: "Page", Slug: "page", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("create Execute() error = %v", err)
	}
	draft := true

	_, err = updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "editor", ID: created.Page.ID, Version: created.Page.Version(), Draft: &draft,
	})
	if err == nil {
		t.Fatal("expected auth-disabled draft transition to fail")
	}

	content := "must not be combined"
	_, err = updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "editor", ID: created.Page.ID, Version: created.Page.Version(), Draft: &draft,
		Title: "Changed", Content: &content,
	})
	if err == nil {
		t.Fatal("expected combined draft transition to fail")
	}

	out, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "editor", ID: created.Page.ID, Version: created.Page.Version(), Draft: &draft, DraftAllowed: true,
	})
	if err != nil {
		t.Fatalf("draft transition Execute() error = %v", err)
	}
	if !out.Page.Draft || out.Page.Title != "Page" {
		t.Fatalf("transitioned page = {draft:%v title:%q}", out.Page.Draft, out.Page.Title)
	}
	if len(effect.events) != 1 || effect.events[0].Before == nil || effect.events[0].Before.Draft || effect.events[0].After == nil || !effect.events[0].After.Draft {
		t.Fatalf("transition event = %+v", effect.events)
	}

	content = "edited while private"
	out, err = updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "editor", ID: out.Page.ID, Version: out.Page.Version(),
		Title: "Edited draft", Slug: out.Page.Slug, Content: &content,
		Tags: []string{"private"}, Properties: map[string]string{"status": "wip"},
	})
	if err != nil {
		t.Fatalf("ordinary draft edit Execute() error = %v", err)
	}
	if !out.Page.Draft || out.Page.Title != "Edited draft" || out.Page.Content != content {
		t.Fatalf("edited draft = {draft:%v title:%q content:%q}", out.Page.Draft, out.Page.Title, out.Page.Content)
	}
}

func TestDraftPage_AllowsCopyAndRefactorPreview(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	created, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "editor", Title: "Draft", Slug: "draft", Kind: pageKind(), Draft: true, DraftAllowed: true,
	})
	if err != nil {
		t.Fatalf("create Execute() error = %v", err)
	}

	copyUC := pages.NewCopyPageUseCase(deps.tree, deps.slug, deps.orchestrator(), deps.assets, slog.Default())
	copyOut, err := copyUC.Execute(context.Background(), pages.CopyPageInput{
		UserID: "editor", SourcePageID: created.Page.ID, Title: "Copy", Slug: "copy",
	})
	if err != nil {
		t.Fatalf("copy Execute() error = %v", err)
	}
	if !copyOut.Page.Draft {
		t.Fatal("copied draft page should remain draft")
	}

	previewUC := pages.NewPreviewPageRefactorUseCase(deps.tree, deps.slug, deps.links, slog.Default())
	if _, err = previewUC.Execute(context.Background(), pages.RefactorPreviewInput{
		PageID: created.Page.ID, Kind: pages.RefactorKindRename, Title: "Renamed", Slug: "renamed",
	}); err != nil {
		t.Fatalf("preview Execute() error = %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// DeletePageUseCase
// ─────────────────────────────────────────────────────────────────────────────

func TestDeletePageUseCase_HappyPath(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	deleteUC := pages.NewDeletePageUseCase(deps.tree, deps.revision, deps.assets, deps.favorites, deps.orchestrator(), slog.Default(), nil)

	created, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "To Delete", Slug: "to-delete", Kind: pageKind(),
	})

	if err := deleteUC.Execute(context.Background(), pages.DeletePageInput{
		UserID:    "user1",
		ID:        created.Page.ID,
		Version:   created.Page.Version(),
		Recursive: false,
	}); err != nil {
		t.Fatalf("unexpected error deleting page: %v", err)
	}

	// Verify it is gone
	if _, err := deps.tree.GetPage(created.Page.ID); !errors.Is(err, tree.ErrPageNotFound) {
		t.Errorf("expected page-not-found after delete, got %v", err)
	}
}

func TestDeletePageUseCase_Root_ReturnsError(t *testing.T) {
	deps := newTestDeps(t)
	deleteUC := pages.NewDeletePageUseCase(deps.tree, deps.revision, deps.assets, deps.favorites, deps.orchestrator(), slog.Default(), nil)

	err := deleteUC.Execute(context.Background(), pages.DeletePageInput{
		UserID: "user1", ID: "root", Recursive: false,
	})
	if err == nil {
		t.Fatal("expected error when deleting root, got nil")
	}
}

func TestDeletePageUseCase_WithChildren_Recursive(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	deleteUC := pages.NewDeletePageUseCase(deps.tree, deps.revision, deps.assets, deps.favorites, deps.orchestrator(), slog.Default(), nil)

	parent, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Parent", Slug: "parent", Kind: pageKind(),
	})
	createUC.Execute(context.Background(), pages.CreatePageInput{ //nolint:errcheck
		UserID: "user1", ParentID: &parent.Page.ID, Title: "Child", Slug: "child", Kind: pageKind(),
	})

	err := deleteUC.Execute(context.Background(), pages.DeletePageInput{
		UserID: "user1", ID: parent.Page.ID, Version: parent.Page.Version(), Recursive: true,
	})
	if err != nil {
		t.Fatalf("unexpected error on recursive delete: %v", err)
	}
}

func TestDeletePageUseCase_NonRecursive_RemovesFavoritesForPage(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	deleteUC := pages.NewDeletePageUseCase(deps.tree, deps.revision, deps.assets, deps.favorites, deps.orchestrator(), slog.Default(), nil)

	created, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "To Delete", Slug: "to-delete", Kind: pageKind(),
	})
	if err := deps.favorites.Add("user1", created.Page.ID); err != nil {
		t.Fatalf("failed to seed favorite: %v", err)
	}

	if err := deleteUC.Execute(context.Background(), pages.DeletePageInput{
		UserID: "user1", ID: created.Page.ID, Version: created.Page.Version(), Recursive: false,
	}); err != nil {
		t.Fatalf("unexpected error deleting page: %v", err)
	}

	ids, err := deps.favorites.ListPageIDsForUser("user1")
	if err != nil {
		t.Fatalf("ListPageIDsForUser: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("expected favorites for deleted page to be cleaned up, got %v", ids)
	}
}

func TestDeletePageUseCase_Recursive_RemovesFavoritesForWholeSubtree(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	deleteUC := pages.NewDeletePageUseCase(deps.tree, deps.revision, deps.assets, deps.favorites, deps.orchestrator(), slog.Default(), nil)

	parent, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Parent", Slug: "parent", Kind: pageKind(),
	})
	child, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", ParentID: &parent.Page.ID, Title: "Child", Slug: "child", Kind: pageKind(),
	})
	if err := deps.favorites.Add("user1", parent.Page.ID); err != nil {
		t.Fatalf("failed to seed favorite: %v", err)
	}
	if err := deps.favorites.Add("user1", child.Page.ID); err != nil {
		t.Fatalf("failed to seed favorite: %v", err)
	}

	if err := deleteUC.Execute(context.Background(), pages.DeletePageInput{
		UserID: "user1", ID: parent.Page.ID, Version: parent.Page.Version(), Recursive: true,
	}); err != nil {
		t.Fatalf("unexpected error on recursive delete: %v", err)
	}

	ids, err := deps.favorites.ListPageIDsForUser("user1")
	if err != nil {
		t.Fatalf("ListPageIDsForUser: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("expected favorites for deleted subtree to be cleaned up, got %v", ids)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// AddFavoriteUseCase / RemoveFavoriteUseCase / ListFavoritesUseCase
// ─────────────────────────────────────────────────────────────────────────────

func TestAddFavoriteUseCase_HappyPath(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	addFavoriteUC := pages.NewAddFavoriteUseCase(deps.tree, deps.favorites)

	created, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Page", Slug: "page", Kind: pageKind(),
	})

	if err := addFavoriteUC.Execute(context.Background(), pages.AddFavoriteInput{
		UserID: "user1", PageID: created.Page.ID,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ids, err := deps.favorites.ListPageIDsForUser("user1")
	if err != nil {
		t.Fatalf("ListPageIDsForUser: %v", err)
	}
	if len(ids) != 1 || ids[0] != created.Page.ID {
		t.Errorf("expected [%s], got %v", created.Page.ID, ids)
	}
}

func TestAddFavoriteUseCase_NonExistentPage_ReturnsError(t *testing.T) {
	deps := newTestDeps(t)
	addFavoriteUC := pages.NewAddFavoriteUseCase(deps.tree, deps.favorites)

	err := addFavoriteUC.Execute(context.Background(), pages.AddFavoriteInput{
		UserID: "user1", PageID: "does-not-exist",
	})
	if !errors.Is(err, tree.ErrPageNotFound) {
		t.Fatalf("expected ErrPageNotFound, got %v", err)
	}
}

func TestAddFavoriteUseCase_HidesDirectAndInheritedDraftsFromViewer(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	addFavoriteUC := pages.NewAddFavoriteUseCase(deps.tree, deps.favorites)
	viewer := &auth.User{ID: "viewer", Role: auth.RoleViewer}

	direct, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "editor", Title: "Direct Draft", Slug: "direct-draft", Kind: pageKind(), Draft: true, DraftAllowed: true,
	})
	if err != nil {
		t.Fatalf("create direct draft: %v", err)
	}
	parent, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "editor", Title: "Draft Parent", Slug: "draft-parent", Kind: sectionKind(), Draft: true, DraftAllowed: true,
	})
	if err != nil {
		t.Fatalf("create draft parent: %v", err)
	}
	inherited, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "editor", ParentID: &parent.Page.ID, Title: "Inherited Draft", Slug: "inherited-draft", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("create inherited draft: %v", err)
	}

	for _, pageID := range []string{direct.Page.ID, inherited.Page.ID} {
		err := addFavoriteUC.Execute(context.Background(), pages.AddFavoriteInput{
			UserID: viewer.ID, PageID: pageID, User: viewer,
		})
		if !errors.Is(err, tree.ErrPageNotFound) {
			t.Fatalf("favorite hidden draft %q error = %v, want ErrPageNotFound", pageID, err)
		}
	}
	ids, err := deps.favorites.ListPageIDsForUser(viewer.ID)
	if err != nil {
		t.Fatalf("list stored favorites: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("hidden drafts were stored as favorites: %v", ids)
	}
}

func TestAddFavoriteUseCase_Idempotent(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	addFavoriteUC := pages.NewAddFavoriteUseCase(deps.tree, deps.favorites)

	created, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Page", Slug: "page", Kind: pageKind(),
	})

	for i := 0; i < 2; i++ {
		if err := addFavoriteUC.Execute(context.Background(), pages.AddFavoriteInput{
			UserID: "user1", PageID: created.Page.ID,
		}); err != nil {
			t.Fatalf("unexpected error on attempt %d: %v", i, err)
		}
	}

	ids, err := deps.favorites.ListPageIDsForUser("user1")
	if err != nil {
		t.Fatalf("ListPageIDsForUser: %v", err)
	}
	if len(ids) != 1 {
		t.Errorf("expected exactly one favorite, got %v", ids)
	}
}

func TestRemoveFavoriteUseCase_HappyPath(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	addFavoriteUC := pages.NewAddFavoriteUseCase(deps.tree, deps.favorites)
	removeFavoriteUC := pages.NewRemoveFavoriteUseCase(deps.favorites)

	created, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Page", Slug: "page", Kind: pageKind(),
	})
	if err := addFavoriteUC.Execute(context.Background(), pages.AddFavoriteInput{
		UserID: "user1", PageID: created.Page.ID,
	}); err != nil {
		t.Fatalf("failed to seed favorite: %v", err)
	}

	if err := removeFavoriteUC.Execute(context.Background(), pages.RemoveFavoriteInput{
		UserID: "user1", PageID: created.Page.ID,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ids, err := deps.favorites.ListPageIDsForUser("user1")
	if err != nil {
		t.Fatalf("ListPageIDsForUser: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("expected no favorites, got %v", ids)
	}
}

func TestRemoveFavoriteUseCase_NotFavorited_IsNoOp(t *testing.T) {
	deps := newTestDeps(t)
	removeFavoriteUC := pages.NewRemoveFavoriteUseCase(deps.favorites)

	if err := removeFavoriteUC.Execute(context.Background(), pages.RemoveFavoriteInput{
		UserID: "user1", PageID: "never-favorited",
	}); err != nil {
		t.Fatalf("expected no error removing a non-favorited page, got %v", err)
	}
}

func TestListFavoritesUseCase_ResolvesFavoritedPages(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	addFavoriteUC := pages.NewAddFavoriteUseCase(deps.tree, deps.favorites)
	listFavoritesUC := pages.NewListFavoritesUseCase(deps.tree, deps.favorites, slog.Default())

	pageA, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Page A", Slug: "page-a", Kind: pageKind(),
	})
	createUC.Execute(context.Background(), pages.CreatePageInput{ //nolint:errcheck
		UserID: "user1", Title: "Page B", Slug: "page-b", Kind: pageKind(),
	})
	if err := addFavoriteUC.Execute(context.Background(), pages.AddFavoriteInput{
		UserID: "user1", PageID: pageA.Page.ID,
	}); err != nil {
		t.Fatalf("failed to seed favorite: %v", err)
	}

	out, err := listFavoritesUC.Execute(context.Background(), pages.ListFavoritesInput{UserID: "user1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Pages) != 1 || out.Pages[0].ID != pageA.Page.ID {
		t.Fatalf("expected only page A, got %#v", out.Pages)
	}

	// Page B was never favorited and must not leak into another user's list.
	otherUserOut, err := listFavoritesUC.Execute(context.Background(), pages.ListFavoritesInput{UserID: "user2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(otherUserOut.Pages) != 0 {
		t.Errorf("expected user2 to have no favorites, got %#v", otherUserOut.Pages)
	}
}

func TestListFavoritesUseCase_FiltersDirectAndInheritedDraftsForViewer(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	listFavoritesUC := pages.NewListFavoritesUseCase(deps.tree, deps.favorites, slog.Default())
	viewer := &auth.User{ID: "viewer", Role: auth.RoleViewer}

	publicPage, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "editor", Title: "Public", Slug: "public", Kind: pageKind(),
	})
	direct, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "editor", Title: "Direct Draft", Slug: "direct-draft", Kind: pageKind(), Draft: true, DraftAllowed: true,
	})
	parent, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "editor", Title: "Draft Parent", Slug: "draft-parent", Kind: sectionKind(), Draft: true, DraftAllowed: true,
	})
	inherited, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "editor", ParentID: &parent.Page.ID, Title: "Inherited Draft", Slug: "inherited-draft", Kind: pageKind(),
	})
	for _, pageID := range []string{publicPage.Page.ID, direct.Page.ID, inherited.Page.ID} {
		if err := deps.favorites.Add(viewer.ID, pageID); err != nil {
			t.Fatalf("seed favorite %q: %v", pageID, err)
		}
	}

	out, err := listFavoritesUC.Execute(context.Background(), pages.ListFavoritesInput{
		UserID: viewer.ID, User: viewer,
	})
	if err != nil {
		t.Fatalf("list favorites: %v", err)
	}
	if len(out.Pages) != 1 || out.Pages[0].ID != publicPage.Page.ID {
		t.Fatalf("viewer favorites = %#v, want only public page", out.Pages)
	}
}

func TestListFavoritesUseCase_SkipsStaleFavoriteForDeletedPage(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	listFavoritesUC := pages.NewListFavoritesUseCase(deps.tree, deps.favorites, slog.Default())

	created, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Page", Slug: "page", Kind: pageKind(),
	})
	// Simulate a stale row (e.g. pre-dating the delete-cascade cleanup) by
	// favoriting an id that does not resolve to a live page.
	if err := deps.favorites.Add("user1", "stale-page-id"); err != nil {
		t.Fatalf("failed to seed stale favorite: %v", err)
	}
	if err := deps.favorites.Add("user1", created.Page.ID); err != nil {
		t.Fatalf("failed to seed favorite: %v", err)
	}

	out, err := listFavoritesUC.Execute(context.Background(), pages.ListFavoritesInput{UserID: "user1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Pages) != 1 || out.Pages[0].ID != created.Page.ID {
		t.Fatalf("expected stale favorite to be silently skipped, got %#v", out.Pages)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Recursive delete snapshot failure
// ─────────────────────────────────────────────────────────────────────────────

func TestDeletePageUseCase_Recursive_RemovesAssetsWhenAffectedSnapshotReadFails(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default())
	effect := &captureEffect{}
	deleteUC := pages.NewDeletePageUseCase(deps.tree, deps.revision, deps.assets, deps.favorites, pagesave.NewPageSaveOrchestrator(nil, effect), slog.Default())

	parent, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Parent", Slug: "parent", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("CreatePage(parent): %v", err)
	}
	child, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", ParentID: &parent.Page.ID, Title: "Child", Slug: "child", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("CreatePage(child): %v", err)
	}
	assetDir := filepath.Join(deps.assets.GetAssetsDir(), child.Page.ID)
	if err := os.MkdirAll(assetDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(assetDir): %v", err)
	}
	if err := os.WriteFile(filepath.Join(assetDir, "draft.txt"), []byte("asset"), 0o644); err != nil {
		t.Fatalf("WriteFile(asset): %v", err)
	}
	if err := os.Remove(filepath.Join(deps.storageDir, "root", "parent", "child.md")); err != nil {
		t.Fatalf("Remove(child page file): %v", err)
	}

	if err := deleteUC.Execute(context.Background(), pages.DeletePageInput{
		UserID: "user1", ID: parent.Page.ID, Version: parent.Page.Version(), Recursive: true,
	}); err != nil {
		t.Fatalf("DeletePage: %v", err)
	}
	if _, err := os.Stat(assetDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("asset directory still exists: %v", err)
	}
	if len(effect.events) != 1 || strings.Join(effect.events[0].AffectedTitles, ",") != "Parent,Child" {
		t.Fatalf("delete event = %#v", effect.events)
	}
}

// MovePageUseCase
func TestMovePageUseCase_HappyPath(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	moveUC := pages.NewMovePageUseCase(deps.tree, deps.orchestrator(), slog.Default(), nil)

	parent, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Parent", Slug: "parent", Kind: pageKind(),
	})
	child, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Child", Slug: "child", Kind: pageKind(),
	})

	if err := moveUC.Execute(context.Background(), pages.MovePageInput{
		UserID:   "user1",
		ID:       child.Page.ID,
		Version:  child.Page.Version(),
		ParentID: parent.Page.ID,
	}); err != nil {
		t.Fatalf("unexpected error moving page: %v", err)
	}

	moved, err := deps.tree.GetPage(child.Page.ID)
	if err != nil {
		t.Fatalf("could not get moved page: %v", err)
	}
	if moved.Parent == nil || moved.Parent.ID != parent.Page.ID {
		t.Errorf("expected parent %q after move, got %v", parent.Page.ID, moved.Parent)
	}
}

func TestMovePageUseCase_VersionConflict_ReturnsVersionConflictError(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	moveUC := pages.NewMovePageUseCase(deps.tree, deps.orchestrator(), slog.Default(), nil)

	parentA, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Parent A", Slug: "parent-a", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("unexpected error creating parent A: %v", err)
	}
	parentB, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Parent B", Slug: "parent-b", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("unexpected error creating parent B: %v", err)
	}
	parentC, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Parent C", Slug: "parent-c", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("unexpected error creating parent C: %v", err)
	}
	child, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID:   "user1",
		ParentID: &parentA.Page.ID,
		Title:    "Child",
		Slug:     "child",
		Kind:     pageKind(),
	})
	if err != nil {
		t.Fatalf("unexpected error creating child: %v", err)
	}
	staleVersion := child.Page.Version()

	if err := moveUC.Execute(context.Background(), pages.MovePageInput{
		UserID:   "user1",
		ID:       child.Page.ID,
		Version:  staleVersion,
		ParentID: parentB.Page.ID,
	}); err != nil {
		t.Fatalf("unexpected error applying first move: %v", err)
	}

	err = moveUC.Execute(context.Background(), pages.MovePageInput{
		UserID:   "user2",
		ID:       child.Page.ID,
		Version:  staleVersion,
		ParentID: parentC.Page.ID,
	})
	if err == nil {
		t.Fatal("expected version conflict, got nil")
	}
	if !errors.Is(err, tree.ErrVersionConflict) {
		t.Fatalf("expected tree.ErrVersionConflict, got %T: %v", err, err)
	}
}

func TestMovePageUseCase_Root_ReturnsError(t *testing.T) {
	deps := newTestDeps(t)
	moveUC := pages.NewMovePageUseCase(deps.tree, deps.orchestrator(), slog.Default(), nil)

	err := moveUC.Execute(context.Background(), pages.MovePageInput{
		UserID: "user1", ID: "root", Version: "root-version", ParentID: "root",
	})
	if err == nil {
		t.Fatal("expected error when moving root, got nil")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// EnsurePathUseCase
// ─────────────────────────────────────────────────────────────────────────────

func TestEnsurePathUseCase_CreatesNewPath(t *testing.T) {
	deps := newTestDeps(t)
	uc := pages.NewEnsurePathUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default())

	out, err := uc.Execute(context.Background(), pages.EnsurePathInput{
		UserID:      "user1",
		TargetPath:  "docs/reference",
		TargetTitle: "Reference",
		Kind:        pageKind(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Page == nil {
		t.Fatal("expected page in output, got nil")
	}
}

func TestEnsurePathUseCase_PersistsFinalPageAsDraftOnCreate(t *testing.T) {
	deps := newTestDeps(t)
	uc := pages.NewEnsurePathUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default())

	out, err := uc.Execute(context.Background(), pages.EnsurePathInput{
		UserID: "editor", TargetPath: "notes/private", TargetTitle: "Private", Kind: pageKind(), Draft: true,
	})
	if err != nil {
		t.Fatalf("Ensure draft path: %v", err)
	}
	if !out.Page.Draft || !strings.Contains(out.Page.RawContent, "\ndraft: true\n") {
		t.Fatalf("final page was not created as draft: draft=%v raw=%q", out.Page.Draft, out.Page.RawContent)
	}
	lookup, err := deps.tree.LookupPagePath("notes")
	if err != nil {
		t.Fatalf("Lookup intermediate section: %v", err)
	}
	if !lookup.Exists || len(lookup.Segments) != 1 || lookup.Segments[0].ID == nil {
		t.Fatalf("intermediate section was not created: %#v", lookup)
	}
	parent, err := deps.tree.FindPageByID(*lookup.Segments[0].ID)
	if err != nil {
		t.Fatalf("Find intermediate section: %v", err)
	}
	if parent.Draft {
		t.Fatal("intermediate section unexpectedly inherited the final page draft state")
	}
}

func TestEnsurePathUseCase_ExistingPath_ReturnsExistingPage(t *testing.T) {
	deps := newTestDeps(t)
	uc := pages.NewEnsurePathUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default())

	out1, err := uc.Execute(context.Background(), pages.EnsurePathInput{
		UserID: "user1", TargetPath: "docs", TargetTitle: "Docs", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("unexpected error on first create: %v", err)
	}

	out2, err := uc.Execute(context.Background(), pages.EnsurePathInput{
		UserID: "user1", TargetPath: "docs", TargetTitle: "Docs", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("unexpected error on second ensure: %v", err)
	}
	if out1.Page.ID != out2.Page.ID {
		t.Errorf("expected same page ID, got %q vs %q", out1.Page.ID, out2.Page.ID)
	}
}

func TestEnsurePathUseCase_RejectsDraftForExistingPublishedPage(t *testing.T) {
	deps := newTestDeps(t)
	uc := pages.NewEnsurePathUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default())

	if _, err := uc.Execute(context.Background(), pages.EnsurePathInput{
		UserID: "editor", TargetPath: "published", TargetTitle: "Published", Kind: pageKind(),
	}); err != nil {
		t.Fatalf("create published page: %v", err)
	}
	_, err := uc.Execute(context.Background(), pages.EnsurePathInput{
		UserID: "editor", TargetPath: "published", TargetTitle: "Published", Kind: pageKind(), Draft: true,
	})
	var validationErr *sharederrors.ValidationErrors
	if !errors.As(err, &validationErr) || len(validationErr.Errors) != 1 || validationErr.Errors[0].Field != "draft" {
		t.Fatalf("error = %#v, want draft validation error", err)
	}
}

func TestEnsurePathUseCase_EmptyPath_ReturnsValidationError(t *testing.T) {
	deps := newTestDeps(t)
	uc := pages.NewEnsurePathUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default())

	_, err := uc.Execute(context.Background(), pages.EnsurePathInput{
		UserID: "user1", TargetPath: "", TargetTitle: "Title", Kind: pageKind(),
	})
	if err == nil {
		t.Fatal("expected validation error for empty path, got nil")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// GetPageUseCase
// ─────────────────────────────────────────────────────────────────────────────

func TestGetPageUseCase_HappyPath(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	getUC := pages.NewGetPageUseCase(deps.tree)

	created, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Home", Slug: "home", Kind: pageKind(),
	})

	out, err := getUC.Execute(context.Background(), pages.GetPageInput{ID: created.Page.ID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Page.ID != created.Page.ID {
		t.Errorf("expected ID %q, got %q", created.Page.ID, out.Page.ID)
	}
}

func TestGetPageUseCase_NotFound_ReturnsError(t *testing.T) {
	deps := newTestDeps(t)
	getUC := pages.NewGetPageUseCase(deps.tree)

	_, err := getUC.Execute(context.Background(), pages.GetPageInput{ID: "nonexistent"})
	if err == nil {
		t.Fatal("expected error for non-existent page, got nil")
	}
}

func TestCreatePageUseCase_ReservedHistorySlug_ReturnsValidationError(t *testing.T) {
	deps := newTestDeps(t)
	uc := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)

	_, err := uc.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1",
		Title:  "Reserved",
		Slug:   "history",
		Kind:   pageKind(),
	})
	if err == nil {
		t.Fatal("expected error for reserved history slug, got nil")
	}
}

func TestCreatePageUseCase_PageExists_ReturnsError(t *testing.T) {
	deps := newTestDeps(t)
	uc := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)

	if _, err := uc.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Duplicate", Slug: "duplicate", Kind: pageKind(),
	}); err != nil {
		t.Fatalf("unexpected error creating initial page: %v", err)
	}

	_, err := uc.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Duplicate", Slug: "duplicate", Kind: pageKind(),
	})
	if err == nil {
		t.Fatal("expected duplicate page error, got nil")
	}
}

func TestCreatePageUseCase_InvalidParent_ReturnsError(t *testing.T) {
	deps := newTestDeps(t)
	uc := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	invalidID := "not-real"

	_, err := uc.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", ParentID: &invalidID, Title: "Broken", Slug: "broken", Kind: pageKind(),
	})
	if err == nil {
		t.Fatal("expected invalid parent error, got nil")
	}
}

func TestCreatePageUseCase_RejectsCaseInsensitiveSlugConflict(t *testing.T) {
	deps := newTestDeps(t)
	uc := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)

	if _, err := uc.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Upper", Slug: "ABCD-efg", Kind: pageKind(),
	}); err != nil {
		t.Fatalf("unexpected error creating initial page: %v", err)
	}

	_, err := uc.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Lower", Slug: "abcd-efg", Kind: pageKind(),
	})
	if err == nil {
		t.Fatal("expected conflict for case-insensitive duplicate slug")
	}
}

func TestCreatePageUseCase_RecordsPageCreatedRevision(t *testing.T) {
	deps := newTestDeps(t)
	uc := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)

	out, err := uc.Execute(context.Background(), pages.CreatePageInput{
		UserID: "editor", Title: "My Page", Slug: "my-page", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	latest, err := deps.revision.GetLatestRevision(out.Page.ID)
	if err != nil {
		t.Fatalf("GetLatestRevision failed: %v", err)
	}
	if latest == nil {
		t.Fatal("expected latest revision, got nil")
	}
	if latest.Type != revision.RevisionTypeContentUpdate {
		t.Fatalf("revision type = %q, want %q", latest.Type, revision.RevisionTypeContentUpdate)
	}
	if latest.Summary != "page created" {
		t.Fatalf("revision summary = %q, want %q", latest.Summary, "page created")
	}
	if latest.AuthorID != "editor" {
		t.Fatalf("revision authorID = %q, want %q", latest.AuthorID, "editor")
	}
}

func TestUpdatePageUseCase_AllowsUppercaseSlug(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)

	created, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Original", Slug: "original", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("unexpected error creating page: %v", err)
	}

	content := "# Updated"
	out, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID:  "user1",
		ID:      created.Page.ID,
		Version: created.Page.Version(),
		Title:   "Original",
		Slug:    "ABCD-efg",
		Content: &content})
	if err != nil {
		t.Fatalf("expected uppercase slug update to succeed, got %v", err)
	}
	if out.Page.Slug != "ABCD-efg" {
		t.Fatalf("expected slug to be preserved, got %q", out.Page.Slug)
	}
}

func TestDeletePageUseCase_EmptyID_ReturnsError(t *testing.T) {
	deps := newTestDeps(t)
	deleteUC := pages.NewDeletePageUseCase(deps.tree, deps.revision, deps.assets, deps.favorites, deps.orchestrator(), slog.Default(), nil)

	err := deleteUC.Execute(context.Background(), pages.DeletePageInput{
		UserID: "user1", ID: "", Recursive: false,
	})
	if err == nil {
		t.Fatal("expected error when deleting empty page ID, got nil")
	}
}

func TestFindByPathUseCase_HappyPath(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	findUC := pages.NewFindByPathUseCase(deps.tree)

	if _, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Company", Slug: "company", Kind: pageKind(),
	}); err != nil {
		t.Fatalf("unexpected error creating page: %v", err)
	}

	out, err := findUC.Execute(context.Background(), pages.FindByPathInput{RoutePath: "company"})
	if err != nil {
		t.Fatalf("unexpected error finding page: %v", err)
	}
	if out.Page.Slug != "company" {
		t.Errorf("expected slug 'company', got %q", out.Page.Slug)
	}
}

func TestFindByPathUseCase_NotFound_ReturnsError(t *testing.T) {
	deps := newTestDeps(t)
	findUC := pages.NewFindByPathUseCase(deps.tree)

	_, err := findUC.Execute(context.Background(), pages.FindByPathInput{RoutePath: "does/not/exist"})
	if err == nil {
		t.Fatal("expected error for invalid path, got nil")
	}
}

func TestSortPagesUseCase_HappyPath(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	sortUC := pages.NewSortPagesUseCase(deps.tree)

	parent, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Parent", Slug: "parent", Kind: pageKind(),
	})
	child1, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", ParentID: &parent.Page.ID, Title: "Child1", Slug: "child1", Kind: pageKind(),
	})
	child2, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", ParentID: &parent.Page.ID, Title: "Child2", Slug: "child2", Kind: pageKind(),
	})

	if err := sortUC.Execute(context.Background(), pages.SortPagesInput{
		ParentID: parent.Page.ID, OrderedIDs: []string{child2.Page.ID, child1.Page.ID},
	}); err != nil {
		t.Fatalf("unexpected error sorting pages: %v", err)
	}

	sortedParent, err := deps.tree.GetPage(parent.Page.ID)
	if err != nil {
		t.Fatalf("failed to reload parent: %v", err)
	}
	if len(sortedParent.Children) != 2 {
		t.Fatalf("expected 2 children, got %d", len(sortedParent.Children))
	}
	if sortedParent.Children[0].ID != child2.Page.ID || sortedParent.Children[1].ID != child1.Page.ID {
		t.Errorf("expected order [%s, %s], got [%s, %s]", child2.Page.ID, child1.Page.ID, sortedParent.Children[0].ID, sortedParent.Children[1].ID)
	}
}

func TestSuggestSlugUseCase_Unique(t *testing.T) {
	deps := newTestDeps(t)
	uc := pages.NewSuggestSlugUseCase(deps.tree, deps.slug)

	out, err := uc.Execute(context.Background(), pages.SuggestSlugInput{
		ParentID: "root",
		Title:    "My Page",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Slug != "my-page" {
		t.Errorf("expected 'my-page', got %q", out.Slug)
	}
}

func TestSuggestSlugUseCase_Conflict(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	uc := pages.NewSuggestSlugUseCase(deps.tree, deps.slug)

	if _, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "My Page", Slug: "my-page", Kind: pageKind(),
	}); err != nil {
		t.Fatalf("unexpected error creating page: %v", err)
	}

	out, err := uc.Execute(context.Background(), pages.SuggestSlugInput{
		ParentID: deps.tree.GetTree().ID,
		Title:    "My Page",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Slug != "my-page-1" {
		t.Errorf("expected 'my-page-1', got %q", out.Slug)
	}
}

func TestSuggestSlugUseCase_DeepHierarchy(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	uc := pages.NewSuggestSlugUseCase(deps.tree, deps.slug)

	arch, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Architecture", Slug: "architecture", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("unexpected error creating architecture: %v", err)
	}
	backend, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", ParentID: &arch.Page.ID, Title: "Backend", Slug: "backend", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("unexpected error creating backend: %v", err)
	}

	out, err := uc.Execute(context.Background(), pages.SuggestSlugInput{
		ParentID: backend.Page.ID,
		Title:    "Data Layer",
	})
	if err != nil {
		t.Fatalf("unexpected error suggesting slug: %v", err)
	}
	if out.Slug != "data-layer" {
		t.Errorf("expected 'data-layer', got %q", out.Slug)
	}

	if _, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", ParentID: &backend.Page.ID, Title: "Data Layer", Slug: "data-layer", Kind: pageKind(),
	}); err != nil {
		t.Fatalf("unexpected error creating duplicate title page: %v", err)
	}

	out2, err := uc.Execute(context.Background(), pages.SuggestSlugInput{
		ParentID: backend.Page.ID,
		Title:    "Data Layer",
	})
	if err != nil {
		t.Fatalf("unexpected error suggesting second slug: %v", err)
	}
	if out2.Slug != "data-layer-1" {
		t.Errorf("expected 'data-layer-1', got %q", out2.Slug)
	}
}

func TestCopyPageUseCase_HappyPath(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	copyUC := pages.NewCopyPageUseCase(deps.tree, deps.slug, deps.orchestrator(), deps.assets, slog.Default())

	original, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Original", Slug: "original", Kind: pageKind(),
	})

	out, err := copyUC.Execute(context.Background(), pages.CopyPageInput{
		UserID: "user1", SourcePageID: original.Page.ID, Title: "Copy of Original", Slug: "copy-of-original",
	})
	if err != nil {
		t.Fatalf("unexpected error copying page: %v", err)
	}
	if out.Page.Title != "Copy of Original" {
		t.Errorf("expected title 'Copy of Original', got %q", out.Page.Title)
	}
	if out.Page.Slug != "copy-of-original" {
		t.Errorf("expected slug 'copy-of-original', got %q", out.Page.Slug)
	}
	if out.Page.ID == original.Page.ID {
		t.Error("expected copied page to have a different ID")
	}
}

func TestCopyPageUseCase_PreservesDraftVisibility(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default())
	effect := &captureEffect{}
	copyUC := pages.NewCopyPageUseCase(
		deps.tree,
		deps.slug,
		pagesave.NewPageSaveOrchestrator(nil, effect),
		deps.assets,
		slog.Default(),
	)
	original, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "owner", Title: "Draft", Slug: "draft", Kind: pageKind(), Draft: true, DraftAllowed: true,
	})
	if err != nil {
		t.Fatalf("create draft source: %v", err)
	}

	out, err := copyUC.Execute(context.Background(), pages.CopyPageInput{
		UserID: "copier", SourcePageID: original.Page.ID, Title: "Draft Copy", Slug: "draft-copy",
	})
	if err != nil {
		t.Fatalf("copy draft: %v", err)
	}
	if !out.Page.Draft || out.Page.Metadata.CreatorID != "copier" {
		t.Fatalf("copy visibility/owner = draft:%v creator:%q", out.Page.Draft, out.Page.Metadata.CreatorID)
	}
	if len(effect.events) != 1 || effect.events[0].After == nil || !effect.events[0].After.Draft {
		t.Fatalf("copy create event observed a published page: %#v", effect.events)
	}
}

func TestCopyPageUseCase_WithParent(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	copyUC := pages.NewCopyPageUseCase(deps.tree, deps.slug, deps.orchestrator(), deps.assets, slog.Default())

	parent, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Parent", Slug: "parent", Kind: pageKind(),
	})
	original, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Original", Slug: "original", Kind: pageKind(),
	})

	out, err := copyUC.Execute(context.Background(), pages.CopyPageInput{
		UserID: "user1", SourcePageID: original.Page.ID, TargetParentID: &parent.Page.ID, Title: "Copy of Original", Slug: "copy-of-original",
	})
	if err != nil {
		t.Fatalf("unexpected error copying page: %v", err)
	}
	if out.Page.Parent == nil || out.Page.Parent.ID != parent.Page.ID {
		t.Errorf("expected parent ID %q, got %v", parent.Page.ID, out.Page.Parent)
	}
}

func TestCopyPageUseCase_NonExistentSource_ReturnsError(t *testing.T) {
	deps := newTestDeps(t)
	copyUC := pages.NewCopyPageUseCase(deps.tree, deps.slug, deps.orchestrator(), deps.assets, slog.Default())

	_, err := copyUC.Execute(context.Background(), pages.CopyPageInput{
		UserID: "user1", SourcePageID: "non-existent-id", Title: "Copy", Slug: "copy",
	})
	if err == nil {
		t.Fatal("expected error for non-existent source page, got nil")
	}
}

func TestCopyPageUseCase_WithAssets(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	copyUC := pages.NewCopyPageUseCase(deps.tree, deps.slug, deps.orchestrator(), deps.assets, slog.Default())

	original, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Original", Slug: "original", Kind: pageKind(),
	})

	file, _, err := test_utils.CreateMultipartFile("image.png", []byte("image content"))
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	defer test_utils.WrapCloseWithErrorCheck(file.Close, t)

	if _, err := deps.assets.SaveAssetForPage(original.Page.PageNode, file, "image.png", 1024); err != nil {
		t.Fatalf("Failed to save asset for original page: %v", err)
	}

	out, err := copyUC.Execute(context.Background(), pages.CopyPageInput{
		UserID: "user1", SourcePageID: original.Page.ID, Title: "Copy of Original", Slug: "copy-of-original",
	})
	if err != nil {
		t.Fatalf("unexpected error copying page: %v", err)
	}

	copiedAssets, err := deps.assets.ListAssetsForPage(out.Page.PageNode)
	if err != nil {
		t.Fatalf("Failed to list assets for copied page: %v", err)
	}
	if len(copiedAssets) != 1 {
		t.Errorf("expected 1 asset for copied page, got %d", len(copiedAssets))
	}
}

func TestCopyPageUseCase_RecordsContentRevision(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	copyUC := pages.NewCopyPageUseCase(deps.tree, deps.slug, deps.orchestrator(), deps.assets, slog.Default())

	original, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Original", Slug: "original", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("unexpected error creating page: %v", err)
	}

	content := "original content"
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "user1", ID: original.Page.ID, Version: original.Page.Version(), Title: original.Page.Title, Slug: original.Page.Slug, Content: &content}); err != nil {
		t.Fatalf("unexpected error updating page: %v", err)
	}

	out, err := copyUC.Execute(context.Background(), pages.CopyPageInput{
		UserID: "editor", SourcePageID: original.Page.ID, Title: "Copy of Original", Slug: "copy-of-original",
	})
	if err != nil {
		t.Fatalf("unexpected error copying page: %v", err)
	}

	latest, err := deps.revision.GetLatestRevision(out.Page.ID)
	if err != nil {
		t.Fatalf("GetLatestRevision failed: %v", err)
	}
	if latest == nil {
		t.Fatal("expected latest revision for copied page")
	}
	if latest.Type != revision.RevisionTypeContentUpdate {
		t.Fatalf("latest revision type = %q, want %q", latest.Type, revision.RevisionTypeContentUpdate)
	}
	if latest.AuthorID != "editor" {
		t.Fatalf("latest author = %q, want %q", latest.AuthorID, "editor")
	}
	if latest.Summary != "page copied" {
		t.Fatalf("latest summary = %q, want %q", latest.Summary, "page copied")
	}
}

func TestCopyPageUseCase_IndexesOutgoingLinksOnCreate(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	copyUC := pages.NewCopyPageUseCase(deps.tree, deps.slug, deps.orchestrator(), deps.assets, slog.Default())

	target, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Target", Slug: "target", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("unexpected error creating target page: %v", err)
	}

	original, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Original", Slug: "original", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("unexpected error creating source page: %v", err)
	}

	content := "Links: [Target](/target)"
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "user1", ID: original.Page.ID, Version: original.Page.Version(), Title: original.Page.Title, Slug: original.Page.Slug, Content: &content}); err != nil {
		t.Fatalf("unexpected error updating source page: %v", err)
	}

	out, err := copyUC.Execute(context.Background(), pages.CopyPageInput{
		UserID: "user1", SourcePageID: original.Page.ID, Title: "Copy", Slug: "copy",
	})
	if err != nil {
		t.Fatalf("unexpected error copying page: %v", err)
	}

	outgoing, err := deps.links.GetOutgoingLinksForPage(out.Page.ID)
	if err != nil {
		t.Fatalf("GetOutgoingLinksForPage failed: %v", err)
	}
	if outgoing.Count != 1 {
		t.Fatalf("expected 1 outgoing link on copied page, got %d", outgoing.Count)
	}
	if outgoing.Outgoings[0].ToPageID != target.Page.ID {
		t.Fatalf("expected copied page link target %q, got %q", target.Page.ID, outgoing.Outgoings[0].ToPageID)
	}
}

func TestUpdatePageUseCase_EventBeforeIsDetachedFromUpdatedNode(t *testing.T) {
	deps := newTestDeps(t)
	effect := &captureEffect{}
	orchestrator := pagesave.NewPageSaveOrchestrator(nil, effect)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, orchestrator, slog.Default(), nil)
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, orchestrator, slog.Default(), nil)

	created, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Old", Slug: "old", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("unexpected error creating page: %v", err)
	}

	content := "updated"
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "user1", ID: created.Page.ID, Version: created.Page.Version(), Title: "New", Slug: "new", Content: &content}); err != nil {
		t.Fatalf("unexpected error updating page: %v", err)
	}

	if len(effect.events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(effect.events))
	}
	event := effect.events[1]
	if event.Operation != pagesave.PageOperationUpdate {
		t.Fatalf("expected update event, got %q", event.Operation)
	}
	if event.Before == nil || event.Before.Title != "Old" || event.Before.Slug != "old" {
		t.Fatalf("Before = %#v, want detached old page", event.Before)
	}
	if event.OldPath != "/old" {
		t.Fatalf("expected OldPath=/old, got %q", event.OldPath)
	}
}

func TestMovePageUseCase_EventBeforeIsOmittedForLiveNodeSafety(t *testing.T) {
	deps := newTestDeps(t)
	effect := &captureEffect{}
	orchestrator := pagesave.NewPageSaveOrchestrator(nil, effect)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, orchestrator, slog.Default(), nil)
	moveUC := pages.NewMovePageUseCase(deps.tree, orchestrator, slog.Default(), nil)

	parentA, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "A", Slug: "a", Kind: sectionKind(),
	})
	if err != nil {
		t.Fatalf("unexpected error creating parent A: %v", err)
	}
	parentB, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "B", Slug: "b", Kind: sectionKind(),
	})
	if err != nil {
		t.Fatalf("unexpected error creating parent B: %v", err)
	}
	child, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", ParentID: &parentA.Page.ID, Title: "Child", Slug: "child", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("unexpected error creating child: %v", err)
	}

	if err := moveUC.Execute(context.Background(), pages.MovePageInput{
		UserID: "user1", ID: child.Page.ID, Version: child.Page.Version(), ParentID: parentB.Page.ID,
	}); err != nil {
		t.Fatalf("unexpected error moving page: %v", err)
	}

	if len(effect.events) != 4 {
		t.Fatalf("expected 4 events, got %d", len(effect.events))
	}
	event := effect.events[3]
	if event.Operation != pagesave.PageOperationMove {
		t.Fatalf("expected move event, got %q", event.Operation)
	}
	if event.Before != nil {
		t.Fatal("expected Before to be omitted for move events")
	}
	if event.OldPath != "/a/child" {
		t.Fatalf("expected OldPath=/a/child, got %q", event.OldPath)
	}
	if got := strings.Join(event.AffectedTitles, ","); got != "Child" {
		t.Fatalf("affected titles = %q, want Child", got)
	}
}

func TestMovePageUseCase_ReportsEffectiveDraftVisibilityChange(t *testing.T) {
	deps := newTestDeps(t)
	effect := &captureEffect{}
	orchestrator := pagesave.NewPageSaveOrchestrator(nil, effect)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, orchestrator, slog.Default())
	moveUC := pages.NewMovePageUseCase(deps.tree, orchestrator, slog.Default())

	draftParent, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Draft Parent", Slug: "draft-parent", Kind: sectionKind(), Draft: true, DraftAllowed: true,
	})
	if err != nil {
		t.Fatalf("create draft parent: %v", err)
	}
	publicPage, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Public", Slug: "public", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("create public page: %v", err)
	}

	if err := moveUC.Execute(context.Background(), pages.MovePageInput{
		UserID: "user1", ID: publicPage.Page.ID, Version: publicPage.Page.Version(), ParentID: draftParent.Page.ID,
	}); err != nil {
		t.Fatalf("move public page into draft: %v", err)
	}
	event := effect.events[len(effect.events)-1]
	if !event.DraftChanged {
		t.Fatal("move into a draft ancestor did not report effective visibility change")
	}
	if len(event.AffectedPages) != 1 || event.AffectedPages[0].Parent == nil || !event.AffectedPages[0].Parent.Draft {
		t.Fatalf("affected page did not retain its draft ancestor snapshot: %#v", event.AffectedPages)
	}
}

func TestUpdatePageUseCase_RejectsAncestorPathDriftWithoutSideEffects(t *testing.T) {
	deps := newTestDeps(t)
	ancestorID, err := deps.tree.CreateNode("user1", nil, "Ancestor", "ancestor", sectionKind())
	if err != nil {
		t.Fatalf("create ancestor: %v", err)
	}
	sourceID, err := deps.tree.CreateNode("user1", ancestorID, "Source", "source", pageKind())
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	containerID, err := deps.tree.CreateNode("user1", nil, "Container", "container", sectionKind())
	if err != nil {
		t.Fatalf("create container: %v", err)
	}
	source, err := deps.tree.GetPage(*sourceID)
	if err != nil {
		t.Fatalf("get source: %v", err)
	}
	ancestor, err := deps.tree.GetPage(*ancestorID)
	if err != nil {
		t.Fatalf("get ancestor: %v", err)
	}
	if err := deps.tree.MoveNode("other", ancestor.ID, *containerID, ancestor.Version()); err != nil {
		t.Fatalf("move ancestor: %v", err)
	}

	effect := &captureEffect{}
	uc := pages.NewUpdatePageUseCase(deps.tree, deps.slug, pagesave.NewPageSaveOrchestrator(nil, effect), slog.Default())
	_, err = uc.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "user1", ID: source.ID, Version: source.Version(), Title: "Renamed", Slug: "renamed", PathPreconditions: &tree.PathPreconditions{ExpectedSourcePath: source.CalculatePath()},
	})
	if !errors.Is(err, tree.ErrVersionConflict) {
		t.Fatalf("expected path version conflict, got %v", err)
	}
	if len(effect.events) != 0 {
		t.Fatalf("path drift emitted side effects: %#v", effect.events)
	}
	after, err := deps.tree.GetPage(source.ID)
	if err != nil {
		t.Fatalf("get source after conflict: %v", err)
	}
	if after.Slug != "source" || after.CalculatePath() != "/container/ancestor/source" {
		t.Fatalf("path drift mutated source: slug=%q path=%q", after.Slug, after.CalculatePath())
	}
}

func TestUpdatePageUseCase_FinalSyncFailureRollsBackWithoutSideEffects(t *testing.T) {
	deps := newTestDeps(t)
	id, err := deps.tree.CreateNode("owner", nil, "Section", "section", sectionKind())
	if err != nil {
		t.Fatalf("create section: %v", err)
	}
	before, err := deps.tree.GetPage(*id)
	if err != nil {
		t.Fatalf("get section before update: %v", err)
	}

	oldDir := filepath.Join(deps.storageDir, "root", "section")
	newDir := filepath.Join(deps.storageDir, "root", "renamed")
	indexPath := filepath.Join(oldDir, "index.md")
	if err := os.Remove(indexPath); err != nil {
		t.Fatalf("remove section index: %v", err)
	}
	if err := os.Mkdir(indexPath, 0o755); err != nil {
		t.Fatalf("create index path collision: %v", err)
	}

	effect := &captureEffect{}
	uc := pages.NewUpdatePageUseCase(deps.tree, deps.slug, pagesave.NewPageSaveOrchestrator(nil, effect), slog.Default())
	draft := true
	_, err = uc.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "editor", ID: before.ID, Version: before.Version(), Title: "Renamed", Slug: "renamed", Draft: &draft, DraftAllowed: true,
	})
	if err == nil {
		t.Fatal("update unexpectedly succeeded")
	}
	if len(effect.events) != 0 {
		t.Fatalf("failed update emitted side effects: %#v", effect.events)
	}
	after, err := deps.tree.SnapshotPageNode(before.ID)
	if err != nil {
		t.Fatalf("snapshot section after update: %v", err)
	}
	if after.Title != before.Title || after.Slug != before.Slug || after.Draft != before.Draft || after.Version() != before.Version() {
		t.Fatalf("failed update changed live state: before=%#v after=%#v", before.PageNode, after)
	}
	if _, err := os.Stat(oldDir); err != nil {
		t.Fatalf("original section was not restored: %v", err)
	}
	if _, err := os.Stat(newDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("renamed section still exists: %v", err)
	}
}

func TestMovePageUseCase_RejectsAncestorPathDriftWithoutSideEffects(t *testing.T) {
	for _, driftDestination := range []bool{false, true} {
		name := "source ancestor"
		if driftDestination {
			name = "destination ancestor"
		}
		t.Run(name, func(t *testing.T) {
			deps := newTestDeps(t)
			sourceAncestorID, err := deps.tree.CreateNode("user1", nil, "Source Ancestor", "source-ancestor", sectionKind())
			if err != nil {
				t.Fatalf("create source ancestor: %v", err)
			}
			sourceID, err := deps.tree.CreateNode("user1", sourceAncestorID, "Source", "source", pageKind())
			if err != nil {
				t.Fatalf("create source: %v", err)
			}
			destinationAncestorID, err := deps.tree.CreateNode("user1", nil, "Destination Ancestor", "destination-ancestor", sectionKind())
			if err != nil {
				t.Fatalf("create destination ancestor: %v", err)
			}
			destinationID, err := deps.tree.CreateNode("user1", destinationAncestorID, "Destination", "destination", sectionKind())
			if err != nil {
				t.Fatalf("create destination: %v", err)
			}
			containerID, err := deps.tree.CreateNode("user1", nil, "Container", "container", sectionKind())
			if err != nil {
				t.Fatalf("create container: %v", err)
			}
			source, err := deps.tree.GetPage(*sourceID)
			if err != nil {
				t.Fatalf("get source: %v", err)
			}
			destination, err := deps.tree.GetPage(*destinationID)
			if err != nil {
				t.Fatalf("get destination: %v", err)
			}
			driftedID := sourceAncestorID
			if driftDestination {
				driftedID = destinationAncestorID
			}
			drifted, err := deps.tree.GetPage(*driftedID)
			if err != nil {
				t.Fatalf("get drifted ancestor: %v", err)
			}
			if err := deps.tree.MoveNode("other", drifted.ID, *containerID, drifted.Version()); err != nil {
				t.Fatalf("move ancestor: %v", err)
			}

			effect := &captureEffect{}
			uc := pages.NewMovePageUseCase(deps.tree, pagesave.NewPageSaveOrchestrator(nil, effect), slog.Default())
			err = uc.Execute(context.Background(), pages.MovePageInput{
				UserID: "user1", ID: source.ID, Version: source.Version(), ParentID: destination.ID,
				PathPreconditions: &tree.PathPreconditions{
					ExpectedSourcePath:            source.CalculatePath(),
					ExpectedDestinationParentPath: destination.CalculatePath(),
				},
			})
			if !errors.Is(err, tree.ErrVersionConflict) {
				t.Fatalf("expected path version conflict, got %v", err)
			}
			if len(effect.events) != 0 {
				t.Fatalf("path drift emitted side effects: %#v", effect.events)
			}
			after, err := deps.tree.GetPage(source.ID)
			if err != nil {
				t.Fatalf("get source after conflict: %v", err)
			}
			if after.Parent == nil || after.Parent.ID != *sourceAncestorID {
				t.Fatalf("path drift moved source: %#v", after.Parent)
			}
		})
	}
}

func TestMovePageUseCase_PathPreconditionsAcceptRootDestination(t *testing.T) {
	deps := newTestDeps(t)
	ancestorID, err := deps.tree.CreateNode("user1", nil, "Ancestor", "ancestor", sectionKind())
	if err != nil {
		t.Fatalf("create ancestor: %v", err)
	}
	sourceID, err := deps.tree.CreateNode("user1", ancestorID, "Source", "source", pageKind())
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	source, err := deps.tree.GetPage(*sourceID)
	if err != nil {
		t.Fatalf("get source: %v", err)
	}
	effect := &captureEffect{}
	uc := pages.NewMovePageUseCase(deps.tree, pagesave.NewPageSaveOrchestrator(nil, effect), slog.Default())
	if err := uc.Execute(context.Background(), pages.MovePageInput{
		UserID: "user1", ID: source.ID, Version: source.Version(), ParentID: "root",
		PathPreconditions: &tree.PathPreconditions{
			ExpectedSourcePath:            source.CalculatePath(),
			ExpectedDestinationParentPath: "",
		},
	}); err != nil {
		t.Fatalf("move to root: %v", err)
	}
	after, err := deps.tree.GetPage(source.ID)
	if err != nil {
		t.Fatalf("get source after move: %v", err)
	}
	if after.CalculatePath() != "/source" || len(effect.events) != 1 {
		t.Fatalf("root move mismatch: path=%q events=%d", after.CalculatePath(), len(effect.events))
	}
}

func TestApplyPageRefactorUseCase_MoveUsesInjectedPageOrchestrator(t *testing.T) {
	deps := newTestDeps(t)
	effect := &captureEffect{}
	orchestrator := pagesave.NewPageSaveOrchestrator(nil, effect)
	applyUC := pages.NewApplyPageRefactorUseCase(deps.tree, deps.slug, deps.links, orchestrator, slog.Default())

	draftParentID, err := deps.tree.CreateNodeWithDraft("user1", nil, "Draft Parent", "draft-parent", sectionKind(), true)
	if err != nil {
		t.Fatalf("create draft parent: %v", err)
	}
	pageID, err := deps.tree.CreateNode("user1", nil, "Public", "public", pageKind())
	if err != nil {
		t.Fatalf("create public page: %v", err)
	}
	page, err := deps.tree.GetPage(*pageID)
	if err != nil {
		t.Fatalf("get public page: %v", err)
	}

	if _, err := applyUC.Execute(context.Background(), pages.RefactorApplyInput{
		UserID: "user1", Version: page.Version(),
		RefactorPreviewInput: pages.RefactorPreviewInput{
			PageID: page.ID, Kind: pages.RefactorKindMove, NewParentID: draftParentID,
		},
	}); err != nil {
		t.Fatalf("apply refactor move: %v", err)
	}
	if len(effect.events) != 1 || effect.events[0].Operation != pagesave.PageOperationMove || !effect.events[0].DraftChanged {
		t.Fatalf("refactor move did not use injected orchestrator: %#v", effect.events)
	}
}

func TestApplyPageRefactorUseCase_FailedRenameDoesNotRewriteIncomingPages(t *testing.T) {
	for _, collision := range []bool{false, true} {
		name := "stale version"
		if collision {
			name = "slug collision"
		}
		t.Run(name, func(t *testing.T) {
			deps := newTestDeps(t)
			index, orchestrator := deps.searchOrchestrator(t)
			createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, orchestrator, slog.Default())
			updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, orchestrator, slog.Default())
			applyUC := pages.NewApplyPageRefactorUseCase(deps.tree, deps.slug, deps.links, orchestrator, slog.Default())

			target, err := createUC.Execute(context.Background(), pages.CreatePageInput{
				UserID: "user1", Title: "Stable Target", Slug: "stabletargettoken", Kind: pageKind(),
			})
			if err != nil {
				t.Fatalf("create target: %v", err)
			}
			ref, err := createUC.Execute(context.Background(), pages.CreatePageInput{
				UserID: "user1", Title: "Referrer", Slug: "referrer", Kind: pageKind(),
			})
			if err != nil {
				t.Fatalf("create referrer: %v", err)
			}
			content := "[[Stable Target]] and [Target](/stabletargettoken)"
			if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
				UserID: "user1", ID: ref.Page.ID, Version: ref.Page.Version(), Title: ref.Page.Title,
				Slug: ref.Page.Slug, Content: &content}); err != nil {
				t.Fatalf("update referrer: %v", err)
			}
			beforeRevision, err := deps.revision.GetLatestRevision(ref.Page.ID)
			if err != nil || beforeRevision == nil {
				t.Fatalf("GetLatestRevision before refactor: %#v, %v", beforeRevision, err)
			}

			if collision {
				if _, err := createUC.Execute(context.Background(), pages.CreatePageInput{
					UserID: "user1", Title: "Collision", Slug: "futuretargettoken", Kind: pageKind(),
				}); err != nil {
					t.Fatalf("create colliding page: %v", err)
				}
			} else {
				concurrentContent := "concurrent update"
				if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
					UserID: "user1", ID: target.Page.ID, Version: target.Page.Version(), Title: target.Page.Title,
					Slug: target.Page.Slug, Content: &concurrentContent}); err != nil {
					t.Fatalf("concurrent target update: %v", err)
				}
			}

			_, err = applyUC.Execute(context.Background(), pages.RefactorApplyInput{
				UserID: "user1", Version: target.Page.Version(), RewriteLinks: true,
				RefactorPreviewInput: pages.RefactorPreviewInput{
					PageID: target.Page.ID, Kind: pages.RefactorKindRename,
					Title: "Future Target", Slug: "futuretargettoken",
				},
			})
			if err == nil || !collision && !errors.Is(err, tree.ErrVersionConflict) {
				t.Fatalf("expected rename failure, got %v", err)
			}

			refAfter, err := deps.tree.GetPage(ref.Page.ID)
			if err != nil {
				t.Fatalf("get referrer after failed refactor: %v", err)
			}
			if refAfter.Content != content {
				t.Fatalf("failed refactor changed referrer content: %q", refAfter.Content)
			}
			afterRevision, err := deps.revision.GetLatestRevision(ref.Page.ID)
			if err != nil || afterRevision == nil {
				t.Fatalf("GetLatestRevision after refactor: %#v, %v", afterRevision, err)
			}
			if afterRevision.ID != beforeRevision.ID {
				t.Fatalf("failed refactor created a referrer revision: before=%q after=%q", beforeRevision.ID, afterRevision.ID)
			}
			result, err := index.Search("Future Target", nil, 0, 20)
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			for _, item := range result.Items {
				if item.PageID == ref.Page.ID {
					t.Fatalf("failed refactor leaked rewritten referrer into search: %#v", item)
				}
			}
		})
	}
}

func TestApplyPageRefactorUseCase_RenameRefreshesSubtreeSearchPaths(t *testing.T) {
	deps := newTestDeps(t)
	index, orchestrator := deps.searchOrchestrator(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, orchestrator, slog.Default())
	applyUC := pages.NewApplyPageRefactorUseCase(deps.tree, deps.slug, deps.links, orchestrator, slog.Default())

	target, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Legacy Root", Slug: "legacyroottoken", Kind: sectionKind(),
	})
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	child, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", ParentID: &target.Page.ID, Title: "Unchanged Child", Slug: "child", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	if _, err := applyUC.Execute(context.Background(), pages.RefactorApplyInput{
		UserID: "user1", Version: target.Page.Version(),
		RefactorPreviewInput: pages.RefactorPreviewInput{
			PageID: target.Page.ID, Kind: pages.RefactorKindRename,
			Title: "Fresh Root", Slug: "freshrootsearchtoken",
		},
	}); err != nil {
		t.Fatalf("apply rename refactor: %v", err)
	}

	result, err := index.Search("Fresh Root", nil, 0, 20)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	foundTarget := false
	for _, item := range result.Items {
		if item.PageID == target.Page.ID {
			if !strings.Contains(item.Title, "Fresh") || item.Path != "freshrootsearchtoken" {
				t.Fatalf("renamed target search entry is stale: %#v", item)
			}
			foundTarget = true
			break
		}
	}
	if !foundTarget {
		t.Fatalf("renamed target was absent from new-path search: %#v", result.Items)
	}

	childResult, err := index.Search("Unchanged Child", nil, 0, 20)
	if err != nil {
		t.Fatalf("Search child: %v", err)
	}
	foundChild := false
	for _, item := range childResult.Items {
		if item.PageID == child.Page.ID {
			if item.Path != "freshrootsearchtoken/child" {
				t.Fatalf("renamed descendant search path is stale: %#v", item)
			}
			foundChild = true
			break
		}
	}
	if !foundChild {
		t.Fatalf("renamed descendant was absent from search: %#v", childResult.Items)
	}
}

func TestApplyPageRefactorUseCase_RenamePreservesChildEditMadeAfterSnapshot(t *testing.T) {
	deps := newTestDeps(t)
	sectionID, err := deps.tree.CreateNode("user1", nil, "Docs", "docs", sectionKind())
	if err != nil {
		t.Fatalf("create section: %v", err)
	}
	sourceID, err := deps.tree.CreateNode("user1", sectionID, "Source", "source", pageKind())
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	if _, err := deps.tree.CreateNode("user1", sectionID, "Target", "target", pageKind()); err != nil {
		t.Fatalf("create target: %v", err)
	}
	source, err := deps.tree.GetPage(*sourceID)
	if err != nil {
		t.Fatalf("get source: %v", err)
	}
	oldContent := "[Target](/docs/target)"
	if err := deps.tree.UpdateNode("user1", source.ID, source.Title, source.Slug, &oldContent, source.Version(), nil, nil, false); err != nil {
		t.Fatalf("seed source content: %v", err)
	}

	concurrentContent := "newer editor content"
	editEffect := &editChildOnRenameEffect{tree: deps.tree, pageID: source.ID, content: concurrentContent}
	orchestrator := pagesave.NewPageSaveOrchestrator(nil, editEffect)
	applyUC := pages.NewApplyPageRefactorUseCase(deps.tree, deps.slug, deps.links, orchestrator, slog.Default())
	section, err := deps.tree.GetPage(*sectionID)
	if err != nil {
		t.Fatalf("get section: %v", err)
	}
	if _, err := applyUC.Execute(context.Background(), pages.RefactorApplyInput{
		UserID: "user1", Version: section.Version(),
		RefactorPreviewInput: pages.RefactorPreviewInput{
			PageID: section.ID, Kind: pages.RefactorKindRename, Title: "Guides", Slug: "guides",
		},
	}); err != nil {
		t.Fatalf("apply rename: %v", err)
	}
	if editEffect.err != nil {
		t.Fatalf("concurrent child edit: %v", editEffect.err)
	}
	after, err := deps.tree.GetPage(source.ID)
	if err != nil {
		t.Fatalf("get source after rename: %v", err)
	}
	if after.Content != concurrentContent {
		t.Fatalf("rename overwrote the newer child edit: %q", after.Content)
	}
}

func TestApplyPageRefactorUseCase_PublicToDraftRenameDoesNotRewritePublicIncomingPages(t *testing.T) {
	deps := newTestDeps(t)
	index, orchestrator := deps.searchOrchestrator(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, orchestrator, slog.Default())
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, orchestrator, slog.Default())
	applyUC := pages.NewApplyPageRefactorUseCase(deps.tree, deps.slug, deps.links, orchestrator, slog.Default())

	target, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Public Target", Slug: "publictarget", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	ref, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Public Referrer", Slug: "publicreferrer", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("create referrer: %v", err)
	}
	content := "[[Public Target]] and [Target](/publictarget)"
	updatedRef, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "user1", ID: ref.Page.ID, Version: ref.Page.Version(), Title: ref.Page.Title,
		Slug: ref.Page.Slug, Content: &content})
	if err != nil {
		t.Fatalf("update referrer: %v", err)
	}
	beforeRevision, err := deps.revision.GetLatestRevision(ref.Page.ID)
	if err != nil || beforeRevision == nil {
		t.Fatalf("GetLatestRevision before refactor: %#v, %v", beforeRevision, err)
	}

	draft := true
	draftContent := "draft-only content"
	updatedTarget, err := applyUC.Execute(context.Background(), pages.RefactorApplyInput{
		UserID: "user1", Version: target.Page.Version(), RewriteLinks: true,
		Draft: &draft, DraftAllowed: true, Tags: []string{"private"}, Properties: map[string]string{"owner": "alice"},
		RefactorPreviewInput: pages.RefactorPreviewInput{
			PageID: target.Page.ID, Kind: pages.RefactorKindRename,
			Title: "Secret Draft", Slug: "secretdraftsearchtoken", Content: &draftContent,
		},
	})
	if err != nil {
		t.Fatalf("apply draft rename: %v", err)
	}
	if !updatedTarget.Draft || updatedTarget.CalculatePath() != "/secretdraftsearchtoken" {
		t.Fatalf("draft target was not renamed: draft=%v path=%q", updatedTarget.Draft, updatedTarget.CalculatePath())
	}
	if !strings.Contains(updatedTarget.RawContent, "draft: true") ||
		!strings.Contains(updatedTarget.RawContent, "- private") ||
		!strings.Contains(updatedTarget.RawContent, "owner: alice") {
		t.Fatalf("atomic draft metadata was not persisted: %q", updatedTarget.RawContent)
	}

	refAfter, err := deps.tree.GetPage(ref.Page.ID)
	if err != nil {
		t.Fatalf("get referrer after draft rename: %v", err)
	}
	if refAfter.Content != content {
		t.Fatalf("draft rename changed public referrer content: %q", refAfter.Content)
	}
	if refAfter.Version() != updatedRef.Page.Version() {
		t.Fatalf("draft rename changed public referrer version: before=%q after=%q", updatedRef.Page.Version(), refAfter.Version())
	}
	afterRevision, err := deps.revision.GetLatestRevision(ref.Page.ID)
	if err != nil || afterRevision == nil {
		t.Fatalf("GetLatestRevision after refactor: %#v, %v", afterRevision, err)
	}
	if afterRevision.ID != beforeRevision.ID {
		t.Fatalf("draft rename created a public referrer revision: before=%q after=%q", beforeRevision.ID, afterRevision.ID)
	}
	result, err := index.Search("Secret Draft", nil, 0, 20)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, item := range result.Items {
		if item.PageID == target.Page.ID || item.PageID == ref.Page.ID {
			t.Fatalf("draft rename leaked into public search: %#v", item)
		}
	}
}

func TestApplyPageRefactorUseCase_DraftToPublicRenameRewritesIncomingPages(t *testing.T) {
	deps := newTestDeps(t)
	orchestrator := deps.orchestrator()
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, orchestrator, slog.Default())
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, orchestrator, slog.Default())
	applyUC := pages.NewApplyPageRefactorUseCase(deps.tree, deps.slug, deps.links, orchestrator, slog.Default())

	target, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Draft Target", Slug: "drafttarget", Kind: pageKind(), Draft: true, DraftAllowed: true,
	})
	if err != nil {
		t.Fatalf("create draft target: %v", err)
	}
	ref, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Public Referrer", Slug: "publicreferrer", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("create referrer: %v", err)
	}
	content := "[[Draft Target]] and [Target](/drafttarget)"
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "user1", ID: ref.Page.ID, Version: ref.Page.Version(), Title: ref.Page.Title,
		Slug: ref.Page.Slug, Content: &content,
	}); err != nil {
		t.Fatalf("update referrer: %v", err)
	}

	published := false
	readyContent := "ready for readers"
	updatedTarget, err := applyUC.Execute(context.Background(), pages.RefactorApplyInput{
		UserID: "user1", Version: target.Page.Version(), RewriteLinks: true,
		Draft: &published, DraftAllowed: true, Tags: []string{"ready"}, Properties: map[string]string{"owner": "alice"},
		RefactorPreviewInput: pages.RefactorPreviewInput{
			PageID: target.Page.ID, Kind: pages.RefactorKindRename,
			Title: "Published Target", Slug: "publishedtarget", Content: &readyContent,
		},
	})
	if err != nil {
		t.Fatalf("publish and rename draft: %v", err)
	}
	if updatedTarget.Draft || updatedTarget.CalculatePath() != "/publishedtarget" {
		t.Fatalf("published target mismatch: draft=%v path=%q", updatedTarget.Draft, updatedTarget.CalculatePath())
	}
	if !strings.Contains(updatedTarget.RawContent, "- ready") ||
		!strings.Contains(updatedTarget.RawContent, "owner: alice") {
		t.Fatalf("atomic published metadata was not persisted: %q", updatedTarget.RawContent)
	}

	refAfter, err := deps.tree.GetPage(ref.Page.ID)
	if err != nil {
		t.Fatalf("get referrer after publish: %v", err)
	}
	want := "[[Published Target]] and [Target](/publishedtarget)"
	if refAfter.Content != want {
		t.Fatalf("published referrer content = %q, want %q", refAfter.Content, want)
	}
}

func TestApplyPageRefactorUseCase_DraftMoveStillRewritesItsRelativeLinks(t *testing.T) {
	deps := newTestDeps(t)
	orchestrator := deps.orchestrator()
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, orchestrator, slog.Default())
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, orchestrator, slog.Default())
	applyUC := pages.NewApplyPageRefactorUseCase(deps.tree, deps.slug, deps.links, orchestrator, slog.Default())

	docs, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Docs", Slug: "docs", Kind: sectionKind(),
	})
	if err != nil {
		t.Fatalf("create docs: %v", err)
	}
	draftPage, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", ParentID: &docs.Page.ID, Title: "Draft Page", Slug: "draft-page", Kind: pageKind(), Draft: true, DraftAllowed: true,
	})
	if err != nil {
		t.Fatalf("create draft page: %v", err)
	}
	vault, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Vault", Slug: "vault", Kind: sectionKind(),
	})
	if err != nil {
		t.Fatalf("create vault: %v", err)
	}
	archive, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", ParentID: &vault.Page.ID, Title: "Archive", Slug: "archive", Kind: sectionKind(),
	})
	if err != nil {
		t.Fatalf("create archive: %v", err)
	}
	content := "[Guide](../../guide)"
	updatedDraft, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "user1", ID: draftPage.Page.ID, Version: draftPage.Page.Version(), Title: draftPage.Page.Title,
		Slug: draftPage.Page.Slug, Content: &content})
	if err != nil {
		t.Fatalf("update draft page: %v", err)
	}

	moved, err := applyUC.Execute(context.Background(), pages.RefactorApplyInput{
		UserID: "user1", Version: updatedDraft.Page.Version(), RewriteLinks: true,
		RefactorPreviewInput: pages.RefactorPreviewInput{
			PageID: draftPage.Page.ID, Kind: pages.RefactorKindMove, NewParentID: &archive.Page.ID,
		},
	})
	if err != nil {
		t.Fatalf("move draft page: %v", err)
	}
	if !moved.Draft || moved.CalculatePath() != "/vault/archive/draft-page" {
		t.Fatalf("draft page move mismatch: draft=%v path=%q", moved.Draft, moved.CalculatePath())
	}
	if moved.Content != "[Guide](../../../guide)" {
		t.Fatalf("draft page relative link was not preserved: %q", moved.Content)
	}
}

func TestApplyPageRefactorUseCase_IncomingRewriteRefreshesSearchIndex(t *testing.T) {
	deps := newTestDeps(t)
	index, err := search.NewSQLiteIndex(deps.storageDir)
	if err != nil {
		t.Fatalf("NewSQLiteIndex: %v", err)
	}
	t.Cleanup(func() { _ = index.Close() })
	orchestrator := pagesave.NewPageSaveOrchestrator(
		nil,
		pagesave.NewSearchIndexSideEffect(index, deps.tree, slog.Default()),
		pagesave.NewLinkIndexSideEffect(deps.links, slog.Default()),
		pagesave.NewRevisionSideEffect(deps.revision, slog.Default()),
	)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, orchestrator, slog.Default())
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, orchestrator, slog.Default())
	applyUC := pages.NewApplyPageRefactorUseCase(deps.tree, deps.slug, deps.links, orchestrator, slog.Default())

	target, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "LegacyWikiToken", Slug: "legacytargettoken", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	source, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Source", Slug: "source", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	content := "[[LegacyWikiToken]]"
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "user1", ID: source.Page.ID, Version: source.Page.Version(), Title: source.Page.Title,
		Slug: source.Page.Slug, Content: &content}); err != nil {
		t.Fatalf("update source: %v", err)
	}

	if _, err := applyUC.Execute(context.Background(), pages.RefactorApplyInput{
		UserID: "user1", Version: target.Page.Version(), RewriteLinks: true,
		RefactorPreviewInput: pages.RefactorPreviewInput{
			PageID: target.Page.ID, Kind: pages.RefactorKindRename, Title: "FreshWikiToken", Slug: "freshtargettoken",
		},
	}); err != nil {
		t.Fatalf("apply rename refactor: %v", err)
	}

	result, err := index.Search("FreshWikiToken", nil, 0, 20)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, item := range result.Items {
		if item.PageID == source.Page.ID {
			if !strings.Contains(item.Excerpt, "FreshWikiToken") {
				t.Fatalf("source search excerpt was stale: %q", item.Excerpt)
			}
			return
		}
	}
	t.Fatalf("rewritten source was absent from fresh-token search: %#v", result.Items)
}

func TestApplyPageRefactorUseCase_SubtreeRewriteRefreshesSearchIndex(t *testing.T) {
	deps := newTestDeps(t)
	index, err := search.NewSQLiteIndex(deps.storageDir)
	if err != nil {
		t.Fatalf("NewSQLiteIndex: %v", err)
	}
	t.Cleanup(func() { _ = index.Close() })
	captured := &captureEffect{}
	orchestrator := pagesave.NewPageSaveOrchestrator(
		nil,
		pagesave.NewSearchIndexSideEffect(index, deps.tree, slog.Default()),
		pagesave.NewLinkIndexSideEffect(deps.links, slog.Default()),
		pagesave.NewRevisionSideEffect(deps.revision, slog.Default()),
		captured,
	)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, orchestrator, slog.Default())
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, orchestrator, slog.Default())
	applyUC := pages.NewApplyPageRefactorUseCase(deps.tree, deps.slug, deps.links, orchestrator, slog.Default())

	subtree, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Subtree", Slug: "legacysubtree", Kind: sectionKind(),
	})
	if err != nil {
		t.Fatalf("create subtree: %v", err)
	}
	source, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", ParentID: &subtree.Page.ID, Title: "Source", Slug: "source", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	_, err = createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", ParentID: &subtree.Page.ID, Title: "Target Leaf", Slug: "targetleaf", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("create target leaf: %v", err)
	}
	destination, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1", Title: "Fresh Container", Slug: "freshcontainer", Kind: sectionKind(),
	})
	if err != nil {
		t.Fatalf("create destination: %v", err)
	}
	content := "[Target](/legacysubtree/targetleaf)"
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "user1", ID: source.Page.ID, Version: source.Page.Version(), Title: source.Page.Title,
		Slug: source.Page.Slug, Content: &content}); err != nil {
		t.Fatalf("update source: %v", err)
	}
	currentSubtree, err := deps.tree.GetPage(subtree.Page.ID)
	if err != nil {
		t.Fatalf("get subtree: %v", err)
	}

	eventStart := len(captured.events)
	if _, err := applyUC.Execute(context.Background(), pages.RefactorApplyInput{
		UserID: "user1", Version: currentSubtree.Version(),
		RefactorPreviewInput: pages.RefactorPreviewInput{
			PageID: subtree.Page.ID, Kind: pages.RefactorKindMove, NewParentID: &destination.Page.ID,
		},
	}); err != nil {
		t.Fatalf("apply move refactor: %v", err)
	}
	foundRewriteEvent := false
	for _, event := range captured.events[eventStart:] {
		if event.Operation != pagesave.PageOperationUpdate || event.After == nil || event.After.ID != source.Page.ID {
			continue
		}
		foundRewriteEvent = true
		if event.After.RawContent == "" || !strings.Contains(event.After.RawContent, "/freshcontainer/legacysubtree/targetleaf") {
			t.Fatalf("rewrite event did not carry complete current page: %#v", event.After)
		}
	}
	if !foundRewriteEvent {
		t.Fatal("subtree rewrite did not run the common post-save contract")
	}

	result, err := index.Search("Source", nil, 0, 20)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, item := range result.Items {
		if item.PageID == source.Page.ID {
			if item.Path != "freshcontainer/legacysubtree/source" {
				t.Fatalf("subtree source search path was stale: %q", item.Path)
			}
			return
		}
	}
	t.Fatalf("rewritten subtree source was absent from search: %#v", result.Items)
}

func TestPreviewPageRefactorUseCase_RenameListsAffectedPages(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	previewUC := pages.NewPreviewPageRefactorUseCase(deps.tree, deps.slug, deps.links, slog.Default())

	target, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Target", Slug: "target", Kind: pageKind(),
	})
	ref, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Ref", Slug: "ref", Kind: pageKind(),
	})
	content := "[Target](/target)"
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "system", ID: ref.Page.ID, Version: ref.Page.Version(), Title: ref.Page.Title, Slug: ref.Page.Slug, Content: &content}); err != nil {
		t.Fatalf("UpdatePage failed: %v", err)
	}

	preview, err := previewUC.Execute(context.Background(), pages.RefactorPreviewInput{
		PageID: target.Page.ID,
		Kind:   pages.RefactorKindRename,
		Title:  target.Page.Title,
		Slug:   "target-renamed",
	})
	if err != nil {
		t.Fatalf("PreviewPageRefactor failed: %v", err)
	}
	if preview.OldPath != "/target" {
		t.Fatalf("OldPath = %q, want %q", preview.OldPath, "/target")
	}
	if preview.NewPath != "/target-renamed" {
		t.Fatalf("NewPath = %q, want %q", preview.NewPath, "/target-renamed")
	}
	if preview.Counts.AffectedPages != 1 {
		t.Fatalf("AffectedPages = %d, want 1", preview.Counts.AffectedPages)
	}
	if len(preview.AffectedPages) != 1 {
		t.Fatalf("expected 1 affected page, got %d", len(preview.AffectedPages))
	}
	if preview.AffectedPages[0].FromPageID != ref.Page.ID {
		t.Fatalf("FromPageID = %q, want %q", preview.AffectedPages[0].FromPageID, ref.Page.ID)
	}
}

func TestApplyPageRefactorUseCase_RenameRewritesIncomingLinks(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default())
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default())
	applyUC := pages.NewApplyPageRefactorUseCase(deps.tree, deps.slug, deps.links, deps.orchestrator(), slog.Default())

	target, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Target", Slug: "target", Kind: pageKind(),
	})
	ref, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Ref", Slug: "ref", Kind: pageKind(),
	})
	content := "[Target](/target)"
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "system", ID: ref.Page.ID, Version: ref.Page.Version(), Title: ref.Page.Title, Slug: ref.Page.Slug, Content: &content}); err != nil {
		t.Fatalf("UpdatePage failed: %v", err)
	}

	beforeRefRevision, err := deps.revision.GetLatestRevision(ref.Page.ID)
	if err != nil {
		t.Fatalf("GetLatestRevision(ref before refactor) failed: %v", err)
	}

	updated, err := applyUC.Execute(context.Background(), pages.RefactorApplyInput{
		UserID:  "system",
		Version: target.Page.Version(),
		RefactorPreviewInput: pages.RefactorPreviewInput{
			PageID:  target.Page.ID,
			Kind:    pages.RefactorKindRename,
			Title:   "Target Renamed",
			Slug:    "target-renamed",
			Content: &target.Page.Content,
		},
		RewriteLinks: true,
	})
	if err != nil {
		t.Fatalf("ApplyPageRefactor failed: %v", err)
	}
	if updated.CalculatePath() != "/target-renamed" {
		t.Fatalf("updated path mismatch: %q", updated.CalculatePath())
	}

	refPage, err := deps.tree.GetPage(ref.Page.ID)
	if err != nil {
		t.Fatalf("GetPage(ref) failed: %v", err)
	}
	if refPage.Content != "[Target](/target-renamed)" {
		t.Fatalf("ref content = %q, want %q", refPage.Content, "[Target](/target-renamed)")
	}

	outgoing, err := deps.links.GetOutgoingLinksForPage(ref.Page.ID)
	if err != nil {
		t.Fatalf("GetOutgoingLinks failed: %v", err)
	}
	if outgoing.Count != 1 {
		t.Fatalf("expected 1 outgoing, got %d", outgoing.Count)
	}
	if outgoing.Outgoings[0].ToPath != "/target-renamed" {
		t.Fatalf("ToPath = %q, want %q", outgoing.Outgoings[0].ToPath, "/target-renamed")
	}
	if outgoing.Outgoings[0].Broken {
		t.Fatalf("expected rewritten link to be healed")
	}

	afterRefRevision, err := deps.revision.GetLatestRevision(ref.Page.ID)
	if err != nil {
		t.Fatalf("GetLatestRevision(ref after refactor) failed: %v", err)
	}
	if afterRefRevision == nil || beforeRefRevision == nil {
		t.Fatalf("expected revisions before and after refactor")
	}
	if afterRefRevision.ID == beforeRefRevision.ID {
		t.Fatalf("expected rewritten ref page to create a new revision")
	}
	if afterRefRevision.Type != revision.RevisionTypeContentUpdate {
		t.Fatalf("expected rewritten ref page latest revision type %q, got %q", revision.RevisionTypeContentUpdate, afterRefRevision.Type)
	}
}

func TestApplyPageRefactorUseCase_RenameRewritesTitleBasedWikiLinks(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default())
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default())
	applyUC := pages.NewApplyPageRefactorUseCase(deps.tree, deps.slug, deps.links, deps.orchestrator(), slog.Default())

	target, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Target", Slug: "target", Kind: pageKind(),
	})
	ref, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Ref", Slug: "ref", Kind: pageKind(),
	})
	content := "[[Target]] and [[Target|Alias]]"
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "system", ID: ref.Page.ID, Version: ref.Page.Version(), Title: ref.Page.Title, Slug: ref.Page.Slug, Content: &content}); err != nil {
		t.Fatalf("UpdatePage failed: %v", err)
	}

	updated, err := applyUC.Execute(context.Background(), pages.RefactorApplyInput{
		UserID:  "system",
		Version: target.Page.Version(),
		RefactorPreviewInput: pages.RefactorPreviewInput{
			PageID:  target.Page.ID,
			Kind:    pages.RefactorKindRename,
			Title:   "Target Renamed",
			Slug:    "target-renamed",
			Content: &target.Page.Content,
		},
		RewriteLinks: true,
	})
	if err != nil {
		t.Fatalf("ApplyPageRefactor failed: %v", err)
	}
	if updated.CalculatePath() != "/target-renamed" {
		t.Fatalf("updated path mismatch: %q", updated.CalculatePath())
	}

	refPage, err := deps.tree.GetPage(ref.Page.ID)
	if err != nil {
		t.Fatalf("GetPage(ref) failed: %v", err)
	}
	wantContent := "[[Target Renamed]] and [[Target Renamed|Alias]]"
	if refPage.Content != wantContent {
		t.Fatalf("ref content = %q, want %q", refPage.Content, wantContent)
	}

	outgoing, err := deps.links.GetOutgoingLinksForPage(ref.Page.ID)
	if err != nil {
		t.Fatalf("GetOutgoingLinks failed: %v", err)
	}
	if outgoing.Count != 1 {
		t.Fatalf("expected 1 outgoing, got %d", outgoing.Count)
	}
	if outgoing.Outgoings[0].ToPath != "Target Renamed" {
		t.Fatalf("ToPath = %q, want %q", outgoing.Outgoings[0].ToPath, "Target Renamed")
	}
	if outgoing.Outgoings[0].ToPageID != updated.ID {
		t.Fatalf("ToPageID = %q, want %q", outgoing.Outgoings[0].ToPageID, updated.ID)
	}
	if outgoing.Outgoings[0].Broken {
		t.Fatalf("expected rewritten wikilink to remain valid")
	}
}

func TestPreviewPageRefactorUseCase_UsesEmptyWarningArrays(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	previewUC := pages.NewPreviewPageRefactorUseCase(deps.tree, deps.slug, deps.links, slog.Default())

	page, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Target", Slug: "target", Kind: pageKind(),
	})

	preview, err := previewUC.Execute(context.Background(), pages.RefactorPreviewInput{
		PageID: page.Page.ID,
		Kind:   pages.RefactorKindRename,
		Title:  page.Page.Title,
		Slug:   "target-renamed",
	})
	if err != nil {
		t.Fatalf("PreviewPageRefactor failed: %v", err)
	}
	if preview.Warnings == nil {
		t.Fatalf("expected preview warnings to be an empty slice, got nil")
	}
	if len(preview.Warnings) != 0 {
		t.Fatalf("expected no preview warnings, got %d", len(preview.Warnings))
	}
	for i, affected := range preview.AffectedPages {
		if affected.Warnings == nil {
			t.Fatalf("affected page %d warnings should be empty slice, got nil", i)
		}
		if affected.MatchedPaths == nil {
			t.Fatalf("affected page %d matched paths should be empty slice, got nil", i)
		}
	}
}

func TestApplyPageRefactorUseCase_RenameRewritesWikilinkDespiteDraftTitleDuplicate(t *testing.T) {
	for _, inherited := range []bool{false, true} {
		name := "direct draft duplicate"
		if inherited {
			name = "inherited draft duplicate"
		}
		t.Run(name, func(t *testing.T) {
			deps := newTestDeps(t)
			orchestrator := deps.orchestrator()
			createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, orchestrator, slog.Default())
			updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, orchestrator, slog.Default())
			previewUC := pages.NewPreviewPageRefactorUseCase(deps.tree, deps.slug, deps.links, slog.Default())
			applyUC := pages.NewApplyPageRefactorUseCase(deps.tree, deps.slug, deps.links, orchestrator, slog.Default())

			target, err := createUC.Execute(context.Background(), pages.CreatePageInput{
				UserID: "system", Title: "Target", Slug: "target", Kind: pageKind(),
			})
			if err != nil {
				t.Fatalf("create target: %v", err)
			}
			if inherited {
				parent, err := createUC.Execute(context.Background(), pages.CreatePageInput{
					UserID: "system", Title: "Drafts", Slug: "drafts", Kind: sectionKind(), Draft: true, DraftAllowed: true,
				})
				if err != nil {
					t.Fatalf("create draft parent: %v", err)
				}
				if _, err := createUC.Execute(context.Background(), pages.CreatePageInput{
					UserID: "system", ParentID: &parent.Page.ID, Title: "Target", Slug: "duplicate", Kind: pageKind(),
				}); err != nil {
					t.Fatalf("create inherited draft duplicate: %v", err)
				}
			} else if _, err := createUC.Execute(context.Background(), pages.CreatePageInput{
				UserID: "system", Title: "Target", Slug: "duplicate", Kind: pageKind(), Draft: true, DraftAllowed: true,
			}); err != nil {
				t.Fatalf("create direct draft duplicate: %v", err)
			}

			ref, err := createUC.Execute(context.Background(), pages.CreatePageInput{
				UserID: "system", Title: "Ref", Slug: "ref", Kind: pageKind(),
			})
			if err != nil {
				t.Fatalf("create referrer: %v", err)
			}
			content := "[[Target]]"
			if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
				UserID: "system", ID: ref.Page.ID, Version: ref.Page.Version(), Title: ref.Page.Title, Slug: ref.Page.Slug, Content: &content,
			}); err != nil {
				t.Fatalf("index referrer: %v", err)
			}

			preview, err := previewUC.Execute(context.Background(), pages.RefactorPreviewInput{
				PageID: target.Page.ID, Kind: pages.RefactorKindRename, Title: "Renamed", Slug: "renamed",
			})
			if err != nil {
				t.Fatalf("preview rename: %v", err)
			}
			if preview.Counts.AffectedPages != 1 || preview.Counts.MatchedLinks != 1 || preview.AffectedPages[0].FromPageID != ref.Page.ID {
				t.Fatalf("preview did not include public referrer: %#v", preview)
			}

			if _, err := applyUC.Execute(context.Background(), pages.RefactorApplyInput{
				UserID: "system", Version: target.Page.Version(), RewriteLinks: true,
				RefactorPreviewInput: pages.RefactorPreviewInput{
					PageID: target.Page.ID, Kind: pages.RefactorKindRename, Title: "Renamed", Slug: "renamed",
				},
			}); err != nil {
				t.Fatalf("apply rename: %v", err)
			}
			updatedRef, err := deps.tree.GetPage(ref.Page.ID)
			if err != nil {
				t.Fatalf("get referrer after rename: %v", err)
			}
			if updatedRef.Content != "[[Renamed]]" {
				t.Fatalf("referrer content = %q, want rewritten wikilink", updatedRef.Content)
			}
		})
	}
}

func TestPreviewPageRefactorUseCase_Rename_ExcludesAmbiguousSentinelPagesFromPreview(t *testing.T) {
	// Scenario: two pages share the title "Grafana" ("grafana" and "grafana-1").
	// A third page has [[Grafana]] in its content — this is an ambiguous sentinel
	// (broken) because both pages match the title. When we rename "grafana" to
	// something else, the [[Grafana]] sentinel must NOT appear in the refactor
	// preview, because:
	//   a) it is ambiguous and cannot be auto-updated
	//   b) after the rename only one "Grafana" page remains, so
	//      HealWikiLinksForTitleIfUnambiguous will resolve it automatically.
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	previewUC := pages.NewPreviewPageRefactorUseCase(deps.tree, deps.slug, deps.links, slog.Default())

	grafana1, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Grafana", Slug: "grafana", Kind: pageKind(),
	})
	grafana2, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Grafana", Slug: "grafana-1", Kind: pageKind(),
	})
	ref, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Ref", Slug: "ref", Kind: pageKind(),
	})

	// Store [[Grafana]] sentinel (ambiguous because both pages share the title).
	content := "[[Grafana]]"
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID:  "system",
		ID:      ref.Page.ID,
		Version: ref.Page.Version(),
		Title:   ref.Page.Title,
		Slug:    ref.Page.Slug,
		Content: &content}); err != nil {
		t.Fatalf("UpdatePage(ref) failed: %v", err)
	}

	preview, err := previewUC.Execute(context.Background(), pages.RefactorPreviewInput{
		PageID: grafana1.Page.ID,
		Kind:   pages.RefactorKindRename,
		Title:  "Prometheus",
		Slug:   "prometheus",
	})
	if err != nil {
		t.Fatalf("PreviewPageRefactor failed: %v", err)
	}

	if preview.Counts.AffectedPages != 0 {
		t.Fatalf(
			"expected 0 affected pages for ambiguous sentinel rename, got %d (pages: %v)",
			preview.Counts.AffectedPages,
			preview.AffectedPages,
		)
	}
	for _, ap := range preview.AffectedPages {
		if ap.FromPageID == ref.Page.ID {
			t.Fatalf("ambiguous sentinel page %q must not appear in refactor preview", ref.Page.ID)
		}
	}

	applyUC := pages.NewApplyPageRefactorUseCase(deps.tree, deps.slug, deps.links, deps.orchestrator(), slog.Default())
	if _, err := applyUC.Execute(context.Background(), pages.RefactorApplyInput{
		UserID: "system", Version: grafana1.Page.Version(), RewriteLinks: true,
		RefactorPreviewInput: pages.RefactorPreviewInput{
			PageID: grafana1.Page.ID, Kind: pages.RefactorKindRename, Title: "Prometheus", Slug: "prometheus",
		},
	}); err != nil {
		t.Fatalf("ApplyPageRefactor failed: %v", err)
	}
	refAfter, err := deps.tree.GetPage(ref.Page.ID)
	if err != nil {
		t.Fatalf("GetPage(ref) after rename: %v", err)
	}
	if refAfter.Content != content {
		t.Fatalf("ambiguous wikilink was rewritten: %q", refAfter.Content)
	}
	outgoing, err := deps.links.GetOutgoingLinksForPage(ref.Page.ID)
	if err != nil || outgoing.Count != 1 || outgoing.Outgoings[0].Broken || outgoing.Outgoings[0].ToPageID != grafana2.Page.ID {
		t.Fatalf("wikilink did not heal to remaining public keeper: outgoing=%#v err=%v", outgoing, err)
	}
}

func TestPreviewPageRefactorUseCase_Move_ExcludesMovedSubtreeFromOptionalAffectedPages(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	previewUC := pages.NewPreviewPageRefactorUseCase(deps.tree, deps.slug, deps.links, slog.Default())

	docs, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Docs", Slug: "docs", Kind: pageKind(),
	})
	pageA, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", ParentID: &docs.Page.ID, Title: "Page A", Slug: "page-a", Kind: pageKind(),
	})
	pageB, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", ParentID: &docs.Page.ID, Title: "Page B", Slug: "page-b", Kind: pageKind(),
	})
	archive, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Archive", Slug: "archive", Kind: pageKind(),
	})

	contentA := "[To B](../page-b)"
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "system", ID: pageA.Page.ID, Version: pageA.Page.Version(), Title: pageA.Page.Title, Slug: pageA.Page.Slug, Content: &contentA}); err != nil {
		t.Fatalf("UpdatePage(pageA) failed: %v", err)
	}

	preview, err := previewUC.Execute(context.Background(), pages.RefactorPreviewInput{
		PageID:      pageA.Page.ID,
		Kind:        pages.RefactorKindMove,
		NewParentID: &archive.Page.ID,
	})
	if err != nil {
		t.Fatalf("PreviewPageRefactor failed: %v", err)
	}
	if preview.Counts.AffectedPages != 0 {
		t.Fatalf("expected no optional affected pages, got %d", preview.Counts.AffectedPages)
	}
	if len(preview.AffectedPages) != 0 {
		t.Fatalf("expected no affected pages, got %d", len(preview.AffectedPages))
	}

	_ = pageB
}

func TestApplyPageRefactorUseCase_Move_RewritesRelativeOutgoingLinksInMovedPage(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default())
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default())
	applyUC := pages.NewApplyPageRefactorUseCase(deps.tree, deps.slug, deps.links, deps.orchestrator(), slog.Default())

	docs, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Docs", Slug: "docs", Kind: pageKind(),
	})
	pageA, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", ParentID: &docs.Page.ID, Title: "Page A", Slug: "page-a", Kind: pageKind(),
	})
	pageB, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", ParentID: &docs.Page.ID, Title: "Page B", Slug: "page-b", Kind: pageKind(),
	})
	archive, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Archive", Slug: "archive", Kind: pageKind(),
	})

	contentA := "[To B](../page-b)"
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "system", ID: pageA.Page.ID, Version: pageA.Page.Version(), Title: pageA.Page.Title, Slug: pageA.Page.Slug, Content: &contentA}); err != nil {
		t.Fatalf("UpdatePage(pageA) failed: %v", err)
	}

	beforeMovedRevision, err := deps.revision.GetLatestRevision(pageA.Page.ID)
	if err != nil {
		t.Fatalf("GetLatestRevision(pageA before refactor) failed: %v", err)
	}
	currentPageA, err := deps.tree.GetPage(pageA.Page.ID)
	if err != nil {
		t.Fatalf("GetPage(pageA before refactor) failed: %v", err)
	}

	updated, err := applyUC.Execute(context.Background(), pages.RefactorApplyInput{
		UserID:  "system",
		Version: currentPageA.Version(),
		RefactorPreviewInput: pages.RefactorPreviewInput{
			PageID:      pageA.Page.ID,
			Kind:        pages.RefactorKindMove,
			NewParentID: &archive.Page.ID,
		},
		RewriteLinks: false,
	})
	if err != nil {
		t.Fatalf("ApplyPageRefactor(move) failed: %v", err)
	}
	if updated.CalculatePath() != "/archive/page-a" {
		t.Fatalf("updated path = %q, want %q", updated.CalculatePath(), "/archive/page-a")
	}

	movedPage, err := deps.tree.GetPage(pageA.Page.ID)
	if err != nil {
		t.Fatalf("GetPage(pageA) failed: %v", err)
	}
	if movedPage.Content != "[To B](../../docs/page-b)" {
		t.Fatalf("moved page content = %q, want %q", movedPage.Content, "[To B](../../docs/page-b)")
	}

	outgoing, err := deps.links.GetOutgoingLinksForPage(pageA.Page.ID)
	if err != nil {
		t.Fatalf("GetOutgoingLinks(pageA) failed: %v", err)
	}
	if outgoing.Count != 1 {
		t.Fatalf("expected 1 outgoing link, got %d", outgoing.Count)
	}
	if outgoing.Outgoings[0].ToPageID != pageB.Page.ID {
		t.Fatalf("ToPageID = %q, want %q", outgoing.Outgoings[0].ToPageID, pageB.Page.ID)
	}
	if outgoing.Outgoings[0].ToPath != "/docs/page-b" {
		t.Fatalf("ToPath = %q, want %q", outgoing.Outgoings[0].ToPath, "/docs/page-b")
	}
	if outgoing.Outgoings[0].Broken {
		t.Fatalf("expected outgoing link to remain valid after move refactor")
	}

	afterMovedRevision, err := deps.revision.GetLatestRevision(pageA.Page.ID)
	if err != nil {
		t.Fatalf("GetLatestRevision(pageA after refactor) failed: %v", err)
	}
	if afterMovedRevision == nil || beforeMovedRevision == nil {
		t.Fatalf("expected revisions before and after move")
	}
	if afterMovedRevision.ID == beforeMovedRevision.ID {
		t.Fatalf("expected moved page rewrite to create a new revision")
	}
	if afterMovedRevision.Type != revision.RevisionTypeContentUpdate {
		t.Fatalf("expected moved page latest revision type %q, got %q", revision.RevisionTypeContentUpdate, afterMovedRevision.Type)
	}
}

func TestApplyPageRefactorUseCase_Move_LeavesTitleBasedWikiLinksUnchanged(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default())
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default())
	applyUC := pages.NewApplyPageRefactorUseCase(deps.tree, deps.slug, deps.links, deps.orchestrator(), slog.Default())

	docs, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Docs", Slug: "docs", Kind: pageKind(),
	})
	target, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", ParentID: &docs.Page.ID, Title: "Target", Slug: "target", Kind: pageKind(),
	})
	ref, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Ref", Slug: "ref", Kind: pageKind(),
	})
	archive, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Archive", Slug: "archive", Kind: pageKind(),
	})

	content := "[[Target]]"
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "system", ID: ref.Page.ID, Version: ref.Page.Version(), Title: ref.Page.Title, Slug: ref.Page.Slug, Content: &content}); err != nil {
		t.Fatalf("UpdatePage(ref) failed: %v", err)
	}

	updated, err := applyUC.Execute(context.Background(), pages.RefactorApplyInput{
		UserID:  "system",
		Version: target.Page.Version(),
		RefactorPreviewInput: pages.RefactorPreviewInput{
			PageID:      target.Page.ID,
			Kind:        pages.RefactorKindMove,
			NewParentID: &archive.Page.ID,
		},
		RewriteLinks: true,
	})
	if err != nil {
		t.Fatalf("ApplyPageRefactor(move) failed: %v", err)
	}
	if updated.CalculatePath() != "/archive/target" {
		t.Fatalf("updated path = %q, want %q", updated.CalculatePath(), "/archive/target")
	}

	refPage, err := deps.tree.GetPage(ref.Page.ID)
	if err != nil {
		t.Fatalf("GetPage(ref) failed: %v", err)
	}
	if refPage.Content != "[[Target]]" {
		t.Fatalf("ref content = %q, want %q", refPage.Content, "[[Target]]")
	}

	outgoing, err := deps.links.GetOutgoingLinksForPage(ref.Page.ID)
	if err != nil {
		t.Fatalf("GetOutgoingLinks(ref) failed: %v", err)
	}
	if outgoing.Count != 1 {
		t.Fatalf("expected 1 outgoing link, got %d", outgoing.Count)
	}
	if outgoing.Outgoings[0].ToPageID != target.Page.ID {
		t.Fatalf("ToPageID = %q, want %q", outgoing.Outgoings[0].ToPageID, target.Page.ID)
	}
	if outgoing.Outgoings[0].ToPath != "Target" {
		t.Fatalf("ToPath = %q, want %q", outgoing.Outgoings[0].ToPath, "Target")
	}
	if outgoing.Outgoings[0].Broken {
		t.Fatalf("expected title-based wikilink to remain valid after move")
	}
}

func TestApplyPageRefactorUseCase_Move_RewritesPathHintWikiLinks(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default())
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default())
	applyUC := pages.NewApplyPageRefactorUseCase(deps.tree, deps.slug, deps.links, deps.orchestrator(), slog.Default())

	docs, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Docs", Slug: "docs", Kind: pageKind(),
	})
	target, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", ParentID: &docs.Page.ID, Title: "Target", Slug: "target", Kind: pageKind(),
	})
	ref, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Ref", Slug: "ref", Kind: pageKind(),
	})
	archive, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Archive", Slug: "archive", Kind: pageKind(),
	})

	content := "[[docs/target]] and [[docs/target|Alias]]"
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "system", ID: ref.Page.ID, Version: ref.Page.Version(), Title: ref.Page.Title, Slug: ref.Page.Slug, Content: &content}); err != nil {
		t.Fatalf("UpdatePage(ref) failed: %v", err)
	}

	updated, err := applyUC.Execute(context.Background(), pages.RefactorApplyInput{
		UserID:  "system",
		Version: target.Page.Version(),
		RefactorPreviewInput: pages.RefactorPreviewInput{
			PageID:      target.Page.ID,
			Kind:        pages.RefactorKindMove,
			NewParentID: &archive.Page.ID,
		},
		RewriteLinks: true,
	})
	if err != nil {
		t.Fatalf("ApplyPageRefactor(move) failed: %v", err)
	}
	if updated.CalculatePath() != "/archive/target" {
		t.Fatalf("updated path = %q, want %q", updated.CalculatePath(), "/archive/target")
	}

	refPage, err := deps.tree.GetPage(ref.Page.ID)
	if err != nil {
		t.Fatalf("GetPage(ref) failed: %v", err)
	}
	wantContent := "[[archive/target]] and [[archive/target|Alias]]"
	if refPage.Content != wantContent {
		t.Fatalf("ref content = %q, want %q", refPage.Content, wantContent)
	}

	outgoing, err := deps.links.GetOutgoingLinksForPage(ref.Page.ID)
	if err != nil {
		t.Fatalf("GetOutgoingLinks(ref) failed: %v", err)
	}
	if outgoing.Count != 1 {
		t.Fatalf("expected 1 outgoing link, got %d", outgoing.Count)
	}
	if outgoing.Outgoings[0].ToPageID != target.Page.ID {
		t.Fatalf("ToPageID = %q, want %q", outgoing.Outgoings[0].ToPageID, target.Page.ID)
	}
	if outgoing.Outgoings[0].ToPath != "/archive/target" {
		t.Fatalf("ToPath = %q, want %q", outgoing.Outgoings[0].ToPath, "/archive/target")
	}
	if outgoing.Outgoings[0].Broken {
		t.Fatalf("expected path-hint wikilink to remain valid after move")
	}
}

func TestEnsurePathUseCase_HealsLinksForAllCreatedSegments(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	ensureUC := pages.NewEnsurePathUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default())

	pageA, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Page A", Slug: "a", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("CreatePage A failed: %v", err)
	}

	contentA := "Links: [X](/x) and [XY](/x/y)"
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "system", ID: pageA.Page.ID, Version: pageA.Page.Version(), Title: pageA.Page.Title, Slug: pageA.Page.Slug, Content: &contentA}); err != nil {
		t.Fatalf("UpdatePage A failed: %v", err)
	}

	if err := deps.links.IndexAllPages(); err != nil {
		t.Fatalf("IndexAllPages failed: %v", err)
	}

	out1, err := deps.links.GetOutgoingLinksForPage(pageA.Page.ID)
	if err != nil {
		t.Fatalf("GetOutgoingLinks failed: %v", err)
	}
	if out1.Count != 2 {
		t.Fatalf("expected 2 outgoings before ensure, got %d: %#v", out1.Count, out1.Outgoings)
	}

	byPath := map[string]bool{}
	for _, it := range out1.Outgoings {
		byPath[it.ToPath] = it.Broken
	}
	if broken, ok := byPath["/x"]; !ok || !broken {
		t.Fatalf("expected /x to be broken before ensure, got map=%#v, out=%#v", byPath, out1.Outgoings)
	}
	if broken, ok := byPath["/x/y"]; !ok || !broken {
		t.Fatalf("expected /x/y to be broken before ensure, got map=%#v, out=%#v", byPath, out1.Outgoings)
	}

	if _, err := ensureUC.Execute(context.Background(), pages.EnsurePathInput{
		UserID: "system", TargetPath: "/x/y", TargetTitle: "X Y", Kind: pageKind(),
	}); err != nil {
		t.Fatalf("EnsurePath failed: %v", err)
	}

	out2, err := deps.links.GetOutgoingLinksForPage(pageA.Page.ID)
	if err != nil {
		t.Fatalf("GetOutgoingLinks (after ensure) failed: %v", err)
	}
	if out2.Count != 2 {
		t.Fatalf("expected 2 outgoings after ensure, got %d: %#v", out2.Count, out2.Outgoings)
	}

	var gotX, gotXY *struct {
		broken bool
		toPage string
	}
	for _, it := range out2.Outgoings {
		if it.ToPath == "/x" {
			gotX = &struct {
				broken bool
				toPage string
			}{it.Broken, it.ToPageID}
		}
		if it.ToPath == "/x/y" {
			gotXY = &struct {
				broken bool
				toPage string
			}{it.Broken, it.ToPageID}
		}
	}

	if gotX == nil || gotX.broken || gotX.toPage == "" {
		t.Fatalf("expected /x healed with ToPageID, got %#v", out2.Outgoings)
	}
	if gotXY == nil || gotXY.broken || gotXY.toPage == "" {
		t.Fatalf("expected /x/y healed with ToPageID, got %#v", out2.Outgoings)
	}
}

func TestDeletePageUseCase_NonRecursive_MarksIncomingBroken(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	deleteUC := pages.NewDeletePageUseCase(deps.tree, deps.revision, deps.assets, deps.favorites, deps.orchestrator(), slog.Default(), nil)

	a, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Page A", Slug: "a", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("CreatePage A failed: %v", err)
	}
	contentA := "Link to B: [Go](/b)"
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "system", ID: a.Page.ID, Version: a.Page.Version(), Title: a.Page.Title, Slug: a.Page.Slug, Content: &contentA}); err != nil {
		t.Fatalf("UpdatePage A failed: %v", err)
	}

	b, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Page B", Slug: "b", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("CreatePage B failed: %v", err)
	}
	contentB := "# Page B"
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "system", ID: b.Page.ID, Version: b.Page.Version(), Title: b.Page.Title, Slug: b.Page.Slug, Content: &contentB}); err != nil {
		t.Fatalf("UpdatePage B failed: %v", err)
	}

	if err := deps.links.IndexAllPages(); err != nil {
		t.Fatalf("IndexAllPages failed: %v", err)
	}
	currentB, err := deps.tree.GetPage(b.Page.ID)
	if err != nil {
		t.Fatalf("GetPage B before delete failed: %v", err)
	}
	if err := deleteUC.Execute(context.Background(), pages.DeletePageInput{
		UserID: "system", ID: b.Page.ID, Version: currentB.Version(), Recursive: false,
	}); err != nil {
		t.Fatalf("DeletePage failed: %v", err)
	}

	out, err := deps.links.GetOutgoingLinksForPage(a.Page.ID)
	if err != nil {
		t.Fatalf("GetOutgoingLinks failed: %v", err)
	}
	if out.Count != 1 {
		t.Fatalf("expected 1 outgoing, got %d", out.Count)
	}
	got := out.Outgoings[0]
	if got.ToPath != "/b" || !got.Broken || got.ToPageID != "" {
		t.Fatalf("unexpected outgoing after delete: %#v", got)
	}

	bl, err := deps.links.GetBacklinksForPage(b.Page.ID)
	if err != nil {
		t.Fatalf("GetBacklinks failed: %v", err)
	}
	if bl.Count != 0 {
		t.Fatalf("expected 0 backlinks after delete, got %d", bl.Count)
	}
}

func TestDeletePageUseCase_Recursive_RemovesOutgoingAndBreaksIncomingForDeletedSubtree(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	deleteUC := pages.NewDeletePageUseCase(deps.tree, deps.revision, deps.assets, deps.favorites, deps.orchestrator(), slog.Default(), nil)

	docs, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Docs", Slug: "docs", Kind: pageKind(),
	})
	a, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", ParentID: &docs.Page.ID, Title: "A", Slug: "a", Kind: pageKind(),
	})
	b, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", ParentID: &docs.Page.ID, Title: "B", Slug: "b", Kind: pageKind(),
	})

	contentA := "Link to B: [B](/docs/b)"
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "system", ID: a.Page.ID, Version: a.Page.Version(), Title: a.Page.Title, Slug: a.Page.Slug, Content: &contentA}); err != nil {
		t.Fatalf("UpdatePage a failed: %v", err)
	}
	contentB := "# B"
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "system", ID: b.Page.ID, Version: b.Page.Version(), Title: b.Page.Title, Slug: b.Page.Slug, Content: &contentB}); err != nil {
		t.Fatalf("UpdatePage b failed: %v", err)
	}

	c, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "C", Slug: "c", Kind: pageKind(),
	})
	contentC := "Incoming link: [B](/docs/b)"
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "system", ID: c.Page.ID, Version: c.Page.Version(), Title: c.Page.Title, Slug: c.Page.Slug, Content: &contentC}); err != nil {
		t.Fatalf("UpdatePage c failed: %v", err)
	}

	if err := deps.links.IndexAllPages(); err != nil {
		t.Fatalf("IndexAllPages failed: %v", err)
	}
	outA, err := deps.links.GetOutgoingLinksForPage(a.Page.ID)
	if err != nil || outA.Count != 1 {
		t.Fatalf("expected 1 outgoing from a before delete, got err=%v out=%#v", err, outA)
	}

	if err := deleteUC.Execute(context.Background(), pages.DeletePageInput{
		UserID: "system", ID: docs.Page.ID, Version: docs.Page.Version(), Recursive: true,
	}); err != nil {
		t.Fatalf("DeletePage(docs, recursive) failed: %v", err)
	}

	outAAfter, err := deps.links.GetOutgoingLinksForPage(a.Page.ID)
	if err != nil {
		t.Fatalf("GetOutgoingLinks(a) after delete failed: %v", err)
	}
	if outAAfter.Count != 0 {
		t.Fatalf("expected 0 outgoing from deleted page a, got %d", outAAfter.Count)
	}

	outC, err := deps.links.GetOutgoingLinksForPage(c.Page.ID)
	if err != nil {
		t.Fatalf("GetOutgoingLinks(c) after delete failed: %v", err)
	}
	if outC.Count != 1 {
		t.Fatalf("expected 1 outgoing from c, got %d", outC.Count)
	}
	got := outC.Outgoings[0]
	if got.ToPath != "/docs/b" || !got.Broken || got.ToPageID != "" {
		t.Fatalf("unexpected outgoing after recursive delete: %#v", got)
	}
}

// Gap 1: deleting one of two same-title pages should heal [[Title]] sentinels
// that are now unambiguous (exactly one page with that title remains).
func TestDeletePageUseCase_SingleDelete_HealsSentinelWhenDuplicateTitleRemoved(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	deleteUC := pages.NewDeletePageUseCase(deps.tree, deps.revision, deps.assets, deps.favorites, deps.orchestrator(), slog.Default(), nil)

	// Two pages share the title "Kafka".
	kafka1, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Kafka", Slug: "kafka1", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("CreatePage kafka1: %v", err)
	}
	kafka2, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Kafka", Slug: "kafka2", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("CreatePage kafka2: %v", err)
	}

	// Source page writes [[Kafka]] while two matches exist → sentinel broken=1.
	source, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Source", Slug: "source", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("CreatePage source: %v", err)
	}
	content := "See [[Kafka]]."
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "system", ID: source.Page.ID, Version: source.Page.Version(),
		Title: source.Page.Title, Slug: source.Page.Slug, Content: &content}); err != nil {
		t.Fatalf("UpdatePage source: %v", err)
	}

	// Precondition: [[Kafka]] is a broken sentinel (ambiguous).
	out, err := deps.links.GetOutgoingLinksForPage(source.Page.ID)
	if err != nil {
		t.Fatalf("GetOutgoingLinksForPage: %v", err)
	}
	if out.Count != 1 || !out.Outgoings[0].Broken {
		t.Fatalf("precondition: expected broken sentinel, got %+v", out)
	}

	// Delete kafka1 → only kafka2 remains → sentinel should be healed.
	if err := deleteUC.Execute(context.Background(), pages.DeletePageInput{
		UserID: "system", ID: kafka1.Page.ID, Version: kafka1.Page.Version(), Recursive: false,
	}); err != nil {
		t.Fatalf("DeletePage kafka1: %v", err)
	}

	out, err = deps.links.GetOutgoingLinksForPage(source.Page.ID)
	if err != nil {
		t.Fatalf("GetOutgoingLinksForPage after delete: %v", err)
	}
	if out.Count != 1 {
		t.Fatalf("expected 1 outgoing, got %d", out.Count)
	}
	if out.Outgoings[0].Broken {
		t.Fatalf("[[Kafka]] should be healed to kafka2 after kafka1 deleted, but is still broken")
	}
	if out.Outgoings[0].ToPageID != kafka2.Page.ID {
		t.Fatalf("ToPageID = %q, want kafka2 %q", out.Outgoings[0].ToPageID, kafka2.Page.ID)
	}
}

// Gap 1 (recursive): deleting a subtree that contains one of two same-title pages
// should heal [[Title]] sentinels for titles that are now unambiguous.
func TestDeletePageUseCase_Recursive_HealsSentinelWhenDuplicateTitleRemoved(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	deleteUC := pages.NewDeletePageUseCase(deps.tree, deps.revision, deps.assets, deps.favorites, deps.orchestrator(), slog.Default(), nil)

	// kafka1 lives inside a section that we will delete recursively.
	section, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Section", Slug: "section", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("CreatePage section: %v", err)
	}
	kafka1, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", ParentID: &section.Page.ID, Title: "Kafka", Slug: "kafka1", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("CreatePage kafka1: %v", err)
	}
	_ = kafka1
	kafka2, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Kafka", Slug: "kafka2", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("CreatePage kafka2: %v", err)
	}

	source, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Source", Slug: "source", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("CreatePage source: %v", err)
	}
	content := "See [[Kafka]]."
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "system", ID: source.Page.ID, Version: source.Page.Version(),
		Title: source.Page.Title, Slug: source.Page.Slug, Content: &content}); err != nil {
		t.Fatalf("UpdatePage source: %v", err)
	}

	out, err := deps.links.GetOutgoingLinksForPage(source.Page.ID)
	if err != nil {
		t.Fatalf("GetOutgoingLinksForPage: %v", err)
	}
	if out.Count != 1 || !out.Outgoings[0].Broken {
		t.Fatalf("precondition: expected broken sentinel, got %+v", out)
	}

	// Delete the whole section (contains kafka1) → kafka2 remains → sentinel healed.
	sectionPage, err := deps.tree.GetPage(section.Page.ID)
	if err != nil {
		t.Fatalf("GetPage section: %v", err)
	}
	if err := deleteUC.Execute(context.Background(), pages.DeletePageInput{
		UserID: "system", ID: sectionPage.ID, Version: sectionPage.Version(), Recursive: true,
	}); err != nil {
		t.Fatalf("DeletePage section (recursive): %v", err)
	}

	out, err = deps.links.GetOutgoingLinksForPage(source.Page.ID)
	if err != nil {
		t.Fatalf("GetOutgoingLinksForPage after recursive delete: %v", err)
	}
	if out.Count != 1 {
		t.Fatalf("expected 1 outgoing, got %d", out.Count)
	}
	if out.Outgoings[0].Broken {
		t.Fatalf("[[Kafka]] should be healed to kafka2 after section deleted, but is still broken")
	}
	if out.Outgoings[0].ToPageID != kafka2.Page.ID {
		t.Fatalf("ToPageID = %q, want kafka2 %q", out.Outgoings[0].ToPageID, kafka2.Page.ID)
	}
}

// Gap 1 (recursive, healed sentinel): when a recursive delete removes the only
// page a [[Title]] sentinel was healed to, the link must be marked broken.
// Path-based invalidation misses healed sentinels because their to_path is
// "wikilink:X", not the route path. Target-ID reconciliation must cover every
// page in the subtree.
//
// Critical setup: [[Kafka]] must be written BEFORE the kafka page exists so it
// is stored as a broken sentinel (to_path="wikilink:Kafka"). Only then does
// healing via HealWikiLinksForPage produce a healed sentinel (broken=0,
// to_page_id=kafka1, to_path="wikilink:Kafka").
func TestDeletePageUseCase_Recursive_MarksHealedWikiLinkSentinelBroken(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	deleteUC := pages.NewDeletePageUseCase(deps.tree, deps.revision, deps.assets, deps.favorites, deps.orchestrator(), slog.Default(), nil)

	// Step 1: source writes [[Kafka]] before any Kafka page exists → broken sentinel.
	source, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Source", Slug: "source", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("CreatePage source: %v", err)
	}
	content := "See [[Kafka]]."
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "system", ID: source.Page.ID, Version: source.Page.Version(),
		Title: source.Page.Title, Slug: source.Page.Slug, Content: &content}); err != nil {
		t.Fatalf("UpdatePage source: %v", err)
	}

	// Step 2: create kafka1 inside a section → HealWikiLinksForPage heals the sentinel
	// to broken=0, to_page_id=kafka1, to_path="wikilink:Kafka" (not the route path).
	section, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Section", Slug: "section", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("CreatePage section: %v", err)
	}
	_, err = createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", ParentID: &section.Page.ID, Title: "Kafka", Slug: "kafka1", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("CreatePage kafka1: %v", err)
	}

	// Precondition: sentinel is healed (broken=0, to_path="wikilink:Kafka").
	out, err := deps.links.GetOutgoingLinksForPage(source.Page.ID)
	if err != nil {
		t.Fatalf("GetOutgoingLinksForPage: %v", err)
	}
	if out.Count != 1 || out.Outgoings[0].Broken {
		t.Fatalf("precondition: expected healed sentinel, got %+v", out)
	}

	// Step 3: delete the whole section recursively.
	// The sentinel must be invalidated by kafka1's target ID; its wikilink path
	// does not share the deleted section's route prefix.
	sectionPage, err := deps.tree.GetPage(section.Page.ID)
	if err != nil {
		t.Fatalf("GetPage section: %v", err)
	}
	if err := deleteUC.Execute(context.Background(), pages.DeletePageInput{
		UserID: "system", ID: sectionPage.ID, Version: sectionPage.Version(), Recursive: true,
	}); err != nil {
		t.Fatalf("DeletePage section (recursive): %v", err)
	}

	out, err = deps.links.GetOutgoingLinksForPage(source.Page.ID)
	if err != nil {
		t.Fatalf("GetOutgoingLinksForPage after recursive delete: %v", err)
	}
	if out.Count != 1 {
		t.Fatalf("expected 1 outgoing, got %d", out.Count)
	}
	if !out.Outgoings[0].Broken {
		t.Fatalf("[[Kafka]] should be broken after kafka1 recursively deleted, but is still resolved to %q", out.Outgoings[0].ToPageID)
	}
}

func TestUpdatePageUseCase_RenamePage_MarksOldBroken_HealsNewExactPath(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)

	a, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "A", Slug: "a", Kind: pageKind(),
	})
	contentA := "Links: [B](/b) and [B2](/b2)"
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "system", ID: a.Page.ID, Version: a.Page.Version(), Title: a.Page.Title, Slug: a.Page.Slug, Content: &contentA}); err != nil {
		t.Fatalf("UpdatePage A failed: %v", err)
	}

	b, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "B", Slug: "b", Kind: pageKind(),
	})
	contentB := "# B"
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "system", ID: b.Page.ID, Version: b.Page.Version(), Title: b.Page.Title, Slug: b.Page.Slug, Content: &contentB}); err != nil {
		t.Fatalf("UpdatePage B failed: %v", err)
	}

	if err := deps.links.IndexAllPages(); err != nil {
		t.Fatalf("IndexAllPages failed: %v", err)
	}
	out1, err := deps.links.GetOutgoingLinksForPage(a.Page.ID)
	if err != nil || out1.Count != 2 {
		t.Fatalf("unexpected outgoing before rename err=%v out=%#v", err, out1)
	}

	currentB, err := deps.tree.GetPage(b.Page.ID)
	if err != nil {
		t.Fatalf("GetPage B before rename: %v", err)
	}
	contentB2 := "# B (renamed)"
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "system", ID: currentB.ID, Version: currentB.Version(), Title: currentB.Title, Slug: "b2", Content: &contentB2}); err != nil {
		t.Fatalf("Rename B failed: %v", err)
	}

	out2, err := deps.links.GetOutgoingLinksForPage(a.Page.ID)
	if err != nil || out2.Count != 2 {
		t.Fatalf("unexpected outgoing after rename err=%v out=%#v", err, out2)
	}
	byPath := map[string]struct {
		broken bool
		toID   string
	}{}
	for _, it := range out2.Outgoings {
		byPath[it.ToPath] = struct {
			broken bool
			toID   string
		}{it.Broken, it.ToPageID}
	}
	if got, ok := byPath["/b"]; !ok || !got.broken || got.toID != "" {
		t.Fatalf("expected /b broken after rename, got %#v", byPath)
	}
	if got, ok := byPath["/b2"]; !ok || got.broken || got.toID != b.Page.ID {
		t.Fatalf("expected /b2 healed to %q, got %#v", b.Page.ID, byPath)
	}
}

func TestUpdatePageUseCase_RenameSubtree_BreaksOldPrefix_HealsNewSubpaths(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)

	docs, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Docs", Slug: "docs", Kind: pageKind(),
	})
	b, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", ParentID: &docs.Page.ID, Title: "B", Slug: "b", Kind: pageKind(),
	})
	contentB := "# B"
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "system", ID: b.Page.ID, Version: b.Page.Version(), Title: b.Page.Title, Slug: b.Page.Slug, Content: &contentB}); err != nil {
		t.Fatalf("UpdatePage B failed: %v", err)
	}

	a, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "A", Slug: "a", Kind: pageKind(),
	})
	contentA := "Links: [Old](/docs/b) and [New](/docs2/b)"
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "system", ID: a.Page.ID, Version: a.Page.Version(), Title: a.Page.Title, Slug: a.Page.Slug, Content: &contentA}); err != nil {
		t.Fatalf("UpdatePage A failed: %v", err)
	}

	if err := deps.links.IndexAllPages(); err != nil {
		t.Fatalf("IndexAllPages failed: %v", err)
	}

	contentDocs2 := "# Docs"
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "system", ID: docs.Page.ID, Version: docs.Page.Version(), Title: docs.Page.Title, Slug: "docs2", Content: &contentDocs2}); err != nil {
		t.Fatalf("Rename docs failed: %v", err)
	}

	out2, err := deps.links.GetOutgoingLinksForPage(a.Page.ID)
	if err != nil || out2.Count != 2 {
		t.Fatalf("unexpected outgoing after subtree rename err=%v out=%#v", err, out2)
	}
	byPath := map[string]struct {
		broken bool
		toID   string
	}{}
	for _, it := range out2.Outgoings {
		byPath[it.ToPath] = struct {
			broken bool
			toID   string
		}{it.Broken, it.ToPageID}
	}
	if got, ok := byPath["/docs/b"]; !ok || !got.broken || got.toID != "" {
		t.Fatalf("expected /docs/b broken, got %#v", byPath)
	}
	if got, ok := byPath["/docs2/b"]; !ok || got.broken || got.toID != b.Page.ID {
		t.Fatalf("expected /docs2/b healed to %q, got %#v", b.Page.ID, byPath)
	}
}

func TestMovePageUseCase_MarksOldBroken_HealsNewExactPath(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	moveUC := pages.NewMovePageUseCase(deps.tree, deps.orchestrator(), slog.Default(), nil)

	a, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "A", Slug: "a", Kind: pageKind(),
	})
	contentA := "Links: [B](/b) and [B2](/projects/b)"
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "system", ID: a.Page.ID, Version: a.Page.Version(), Title: a.Page.Title, Slug: a.Page.Slug, Content: &contentA}); err != nil {
		t.Fatalf("UpdatePage A failed: %v", err)
	}

	b, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "B", Slug: "b", Kind: pageKind(),
	})
	contentB := "# B"
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "system", ID: b.Page.ID, Version: b.Page.Version(), Title: b.Page.Title, Slug: b.Page.Slug, Content: &contentB}); err != nil {
		t.Fatalf("UpdatePage B failed: %v", err)
	}

	projects, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Projects", Slug: "projects", Kind: pageKind(),
	})
	if err := deps.links.IndexAllPages(); err != nil {
		t.Fatalf("IndexAllPages failed: %v", err)
	}
	currentB, err := deps.tree.GetPage(b.Page.ID)
	if err != nil {
		t.Fatalf("GetPage B before move: %v", err)
	}

	if err := moveUC.Execute(context.Background(), pages.MovePageInput{
		UserID: "system", ID: currentB.ID, Version: currentB.Version(), ParentID: projects.Page.ID,
	}); err != nil {
		t.Fatalf("MovePage failed: %v", err)
	}

	out2, err := deps.links.GetOutgoingLinksForPage(a.Page.ID)
	if err != nil || out2.Count != 2 {
		t.Fatalf("unexpected outgoing after move err=%v out=%#v", err, out2)
	}
	state := map[string]struct {
		broken bool
		toID   string
	}{}
	for _, it := range out2.Outgoings {
		state[it.ToPath] = struct {
			broken bool
			toID   string
		}{it.Broken, it.ToPageID}
	}
	if got := state["/b"]; !got.broken || got.toID != "" {
		t.Fatalf("expected /b broken after move, got %#v", state)
	}
	if got := state["/projects/b"]; got.broken || got.toID != b.Page.ID {
		t.Fatalf("expected /projects/b healed to %q, got %#v", b.Page.ID, state)
	}
}

func TestMovePageUseCase_MoveSubtree_BreaksOldPrefix_HealsNewSubpaths(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	moveUC := pages.NewMovePageUseCase(deps.tree, deps.orchestrator(), slog.Default(), nil)

	docs, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Docs", Slug: "docs", Kind: pageKind(),
	})
	b, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", ParentID: &docs.Page.ID, Title: "B", Slug: "b", Kind: pageKind(),
	})
	contentB := "# B"
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "system", ID: b.Page.ID, Version: b.Page.Version(), Title: b.Page.Title, Slug: b.Page.Slug, Content: &contentB}); err != nil {
		t.Fatalf("UpdatePage B failed: %v", err)
	}

	archive, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Archive", Slug: "archive", Kind: pageKind(),
	})
	a, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "A", Slug: "a", Kind: pageKind(),
	})
	contentA := "Links: [Old](/docs/b) and [New](/archive/docs/b)"
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "system", ID: a.Page.ID, Version: a.Page.Version(), Title: a.Page.Title, Slug: a.Page.Slug, Content: &contentA}); err != nil {
		t.Fatalf("UpdatePage A failed: %v", err)
	}
	if err := deps.links.IndexAllPages(); err != nil {
		t.Fatalf("IndexAllPages failed: %v", err)
	}

	if err := moveUC.Execute(context.Background(), pages.MovePageInput{
		UserID: "system", ID: docs.Page.ID, Version: docs.Page.Version(), ParentID: archive.Page.ID,
	}); err != nil {
		t.Fatalf("MovePage(docs -> archive) failed: %v", err)
	}

	out, err := deps.links.GetOutgoingLinksForPage(a.Page.ID)
	if err != nil || out.Count != 2 {
		t.Fatalf("unexpected outgoing after subtree move err=%v out=%#v", err, out)
	}
	state := map[string]struct {
		broken bool
		toID   string
	}{}
	for _, it := range out.Outgoings {
		state[it.ToPath] = struct {
			broken bool
			toID   string
		}{it.Broken, it.ToPageID}
	}
	if got := state["/docs/b"]; !got.broken || got.toID != "" {
		t.Fatalf("expected /docs/b broken after move, got %#v", state)
	}
	if got := state["/archive/docs/b"]; got.broken || got.toID != b.Page.ID {
		t.Fatalf("expected /archive/docs/b healed to %q, got %#v", b.Page.ID, state)
	}
}

func TestMovePageUseCase_ReindexesRelativeLinks(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	moveUC := pages.NewMovePageUseCase(deps.tree, deps.orchestrator(), slog.Default(), nil)

	docs, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Docs", Slug: "docs", Kind: pageKind(),
	})
	docsShared, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", ParentID: &docs.Page.ID, Title: "Shared", Slug: "shared", Kind: pageKind(),
	})
	contentDocsShared := "# Docs Shared"
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "system", ID: docsShared.Page.ID, Version: docsShared.Page.Version(), Title: docsShared.Page.Title, Slug: docsShared.Page.Slug, Content: &contentDocsShared}); err != nil {
		t.Fatalf("UpdatePage /docs/shared failed: %v", err)
	}

	a, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", ParentID: &docs.Page.ID, Title: "A", Slug: "a", Kind: pageKind(),
	})
	contentA := "Relative: [S](../shared)"
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "system", ID: a.Page.ID, Version: a.Page.Version(), Title: a.Page.Title, Slug: a.Page.Slug, Content: &contentA}); err != nil {
		t.Fatalf("UpdatePage /docs/a failed: %v", err)
	}

	guide, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Guide", Slug: "guide", Kind: pageKind(),
	})
	guideShared, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", ParentID: &guide.Page.ID, Title: "Shared", Slug: "shared", Kind: pageKind(),
	})
	contentGuideShared := "# Guide Shared"
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "system", ID: guideShared.Page.ID, Version: guideShared.Page.Version(), Title: guideShared.Page.Title, Slug: guideShared.Page.Slug, Content: &contentGuideShared}); err != nil {
		t.Fatalf("UpdatePage /guide/shared failed: %v", err)
	}

	if err := deps.links.IndexAllPages(); err != nil {
		t.Fatalf("IndexAllPages failed: %v", err)
	}

	out1, err := deps.links.GetOutgoingLinksForPage(a.Page.ID)
	if err != nil || out1.Count != 1 {
		t.Fatalf("unexpected outgoing before move err=%v out=%#v", err, out1)
	}
	if out1.Outgoings[0].ToPath != "/docs/shared" || out1.Outgoings[0].Broken || out1.Outgoings[0].ToPageID != docsShared.Page.ID {
		t.Fatalf("unexpected outgoing before move: %#v", out1.Outgoings[0])
	}
	currentA, err := deps.tree.GetPage(a.Page.ID)
	if err != nil {
		t.Fatalf("GetPage A before move: %v", err)
	}

	if err := moveUC.Execute(context.Background(), pages.MovePageInput{
		UserID: "system", ID: currentA.ID, Version: currentA.Version(), ParentID: guide.Page.ID,
	}); err != nil {
		t.Fatalf("MovePage(a -> guide) failed: %v", err)
	}

	out2, err := deps.links.GetOutgoingLinksForPage(a.Page.ID)
	if err != nil || out2.Count != 1 {
		t.Fatalf("unexpected outgoing after move err=%v out=%#v", err, out2)
	}
	if out2.Outgoings[0].ToPath != "/guide/shared" || out2.Outgoings[0].Broken || out2.Outgoings[0].ToPageID != guideShared.Page.ID {
		t.Fatalf("unexpected outgoing after move: %#v", out2.Outgoings[0])
	}
}

func TestAssetUseCases_RecordAssetRevisionForUser(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	uploadUC := wikiassets.NewUploadAssetUseCase(deps.tree, deps.assets, deps.revision, slog.Default())
	renameUC := wikiassets.NewRenameAssetUseCase(deps.tree, deps.assets, deps.revision, slog.Default())
	deleteUC := wikiassets.NewDeleteAssetUseCase(deps.tree, deps.assets, deps.revision, slog.Default())
	listUC := wikiassets.NewListAssetsUseCase(deps.tree, deps.assets)

	writeAsset := func(t *testing.T, pageID, name string, data []byte) {
		t.Helper()
		assetDir := filepath.Join(deps.assets.GetAssetsDir(), pageID)
		if err := os.MkdirAll(assetDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(assetDir) failed: %v", err)
		}
		if err := os.WriteFile(filepath.Join(assetDir, name), data, 0o644); err != nil {
			t.Fatalf("WriteFile(asset) failed: %v", err)
		}
	}

	tests := []struct {
		name      string
		setup     func(t *testing.T, pageID string)
		operate   func(t *testing.T, pageID string)
		wantAsset string
	}{
		{
			name: "upload",
			operate: func(t *testing.T, pageID string) {
				t.Helper()
				file, err := os.CreateTemp(t.TempDir(), "asset-upload-*")
				if err != nil {
					t.Fatalf("CreateTemp failed: %v", err)
				}
				t.Cleanup(func() {
					if err := file.Close(); err != nil {
						t.Fatalf("Close(file) failed: %v", err)
					}
				})
				if _, err := file.WriteString("payload"); err != nil {
					t.Fatalf("WriteString(file) failed: %v", err)
				}
				if _, err := file.Seek(0, io.SeekStart); err != nil {
					t.Fatalf("Seek(file) failed: %v", err)
				}
				if _, err := uploadUC.Execute(context.Background(), wikiassets.UploadAssetInput{
					UserID: "editor", PageID: pageID, File: file, Filename: "uploaded.txt", MaxBytes: 1024,
				}); err != nil {
					t.Fatalf("UploadAsset failed: %v", err)
				}
			},
			wantAsset: "uploaded.txt",
		},
		{
			name: "rename",
			setup: func(t *testing.T, pageID string) {
				t.Helper()
				writeAsset(t, pageID, "old.txt", []byte("payload"))
			},
			operate: func(t *testing.T, pageID string) {
				t.Helper()
				if _, err := renameUC.Execute(context.Background(), wikiassets.RenameAssetInput{
					UserID: "editor", PageID: pageID, OldFilename: "old.txt", NewFilename: "new.txt",
				}); err != nil {
					t.Fatalf("RenameAsset failed: %v", err)
				}
			},
			wantAsset: "new.txt",
		},
		{
			name: "delete",
			setup: func(t *testing.T, pageID string) {
				t.Helper()
				writeAsset(t, pageID, "delete.txt", []byte("payload"))
				if _, _, err := deps.revision.RecordAssetChange(pageID, "system", ""); err != nil {
					t.Fatalf("RecordAssetChange failed: %v", err)
				}
			},
			operate: func(t *testing.T, pageID string) {
				t.Helper()
				if err := deleteUC.Execute(context.Background(), wikiassets.DeleteAssetInput{
					UserID: "editor", PageID: pageID, Filename: "delete.txt",
				}); err != nil {
					t.Fatalf("DeleteAsset failed: %v", err)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			page, err := createUC.Execute(context.Background(), pages.CreatePageInput{
				UserID: "system", Title: "Asset Page " + tc.name, Slug: "asset-page-" + tc.name, Kind: pageKind(),
			})
			if err != nil {
				t.Fatalf("CreatePage failed: %v", err)
			}
			if tc.setup != nil {
				tc.setup(t, page.Page.ID)
			}
			tc.operate(t, page.Page.ID)

			latest, err := deps.revision.GetLatestRevision(page.Page.ID)
			if err != nil {
				t.Fatalf("GetLatestRevision failed: %v", err)
			}
			if latest == nil || latest.Type != revision.RevisionTypeAssetUpdate {
				t.Fatalf("latest revision = %#v", latest)
			}
			if latest.AuthorID != "editor" {
				t.Fatalf("latest author = %q, want %q", latest.AuthorID, "editor")
			}

			assetsOut, err := listUC.Execute(context.Background(), wikiassets.ListAssetsInput{PageID: page.Page.ID})
			if err != nil {
				t.Fatalf("ListAssets failed: %v", err)
			}
			if tc.wantAsset == "" {
				if len(assetsOut.Files) != 0 {
					t.Fatalf("assets = %#v, want empty", assetsOut.Files)
				}
				return
			}
			if len(assetsOut.Files) != 1 || !strings.HasSuffix(assetsOut.Files[0], "/"+tc.wantAsset) {
				t.Fatalf("assets = %#v, want suffix %q", assetsOut.Files, tc.wantAsset)
			}
		})
	}
}

func TestCheckIntegrityUseCase_Passthrough(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	checkUC := wikirevisions.NewCheckIntegrityUseCase(deps.revision)

	page, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Page", Slug: "page", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("CreatePage failed: %v", err)
	}
	content := "hello"
	pageOut, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "system", ID: page.Page.ID, Version: page.Page.Version(), Title: page.Page.Title, Slug: page.Page.Slug, Content: &content})
	if err != nil {
		t.Fatalf("UpdatePage failed: %v", err)
	}

	rev, err := deps.revision.GetLatestRevision(pageOut.Page.ID)
	if err != nil || rev == nil {
		t.Fatalf("GetLatestRevision failed: %#v %v", rev, err)
	}
	contentBlobPath := filepath.Join(deps.storageDir, ".leafwiki", "blobs", "content", pageOut.Page.ID, "sha256", rev.ContentHash[:2], rev.ContentHash)
	if err := os.Remove(contentBlobPath); err != nil {
		t.Fatalf("Remove content blob failed: %v", err)
	}

	out, err := checkUC.Execute(context.Background(), wikirevisions.CheckIntegrityInput{PageID: pageOut.Page.ID})
	if err != nil {
		t.Fatalf("CheckRevisionIntegrity failed: %v", err)
	}
	if len(out.Issues) != 1 {
		t.Fatalf("expected 1 integrity issue, got %#v", out.Issues)
	}
	if out.Issues[0].Code != "missing_content_blob" {
		t.Fatalf("unexpected integrity issue: %#v", out.Issues[0])
	}
}

func TestEnsurePathUseCase_RecordsRevisionForEachCreatedSegment(t *testing.T) {
	deps := newTestDeps(t)
	uc := pages.NewEnsurePathUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default())

	out, err := uc.Execute(context.Background(), pages.EnsurePathInput{
		UserID: "system", TargetPath: "/x/y", TargetTitle: "X Y", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("EnsurePath failed: %v", err)
	}

	latestY, err := deps.revision.GetLatestRevision(out.Page.ID)
	if err != nil {
		t.Fatalf("GetLatestRevision(y) failed: %v", err)
	}
	if latestY == nil || latestY.Summary != "page created via ensure path" {
		t.Fatalf("unexpected y latest revision: %#v", latestY)
	}

	xPage, err := deps.tree.FindPageByRoutePath("x")
	if err != nil {
		t.Fatalf("FindByPath x failed: %v", err)
	}
	latestX, err := deps.revision.GetLatestRevision(xPage.ID)
	if err != nil {
		t.Fatalf("GetLatestRevision(x) failed: %v", err)
	}
	if latestX == nil || latestX.Summary != "page created via ensure path" {
		t.Fatalf("unexpected x latest revision: %#v", latestX)
	}
}

func TestMovePageUseCase_RecordsStructureRevision(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	moveUC := pages.NewMovePageUseCase(deps.tree, deps.orchestrator(), slog.Default(), nil)

	dest, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Dest", Slug: "dest", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("CreatePage(dest) failed: %v", err)
	}
	page, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Move Me", Slug: "move-me", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("CreatePage(page) failed: %v", err)
	}

	if err := moveUC.Execute(context.Background(), pages.MovePageInput{
		UserID: "system", ID: page.Page.ID, Version: page.Page.Version(), ParentID: dest.Page.ID,
	}); err != nil {
		t.Fatalf("MovePage failed: %v", err)
	}

	latest, err := deps.revision.GetLatestRevision(page.Page.ID)
	if err != nil {
		t.Fatalf("GetLatestRevision failed: %v", err)
	}
	if latest == nil || latest.Type != revision.RevisionTypeStructureUpdate {
		t.Fatalf("latest revision = %#v", latest)
	}
	if latest.ParentID != dest.Page.ID {
		t.Fatalf("latest parent id = %q, want %q", latest.ParentID, dest.Page.ID)
	}
}

func TestUpdatePageUseCase_TitleOnlyCreatesStructureRevision(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)

	page, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Original", Slug: "original", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("CreatePage failed: %v", err)
	}

	content := "same content"
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "system", ID: page.Page.ID, Version: page.Page.Version(), Title: page.Page.Title, Slug: page.Page.Slug, Content: &content}); err != nil {
		t.Fatalf("UpdatePage(initial content) failed: %v", err)
	}

	beforeLatest, err := deps.revision.GetLatestRevision(page.Page.ID)
	if err != nil {
		t.Fatalf("GetLatestRevision(before rename) failed: %v", err)
	}
	if beforeLatest == nil {
		t.Fatal("expected initial content revision")
	}
	currentPage, err := deps.tree.GetPage(page.Page.ID)
	if err != nil {
		t.Fatalf("GetPage before title update: %v", err)
	}

	updatedPage, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "system", ID: currentPage.ID, Version: currentPage.Version(), Title: "Renamed Title", Slug: currentPage.Slug, Content: nil})
	if err != nil {
		t.Fatalf("UpdatePage(title only) failed: %v", err)
	}
	if updatedPage.Page.Title != "Renamed Title" {
		t.Fatalf("updated title = %q", updatedPage.Page.Title)
	}

	afterLatest, err := deps.revision.GetLatestRevision(page.Page.ID)
	if err != nil {
		t.Fatalf("GetLatestRevision(after rename) failed: %v", err)
	}
	if afterLatest == nil || afterLatest.ID == beforeLatest.ID {
		t.Fatalf("expected new revision for title-only change, got before=%#v after=%#v", beforeLatest, afterLatest)
	}
	if afterLatest.Type != revision.RevisionTypeStructureUpdate {
		t.Fatalf("latest revision type = %q", afterLatest.Type)
	}

	revisions, err := deps.revision.ListRevisions(page.Page.ID)
	if err != nil {
		t.Fatalf("ListRevisions failed: %v", err)
	}
	if len(revisions) != 3 {
		t.Fatalf("revision count = %d, want 3", len(revisions))
	}
}

func TestUpdatePageUseCase_TitleOnlyWithUnchangedContentCreatesStructureRevision(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), nil)

	page, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Original", Slug: "original", Kind: pageKind(),
	})
	if err != nil {
		t.Fatalf("CreatePage failed: %v", err)
	}

	content := "same content"
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "system", ID: page.Page.ID, Version: page.Page.Version(), Title: page.Page.Title, Slug: page.Page.Slug, Content: &content}); err != nil {
		t.Fatalf("UpdatePage(initial content) failed: %v", err)
	}

	beforeLatest, err := deps.revision.GetLatestRevision(page.Page.ID)
	if err != nil {
		t.Fatalf("GetLatestRevision(before rename) failed: %v", err)
	}
	if beforeLatest == nil {
		t.Fatal("expected initial content revision")
	}
	currentPage, err := deps.tree.GetPage(page.Page.ID)
	if err != nil {
		t.Fatalf("GetPage before title update: %v", err)
	}

	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "system", ID: currentPage.ID, Version: currentPage.Version(), Title: "Renamed Title", Slug: currentPage.Slug, Content: &content}); err != nil {
		t.Fatalf("UpdatePage(title only with unchanged content) failed: %v", err)
	}

	afterLatest, err := deps.revision.GetLatestRevision(page.Page.ID)
	if err != nil {
		t.Fatalf("GetLatestRevision(after rename) failed: %v", err)
	}
	if afterLatest == nil || afterLatest.ID == beforeLatest.ID {
		t.Fatalf("expected new revision for title-only change, got before=%#v after=%#v", beforeLatest, afterLatest)
	}
	if afterLatest.Type != revision.RevisionTypeStructureUpdate {
		t.Fatalf("latest revision type = %q", afterLatest.Type)
	}
	if afterLatest.Title != "Renamed Title" {
		t.Fatalf("latest revision title = %q", afterLatest.Title)
	}
}

func TestApplyPageRefactorUseCase_Move_LeavesIntraSubtreeRelativeLinksUnchanged(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default())
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default())
	applyUC := pages.NewApplyPageRefactorUseCase(deps.tree, deps.slug, deps.links, deps.orchestrator(), slog.Default())

	// Structure:
	//   /docs          (section to be moved)
	//   /docs/sub      (sub-page with relative link to /docs/sibling)
	//   /docs/sibling  (another sub-page in the same section)
	//   /archive       (target parent)
	docs, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Docs", Slug: "docs", Kind: pageKind(),
	})
	sub, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", ParentID: &docs.Page.ID, Title: "Sub", Slug: "sub", Kind: pageKind(),
	})
	sibling, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", ParentID: &docs.Page.ID, Title: "Sibling", Slug: "sibling", Kind: pageKind(),
	})
	archive, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Archive", Slug: "archive", Kind: pageKind(),
	})
	_ = sibling

	// /docs/sub has a relative link to its sibling: ../sibling → /docs/sibling
	// After the move both pages travel together so the relative path must stay ../sibling.
	subContent := "[To Sibling](../sibling)"
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "system", ID: sub.Page.ID, Version: sub.Page.Version(),
		Title: sub.Page.Title, Slug: sub.Page.Slug, Content: &subContent}); err != nil {
		t.Fatalf("UpdatePage(sub) failed: %v", err)
	}

	_, err := applyUC.Execute(context.Background(), pages.RefactorApplyInput{
		UserID:  "system",
		Version: docs.Page.Version(),
		RefactorPreviewInput: pages.RefactorPreviewInput{
			PageID:      docs.Page.ID,
			Kind:        pages.RefactorKindMove,
			NewParentID: &archive.Page.ID,
		},
		RewriteLinks: true,
	})
	if err != nil {
		t.Fatalf("ApplyPageRefactor(move section) failed: %v", err)
	}

	movedSub, err := deps.tree.GetPage(sub.Page.ID)
	if err != nil {
		t.Fatalf("GetPage(sub) failed: %v", err)
	}
	// The relative link ../sibling must be unchanged: both pages moved together.
	if movedSub.Content != "[To Sibling](../sibling)" {
		t.Fatalf("intra-subtree relative link changed unexpectedly: got %q, want %q",
			movedSub.Content, "[To Sibling](../sibling)")
	}
}

func TestApplyPageRefactorUseCase_Move_RewritesAbsoluteLinksInSubPagesPointingWithinSection(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default())
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default())
	applyUC := pages.NewApplyPageRefactorUseCase(deps.tree, deps.slug, deps.links, deps.orchestrator(), slog.Default())

	// Structure:
	//   /docs             (section to be moved)
	//   /docs/sub         (sub-page with absolute links into the same section)
	//   /docs/sibling     (another sub-page in the same section)
	//   /archive          (target parent)
	docs, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Docs", Slug: "docs", Kind: pageKind(),
	})
	sub, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", ParentID: &docs.Page.ID, Title: "Sub", Slug: "sub", Kind: pageKind(),
	})
	sibling, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", ParentID: &docs.Page.ID, Title: "Sibling", Slug: "sibling", Kind: pageKind(),
	})
	archive, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Archive", Slug: "archive", Kind: pageKind(),
	})
	_ = sibling

	// /docs/sub has an absolute link to /docs/sibling (another sub-page in the same section)
	subContent := "[To Sibling](/docs/sibling)"
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "system", ID: sub.Page.ID, Version: sub.Page.Version(),
		Title: sub.Page.Title, Slug: sub.Page.Slug, Content: &subContent}); err != nil {
		t.Fatalf("UpdatePage(sub) failed: %v", err)
	}

	// Move /docs under /archive → /archive/docs, sub-pages become /archive/docs/sub and /archive/docs/sibling
	_, err := applyUC.Execute(context.Background(), pages.RefactorApplyInput{
		UserID:  "system",
		Version: docs.Page.Version(),
		RefactorPreviewInput: pages.RefactorPreviewInput{
			PageID:      docs.Page.ID,
			Kind:        pages.RefactorKindMove,
			NewParentID: &archive.Page.ID,
		},
		RewriteLinks: true,
	})
	if err != nil {
		t.Fatalf("ApplyPageRefactor(move section) failed: %v", err)
	}

	movedSub, err := deps.tree.GetPage(sub.Page.ID)
	if err != nil {
		t.Fatalf("GetPage(sub) failed: %v", err)
	}
	// Absolute link /docs/sibling → /archive/docs/sibling
	if movedSub.Content != "[To Sibling](/archive/docs/sibling)" {
		t.Fatalf("sub-page content = %q, want %q", movedSub.Content, "[To Sibling](/archive/docs/sibling)")
	}
}

func TestApplyPageRefactorUseCase_Move_RewritesLinksInSubPages(t *testing.T) {
	deps := newTestDeps(t)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default())
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default())
	applyUC := pages.NewApplyPageRefactorUseCase(deps.tree, deps.slug, deps.links, deps.orchestrator(), slog.Default())

	// Structure:
	//   /docs          (section to be moved)
	//   /docs/sub      (sub-page with a relative link to /guide)
	//   /guide         (external page)
	//   /linker        (external page linking to the sub-page)
	//   /archive       (target parent)
	docs, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Docs", Slug: "docs", Kind: pageKind(),
	})
	sub, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", ParentID: &docs.Page.ID, Title: "Sub", Slug: "sub", Kind: pageKind(),
	})
	guide, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Guide", Slug: "guide", Kind: pageKind(),
	})
	linker, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Linker", Slug: "linker", Kind: pageKind(),
	})
	archive, _ := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "system", Title: "Archive", Slug: "archive", Kind: pageKind(),
	})
	_ = guide

	// /docs/sub has a relative link to /guide: ../../guide
	subContent := "[To Guide](../../guide)"
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "system", ID: sub.Page.ID, Version: sub.Page.Version(),
		Title: sub.Page.Title, Slug: sub.Page.Slug, Content: &subContent}); err != nil {
		t.Fatalf("UpdatePage(sub) failed: %v", err)
	}

	// /linker has an absolute link to /docs/sub
	linkerContent := "[To Sub](/docs/sub)"
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID: "system", ID: linker.Page.ID, Version: linker.Page.Version(),
		Title: linker.Page.Title, Slug: linker.Page.Slug, Content: &linkerContent}); err != nil {
		t.Fatalf("UpdatePage(linker) failed: %v", err)
	}

	// Move /docs under /archive → becomes /archive/docs, sub-page becomes /archive/docs/sub
	_, err := applyUC.Execute(context.Background(), pages.RefactorApplyInput{
		UserID:  "system",
		Version: docs.Page.Version(),
		RefactorPreviewInput: pages.RefactorPreviewInput{
			PageID:      docs.Page.ID,
			Kind:        pages.RefactorKindMove,
			NewParentID: &archive.Page.ID,
		},
		RewriteLinks: true,
	})
	if err != nil {
		t.Fatalf("ApplyPageRefactor(move section) failed: %v", err)
	}

	// Sub-page should now be at /archive/docs/sub
	movedSub, err := deps.tree.GetPage(sub.Page.ID)
	if err != nil {
		t.Fatalf("GetPage(sub) failed: %v", err)
	}
	if movedSub.CalculatePath() != "/archive/docs/sub" {
		t.Fatalf("sub-page path = %q, want /archive/docs/sub", movedSub.CalculatePath())
	}

	// The relative link in the sub-page should be updated:
	// from ../../guide (resolves to /guide from /docs/sub)
	// to ../../../guide (resolves to /guide from /archive/docs/sub)
	if movedSub.Content != "[To Guide](../../../guide)" {
		t.Fatalf("sub-page content = %q, want %q", movedSub.Content, "[To Guide](../../../guide)")
	}

	// The linker's absolute link to the sub-page should be updated
	updatedLinker, err := deps.tree.GetPage(linker.Page.ID)
	if err != nil {
		t.Fatalf("GetPage(linker) failed: %v", err)
	}
	if updatedLinker.Content != "[To Sub](/archive/docs/sub)" {
		t.Fatalf("linker content = %q, want %q", updatedLinker.Content, "[To Sub](/archive/docs/sub)")
	}
}

func TestUpdatePageUseCase_EmitsSuccessMetrics(t *testing.T) {
	deps := newTestDeps(t)
	metrics := httpmetrics.NewHTTPMetrics()
	orchestrator := pagesave.NewPageSaveOrchestrator(
		metrics,
		pagesave.NewLinkIndexSideEffect(deps.links, slog.Default(), metrics),
		pagesave.NewRevisionSideEffect(deps.revision, slog.Default(), metrics),
	)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, orchestrator, slog.Default(), metrics)
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, orchestrator, slog.Default(), metrics)

	created, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1",
		Title:  "Home",
		Slug:   "home",
		Kind:   pageKind(),
	})
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	content := "updated"
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID:  "user1",
		ID:      created.Page.ID,
		Version: created.Page.Version(),
		Title:   "Home Updated",
		Slug:    "home",
		Content: &content,
		Kind:    pageKind(),
	}); err != nil {
		t.Fatalf("update failed: %v", err)
	}

	body := metricsBody(t, metrics)
	if !strings.Contains(body, `leafwiki_pagesave_operations_total{operation="update",result="success"} 1`) {
		t.Fatalf("expected success workflow metric, got: %s", body)
	}
	if !strings.Contains(body, `leafwiki_pagesave_duration_seconds_bucket{operation="update",result="success"`) {
		t.Fatalf("expected success duration metric, got: %s", body)
	}
}

func TestUpdatePageUseCase_EmitsFailureMetrics(t *testing.T) {
	deps := newTestDeps(t)
	metrics := httpmetrics.NewHTTPMetrics()
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, deps.orchestrator(), slog.Default(), metrics)

	_, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID:  "user1",
		ID:      "missing",
		Version: tree.VersionUnchecked,
		Title:   "",
		Slug:    "valid-slug",
		Kind:    pageKind(),
	})
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}

	body := metricsBody(t, metrics)
	if !strings.Contains(body, `leafwiki_pagesave_operations_total{operation="update",result="error"} 1`) {
		t.Fatalf("expected failure workflow metric, got: %s", body)
	}
	if !strings.Contains(body, `leafwiki_pagesave_failures_total{operation="update",result="error"} 1`) {
		t.Fatalf("expected failure counter, got: %s", body)
	}
}

func TestApplyPageRefactorUseCase_EmitsRenameMetrics(t *testing.T) {
	deps := newTestDeps(t)
	metrics := httpmetrics.NewHTTPMetrics()
	orchestrator := pagesave.NewPageSaveOrchestrator(
		metrics,
		pagesave.NewLinkIndexSideEffect(deps.links, slog.Default(), metrics),
		pagesave.NewRevisionSideEffect(deps.revision, slog.Default(), metrics),
	)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, orchestrator, slog.Default(), metrics)
	updateUC := pages.NewUpdatePageUseCase(deps.tree, deps.slug, orchestrator, slog.Default(), metrics)
	applyUC := pages.NewApplyPageRefactorUseCase(deps.tree, deps.slug, deps.links, orchestrator, slog.Default(), metrics)

	target, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1",
		Title:  "Target",
		Slug:   "target",
		Kind:   pageKind(),
	})
	if err != nil {
		t.Fatalf("target create failed: %v", err)
	}
	source, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1",
		Title:  "Source",
		Slug:   "source",
		Kind:   pageKind(),
	})
	if err != nil {
		t.Fatalf("source create failed: %v", err)
	}

	content := "[[target]]"
	if _, err := updateUC.Execute(context.Background(), pages.UpdatePageInput{
		UserID:  "user1",
		ID:      source.Page.ID,
		Version: source.Page.Version(),
		Title:   source.Page.Title,
		Slug:    source.Page.Slug,
		Content: &content,
		Kind:    pageKind(),
	}); err != nil {
		t.Fatalf("source update failed: %v", err)
	}

	if _, err := applyUC.Execute(context.Background(), pages.RefactorApplyInput{
		UserID:       "user1",
		Version:      target.Page.Version(),
		RewriteLinks: true,
		RefactorPreviewInput: pages.RefactorPreviewInput{
			PageID:  target.Page.ID,
			Kind:    pages.RefactorKindRename,
			Title:   "Target Renamed",
			Slug:    "target-renamed",
			Content: &target.Page.Content,
		},
	}); err != nil {
		t.Fatalf("apply refactor failed: %v", err)
	}

	body := metricsBody(t, metrics)
	if !strings.Contains(body, `leafwiki_refactor_affected_pages_bucket{kind="rename",rewrite_links="true"`) {
		t.Fatalf("expected refactor affected pages metric, got: %s", body)
	}
	if !strings.Contains(body, `leafwiki_refactor_matched_links_bucket{kind="rename",rewrite_links="true"`) {
		t.Fatalf("expected refactor matched links metric, got: %s", body)
	}
	if !strings.Contains(body, `leafwiki_refactor_duration_seconds_bucket{kind="rename",rewrite_links="true"`) {
		t.Fatalf("expected refactor duration metric, got: %s", body)
	}
}

func TestApplyPageRefactorUseCase_EmitsMoveMetrics(t *testing.T) {
	deps := newTestDeps(t)
	metrics := httpmetrics.NewHTTPMetrics()
	orchestrator := pagesave.NewPageSaveOrchestrator(
		metrics,
		pagesave.NewLinkIndexSideEffect(deps.links, slog.Default(), metrics),
		pagesave.NewRevisionSideEffect(deps.revision, slog.Default(), metrics),
	)
	createUC := pages.NewCreatePageUseCase(deps.tree, deps.slug, orchestrator, slog.Default(), metrics)
	applyUC := pages.NewApplyPageRefactorUseCase(deps.tree, deps.slug, deps.links, orchestrator, slog.Default(), metrics)

	parent, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1",
		Title:  "Parent",
		Slug:   "parent",
		Kind:   sectionKind(),
	})
	if err != nil {
		t.Fatalf("parent create failed: %v", err)
	}
	child, err := createUC.Execute(context.Background(), pages.CreatePageInput{
		UserID: "user1",
		Title:  "Child",
		Slug:   "child",
		Kind:   pageKind(),
	})
	if err != nil {
		t.Fatalf("child create failed: %v", err)
	}

	if _, err := applyUC.Execute(context.Background(), pages.RefactorApplyInput{
		UserID:       "user1",
		Version:      child.Page.Version(),
		RewriteLinks: false,
		RefactorPreviewInput: pages.RefactorPreviewInput{
			PageID:      child.Page.ID,
			Kind:        pages.RefactorKindMove,
			NewParentID: &parent.Page.ID,
		},
	}); err != nil {
		t.Fatalf("move refactor failed: %v", err)
	}

	body := metricsBody(t, metrics)
	if !strings.Contains(body, `leafwiki_refactor_duration_seconds_bucket{kind="move",rewrite_links="false"`) {
		t.Fatalf("expected move refactor duration metric, got: %s", body)
	}
}
