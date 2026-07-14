package pages

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	coreauth "github.com/perber/wiki/internal/core/auth"
	"github.com/perber/wiki/internal/core/markdown"
	"github.com/perber/wiki/internal/core/pagevisibility"
	sharederrors "github.com/perber/wiki/internal/core/shared/errors"
	"github.com/perber/wiki/internal/core/tree"
	httpinternal "github.com/perber/wiki/internal/http"
	"github.com/perber/wiki/internal/http/dto"
	authmw "github.com/perber/wiki/internal/http/middleware/auth"
	"github.com/perber/wiki/internal/http/middleware/security"
)

const (
	pagesIdRoutePath         = "/pages/:id"
	errInvalidRequestUserMsg = "Invalid request"
	errInvalidRequestLogMsg  = "invalid request"
)

// Routes is the RouteRegistrar for the pages domain.
type Routes struct {
	treeService      *tree.TreeService
	createPage       *CreatePageUseCase
	updatePage       *UpdatePageUseCase
	deletePage       *DeletePageUseCase
	movePage         *MovePageUseCase
	convertPage      *ConvertPageUseCase
	copyPage         *CopyPageUseCase
	getPage          *GetPageUseCase
	findByPath       *FindByPathUseCase
	findByTitle      *FindByTitleUseCase
	lookupPath       *LookupPagePathUseCase
	resolvePermalink *ResolvePermalinkUseCase
	sortPages        *SortPagesUseCase
	ensurePath       *EnsurePathUseCase
	suggestSlug      *SuggestSlugUseCase
	previewRefactor  *PreviewPageRefactorUseCase
	applyRefactor    *ApplyPageRefactorUseCase
	pinPage          *PinPageUseCase
	userResolver     *coreauth.UserResolver
	authService      *coreauth.AuthService
	authDisabled     bool
}

// RoutesConfig holds the dependencies required to build a Routes instance.
type RoutesConfig struct {
	TreeService      *tree.TreeService
	CreatePage       *CreatePageUseCase
	UpdatePage       *UpdatePageUseCase
	DeletePage       *DeletePageUseCase
	MovePage         *MovePageUseCase
	ConvertPage      *ConvertPageUseCase
	CopyPage         *CopyPageUseCase
	GetPage          *GetPageUseCase
	FindByPath       *FindByPathUseCase
	FindByTitle      *FindByTitleUseCase
	LookupPath       *LookupPagePathUseCase
	ResolvePermalink *ResolvePermalinkUseCase
	SortPages        *SortPagesUseCase
	EnsurePath       *EnsurePathUseCase
	SuggestSlug      *SuggestSlugUseCase
	PreviewRefactor  *PreviewPageRefactorUseCase
	ApplyRefactor    *ApplyPageRefactorUseCase
	PinPage          *PinPageUseCase
	UserResolver     *coreauth.UserResolver
	AuthService      *coreauth.AuthService
}

// NewRoutes constructs the pages RouteRegistrar.
func NewRoutes(cfg RoutesConfig) *Routes {
	return &Routes{
		treeService:      cfg.TreeService,
		createPage:       cfg.CreatePage,
		updatePage:       cfg.UpdatePage,
		deletePage:       cfg.DeletePage,
		movePage:         cfg.MovePage,
		convertPage:      cfg.ConvertPage,
		copyPage:         cfg.CopyPage,
		getPage:          cfg.GetPage,
		findByPath:       cfg.FindByPath,
		findByTitle:      cfg.FindByTitle,
		lookupPath:       cfg.LookupPath,
		resolvePermalink: cfg.ResolvePermalink,
		sortPages:        cfg.SortPages,
		ensurePath:       cfg.EnsurePath,
		suggestSlug:      cfg.SuggestSlug,
		previewRefactor:  cfg.PreviewRefactor,
		applyRefactor:    cfg.ApplyRefactor,
		pinPage:          cfg.PinPage,
		userResolver:     cfg.UserResolver,
		authService:      cfg.AuthService,
	}
}

