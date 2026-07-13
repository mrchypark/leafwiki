package pages

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/perber/wiki/internal/core/auth"
	"github.com/perber/wiki/internal/core/tree"
)

func TestGetPageRoute_DraftIsVisibleOnlyToAuthenticatedEditors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	treeService := tree.NewTreeService(t.TempDir())
	if err := treeService.LoadTree(); err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	kind := tree.NodeKindPage
	id, err := treeService.CreateNodeWithDraft("editor", nil, "Secret draft", "secret-draft", &kind, true)
	if err != nil {
		t.Fatalf("CreateNodeWithDraft: %v", err)
	}

	for _, tc := range []struct {
		name         string
		user         *auth.User
		authDisabled bool
		wantStatus   int
	}{
		{name: "editor", user: &auth.User{Role: auth.RoleEditor}, wantStatus: http.StatusOK},
		{name: "viewer", user: &auth.User{Role: auth.RoleViewer}, wantStatus: http.StatusNotFound},
		{name: "anonymous", wantStatus: http.StatusNotFound},
		{name: "auth disabled", user: &auth.User{Role: auth.RoleEditor}, authDisabled: true, wantStatus: http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			routes := &Routes{treeService: treeService, getPage: NewGetPageUseCase(treeService), authDisabled: tc.authDisabled}
			router := gin.New()
			router.GET("/pages/:id", func(c *gin.Context) {
				if tc.user != nil {
					c.Set("user", tc.user)
				}
			}, routes.handleGetPage)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/pages/"+*id, nil))
			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, tc.wantStatus)
			}
			if recorder.Code == http.StatusOK && !strings.Contains(recorder.Body.String(), `"draft":true`) {
				t.Fatalf("response does not expose draft state to editor: %s", recorder.Body.String())
			}
		})
	}
}

func TestSetDraftRoute_RejectsMissingDraftField(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	routes := &Routes{}
	router.PUT("/pages/:id/draft", routes.handleSetDraft)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/pages/page-id/draft", strings.NewReader(`{"version":"v1"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}
