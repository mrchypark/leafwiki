package pages

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/perber/wiki/internal/core/assets"
	"github.com/perber/wiki/internal/core/auth"
	"github.com/perber/wiki/internal/core/tree"
	httpinternal "github.com/perber/wiki/internal/http"
	authmw "github.com/perber/wiki/internal/http/middleware/auth"
	"github.com/perber/wiki/internal/http/middleware/security"
	"github.com/perber/wiki/internal/wiki/pagesave"
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

func TestPageResponse_ReturnsNotFoundWhenPageIsInvisible(t *testing.T) {
	gin.SetMode(gin.TestMode)
	routes := &Routes{authDisabled: true}
	page := &tree.Page{PageNode: &tree.PageNode{ID: "draft", Slug: "draft", Draft: true}}
	router := gin.New()
	router.Use(gin.Recovery())
	router.GET("/pages/:id", func(c *gin.Context) {
		routes.respondPage(c, http.StatusOK, page)
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/pages/draft", nil))

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusNotFound, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"code":"page_not_found"`) {
		t.Fatalf("body = %s, want page_not_found error", recorder.Body.String())
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

func TestPublicTreeRoute_AuthDisabledIgnoresStaleAccessCookie(t *testing.T) {
	treeService := newLoadedRouteTree(t)
	routes := &Routes{treeService: treeService}
	router := gin.New()
	routes.RegisterRoutes(httpinternal.RouterContext{
		Engine:      router,
		Base:        router,
		AuthCookies: authmw.NewAuthCookies(true, time.Hour, time.Hour),
		CSRFCookie:  security.NewCSRFCookie(true, time.Hour),
		Opts: httpinternal.RouterOptions{
			PublicAccess: true,
			AuthDisabled: true,
		},
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/tree", nil)
	request.AddCookie(&http.Cookie{Name: "leafwiki_at", Value: "stale-access-token"})
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
}

func TestMoveRoute_InvalidJSONIsRejectedBeforePageVisibilityLookup(t *testing.T) {
	treeService := newLoadedRouteTree(t)
	routes := &Routes{
		treeService: treeService,
		movePage: NewMovePageUseCase(
			treeService,
			pagesave.NewPageSaveOrchestrator(nil),
			slog.Default(),
		),
	}
	router := registerDraftMutationRoutes(t, routes, &auth.User{ID: "editor", Role: auth.RoleEditor}, false)

	recorder := performDraftMutationRequest(router, http.MethodPut, "/api/pages/missing/move", `{`)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
}

func TestMoveRoute_DraftPageIsMutableOnlyByAuthenticatedEditors(t *testing.T) {
	for _, tc := range []struct {
		name         string
		user         *auth.User
		authDisabled bool
		wantStatus   int
	}{
		{name: "editor", user: &auth.User{ID: "editor", Role: auth.RoleEditor}, wantStatus: http.StatusOK},
		{name: "viewer", user: &auth.User{ID: "viewer", Role: auth.RoleViewer}, wantStatus: http.StatusForbidden},
		{name: "auth disabled", authDisabled: true, wantStatus: http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			treeService := newLoadedRouteTree(t)
			kind := tree.NodeKindPage
			sourceID, err := treeService.CreateNodeWithDraft("editor", nil, "Draft", "draft", &kind, true)
			if err != nil {
				t.Fatalf("CreateNodeWithDraft: %v", err)
			}
			targetID, err := treeService.CreateNode("editor", nil, "Target", "target", &kind)
			if err != nil {
				t.Fatalf("CreateNode target: %v", err)
			}
			source, err := treeService.GetPage(*sourceID)
			if err != nil {
				t.Fatalf("GetPage source: %v", err)
			}

			routes := &Routes{
				treeService: treeService,
				movePage: NewMovePageUseCase(
					treeService,
					pagesave.NewPageSaveOrchestrator(nil),
					slog.Default(),
				),
			}
			router := registerDraftMutationRoutes(t, routes, tc.user, tc.authDisabled)
			body := `{"version":"` + source.Version() + `","parentId":"` + *targetID + `"}`
			recorder := performDraftMutationRequest(router, http.MethodPut, "/api/pages/"+*sourceID+"/move", body)
			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, tc.wantStatus, recorder.Body.String())
			}
			if tc.wantStatus == http.StatusOK {
				moved, err := treeService.GetPage(*sourceID)
				if err != nil {
					t.Fatalf("GetPage moved source: %v", err)
				}
				if moved.Parent == nil || moved.Parent.ID != *targetID || !moved.Draft {
					t.Fatalf("moved draft = parent:%v draft:%v", moved.Parent, moved.Draft)
				}
			}
		})
	}
}

func TestConvertRoute_DraftSectionIsMutableOnlyByAuthenticatedEditors(t *testing.T) {
	for _, tc := range []struct {
		name         string
		user         *auth.User
		authDisabled bool
		wantStatus   int
	}{
		{name: "editor", user: &auth.User{ID: "editor", Role: auth.RoleEditor}, wantStatus: http.StatusNoContent},
		{name: "viewer", user: &auth.User{ID: "viewer", Role: auth.RoleViewer}, wantStatus: http.StatusForbidden},
		{name: "auth disabled", authDisabled: true, wantStatus: http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			treeService := newLoadedRouteTree(t)
			kind := tree.NodeKindSection
			sectionID, err := treeService.CreateNodeWithDraft("editor", nil, "Draft section", "draft-section", &kind, true)
			if err != nil {
				t.Fatalf("CreateNodeWithDraft: %v", err)
			}
			section, err := treeService.GetPage(*sectionID)
			if err != nil {
				t.Fatalf("GetPage section: %v", err)
			}

			routes := &Routes{
				treeService: treeService,
				convertPage: NewConvertPageUseCase(treeService, nil, slog.Default()),
			}
			router := registerDraftMutationRoutes(t, routes, tc.user, tc.authDisabled)
			body := `{"version":"` + section.Version() + `","targetKind":"page"}`
			recorder := performDraftMutationRequest(router, http.MethodPost, "/api/pages/convert/"+*sectionID, body)
			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, tc.wantStatus, recorder.Body.String())
			}
			if tc.wantStatus == http.StatusNoContent {
				converted, err := treeService.GetPage(*sectionID)
				if err != nil {
					t.Fatalf("GetPage converted section: %v", err)
				}
				if converted.Kind != tree.NodeKindPage || !converted.Draft {
					t.Fatalf("converted draft = kind:%q draft:%v", converted.Kind, converted.Draft)
				}
			}
		})
	}
}

func TestAuthDisabledRoutes_AcceptExplicitUnchangedDraftFalseAndRejectDraftTrue(t *testing.T) {
	treeService := newLoadedRouteTree(t)
	slugService := tree.NewSlugService()
	kind := tree.NodeKindPage
	pageID, err := treeService.CreateNode("public-editor", nil, "Page", "page", &kind)
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	orchestrator := pagesave.NewPageSaveOrchestrator(nil)
	routes := &Routes{
		treeService: treeService,
		updatePage:  NewUpdatePageUseCase(treeService, slugService, orchestrator, slog.Default()),
		applyRefactor: NewApplyPageRefactorUseCase(
			treeService, slugService, nil, orchestrator, slog.Default(),
		),
	}
	router := registerDraftMutationRoutesWithOptions(t, routes, nil, httpinternal.RouterOptions{
		AuthDisabled:       true,
		EnableLinkRefactor: true,
	})

	page, err := treeService.GetPage(*pageID)
	if err != nil {
		t.Fatalf("GetPage: %v", err)
	}
	updateBody := `{"version":"` + page.Version() + `","title":"Page","slug":"page","draft":false}`
	if recorder := performDraftMutationRequest(router, http.MethodPut, "/api/pages/"+*pageID, updateBody); recorder.Code != http.StatusOK {
		t.Fatalf("explicit draft:false update status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	page, err = treeService.GetPage(*pageID)
	if err != nil {
		t.Fatalf("GetPage after update: %v", err)
	}
	refactorBody := `{"version":"` + page.Version() + `","kind":"rename","title":"Renamed","slug":"renamed","draft":false}`
	if recorder := performDraftMutationRequest(router, http.MethodPost, "/api/pages/"+*pageID+"/refactor/apply", refactorBody); recorder.Code != http.StatusOK {
		t.Fatalf("explicit draft:false refactor status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	page, err = treeService.GetPage(*pageID)
	if err != nil {
		t.Fatalf("GetPage after refactor: %v", err)
	}
	draftUpdateBody := `{"version":"` + page.Version() + `","title":"Renamed","slug":"renamed","draft":true}`
	if recorder := performDraftMutationRequest(router, http.MethodPut, "/api/pages/"+*pageID, draftUpdateBody); recorder.Code != http.StatusForbidden {
		t.Fatalf("draft:true update status = %d, want %d; body=%s", recorder.Code, http.StatusForbidden, recorder.Body.String())
	}
	draftRefactorBody := `{"version":"` + page.Version() + `","kind":"rename","title":"Blocked","slug":"blocked","draft":true}`
	if recorder := performDraftMutationRequest(router, http.MethodPost, "/api/pages/"+*pageID+"/refactor/apply", draftRefactorBody); recorder.Code != http.StatusForbidden {
		t.Fatalf("draft:true refactor status = %d, want %d; body=%s", recorder.Code, http.StatusForbidden, recorder.Body.String())
	}
}

func TestSetDraftRoute_AuthDisabledSameFalseIsIdempotentButStillChecksVersion(t *testing.T) {
	treeService := newLoadedRouteTree(t)
	slugService := tree.NewSlugService()
	kind := tree.NodeKindPage
	pageID, err := treeService.CreateNode("public-editor", nil, "Page", "page", &kind)
	if err != nil {
		t.Fatalf("CreateNode: %v", err)
	}
	routes := &Routes{
		treeService: treeService,
		updatePage: NewUpdatePageUseCase(
			treeService, slugService, pagesave.NewPageSaveOrchestrator(nil), slog.Default(),
		),
	}
	router := registerDraftMutationRoutes(t, routes, nil, true)
	page, err := treeService.GetPage(*pageID)
	if err != nil {
		t.Fatalf("GetPage: %v", err)
	}
	version := page.Version()
	body := `{"version":"` + version + `","draft":false}`
	if recorder := performDraftMutationRequest(router, http.MethodPut, "/api/pages/"+*pageID+"/draft", body); recorder.Code != http.StatusOK {
		t.Fatalf("fresh same-false status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	content := "concurrent edit"
	if err := treeService.UpdateNode("other", page.ID, page.Title, page.Slug, &content, version, nil, nil, false); err != nil {
		t.Fatalf("concurrent UpdateNode: %v", err)
	}
	if recorder := performDraftMutationRequest(router, http.MethodPut, "/api/pages/"+*pageID+"/draft", body); recorder.Code != http.StatusConflict {
		t.Fatalf("stale same-false status = %d, want %d; body=%s", recorder.Code, http.StatusConflict, recorder.Body.String())
	}
}

func TestUpdateRoute_UpdatesVisibleParentContentWhenHiddenDraftChildExists(t *testing.T) {
	treeService := newLoadedRouteTree(t)
	parent, child := createVisibleSectionWithHiddenDraftChild(t, treeService, "Public parent", "public-parent")
	routes := &Routes{
		treeService: treeService,
		updatePage: NewUpdatePageUseCase(
			treeService,
			tree.NewSlugService(),
			pagesave.NewPageSaveOrchestrator(nil),
			slog.Default(),
		),
	}
	router := registerDraftMutationRoutes(t, routes, nil, true)
	body := `{"version":"` + parent.Version() + `","title":"Public parent","slug":"public-parent","content":"updated public content"}`

	recorder := performDraftMutationRequest(router, http.MethodPut, "/api/pages/"+parent.ID, body)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	updatedParent, err := treeService.GetPage(parent.ID)
	if err != nil {
		t.Fatalf("GetPage parent: %v", err)
	}
	if updatedParent.Content != "updated public content" {
		t.Fatalf("parent content = %q, want updated public content", updatedParent.Content)
	}
	assertHiddenDraftChildUnchanged(t, treeService, child, parent.ID, "/public-parent/hidden-child")
	if strings.Contains(recorder.Body.String(), child.ID) || strings.Contains(recorder.Body.String(), child.Title) {
		t.Fatalf("response exposed hidden draft child: %s", recorder.Body.String())
	}
}

func TestUpdateRoute_RejectsSlugChangeThatWouldMoveHiddenDraftChild(t *testing.T) {
	treeService := newLoadedRouteTree(t)
	parent, child := createVisibleSectionWithHiddenDraftChild(t, treeService, "Public parent", "public-parent")
	routes := &Routes{
		treeService: treeService,
		updatePage: NewUpdatePageUseCase(
			treeService,
			tree.NewSlugService(),
			pagesave.NewPageSaveOrchestrator(nil),
			slog.Default(),
		),
	}
	router := registerDraftMutationRoutes(t, routes, nil, true)
	body := `{"version":"` + parent.Version() + `","title":"Public parent","slug":"renamed-parent"}`

	recorder := performDraftMutationRequest(router, http.MethodPut, "/api/pages/"+parent.ID, body)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusNotFound, recorder.Body.String())
	}
	unchangedParent, err := treeService.GetPage(parent.ID)
	if err != nil {
		t.Fatalf("GetPage parent: %v", err)
	}
	if unchangedParent.Slug != "public-parent" {
		t.Fatalf("parent slug = %q, want public-parent", unchangedParent.Slug)
	}
	assertHiddenDraftChildUnchanged(t, treeService, child, parent.ID, "/public-parent/hidden-child")
}

func TestUpdateRoute_ReturnsDraftUnavailableForDraftTransitionRegardlessOfHiddenChildren(t *testing.T) {
	for _, tc := range []struct {
		name        string
		hiddenChild bool
	}{
		{name: "without hidden child"},
		{name: "with hidden child", hiddenChild: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			treeService := newLoadedRouteTree(t)
			sectionKind := tree.NodeKindSection
			parentID, err := treeService.CreateNode("editor", nil, "Public parent", "public-parent", &sectionKind)
			if err != nil {
				t.Fatalf("CreateNode parent: %v", err)
			}
			parent, err := treeService.GetPage(*parentID)
			if err != nil {
				t.Fatalf("GetPage parent: %v", err)
			}
			var child *tree.Page
			if tc.hiddenChild {
				pageKind := tree.NodeKindPage
				childID, err := treeService.CreateNodeWithDraft("editor", parentID, "Hidden child", "hidden-child", &pageKind, true)
				if err != nil {
					t.Fatalf("CreateNodeWithDraft child: %v", err)
				}
				child, err = treeService.GetPage(*childID)
				if err != nil {
					t.Fatalf("GetPage child: %v", err)
				}
			}
			routes := &Routes{
				treeService: treeService,
				updatePage: NewUpdatePageUseCase(
					treeService,
					tree.NewSlugService(),
					pagesave.NewPageSaveOrchestrator(nil),
					slog.Default(),
				),
			}
			router := registerDraftMutationRoutes(t, routes, nil, true)
			body := `{"version":"` + parent.Version() + `","title":"Public parent","slug":"public-parent","draft":true}`

			recorder := performDraftMutationRequest(router, http.MethodPut, "/api/pages/"+parent.ID, body)

			if recorder.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusForbidden, recorder.Body.String())
			}
			if !strings.Contains(recorder.Body.String(), `"code":"`+ErrCodePageDraftUnavailable+`"`) {
				t.Fatalf("body = %s, want %s", recorder.Body.String(), ErrCodePageDraftUnavailable)
			}
			unchangedParent, err := treeService.GetPage(parent.ID)
			if err != nil {
				t.Fatalf("GetPage parent: %v", err)
			}
			if unchangedParent.Draft {
				t.Fatal("parent became draft despite unavailable draft transition")
			}
			if child != nil {
				assertHiddenDraftChildUnchanged(t, treeService, child, parent.ID, "/public-parent/hidden-child")
			}
		})
	}
}

func TestCopyRoute_CopiesVisibleSourceWithoutCopyingItsHiddenDraftChild(t *testing.T) {
	storageDir := t.TempDir()
	treeService := tree.NewTreeService(storageDir)
	if err := treeService.LoadTree(); err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	source, child := createVisibleSectionWithHiddenDraftChild(t, treeService, "Visible source", "visible-source")
	sourceContent := "visible source content"
	if err := treeService.UpdateNode("editor", source.ID, source.Title, source.Slug, &sourceContent, source.Version(), nil, nil, false); err != nil {
		t.Fatalf("UpdateNode source: %v", err)
	}
	sectionKind := tree.NodeKindSection
	targetID, err := treeService.CreateNode("editor", nil, "Clean target", "clean-target", &sectionKind)
	if err != nil {
		t.Fatalf("CreateNode target: %v", err)
	}
	slugService := tree.NewSlugService()
	routes := &Routes{
		treeService: treeService,
		copyPage: NewCopyPageUseCase(
			treeService,
			slugService,
			pagesave.NewPageSaveOrchestrator(nil),
			assets.NewAssetService(storageDir, slugService),
			slog.Default(),
		),
	}
	router := registerDraftMutationRoutes(t, routes, nil, true)
	body := `{"targetParentId":"` + *targetID + `","title":"Copied page","slug":"copied-page"}`

	recorder := performDraftMutationRequest(router, http.MethodPost, "/api/pages/copy/"+source.ID, body)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusCreated, recorder.Body.String())
	}
	copied, err := treeService.FindPageByRoutePath("clean-target/copied-page")
	if err != nil {
		t.Fatalf("FindPageByRoutePath copied page: %v", err)
	}
	if copied.Parent == nil || copied.Parent.ID != *targetID || copied.Content != sourceContent || copied.Draft {
		t.Fatalf("copied page = parent:%v content:%q draft:%v", copied.Parent, copied.Content, copied.Draft)
	}
	assertHiddenDraftChildUnchanged(t, treeService, child, source.ID, "/visible-source/hidden-child")
	if len(copied.Children) != 0 {
		t.Fatalf("copied page included source children: %#v", copied.Children)
	}
}

func TestCopyRoute_RejectsVisibleTargetWithHiddenDraftChildWithoutChangingChildOrder(t *testing.T) {
	storageDir := t.TempDir()
	treeService := tree.NewTreeService(storageDir)
	if err := treeService.LoadTree(); err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	pageKind := tree.NodeKindPage
	sourceID, err := treeService.CreateNode("editor", nil, "Visible source", "visible-source", &pageKind)
	if err != nil {
		t.Fatalf("CreateNode source: %v", err)
	}
	target, child := createVisibleSectionWithHiddenDraftChild(t, treeService, "Visible target", "visible-target")
	beforeOrder := snapshotChildIDs(t, treeService, target.ID)
	slugService := tree.NewSlugService()
	routes := &Routes{
		treeService: treeService,
		copyPage: NewCopyPageUseCase(
			treeService,
			slugService,
			pagesave.NewPageSaveOrchestrator(nil),
			assets.NewAssetService(storageDir, slugService),
			slog.Default(),
		),
	}
	router := registerDraftMutationRoutes(t, routes, nil, true)
	body := `{"targetParentId":"` + target.ID + `","title":"Blocked copy","slug":"blocked-copy"}`

	recorder := performDraftMutationRequest(router, http.MethodPost, "/api/pages/copy/"+*sourceID, body)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusNotFound, recorder.Body.String())
	}
	if _, err := treeService.FindPageByRoutePath("visible-target/blocked-copy"); err == nil {
		t.Fatal("copy was created under target with hidden draft child")
	}
	afterOrder := snapshotChildIDs(t, treeService, target.ID)
	if !slices.Equal(afterOrder, beforeOrder) {
		t.Fatalf("target child order = %v, want %v", afterOrder, beforeOrder)
	}
	assertHiddenDraftChildUnchanged(t, treeService, child, target.ID, "/visible-target/hidden-child")
}

func TestDeleteRoute_DoesNotRevealOrDeleteHiddenDraftChild(t *testing.T) {
	treeService := newLoadedRouteTree(t)
	parent, child := createVisibleSectionWithHiddenDraftChild(t, treeService, "Public parent", "public-parent")
	routes := &Routes{
		treeService: treeService,
		deletePage: NewDeletePageUseCase(
			treeService,
			nil,
			nil,
			pagesave.NewPageSaveOrchestrator(nil),
			slog.Default(),
		),
	}
	router := registerDraftMutationRoutes(t, routes, nil, true)

	recorder := performDraftMutationRequest(router, http.MethodDelete, "/api/pages/"+parent.ID+"?version="+parent.Version(), "")

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusNotFound, recorder.Body.String())
	}
	if _, err := treeService.GetPage(parent.ID); err != nil {
		t.Fatalf("visible parent was deleted: %v", err)
	}
	assertHiddenDraftChildUnchanged(t, treeService, child, parent.ID, "/public-parent/hidden-child")
}

func TestConvertRoute_DoesNotRevealOrConvertSectionWithHiddenDraftChild(t *testing.T) {
	treeService := newLoadedRouteTree(t)
	parent, child := createVisibleSectionWithHiddenDraftChild(t, treeService, "Public parent", "public-parent")
	routes := &Routes{
		treeService: treeService,
		convertPage: NewConvertPageUseCase(
			treeService,
			nil,
			slog.Default(),
		),
	}
	router := registerDraftMutationRoutes(t, routes, nil, true)
	body := `{"version":"` + parent.Version() + `","targetKind":"page"}`

	recorder := performDraftMutationRequest(router, http.MethodPost, "/api/pages/convert/"+parent.ID, body)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusNotFound, recorder.Body.String())
	}
	unchangedParent, err := treeService.GetPage(parent.ID)
	if err != nil {
		t.Fatalf("GetPage parent: %v", err)
	}
	if unchangedParent.Kind != tree.NodeKindSection {
		t.Fatalf("parent kind = %q, want %q", unchangedParent.Kind, tree.NodeKindSection)
	}
	assertHiddenDraftChildUnchanged(t, treeService, child, parent.ID, "/public-parent/hidden-child")
}

func createVisibleSectionWithHiddenDraftChild(t *testing.T, treeService *tree.TreeService, title, slug string) (*tree.Page, *tree.Page) {
	t.Helper()
	sectionKind := tree.NodeKindSection
	parentID, err := treeService.CreateNode("editor", nil, title, slug, &sectionKind)
	if err != nil {
		t.Fatalf("CreateNode parent: %v", err)
	}
	pageKind := tree.NodeKindPage
	childID, err := treeService.CreateNodeWithDraft("editor", parentID, "Hidden child", "hidden-child", &pageKind, true)
	if err != nil {
		t.Fatalf("CreateNodeWithDraft child: %v", err)
	}
	parent, err := treeService.GetPage(*parentID)
	if err != nil {
		t.Fatalf("GetPage parent: %v", err)
	}
	child, err := treeService.GetPage(*childID)
	if err != nil {
		t.Fatalf("GetPage child: %v", err)
	}
	return parent, child
}

func assertHiddenDraftChildUnchanged(t *testing.T, treeService *tree.TreeService, before *tree.Page, parentID, path string) {
	t.Helper()
	after, err := treeService.GetPage(before.ID)
	if err != nil {
		t.Fatalf("GetPage hidden child: %v", err)
	}
	if after.Parent == nil || after.Parent.ID != parentID || !after.Draft || after.CalculatePath() != path || after.Content != before.Content {
		t.Fatalf("hidden child changed = parent:%v draft:%v path:%q content:%q", after.Parent, after.Draft, after.CalculatePath(), after.Content)
	}
}

func snapshotChildIDs(t *testing.T, treeService *tree.TreeService, pageID string) []string {
	t.Helper()
	node, err := treeService.SnapshotPageSubtree(pageID)
	if err != nil {
		t.Fatalf("SnapshotPageSubtree: %v", err)
	}
	ids := make([]string, 0, len(node.Children))
	for _, child := range node.Children {
		ids = append(ids, child.ID)
	}
	return ids
}

func newLoadedRouteTree(t *testing.T) *tree.TreeService {
	t.Helper()
	service := tree.NewTreeService(t.TempDir())
	if err := service.LoadTree(); err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	return service
}

func registerDraftMutationRoutes(t *testing.T, routes *Routes, user *auth.User, authDisabled bool) *gin.Engine {
	return registerDraftMutationRoutesWithOptions(t, routes, user, httpinternal.RouterOptions{AuthDisabled: authDisabled})
}

func registerDraftMutationRoutesWithOptions(t *testing.T, routes *Routes, user *auth.User, opts httpinternal.RouterOptions) *gin.Engine {
	t.Helper()
	router := gin.New()
	if user != nil {
		router.Use(func(c *gin.Context) {
			c.Set("user", user)
			c.Next()
		})
	}
	routes.RegisterRoutes(httpinternal.RouterContext{
		Engine:      router,
		Base:        router,
		AuthCookies: authmw.NewAuthCookies(true, time.Hour, time.Hour),
		CSRFCookie:  security.NewCSRFCookie(true, time.Hour),
		Opts:        opts,
	})
	return router
}

func performDraftMutationRequest(router *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", "test-csrf")
	request.AddCookie(&http.Cookie{Name: "leafwiki_csrf", Value: "test-csrf"})
	router.ServeHTTP(recorder, request)
	return recorder
}
