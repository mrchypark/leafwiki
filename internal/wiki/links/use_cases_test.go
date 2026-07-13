package links

import (
	"context"
	"testing"

	"github.com/perber/wiki/internal/core/auth"
	"github.com/perber/wiki/internal/core/tree"
	corelinks "github.com/perber/wiki/internal/links"
)

func TestGetLinkStatusUseCase_DraftRequiresEditorAndReturnsNoSharedIndexData(t *testing.T) {
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
	store, err := corelinks.NewLinksStore(dir)
	if err != nil {
		t.Fatalf("NewLinksStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	uc := NewGetLinkStatusUseCase(corelinks.NewLinkService(dir, treeService, store), treeService)

	if _, err := uc.Execute(context.Background(), GetLinkStatusInput{PageID: *id, User: &auth.User{Role: auth.RoleViewer}}); err == nil {
		t.Fatal("viewer unexpectedly accessed draft link status")
	}
	out, err := uc.Execute(context.Background(), GetLinkStatusInput{PageID: *id, User: &auth.User{Role: auth.RoleEditor}})
	if err != nil {
		t.Fatalf("editor Execute: %v", err)
	}
	if out.Status == nil || len(out.Status.Backlinks) != 0 || len(out.Status.Outgoings) != 0 {
		t.Fatalf("draft link status = %#v", out.Status)
	}
}
