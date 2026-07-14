package links

import (
	"context"
	"testing"

	"github.com/perber/wiki/internal/core/auth"
	sharederrors "github.com/perber/wiki/internal/core/shared/errors"
	"github.com/perber/wiki/internal/core/tree"
	corelinks "github.com/perber/wiki/internal/links"
)

func TestGetLinkStatusUseCase_FiltersStaleDraftRowsAndDirectAccess(t *testing.T) {
	dir := t.TempDir()
	treeService := tree.NewTreeService(dir)
	if err := treeService.LoadTree(); err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	store, err := corelinks.NewLinksStore(dir)
	if err != nil {
		t.Fatalf("NewLinksStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	service := corelinks.NewLinkService(dir, treeService, store)
	kind := tree.NodeKindPage
	create := func(title, slug, content string) string {
		id, err := treeService.CreateNode("owner", nil, title, slug, &kind)
		if err != nil {
			t.Fatalf("CreateNode: %v", err)
		}
		if err := treeService.UpdateNode("owner", *id, title, slug, &content, tree.VersionUnchecked, nil, nil, false); err != nil {
			t.Fatalf("UpdateNode: %v", err)
		}
		return *id
	}
	publicTargetID := create("Public Target", "public-target", "public")
	draftTargetID := create("Draft Target", "draft-target", "private")
	draftSourceID := create("Draft Source", "draft-source", "[public](/public-target)")
	publicSourceID := create("Public Source", "public-source", "[draft](/draft-target)")
	if err := service.IndexAllPages(); err != nil {
		t.Fatalf("IndexAllPages: %v", err)
	}
	for _, id := range []string{draftTargetID, draftSourceID} {
		page, err := treeService.GetPage(id)
		if err != nil {
			t.Fatalf("GetPage: %v", err)
		}
		draft := true
		if err := treeService.UpdateNodeWithDraft("owner", id, page.Title, page.Slug, nil, tree.VersionUnchecked, nil, nil, false, &draft); err != nil {
			t.Fatalf("UpdateNodeWithDraft: %v", err)
		}
	}
	useCase := NewGetLinkStatusUseCase(service, treeService)

	publicTarget, err := useCase.Execute(context.Background(), GetLinkStatusInput{PageID: publicTargetID})
	if err != nil {
		t.Fatalf("public target Execute: %v", err)
	}
	if publicTarget.Status.Counts.Backlinks != 0 || len(publicTarget.Status.Backlinks) != 0 {
		t.Fatalf("stale draft backlink leaked: %#v", publicTarget.Status)
	}
	publicSource, err := useCase.Execute(context.Background(), GetLinkStatusInput{PageID: publicSourceID})
	if err != nil {
		t.Fatalf("public source Execute: %v", err)
	}
	if publicSource.Status.Counts.Outgoings != 0 || len(publicSource.Status.Outgoings) != 0 {
		t.Fatalf("stale draft outgoing leaked: %#v", publicSource.Status)
	}
	_, err = useCase.Execute(context.Background(), GetLinkStatusInput{PageID: draftTargetID})
	localized, ok := sharederrors.AsLocalizedError(err)
	if !ok || localized.Code != ErrCodeLinkPageNotFound {
		t.Fatalf("draft direct access error = %#v", err)
	}

	for _, user := range []*auth.User{
		{ID: "owner", Role: auth.RoleEditor},
		{ID: "admin", Role: auth.RoleAdmin},
	} {
		out, err := useCase.Execute(context.Background(), GetLinkStatusInput{PageID: draftTargetID, User: user})
		if err != nil {
			t.Fatalf("authorized draft Execute: %v", err)
		}
		if out.Status == nil || out.Status.Counts != (corelinks.LinkStatusCounts{}) || len(out.Status.Backlinks) != 0 || len(out.Status.Outgoings) != 0 {
			t.Fatalf("authorized draft status was not safely empty: %#v", out.Status)
		}
	}
}
