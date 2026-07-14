package pagesave

import (
	"testing"

	"github.com/perber/wiki/internal/core/tree"
	"github.com/perber/wiki/internal/properties"
	"github.com/perber/wiki/internal/test_utils"
)

func setupPropertiesEffectTest(t *testing.T) (*tree.TreeService, *properties.PropertiesService, *PropertiesSideEffect) {
	t.Helper()
	dir := t.TempDir()

	treeSvc := tree.NewTreeService(dir)
	if err := treeSvc.LoadTree(); err != nil {
		t.Fatalf("LoadTree: %v", err)
	}

	store, err := properties.NewPropertiesStore(dir)
	if err != nil {
		t.Fatalf("NewPropertiesStore: %v", err)
	}
	t.Cleanup(func() { test_utils.WrapCloseWithErrorCheck(store.Close, t) })

	svc := properties.NewPropertiesService(store)
	effect := NewPropertiesSideEffect(svc, treeSvc, nil)
	return treeSvc, svc, effect
}

// ─── PropertiesSideEffect ─────────────────────────────────────────────────────

func TestPropertiesSideEffect_Apply_Create_IndexesPropertiesFromRawContent(t *testing.T) {
	treeSvc, propsSvc, effect := setupPropertiesEffectTest(t)

	raw := "---\nstatus: draft\nauthor: alice\n---\n\nPage body."
	page := createPageWithFrontmatter(t, treeSvc, "Props Page", "props-page", raw)

	effect.Apply(PageSaveEvent{
		Operation: PageOperationCreate,
		After:     page,
	})

	ids, err := propsSvc.GetPageIDsByProperty("status", "draft")
	if err != nil {
		t.Fatalf("GetPageIDsByProperty: %v", err)
	}
	if len(ids) != 1 || ids[0] != page.ID {
		t.Errorf("expected page %q indexed under status=draft, got %v", page.ID, ids)
	}

	ids2, err := propsSvc.GetPageIDsByProperty("author", "alice")
	if err != nil {
		t.Fatalf("GetPageIDsByProperty (author): %v", err)
	}
	if len(ids2) != 1 || ids2[0] != page.ID {
		t.Errorf("expected page %q indexed under author=alice, got %v", page.ID, ids2)
	}
}

func TestPropertiesSideEffect_Apply_Update_ReindexesProperties(t *testing.T) {
	treeSvc, propsSvc, effect := setupPropertiesEffectTest(t)

	raw := "---\nstatus: draft\n---\n\nOriginal."
	page := createPageWithFrontmatter(t, treeSvc, "Update Props", "update-props", raw)
	effect.Apply(PageSaveEvent{Operation: PageOperationCreate, After: page})

	newRaw := "---\nstatus: published\n---\n\nUpdated."
	if err := treeSvc.UpdateNode("system", page.ID, "Update Props", "update-props", &newRaw, tree.VersionUnchecked, nil, nil, true); err != nil {
		t.Fatalf("UpdateNode: %v", err)
	}
	updated, err := treeSvc.GetPage(page.ID)
	if err != nil {
		t.Fatalf("GetPage after update: %v", err)
	}
	effect.Apply(PageSaveEvent{Operation: PageOperationUpdate, After: updated})

	old, err := propsSvc.GetPageIDsByProperty("status", "draft")
	if err != nil {
		t.Fatalf("GetPageIDsByProperty (old): %v", err)
	}
	if len(old) != 0 {
		t.Errorf("expected status=draft to be gone, got %v", old)
	}

	fresh, err := propsSvc.GetPageIDsByProperty("status", "published")
	if err != nil {
		t.Fatalf("GetPageIDsByProperty (new): %v", err)
	}
	if len(fresh) != 1 || fresh[0] != updated.ID {
		t.Errorf("expected status=published to be indexed, got %v", fresh)
	}
}

