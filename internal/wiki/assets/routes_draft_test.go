package assets

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/perber/wiki/internal/core/auth"
	"github.com/perber/wiki/internal/core/tree"
)

func TestRequireStaticAssetVisibility_BehindBasePathAllowsDraftAssetsOnlyForEditorsAndAdmins(t *testing.T) {
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
			router.GET("/wiki/assets/*filepath", func(c *gin.Context) {
				if tc.user != nil {
					c.Set("user", tc.user)
				}
			}, routes.requireStaticAssetVisibility(tc.authDisabled), func(c *gin.Context) { c.Status(http.StatusNoContent) })
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/wiki/assets/"+*id+"/secret.png", nil))
			if recorder.Code != tc.status {
				t.Fatalf("status = %d, want %d", recorder.Code, tc.status)
			}
		})
	}
}

func TestRequireStaticAssetVisibility_AppliesVisibilitySafeCachePolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	treeService := tree.NewTreeService(t.TempDir())
	if err := treeService.LoadTree(); err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	kind := tree.NodeKindPage
	draftID, err := treeService.CreateNode("owner", nil, "Draft", "draft", &kind)
	if err != nil {
		t.Fatalf("CreateNode draft: %v", err)
	}
	draft, err := treeService.FindPageByID(*draftID)
	if err != nil {
		t.Fatalf("FindPageByID draft: %v", err)
	}
	draft.Draft = true
	publicID, err := treeService.CreateNode("owner", nil, "Public", "public", &kind)
	if err != nil {
		t.Fatalf("CreateNode public: %v", err)
	}
	routes := &Routes{tree: treeService}

	for _, tc := range []struct {
		name         string
		pageID       string
		user         *auth.User
		authDisabled bool
		cacheControl string
		pragma       string
	}{
		{
			name:         "draft with authentication enabled",
			pageID:       *draftID,
			user:         &auth.User{ID: "owner", Role: auth.RoleEditor},
			cacheControl: "private, no-store",
			pragma:       "no-cache",
		},
		{
			name:         "public with authentication enabled",
			pageID:       *publicID,
			cacheControl: "private, no-store",
			pragma:       "no-cache",
		},
		{
			name:         "public with authentication disabled",
			pageID:       *publicID,
			authDisabled: true,
			cacheControl: "public, max-age=3600",
			pragma:       "cache",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.GET("/assets/*filepath", func(c *gin.Context) {
				if tc.user != nil {
					c.Set("user", tc.user)
				}
				c.Header("Cache-Control", "public, max-age=3600")
				c.Header("Pragma", "cache")
			}, routes.requireStaticAssetVisibility(tc.authDisabled), func(c *gin.Context) { c.Status(http.StatusNoContent) })
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/assets/"+tc.pageID+"/file.png", nil))
			if got := recorder.Header().Get("Cache-Control"); got != tc.cacheControl {
				t.Fatalf("Cache-Control = %q, want %q", got, tc.cacheControl)
			}
			if got := recorder.Header().Get("Pragma"); got != tc.pragma {
				t.Fatalf("Pragma = %q, want %q", got, tc.pragma)
			}
		})
	}
}

func TestRequireDraftManagement_AllowsDraftOnlyForEditorsAndAdmins(t *testing.T) {
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
			}, routes.requireDraftManagement(tc.authDisabled), func(c *gin.Context) { c.Status(http.StatusNoContent) })
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/pages/"+*id, nil))
			if recorder.Code != tc.status {
				t.Fatalf("status = %d, want %d", recorder.Code, tc.status)
			}
		})
	}
}

func TestRequireDraftManagement_NonDraftDescendantInheritsDraftAncestorVisibility(t *testing.T) {
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
			router.GET("/pages/:id", func(c *gin.Context) { c.Set("user", tc.user) }, routes.requireDraftManagement(tc.authDisabled), func(c *gin.Context) { c.Status(http.StatusNoContent) })
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/pages/"+*childID, nil))
			if recorder.Code != tc.status {
				t.Fatalf("status = %d, want %d", recorder.Code, tc.status)
			}
		})
	}
}
