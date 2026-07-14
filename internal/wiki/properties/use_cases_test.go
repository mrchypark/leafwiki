package properties

import (
	"context"
	"testing"

	"github.com/perber/wiki/internal/core/tree"
	coreprop "github.com/perber/wiki/internal/properties"
	"github.com/perber/wiki/internal/test_utils"
)

func TestPropertyUseCases_FilterStaleDraftRowsAndCounts(t *testing.T) {
	dir := t.TempDir()
	treeService := tree.NewTreeService(dir)
	if err := treeService.LoadTree(); err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	store, err := coreprop.NewPropertiesStore(dir)
	if err != nil {
		t.Fatalf("NewPropertiesStore: %v", err)
	}
	t.Cleanup(func() { test_utils.WrapCloseWithErrorCheck(store.Close, t) })
	service := coreprop.NewPropertiesService(store)
	kind := tree.NodeKindPage
	create := func(title, slug, raw string) string {
		id, err := treeService.CreateNode("owner", nil, title, slug, &kind)
		if err != nil {
			t.Fatalf("CreateNode: %v", err)
		}
		if err := treeService.UpdateNode("owner", *id, title, slug, &raw, tree.VersionUnchecked, nil, nil, true); err != nil {
			t.Fatalf("UpdateNode: %v", err)
		}
		page, err := treeService.GetPage(*id)
		if err != nil {
			t.Fatalf("GetPage: %v", err)
		}
		if err := service.IndexPageContent(page.ID, page.RawContent); err != nil {
			t.Fatalf("IndexPageContent: %v", err)
		}
		return *id
	}
	publicID := create("Public", "public", "---\nstatus: shared\n---\n\npublic")
	draftID := create("Draft", "draft", "---\nstatus: shared\nsecret: hidden\n---\n\nprivate")
	draft, err := treeService.FindPageByID(draftID)
	if err != nil {
		t.Fatalf("FindPageByID: %v", err)
	}
	draft.Draft = true

	pages, err := NewGetPagesByPropertyUseCase(service, treeService, nil).Execute(context.Background(), GetPagesByPropertyInput{Key: "status", Value: "shared"})
	if err != nil {
		t.Fatalf("GetPages Execute: %v", err)
	}
	if len(pages.Pages) != 1 || pages.Pages[0].ID != publicID {
		t.Fatalf("stale draft leaked from pages: %#v", pages.Pages)
	}

	keys, err := NewGetPropertyKeysUseCase(service, treeService).Execute(context.Background(), GetPropertyKeysInput{Limit: 50})
	if err != nil {
		t.Fatalf("GetPropertyKeys Execute: %v", err)
	}
	if len(keys.Keys) != 1 || keys.Keys[0].Key != "status" || keys.Keys[0].Count != 1 {
		t.Fatalf("stale draft affected property counts: %#v", keys.Keys)
	}
}