// RegisterRoutes implements RouteRegistrar.
func (r *Routes) RegisterRoutes(ctx httpinternal.RouterContext) {
	opts := ctx.Opts

	if opts.PublicAccess {
		pub := ctx.Base.Group("/api")
		if !opts.AuthDisabled {
			pub.Use(authmw.OptionalAuth(r.authService, ctx.AuthCookies))
		}
		pub.GET("/tree", r.handleGetTree)
		pub.GET("/pages/by-path", r.handleGetByPath)
		pub.GET("/pages/by-title", r.handleFindByTitle)
		pub.GET("/pages/lookup", r.handleLookupPath)
		pub.GET("/pages/permalink/:id", r.handleResolvePermalink)
		pub.GET(pagesIdRoutePath, r.handleGetPage)
	}
	r.authDisabled = opts.AuthDisabled

	authGroup := ctx.Base.Group("/api")
	authGroup.Use(
		authmw.InjectPublicEditor(opts.AuthDisabled),
		authmw.RequireAuth(r.authService, ctx.AuthCookies, opts.AuthDisabled),
		security.CSRFMiddleware(ctx.CSRFCookie),
	)

	if !opts.PublicAccess {
		authGroup.GET("/tree", r.handleGetTree)
		authGroup.GET(pagesIdRoutePath, r.handleGetPage)
		authGroup.GET("/pages/lookup", r.handleLookupPath)
		authGroup.GET("/pages/by-path", r.handleGetByPath)
		authGroup.GET("/pages/by-title", r.handleFindByTitle)
		authGroup.GET("/pages/permalink/:id", r.handleResolvePermalink)
	}

	authGroup.GET("/pages/slug-suggestion", authmw.RequireEditorOrAdmin(), r.handleSuggestSlug)
	authGroup.POST("/pages", authmw.RequireEditorOrAdmin(), r.handleCreate)
	authGroup.PUT(pagesIdRoutePath, authmw.RequireEditorOrAdmin(), r.handleUpdate)
	authGroup.PUT("/pages/:id/draft", authmw.RequireEditorOrAdmin(), r.requireVisiblePage(), r.handleSetDraft)
	authGroup.DELETE(pagesIdRoutePath, authmw.RequireEditorOrAdmin(), r.requireVisiblePage(), r.handleDelete)
	authGroup.PUT("/pages/:id/move", authmw.RequireEditorOrAdmin(), r.handleMove)
	authGroup.PUT("/pages/:id/sort", authmw.RequireEditorOrAdmin(), r.handleSort)
	authGroup.PUT("/pages/:id/pin", authmw.RequireEditorOrAdmin(), r.requireVisiblePage(), r.handlePin)
	authGroup.POST("/pages/ensure", authmw.RequireEditorOrAdmin(), r.handleEnsurePath)
	authGroup.POST("/pages/convert/:id", authmw.RequireEditorOrAdmin(), r.requireVisiblePage(), r.handleConvert)
	authGroup.POST("/pages/copy/:id", authmw.RequireEditorOrAdmin(), r.requireVisiblePage(), r.handleCopy)
	if opts.EnableLinkRefactor {
		authGroup.POST("/pages/:id/refactor/preview", authmw.RequireEditorOrAdmin(), r.requireVisiblePage(), r.handleRefactorPreview)
		authGroup.POST("/pages/:id/refactor/apply", authmw.RequireEditorOrAdmin(), r.requireVisiblePage(), r.handleRefactorApply)
	}
}

// ─── Handlers ───────────────────────────────────────────────────────────────

func (r *Routes) handleGetTree(c *gin.Context) {
	r.setVisibilityCacheHeader(c)
	root := pagevisibility.Prune(r.treeService.SnapshotTree(), authmw.TryGetUser(c), r.authDisabled)
	depth := -1
	if depthStr := strings.TrimSpace(c.Query("depth")); depthStr != "" {
		if parsed, err := strconv.Atoi(depthStr); err == nil {
			depth = parsed
		}
	}
	c.JSON(http.StatusOK, dto.ToAPINodeWithDepth(root, "", r.userResolver, depth))
}