func TestPropertiesSideEffect_Apply_Delete_RemovesProperties(t *testing.T) {
	treeSvc, propsSvc, effect := setupPropertiesEffectTest(t)

	raw := "---\nstatus: draft\n---\n\nBody."
	page := createPageWithFrontmatter(t, treeSvc, "Delete Props", "delete-props", raw)
	effect.Apply(PageSaveEvent{Operation: PageOperationCreate, After: page})
	if err := treeSvc.DeleteNode("system", page.ID, false, page.Version()); err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}

	effect.Apply(PageSaveEvent{
		Operation:     PageOperationDelete,
		AffectedPages: []*tree.Page{page},
	})

	ids, err := propsSvc.GetPageIDsByProperty("status", "draft")
	if err != nil {
		t.Fatalf("GetPageIDsByProperty: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("expected properties removed after delete, got %v", ids)
	}
}

func TestPropertiesSideEffect_Apply_Update_RemovesDraftAndReindexesWhenPublished(t *testing.T) {
	treeSvc, propsSvc, effect := setupPropertiesEffectTest(t)
	page := createPageWithFrontmatter(t, treeSvc, "Draft Props", "draft-props", "---\nstatus: secret\n---\n\nBody.")
	effect.Apply(PageSaveEvent{Operation: PageOperationCreate, After: page})

	page = setDraftForTest(t, treeSvc, page, true)
	effect.Apply(PageSaveEvent{Operation: PageOperationUpdate, After: page})
	ids, err := propsSvc.GetPageIDsByProperty("status", "secret")
	if err != nil {
		t.Fatalf("GetPageIDsByProperty draft: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("draft page remained in property index: %v", ids)
	}

	page = setDraftForTest(t, treeSvc, page, false)
	effect.Apply(PageSaveEvent{Operation: PageOperationUpdate, After: page})
	ids, err = propsSvc.GetPageIDsByProperty("status", "secret")
	if err != nil {
		t.Fatalf("GetPageIDsByProperty published: %v", err)
	}
	if len(ids) != 1 || ids[0] != page.ID {
		t.Fatalf("published page was not reindexed: %v", ids)
	}
}

func TestPropertiesSideEffect_Move_ReevaluatesDraftVisibility(t *testing.T) {
	treeSvc, propsSvc, effect := setupPropertiesEffectTest(t)
	hidden := createPageWithFrontmatter(t, treeSvc, "Hidden Props", "hidden-props", "---\nstatus: secret\n---\n\nBody.")
	visible := createPageWithFrontmatter(t, treeSvc, "Visible Props", "visible-props", "---\nstatus: public\n---\n\nBody.")
	effect.Apply(PageSaveEvent{Operation: PageOperationCreate, After: hidden})
	draftParentID, err := treeSvc.CreateNodeWithDraft("editor", nil, "Draft Parent", "draft-parent", pageKindPtr(), true)
	if err != nil {
		t.Fatalf("CreateNodeWithDraft: %v", err)
	}
	if err := treeSvc.MoveNode("editor", hidden.ID, *draftParentID, hidden.Version()); err != nil {
		t.Fatalf("MoveNode into draft: %v", err)
	}
	hidden, err = treeSvc.GetPage(hidden.ID)
	if err != nil {
		t.Fatalf("GetPage hidden: %v", err)
	}
	effect.Apply(PageSaveEvent{Operation: PageOperationMove, DraftChanged: true, AffectedPages: []*tree.Page{hidden, visible}})
	ids, err := propsSvc.GetPageIDsByProperty("status", "secret")
	if err != nil {
		t.Fatalf("GetPageIDsByProperty draft: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("draft page remained in property index after move: %v", ids)
	}

	ids, err = propsSvc.GetPageIDsByProperty("status", "public")
	if err != nil {
		t.Fatalf("GetPageIDsByProperty visible: %v", err)
	}
	if len(ids) != 1 || ids[0] != visible.ID {
		t.Fatalf("visible affected page was not indexed after move: %v", ids)
	}
}
