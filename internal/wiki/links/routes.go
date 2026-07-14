package links

import (
	"net/http"

	"github.com/gin-gonic/gin"
	coreauth "github.com/perber/wiki/internal/core/auth"
	"github.com/perber/wiki/internal/core/pagevisibility"
	httpinternal "github.com/perber/wiki/internal/http"
	authmw "github.com/perber/wiki/internal/http/middleware/auth"
	"github.com/perber/wiki/internal/http/middleware/security"
)

// Routes is the RouteRegistrar for the links domain.
type Routes struct {
	getLinkStatus *GetLinkStatusUseCase
	authService   *coreauth.AuthService
	authDisabled  bool
}

// RoutesConfig holds the dependencies required to build a Routes instance.
type RoutesConfig struct {
	GetLinkStatus *GetLinkStatusUseCase
	AuthService   *coreauth.AuthService
}

// NewRoutes constructs the links RouteRegistrar.
func NewRoutes(cfg RoutesConfig) *Routes {
	return &Routes{
		getLinkStatus: cfg.GetLinkStatus,
		authService:   cfg.AuthService,
	}
}

// RegisterRoutes implements RouteRegistrar.
func (r *Routes) RegisterRoutes(ctx httpinternal.RouterContext) {
	opts := ctx.Opts
	r.authDisabled = opts.AuthDisabled

	if opts.PublicAccess {
		pub := ctx.Base.Group("/api")
		if !opts.AuthDisabled {
			pub.Use(authmw.OptionalAuth(r.authService, ctx.AuthCookies))
		}
		pub.GET("/pages/:id/links", r.handleGetLinkStatus)
	}

	authGroup := ctx.Base.Group("/api")
	authGroup.Use(
		authmw.InjectPublicEditor(opts.AuthDisabled),
		authmw.RequireAuth(r.authService, ctx.AuthCookies, opts.AuthDisabled),
		security.CSRFMiddleware(ctx.CSRFCookie),
	)

	if !opts.PublicAccess {
		authGroup.GET("/pages/:id/links", r.handleGetLinkStatus)
	}
}

// ─── Handlers ───────────────────────────────────────────────────────────────

func (r *Routes) handleGetLinkStatus(c *gin.Context) {
	if !r.authDisabled {
		c.Header("Cache-Control", "private, no-store")
	}
	pageID := c.Param("id")
	out, err := r.getLinkStatus.Execute(c.Request.Context(), GetLinkStatusInput{
		PageID: pageID, User: authmw.TryGetUser(c), AuthDisabled: r.authDisabled,
	})
	if err != nil {
		respondWithLinkError(c, err)
		return
	}
	if r.getLinkStatus != nil && r.getLinkStatus.tree != nil {
		if node, findErr := r.getLinkStatus.tree.SnapshotPageNode(pageID); findErr == nil && pagevisibility.IsInDraftSubtree(node) {
			c.Header("Cache-Control", "private, no-store")
			c.Header("Pragma", "no-cache")
		}
	}
	c.JSON(http.StatusOK, out.Status)
}