func (r *Routes) handleGetPage(c *gin.Context) {
	r.setVisibilityCacheHeader(c)
	id := strings.TrimSpace(c.Param("id"))
	out, err := r.getPage.Execute(c.Request.Context(), GetPageInput{ID: id})
	if err != nil {
		respondWithPageError(c, err)
		return
	}
	if !pagevisibility.CanView(out.Page.PageNode, authmw.TryGetUser(c), r.authDisabled) {
		respondWithPageError(c, tree.ErrPageNotFound)
		return
	}
	r.respondPage(c, http.StatusOK, out.Page)
}

func (r *Routes) handleGetByPath(c *gin.Context) {
	r.setVisibilityCacheHeader(c)
	routePath := strings.TrimSpace(c.Query("path"))
	if routePath == "" {
		respondWithPageStatusError(c, http.StatusBadRequest, ErrCodePageMissingPath, "Missing path", "missing path")
		return
	}
	out, err := r.findByPath.Execute(c.Request.Context(), FindByPathInput{RoutePath: routePath})
	if err != nil {
		respondWithPageError(c, err)
		return
	}
	if !pagevisibility.CanView(out.Page.PageNode, authmw.TryGetUser(c), r.authDisabled) {
		respondWithPageError(c, tree.ErrPageNotFound)
		return
	}
	depth := 0
	if out.Page.Kind == tree.NodeKindSection {
		depth = 1
	}
	r.respondPageWithDepth(c, http.StatusOK, out.Page, depth)
}

func (r *Routes) handleFindByTitle(c *gin.Context) {
	r.setVisibilityCacheHeader(c)
	title := strings.TrimSpace(c.Query("title"))
	if title == "" {
		respondWithPageStatusError(c, http.StatusBadRequest, ErrCodePageMissingTitle, "Missing title query parameter", "missing title")
		return
	}
	out := r.findByTitle.Execute(c.Request.Context(), title)
	visible := out.Matches[:0]
	for _, match := range out.Matches {
		if pagevisibility.CanView(match.VisibilityNode, authmw.TryGetUser(c), r.authDisabled) {
			visible = append(visible, match)
		}
	}
	out.Matches = visible
	out.Count = len(visible)
	c.JSON(http.StatusOK, out)
}

func (r *Routes) handleLookupPath(c *gin.Context) {
	r.setVisibilityCacheHeader(c)
	path := strings.TrimSpace(c.Query("path"))
	out, err := r.lookupPath.Execute(c.Request.Context(), LookupPagePathInput{Path: path})
	if err != nil {
		respondWithPageError(c, err)
		return
	}
	for _, segment := range out.Lookup.Segments {
		if segment.ID == nil {
			continue
		}
		if !pagevisibility.CanView(segment.VisibilityNode, authmw.TryGetUser(c), r.authDisabled) {
			respondWithPageError(c, tree.ErrPageNotFound)
			return
		}
	}
	c.JSON(http.StatusOK, out.Lookup)
}

func (r *Routes) handleResolvePermalink(c *gin.Context) {
	r.setVisibilityCacheHeader(c)
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		respondWithPageStatusError(c, http.StatusBadRequest, ErrCodePageMissingID, "Page ID is required", "page id is required")
		return
	}
	out, err := r.resolvePermalink.Execute(c.Request.Context(), ResolvePermalinkInput{ID: id})
	if err != nil {
		respondWithPageError(c, err)
		return
	}
	if !pagevisibility.CanView(out.Target.VisibilityNode, authmw.TryGetUser(c), r.authDisabled) {
		respondWithPageError(c, tree.ErrPageNotFound)
		return
	}
	c.JSON(http.StatusOK, out.Target)
}

func (r *Routes) handleSuggestSlug(c *gin.Context) {
	title := strings.TrimSpace(c.Query("title"))
	if title == "" {
		respondWithPageStatusError(c, http.StatusBadRequest, ErrCodePageMissingTitle, "Title query param is required", "title query param is required")
		return
	}
	for _, id := range []string{strings.TrimSpace(c.Query("parentId")), strings.TrimSpace(c.Query("currentId"))} {
		if id != "" && id != "root" && !r.requireVisibleNode(c, id) {
			return
		}
	}
	out, err := r.suggestSlug.Execute(c.Request.Context(), SuggestSlugInput{
		ParentID:  strings.TrimSpace(c.Query("parentId")),
		CurrentID: strings.TrimSpace(c.Query("currentId")),
		Title:     title,
	})
	if err != nil {
		respondWithPageError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"slug": out.Slug})
}

