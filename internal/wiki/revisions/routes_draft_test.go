package revisions

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/perber/wiki/internal/core/auth"
	"github.com/perber/wiki/internal/core/tree"
)

func TestRequirePageVisibility_AllowsDraftRevisionsOnlyForEditorsAndAdmins(t *testing.T) {
	gin.SetMode(gin.TestMode)
	treeService := tree.NewTreeService(t.TempDir())
	if err := treeService.LoadTree(); err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	kind := tree.NodeKindPage
	id, err := treeService.CreateNode("owner", nil, "Draft", "draft", &kind)
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	node, err := treeService.FindPageByID(*id)
	if err != nil {
		t.Fatalf("FindPageByID: %v", err)
	}
	node.Draft = true

	routes := &Routes{tree: treeService}
	for _, tc := range []struct {
		name         string
		user         *auth.User
		authDisabled bool
		status       int
	}{
		{name: "editor", user: &auth.User{ID: "other", Role: auth.RoleEditor}, status: http.StatusNoContent},
		{name: "admin", user: &auth.User{ID: "admin", Role: auth.RoleAdmin}, status: http.StatusNoContent},
		{name: "creator viewer", user: &auth.User{ID: "owner", Role: auth.RoleViewer}, status: http.StatusNotFound},
		{name: "anonymous", status: http.StatusNotFound},
		{name: "auth disabled editor", user: &auth.User{ID: "other", Role: auth.RoleEditor}, authDisabled: true, status: http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.GET("/pages/:id", func(c *gin.Context) {
				if tc.user != nil {
					c.Set("user", tc.user)
				}
			}, routes.requirePageVisibility(tc.authDisabled), func(c *gin.Context) { c.Status(http.StatusNoContent) })
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/pages/"+*id, nil))
			if recorder.Code != tc.status {
				t.Fatalf("status = %d, want %d", recorder.Code, tc.status)
			}
			if tc.status == http.StatusNoContent {
				if got := recorder.Header().Get("Cache-Control"); got != "private, no-store" {
					t.Fatalf("Cache-Control = %q", got)
				}
				if got := recorder.Header().Get("Pragma"); got != "no-cache" {
					t.Fatalf("Pragma = %q", got)
				}
			}
		})
	}
}

func TestRequirePageVisibility_NonDraftDescendantInheritsDraftAncestorVisibility(t *testing.T) {
	gin.SetMode(gin.TestMode)
	treeService := tree.NewTreeService(t.TempDir())
	if err := treeService.LoadTree(); err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	section := tree.NodeKindSection
	parentID, err := treeService.CreateNode("owner", nil, "Draft Parent", "draft-parent", &section)
	if err != nil {
		t.Fatalf("CreateNode parent: %v", err)
	}
	parent, err := treeService.FindPageByID(*parentID)
	if err != nil {
		t.Fatalf("FindPageByID parent: %v", err)
	}
	parent.Draft = true
	page := tree.NodeKindPage
	childID, err := treeService.CreateNode("other", parentID, "Child", "child", &page)
	if err != nil {
		t.Fatalf("CreateNode child: %v", err)
	}
	routes := &Routes{tree: treeService}

	for _, tc := range []struct {
		name         string
		user         *auth.User
		authDisabled bool
		status       int
	}{
		{name: "editor", user: &auth.User{ID: "other", Role: auth.RoleEditor}, status: http.StatusNoContent},
		{name: "creator viewer", user: &auth.User{ID: "owner", Role: auth.RoleViewer}, status: http.StatusNotFound},
		{name: "auth disabled", user: &auth.User{Role: auth.RoleEditor}, authDisabled: true, status: http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.GET("/pages/:id", func(c *gin.Context) { c.Set("user", tc.user) }, routes.requirePageVisibility(tc.authDisabled), func(c *gin.Context) { c.Status(http.StatusNoContent) })
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/pages/"+*childID, nil))
			if recorder.Code != tc.status {
				t.Fatalf("status = %d, want %d", recorder.Code, tc.status)
			}
		})
	}
}
