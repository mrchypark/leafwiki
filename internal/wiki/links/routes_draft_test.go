package links

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/perber/wiki/internal/core/auth"
	"github.com/perber/wiki/internal/core/tree"
	corelinks "github.com/perber/wiki/internal/links"
)

func TestHandleGetLinkStatus_DraftResponseIsPrivateAndRemainsHiddenFromAnonymous(t *testing.T) {
	gin.SetMode(gin.TestMode)
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
	sectionKind := tree.NodeKindSection
	draftParentID, err := treeService.CreateNodeWithDraft("creator", nil, "Draft", "draft", &sectionKind, true)
	if err != nil {
		t.Fatalf("CreateNodeWithDraft: %v", err)
	}
	pageKind := tree.NodeKindPage
	draftID, err := treeService.CreateNode("creator", draftParentID, "Draft Child", "child", &pageKind)
	if err != nil {
		t.Fatalf("CreateNode(draft child): %v", err)
	}
	publishedID, err := treeService.CreateNode("creator", nil, "Published", "published", &pageKind)
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}

	routes := &Routes{getLinkStatus: NewGetLinkStatusUseCase(service, treeService)}
	request := func(pageID string, user *auth.User) *httptest.ResponseRecorder {
		router := gin.New()
		router.GET("/pages/:id/links", func(c *gin.Context) {
			if user != nil {
				c.Set("user", user)
			}
		}, routes.handleGetLinkStatus)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/pages/"+pageID+"/links", nil))
		return recorder
	}

	editor := request(*draftID, &auth.User{ID: "editor", Role: auth.RoleEditor})
	if editor.Code != http.StatusOK {
		t.Fatalf("editor draft status = %d, want 200; body=%s", editor.Code, editor.Body.String())
	}
	if got := editor.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("draft Cache-Control = %q", got)
	}
	if got := editor.Header().Get("Pragma"); got != "no-cache" {
		t.Fatalf("draft Pragma = %q", got)
	}

	anonymous := request(*draftID, nil)
	if anonymous.Code != http.StatusNotFound {
		t.Fatalf("anonymous draft status = %d, want 404; body=%s", anonymous.Code, anonymous.Body.String())
	}

	published := request(*publishedID, nil)
	if published.Code != http.StatusOK {
		t.Fatalf("published status = %d, want 200; body=%s", published.Code, published.Body.String())
	}
	if got := published.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("published Cache-Control changed to %q", got)
	}
	if got := published.Header().Get("Pragma"); got != "" {
		t.Fatalf("published Pragma changed to %q", got)
	}
}