func (r *Routes) handleCreate(c *gin.Context) {
	var req struct {
		ParentID *string `json:"parentId"`
		Title    string  `json:"title" binding:"required"`
		Slug     string  `json:"slug" binding:"required"`
		Kind     *string `json:"kind"`
		Draft    bool    `json:"draft"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondWithPageStatusError(c, http.StatusBadRequest, ErrCodePageInvalidRequest, errInvalidRequestUserMsg, errInvalidRequestLogMsg)
		return
	}
	user := authmw.MustGetUser(c)
	if user == nil {
		return
	}
	if req.Draft && r.authDisabled {
		respondWithDraftUnavailable(c)
		return
	}
	if req.ParentID != nil && *req.ParentID != "" && *req.ParentID != "root" && !r.requireVisibleSubtree(c, *req.ParentID) {
		return
	}
	kind := kindFromString(req.Kind)
	out, err := r.createPage.Execute(c.Request.Context(), CreatePageInput{
		UserID: user.ID, ParentID: req.ParentID, Title: req.Title, Slug: req.Slug, Kind: &kind, Draft: req.Draft,
		DraftAllowed: pagevisibility.CanViewDrafts(user, r.authDisabled),
	})
	if err != nil {
		respondWithPageError(c, err)
		return
	}
	r.respondPage(c, http.StatusCreated, out.Page)
}

func (r *Routes) handleSetDraft(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	var req struct {
		Version string `json:"version" binding:"required"`
		Draft   *bool  `json:"draft" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondWithPageStatusError(c, http.StatusBadRequest, ErrCodePageInvalidRequest, errInvalidRequestUserMsg, errInvalidRequestLogMsg)
		return
	}
	user := authmw.MustGetUser(c)
	if user == nil {
		return
	}
	out, err := r.updatePage.Execute(c.Request.Context(), UpdatePageInput{
		UserID: user.ID, ID: id, Version: req.Version,
		Draft: req.Draft, DraftAllowed: pagevisibility.CanViewDrafts(user, r.authDisabled),
	})
	if err != nil {
		respondWithPageError(c, err)
		return
	}
	r.respondPage(c, http.StatusOK, out.Page)
}

