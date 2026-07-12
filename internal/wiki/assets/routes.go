package assets

import (
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	coreauth "github.com/perber/wiki/internal/core/auth"
	"github.com/perber/wiki/internal/core/pagevisibility"
	"github.com/perber/wiki/internal/core/tree"
	httpinternal "github.com/perber/wiki/internal/http"
	authmw "github.com/perber/wiki/internal/http/middleware/auth"
	"github.com/perber/wiki/internal/http/middleware/security"
)

const maxMultipartMemory = 32 << 20 // 32 MiB

// Routes is the RouteRegistrar for the assets domain.
type Routes struct {
	upload      *UploadAssetUseCase
	list        *ListAssetsUseCase
	rename      *RenameAssetUseCase
	delete      *DeleteAssetUseCase
	authService *coreauth.AuthService
	assetsDir   string
	log         *slog.Logger
	tree        *tree.TreeService
}

// RoutesConfig holds the dependencies required to build a Routes instance.
type RoutesConfig struct {
	Upload      *UploadAssetUseCase
	List        *ListAssetsUseCase
	Rename      *RenameAssetUseCase
	Delete      *DeleteAssetUseCase
	AuthService *coreauth.AuthService
	AssetsDir   string
	Log         *slog.Logger
	Tree        *tree.TreeService
}

// NewRoutes constructs the assets RouteRegistrar.
func NewRoutes(cfg RoutesConfig) *Routes {
	return &Routes{
		upload:      cfg.Upload,
		list:        cfg.List,
		rename:      cfg.Rename,
		delete:      cfg.Delete,
		authService: cfg.AuthService,
		assetsDir:   cfg.AssetsDir,
		log:         cfg.Log,
		tree:        cfg.Tree,
	}
}

// RegisterRoutes implements RouteRegistrar.
func (r *Routes) RegisterRoutes(ctx httpinternal.RouterContext) {
	opts := ctx.Opts

	// Static file serving for /assets with access control.
	if r.assetsDir != "" {
		assetsFS := gin.Dir(r.assetsDir, false)
		assetsGroup := ctx.Base.Group("/assets")
		if opts.AuthDisabled {
			assetsGroup.Use(authmw.InjectPublicEditor(true))
		} else if opts.PublicAccess {
			assetsGroup.Use(authmw.OptionalAuth(r.authService, ctx.AuthCookies))
		} else {
			assetsGroup.Use(
				authmw.RequireAuth(r.authService, ctx.AuthCookies, false),
			)
		}
		assetsGroup.Use(r.requireStaticAssetVisibility(opts.AuthDisabled))
		assetsGroup.StaticFS("/", assetsFS)
	}

	authGroup := ctx.Base.Group("/api")
	authGroup.Use(
		authmw.InjectPublicEditor(opts.AuthDisabled),
		authmw.RequireAuth(r.authService, ctx.AuthCookies, opts.AuthDisabled),
		security.CSRFMiddleware(ctx.CSRFCookie),
		r.requireDraftManagement(opts.AuthDisabled),
	)

	authGroup.POST("/pages/:id/assets", authmw.RequireEditorOrAdmin(), r.handleUpload(opts.MaxAssetUploadSizeBytes))
	authGroup.GET("/pages/:id/assets", authmw.RequireEditorOrAdmin(), r.handleList)
	authGroup.PUT("/pages/:id/assets/rename", authmw.RequireEditorOrAdmin(), r.handleRename)
	authGroup.DELETE("/pages/:id/assets/:name", authmw.RequireEditorOrAdmin(), r.handleDelete)
}

func (r *Routes) requireStaticAssetVisibility(authDisabled bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		pageID := strings.TrimSpace(c.Param("id"))
		if pageID == "" {
			pageID = assetPageID(c.Request.URL.Path)
		}
		if r.tree == nil {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		node, err := r.tree.FindPageByID(pageID)
		if err != nil || !pagevisibility.CanView(node, authmw.TryGetUser(c), authDisabled) {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		if pagevisibility.IsInDraftSubtree(node) {
			c.Header("Cache-Control", "private, no-store")
			c.Header("Pragma", "no-cache")
		}
		c.Next()
	}
}

func (r *Routes) requireDraftManagement(authDisabled bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if r.tree == nil {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		node, err := r.tree.FindPageByID(strings.TrimSpace(c.Param("id")))
		user := authmw.TryGetUser(c)
		if err != nil || !pagevisibility.CanView(node, user, authDisabled) || node.Draft && !pagevisibility.CanManageDraft(node, user, authDisabled) {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		c.Next()
	}
}

func assetPageID(requestPath string) string {
	path := strings.TrimPrefix(requestPath, "/assets/")
	pageID, _, _ := strings.Cut(path, "/")
	decoded, err := url.PathUnescape(pageID)
	if err != nil {
		return ""
	}
	return decoded
}

// ─── Handlers ───────────────────────────────────────────────────────────────

func (r *Routes) handleUpload(maxUploadSize int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxUploadSize)

		if err := c.Request.ParseMultipartForm(maxMultipartMemory); err != nil {
			respondWithAssetStatusError(c, http.StatusRequestEntityTooLarge, ErrCodeAssetFileTooLarge, "File too large", "file too large")
			return
		}

		pageID := c.Param("id")
		file, header, err := c.Request.FormFile("file")
		if err != nil {
			respondWithAssetStatusError(c, http.StatusBadRequest, ErrCodeAssetMissingFile, "Missing file", "missing file")
			return
		}
		defer func() {
			if err := file.Close(); err != nil {
				r.log.Error("could not close uploaded file", "error", err)
			}
		}()

		user := authmw.MustGetUser(c)
		if user == nil {
			return
		}

		out, err := r.upload.Execute(c.Request.Context(), UploadAssetInput{
			UserID: user.ID, PageID: pageID, File: file, Filename: header.Filename, MaxBytes: maxUploadSize,
		})
		if err != nil {
			respondWithAssetError(c, err)
			return
		}
		c.JSON(http.StatusCreated, gin.H{"file": out.URL})
	}
}

func (r *Routes) handleList(c *gin.Context) {
	pageID := c.Param("id")
	out, err := r.list.Execute(c.Request.Context(), ListAssetsInput{PageID: pageID})
	if err != nil {
		respondWithAssetError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"files": out.Files})
}

func (r *Routes) handleRename(c *gin.Context) {
	pageID := c.Param("id")
	var req struct {
		OldFilename string `json:"old_filename" binding:"required"`
		NewFilename string `json:"new_filename" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		respondWithAssetStatusError(c, http.StatusBadRequest, ErrCodeAssetInvalidPayload, "Invalid payload", "invalid payload")
		return
	}
	user := authmw.MustGetUser(c)
	if user == nil {
		return
	}
	out, err := r.rename.Execute(c.Request.Context(), RenameAssetInput{
		UserID: user.ID, PageID: pageID, OldFilename: req.OldFilename, NewFilename: req.NewFilename,
	})
	if err != nil {
		respondWithAssetError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"url": out.URL})
}

func (r *Routes) handleDelete(c *gin.Context) {
	pageID := c.Param("id")
	filename := c.Param("name")
	if filename == "" {
		respondWithAssetStatusError(c, http.StatusBadRequest, ErrCodeAssetMissingName, "Missing filename", "missing filename")
		return
	}
	user := authmw.MustGetUser(c)
	if user == nil {
		return
	}
	if err := r.delete.Execute(c.Request.Context(), DeleteAssetInput{
		UserID: user.ID, PageID: pageID, Filename: filename,
	}); err != nil {
		respondWithAssetError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "asset deleted"})
}
