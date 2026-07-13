package revisions

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/perber/wiki/internal/core/auth"
	"github.com/perber/wiki/internal/core/tree"
)

func TestRevisionRoute_DraftHistoryRequiresAuthenticatedEditor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	treeService := tree.NewTreeService(t.TempDir())
	if err := treeService.LoadTree(); err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	kind := tree.NodeKindPage
	id, err := treeService.CreateNodeWithDraft("editor", nil, "Draft", "draft", &kind, true)
	if err != nil {
		t.Fatalf("CreateNodeWithDraft: %v", err)
	}
	routes := &Routes{tree: treeService}

	for _, tc := range []struct {
		name       string
		user       *auth.User
		wantStatus int
	}{
		{name: "editor", user: &auth.User{Role: auth.RoleEditor}, wantStatus: http.StatusNoContent},
		{name: "viewer", user: &auth.User{Role: auth.RoleViewer}, wantStatus: http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.GET("/pages/:id/revisions", func(c *gin.Context) { c.Set("user", tc.user) }, routes.requireVisiblePage(false), func(c *gin.Context) { c.Status(http.StatusNoContent) })
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/pages/"+*id+"/revisions", nil))
			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, tc.wantStatus)
			}
		})
	}
}