func (r *Routes) handleUpdate(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	var req struct {
		Version    string            `json:"version" binding:"required"`
		Title      string            `json:"title" binding:"required"`
		Slug       string            `json:"slug" binding:"required"`
		Content    *string           `json:"content"`
		Tags       []string          `json:"tags"`
		Properties map[string]string `json:"properties"`
		Draft      *bool             `json:"draft"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondWithPageStatusError(c, http.StatusBadRequest, ErrCodePageInvalidRequest, errInvalidRequestUserMsg, errInvalidRequestLogMsg)
		return
	}
	if err := validatePageMetadataInput(req.Tags, req.Properties); err != nil {
		respondWithPageError(c, err)
		return
	}
	user := authmw.MustGetUser(c)
	if user == nil {
		return
	}
	node, err := r.treeService.SnapshotPageSubtree(id)
	if err != nil || !pagevisibility.CanView(node, user, r.authDisabled) {
		respondWithPageError(c, tree.ErrPageNotFound)
		return
	}
	draftChanging := req.Draft != nil && *req.Draft != node.Draft
	if req.Slug != node.Slug && !pagevisibility.CanViewSubtree(node, user, r.authDisabled) {
		respondWithPageError(c, tree.ErrPageNotFound)
		return
	}
	if draftChanging && !pagevisibility.CanManageDraft(node, user, r.authDisabled) {
		respondWithDraftUnavailable(c)
		return
	}

	// Normalize tags to lowercase so the on-disk file is always consistent with
	// what the search index stores. Preserve nil so callers can distinguish
	// "no tags field" (nil → preserve existing) from "empty tags" (non-nil).
	normalizedTags := req.Tags
	if req.Tags != nil {
		normalizedTags = normalizeTagInputs(req.Tags)
	}

	out, err := r.updatePage.Execute(c.Request.Context(), UpdatePageInput{
		UserID: user.ID, ID: id, Version: req.Version, Title: req.Title, Slug: req.Slug,
		Content: req.Content,
		Tags:    normalizedTags, Properties: req.Properties,
		Draft: req.Draft, DraftAllowed: pagevisibility.CanViewDrafts(user, r.authDisabled),
	})
	if err != nil {
		respondWithPageError(c, err)
		return
	}
	r.respondPage(c, http.StatusOK, out.Page)
}

func (r *Routes) canView(c *gin.Context, id string) bool {
	node, err := r.treeService.SnapshotPageNode(id)
	return err == nil && pagevisibility.CanView(node, authmw.TryGetUser(c), r.authDisabled)
}

func (r *Routes) setVisibilityCacheHeader(c *gin.Context) {
	if !r.authDisabled {
		c.Header("Cache-Control", "private, no-store")
	}
}

func (r *Routes) requireVisibleSubtree(c *gin.Context, id string) bool {
	node, err := r.treeService.SnapshotPageSubtree(id)
	if err != nil || !pagevisibility.CanViewSubtree(node, authmw.TryGetUser(c), r.authDisabled) {
		respondWithPageError(c, tree.ErrPageNotFound)
		return false
	}
	return true
}

func (r *Routes) requireVisibleNode(c *gin.Context, id string) bool {
	if !r.canView(c, id) {
		respondWithPageError(c, tree.ErrPageNotFound)
		return false
	}
	return true
}

func (r *Routes) requireVisiblePage() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !r.requireVisibleNode(c, strings.TrimSpace(c.Param("id"))) {
			c.Abort()
			return
		}
		c.Next()
	}
}

func (r *Routes) requireVisibleID(c *gin.Context, id string) bool {
	return r.requireVisibleNode(c, id)
}

func (r *Routes) handleDelete(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	recursive := c.DefaultQuery("recursive", "false") == "true"
	version := c.Query("version")
	user := authmw.MustGetUser(c)
	if user == nil {
		return
	}
	if !r.requireVisibleSubtree(c, id) {
		return
	}
	if err := r.deletePage.Execute(c.Request.Context(), DeletePageInput{
		UserID: user.ID, ID: id, Version: version, Recursive: recursive,
	}); err != nil {
		respondWithPageError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Page deleted"})
}

func (r *Routes) handleMove(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	var req struct {
		Version  string `json:"version" binding:"required"`
		ParentID string `json:"parentId"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondWithPageStatusError(c, http.StatusBadRequest, ErrCodePageInvalidPayload, "Invalid payload", "invalid payload")
		return
	}
	user := authmw.MustGetUser(c)
	if user == nil {
		return
	}
	if !r.requireVisibleSubtree(c, id) || req.ParentID != "" && req.ParentID != "root" && !r.requireVisibleSubtree(c, req.ParentID) {
		return
	}
	if err := r.movePage.Execute(c.Request.Context(), MovePageInput{
		UserID: user.ID, ID: id, Version: req.Version, ParentID: req.ParentID,
	}); err != nil {
		respondWithPageError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Page moved"})
}

func (r *Routes) handleSort(c *gin.Context) {
	parentID := strings.TrimSpace(c.Param("id"))
	var req struct {
		OrderedIDs []string `json:"orderedIds"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondWithPageStatusError(c, http.StatusBadRequest, ErrCodePageInvalidRequest, errInvalidRequestUserMsg, errInvalidRequestLogMsg)
		return
	}
	if parentID != "" && parentID != "root" && !r.requireVisibleSubtree(c, parentID) {
		return
	}
	for _, id := range req.OrderedIDs {
		if !r.requireVisibleSubtree(c, id) {
			return
		}
	}
	if err := r.sortPages.Execute(c.Request.Context(), SortPagesInput{
		ParentID: parentID, OrderedIDs: req.OrderedIDs,
	}); err != nil {
		respondWithPageError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Pages sorted successfully"})
}

func (r *Routes) handlePin(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	var req struct {
		Version string `json:"version" binding:"required"`
		Pinned  bool   `json:"pinned"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondWithPageStatusError(c, http.StatusBadRequest, ErrCodePageInvalidRequest, errInvalidRequestUserMsg, errInvalidRequestLogMsg)
		return
	}
	if !r.requireVisibleSubtree(c, id) {
		return
	}
	out, err := r.pinPage.Execute(c.Request.Context(), PinPageInput{
		ID:      id,
		Version: req.Version,
		Pinned:  req.Pinned,
	})
	if err != nil {
		respondWithPageError(c, err)
		return
	}
	r.respondPage(c, http.StatusOK, out.Page)
}

func (r *Routes) handleEnsurePath(c *gin.Context) {
	var req struct {
		Path  string  `json:"path" binding:"required"`
		Title string  `json:"title" binding:"required"`
		Kind  *string `json:"kind"`
		Draft bool    `json:"draft"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondWithPageStatusError(c, http.StatusBadRequest, ErrCodePageInvalidRequest, errInvalidRequestUserMsg, errInvalidRequestLogMsg)
		return
	}
	user := authmw.MustGetUser(c)
	if user == nil {
		return
	}
	if req.Draft && r.authDisabled {
		respondWithDraftUnavailable(c)
		return
	}
	lookup, err := r.lookupPath.Execute(c.Request.Context(), LookupPagePathInput{Path: req.Path})
	if err != nil {
		respondWithPageError(c, err)
		return
	}
	for _, segment := range lookup.Lookup.Segments {
		if segment.ID == nil {
			continue
		}
		if !r.canView(c, *segment.ID) {
			respondWithPageError(c, tree.ErrPageNotFound)
			return
		}
	}
	kind := kindFromString(req.Kind)
	out, err := r.ensurePath.Execute(c.Request.Context(), EnsurePathInput{
		UserID: user.ID, TargetPath: req.Path, TargetTitle: req.Title, Kind: &kind, Draft: req.Draft,
	})
	if err != nil {
		respondWithPageError(c, err)
		return
	}
	node, err := r.treeService.SnapshotPageSubtree(out.Page.ID)
	if err != nil || !pagevisibility.CanViewSubtree(node, user, r.authDisabled) {
		respondWithPageError(c, tree.ErrPageNotFound)
		return
	}
	r.respondPage(c, http.StatusOK, out.Page)
}

func (r *Routes) handleConvert(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	var req struct {
		Kind    string `json:"targetKind" binding:"required"`
		Version string `json:"version" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondWithPageStatusError(c, http.StatusBadRequest, ErrCodePageInvalidRequest, errInvalidRequestUserMsg, errInvalidRequestLogMsg)
		return
	}
	if req.Kind != "page" && req.Kind != "section" {
		respondWithPageStatusError(c, http.StatusBadRequest, ErrCodePageInvalidTargetKind, "Invalid targetKind", "invalid target kind")
		return
	}
	user := authmw.MustGetUser(c)
	if user == nil {
		return
	}
	if !r.requireVisibleSubtree(c, id) {
		return
	}
	if err := r.convertPage.Execute(c.Request.Context(), ConvertPageInput{
		UserID: user.ID, ID: id, Version: req.Version, TargetKind: tree.NodeKind(req.Kind),
	}); err != nil {
		respondWithPageError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (r *Routes) handleCopy(c *gin.Context) {
	sourceID := strings.TrimSpace(c.Param("id"))
	var req struct {
		ParentID *string `json:"targetParentId"`
		Title    string  `json:"title" binding:"required"`
		Slug     string  `json:"slug" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondWithPageStatusError(c, http.StatusBadRequest, ErrCodePageInvalidRequest, errInvalidRequestUserMsg, errInvalidRequestLogMsg)
		return
	}
	user := authmw.MustGetUser(c)
	if user == nil {
		return
	}
	if req.ParentID != nil && *req.ParentID != "" && *req.ParentID != "root" && !r.requireVisibleSubtree(c, *req.ParentID) {
		return
	}
	out, err := r.copyPage.Execute(c.Request.Context(), CopyPageInput{
		UserID: user.ID, SourcePageID: sourceID, TargetParentID: req.ParentID,
		Title: req.Title, Slug: req.Slug,
	})
	if err != nil {
		respondWithPageError(c, err)
		return
	}
	r.respondPage(c, http.StatusCreated, out.Page)
}

func (r *Routes) handleRefactorPreview(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	var req struct {
		Kind        string  `json:"kind" binding:"required"`
		Title       string  `json:"title"`
		Slug        string  `json:"slug"`
		Content     *string `json:"content"`
		NewParentID *string `json:"parentId"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondWithPageStatusError(c, http.StatusBadRequest, ErrCodePageInvalidRequest, errInvalidRequestUserMsg, errInvalidRequestLogMsg)
		return
	}
	if !r.requireVisibleSubtree(c, id) || req.NewParentID != nil && *req.NewParentID != "" && *req.NewParentID != "root" && !r.requireVisibleSubtree(c, *req.NewParentID) {
		return
	}
	out, err := r.previewRefactor.Execute(c.Request.Context(), RefactorPreviewInput{
		PageID: id, Kind: req.Kind, Title: req.Title, Slug: req.Slug,
		Content: req.Content, NewParentID: req.NewParentID,
	})
	if err != nil {
		respondWithPageError(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (r *Routes) handleRefactorApply(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	var req struct {
		Version      string            `json:"version" binding:"required"`
		Kind         string            `json:"kind" binding:"required"`
		Title        string            `json:"title"`
		Slug         string            `json:"slug"`
		Content      *string           `json:"content"`
		Tags         []string          `json:"tags"`
		Properties   map[string]string `json:"properties"`
		Draft        *bool             `json:"draft"`
		NewParentID  *string           `json:"parentId"`
		RewriteLinks bool              `json:"rewriteLinks"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondWithPageStatusError(c, http.StatusBadRequest, ErrCodePageInvalidRequest, errInvalidRequestUserMsg, errInvalidRequestLogMsg)
		return
	}
	if req.NewParentID != nil && *req.NewParentID != "" && *req.NewParentID != "root" && !r.requireVisibleID(c, *req.NewParentID) {
		return
	}
	user := authmw.MustGetUser(c)
	if user == nil {
		return
	}
	if err := validatePageMetadataInput(req.Tags, req.Properties); err != nil {
		respondWithPageError(c, err)
		return
	}
	if !r.requireVisibleSubtree(c, id) || req.NewParentID != nil && *req.NewParentID != "" && *req.NewParentID != "root" && !r.requireVisibleSubtree(c, *req.NewParentID) {
		return
	}
	if req.Draft != nil && *req.Draft && r.authDisabled {
		respondWithDraftUnavailable(c)
		return
	}
	normalizedTags := req.Tags
	if req.Tags != nil {
		normalizedTags = normalizeTagInputs(req.Tags)
	}
	page, err := r.applyRefactor.Execute(c.Request.Context(), RefactorApplyInput{
		Version: req.Version, UserID: user.ID,
		Tags: normalizedTags, Properties: req.Properties, Draft: req.Draft,
		DraftAllowed: pagevisibility.CanViewDrafts(user, r.authDisabled),
		RefactorPreviewInput: RefactorPreviewInput{
			PageID: id, Kind: req.Kind, Title: req.Title, Slug: req.Slug,
			Content: req.Content, NewParentID: req.NewParentID,
		},
		RewriteLinks: req.RewriteLinks,
	})
	if err != nil {
		respondWithPageError(c, err)
		return
	}
	r.respondPage(c, http.StatusOK, page)
}

func (r *Routes) respondPage(c *gin.Context, status int, page *tree.Page) {
	r.respondPageWithDepth(c, status, page, -1)
}

func (r *Routes) respondPageWithDepth(c *gin.Context, status int, page *tree.Page, depth int) {
	visible := *page
	visible.PageNode = pagevisibility.Prune(page.PageNode, authmw.TryGetUser(c), r.authDisabled)
	if visible.PageNode == nil {
		respondWithPageError(c, tree.ErrPageNotFound)
		return
	}
	apiPage := dto.ToAPIPageWithDepth(&visible, r.userResolver, depth)
	r.enrichPageMetadata(apiPage, visible.RawContent)
	c.JSON(status, apiPage)
}

func (r *Routes) enrichPageMetadata(page *dto.Page, raw string) {
	if page == nil {
		return
	}

	page.Tags = []string{}
	page.Properties = map[string]string{}

	fm, _, has, err := markdown.ParseFrontmatter(raw)
	if err != nil || !has || len(fm.ExtraFields) == 0 {
		return
	}

	tags, properties := extractPageMetadata(fm.ExtraFields)
	page.Tags = tags
	page.Properties = properties
}

func extractPageMetadata(fields map[string]interface{}) ([]string, map[string]string) {
	tags := []string{}
	properties := map[string]string{}

	for rawKey, value := range fields {
		key := strings.TrimSpace(rawKey)
		lower := strings.ToLower(key)

		if lower == "tags" {
			tags = normalizeMetadataTags(value)
			continue
		}
		if markdown.IsSystemKey(key) {
			continue
		}

		flattenMetadataEntry(key, value, properties)
	}

	return tags, properties
}

// flattenMetadataEntry recursively flattens nested YAML maps into dot-notation
// keys (e.g. {"a": {"b": "v"}} → properties["a.b"] = "v").
func flattenMetadataEntry(prefix string, value interface{}, properties map[string]string) {
	flattenMetadataEntryDepth(prefix, value, properties, 0)
}

func flattenMetadataEntryDepth(prefix string, value interface{}, properties map[string]string, depth int) {
	if depth >= maxFlattenDepth {
		return
	}
	switch v := value.(type) {
	case string:
		s := strings.TrimSpace(v)
		if s != "" && !strings.ContainsRune(s, '\n') {
			if _, exists := properties[prefix]; !exists {
				properties[prefix] = s
			}
		}
	case map[string]interface{}:
		for childKey, childValue := range v {
			childKey = strings.TrimSpace(childKey)
			if childKey == "" {
				continue
			}
			if strings.HasPrefix(strings.ToLower(childKey), "leafwiki_") {
				continue
			}
			flattenMetadataEntryDepth(prefix+"."+childKey, childValue, properties, depth+1)
		}
	}
}

const maxFlattenDepth = 20

func normalizeMetadataTags(value interface{}) []string {
	list, ok := value.([]interface{})
	if !ok {
		return []string{}
	}

	rawTags := make([]string, 0, len(list))
	for _, item := range list {
		tag, ok := item.(string)
		if !ok {
			continue
		}
		rawTags = append(rawTags, tag)
	}

	return normalizeTagInputs(rawTags)
}

func normalizeTagInputs(tags []string) []string {
	seen := make(map[string]struct{}, len(tags))
	result := make([]string, 0, len(tags))

	for _, tag := range tags {
		normalized := strings.ToLower(strings.TrimSpace(tag))
		if normalized == "" {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}

	return result
}

func validatePageMetadataInput(tags []string, properties map[string]string) error {
	ve := sharederrors.NewValidationErrors()
	seenTags := map[string]struct{}{}

	for index, tag := range tags {
		trimmed := strings.TrimSpace(tag)
		field := "tags[" + strconv.Itoa(index) + "]"
		if trimmed == "" {
			ve.Add(field, "Tag must not be empty")
			continue
		}
		if trimmed != tag {
			ve.Add(field, "Tag must not contain leading or trailing whitespace")
			continue
		}
		key := strings.ToLower(trimmed)
		if _, exists := seenTags[key]; exists {
			ve.Add(field, "Tag must be unique")
			continue
		}
		seenTags[key] = struct{}{}
	}

	for rawKey := range properties {
		key := strings.TrimSpace(rawKey)
		field := "properties." + rawKey
		switch {
		case key == "":
			ve.Add(field, "Property key must not be empty")
		case key != rawKey:
			ve.Add(field, "Property key must not contain leading or trailing whitespace")
		case markdown.IsSystemKey(key):
			ve.Add(field, "Property key is reserved")
		}
	}

	if ve.HasErrors() {
		return ve
	}

	return nil
}

// ─── Helpers ────────────────────────────────────────────────────────────────

// kindFromString converts an optional string pointer to a NodeKind.
// Defaults to NodeKindPage when nil or unrecognized.
func kindFromString(s *string) tree.NodeKind {
	if s != nil && *s == string(tree.NodeKindSection) {
		return tree.NodeKindSection
	}
	return tree.NodeKindPage
}
