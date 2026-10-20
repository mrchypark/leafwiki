package wiki

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/perber/wiki/internal/avatar"
	"github.com/perber/wiki/internal/branding"
	"github.com/perber/wiki/internal/core/assets"
	"github.com/perber/wiki/internal/core/auth"
	"github.com/perber/wiki/internal/core/draft"
	"github.com/perber/wiki/internal/core/email"
	"github.com/perber/wiki/internal/core/ignore"
	"github.com/perber/wiki/internal/core/revision"
	"github.com/perber/wiki/internal/core/tree"
	"github.com/perber/wiki/internal/favorites"
	httpmetrics "github.com/perber/wiki/internal/http/metrics"
	"github.com/perber/wiki/internal/links"
	"github.com/perber/wiki/internal/properties"
	"github.com/perber/wiki/internal/search"
	"github.com/perber/wiki/internal/tags"
	"github.com/perber/wiki/internal/usersettings"
	wikiapikeys "github.com/perber/wiki/internal/wiki/apikeys"
	wikiassets "github.com/perber/wiki/internal/wiki/assets"
	wikiauth "github.com/perber/wiki/internal/wiki/auth"
	wikiavatar "github.com/perber/wiki/internal/wiki/avatar"
	wikibackup "github.com/perber/wiki/internal/wiki/backup"
	wikibranding "github.com/perber/wiki/internal/wiki/branding"
	wikihealth "github.com/perber/wiki/internal/wiki/health"
	wikiimporter "github.com/perber/wiki/internal/wiki/importer"
	wikiinstancesettings "github.com/perber/wiki/internal/wiki/instancesettings"
	wikilinks "github.com/perber/wiki/internal/wiki/links"
	wikipages "github.com/perber/wiki/internal/wiki/pages"
	wikiproperties "github.com/perber/wiki/internal/wiki/properties"
	wikirestore "github.com/perber/wiki/internal/wiki/restore"
	wikiresync "github.com/perber/wiki/internal/wiki/resync"
	wikirevisions "github.com/perber/wiki/internal/wiki/revisions"
	wikisearch "github.com/perber/wiki/internal/wiki/search"
	wikisnapshot "github.com/perber/wiki/internal/wiki/snapshot"
	wikitags "github.com/perber/wiki/internal/wiki/tags"
	wikiusersettings "github.com/perber/wiki/internal/wiki/usersettings"
)

type Wiki struct {
	tree              *tree.TreeService
	slug              *tree.SlugService
	auth              *auth.AuthService
	userResolver      *auth.UserResolver
	user              *auth.UserService
	apiKeys           *auth.APIKeyService
	totp              *auth.TOTPService
	emailTokenStore   *auth.EmailTokenStore
	emailTokenService *auth.EmailTokenService
	asset             *assets.AssetService
	branding          *branding.BrandingService
	avatar            *avatar.AvatarService
	searchIndex       *search.SQLiteIndex
	status            *search.IndexingStatus
	storageDir        string
	drafts            *draft.Store

	pagesRoutes            *wikipages.Routes
	authRoutes             *wikiauth.Routes
	assetsRoutes           *wikiassets.Routes
	revisionsRoutes        *wikirevisions.Routes
	searchRoutes           *wikisearch.Routes
	linksRoutes            *wikilinks.Routes
	tagsRoutes             *wikitags.Routes
	propertiesRoutes       *wikiproperties.Routes
	brandingRoutes         *wikibranding.Routes
	avatarRoutes           *wikiavatar.Routes
	apiKeysRoutes          *wikiapikeys.Routes
	importerRoutes         *wikiimporter.Routes
	healthRoutes           *wikihealth.Routes
	revision               *revision.Service
	links                  *links.LinkService
	tags                   *tags.TagsService
	props                  *properties.PropertiesService
	favorites              *favorites.FavoritesStore
	userSettings           *usersettings.UserSettingsService
	userSettingsRoutes     *wikiusersettings.Routes
	backupRoutes           *wikibackup.Routes
	snapshotRoutes         *wikisnapshot.Routes
	restoreRoutes          *wikirestore.Routes
	instanceSettingsRoutes *wikiinstancesettings.Routes
	resyncRoutes           *wikiresync.Routes
	resyncJob              *wikiresync.ResyncJob
	ignoreCache            *ignore.Cache
	reloadMu               sync.Mutex
	reloadWG               sync.WaitGroup
	shutdownCtx            context.Context
	shutdownCancel         context.CancelFunc
	log                    *slog.Logger
	metrics                *httpmetrics.HTTPMetrics
}

const SYSTEM_USER_ID = "system"

type WikiOptions struct {
	StorageDir              string
	AdminUsername           string
	AdminEmail              string
	AdminPassword           string
	JWTSecret               string
	AccessTokenTimeout      time.Duration
	RefreshTokenTimeout     time.Duration
	AuthDisabled            bool
	EnableRevision          bool
	EnableAPIKeyManagement  bool
	MaxRevisionHistory      int
	EditorLimit             int
	MaxAssetUploadSizeBytes int64
	RevisionCoalesceWindow  time.Duration
	TOTPEncryptionKey       string
	SMTP                    email.Config
	Metrics                 *httpmetrics.HTTPMetrics
}

func NewWiki(options *WikiOptions) (*Wiki, error) {
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	w := &Wiki{storageDir: options.StorageDir, drafts: draft.NewStore(options.StorageDir), log: slog.Default().With("component", "Wiki"), resyncJob: wikiresync.NewResyncJob(), shutdownCtx: shutdownCtx, shutdownCancel: shutdownCancel, metrics: options.Metrics}
	if err := w.initAuth(options); err != nil {
		return nil, err
	}
	if err := w.initEmail(options); err != nil {
		return nil, err
	}
	if err := w.initCoreServices(options); err != nil {
		return nil, err
	}
	if err := w.initLinkService(); err != nil {
		return nil, err
	}
	if err := w.initTagsService(); err != nil {
		return nil, err
	}
	if err := w.initPropertiesService(); err != nil {
		return nil, err
	}
	if err := w.initFavoritesService(); err != nil {
		return nil, err
	}
	if err := w.initUserSettingsService(); err != nil {
		return nil, err
	}
	w.bootstrapTagsAndProperties()
	if err := w.initSearch(); err != nil {
		return nil, err
	}
	if err := w.initBranding(); err != nil {
		return nil, err
	}
	if err := w.initAvatarService(); err != nil {
		return nil, err
	}
	if err := w.EnsureWelcomePage(); err != nil {
		return nil, err
	}
	if options.EnableRevision {
		w.revision = revision.NewService(w.storageDir, w.tree, w.log, revision.ServiceOptions{MaxRevisions: options.MaxRevisionHistory, CoalesceWindow: options.RevisionCoalesceWindow})
		w.ensureBaselineRevisions()
	}
	w.metrics.RegisterRuntimeStats(runtimeStatsSource{w: w})
	w.buildRoutes(options)
	return w, nil
}

func (w *Wiki) AuthService() *auth.AuthService             { return w.auth }
func (w *Wiki) BrandingService() *branding.BrandingService { return w.branding }
func (w *Wiki) TOTPService() *auth.TOTPService             { return w.totp }
func (w *Wiki) GetStorageDir() string                      { return w.storageDir }

func (w *Wiki) UserService() *auth.UserService {
	if w.auth != nil {
		return w.auth.UserService()
	}
	return w.user
}
func (w *Wiki) UserResolver() *auth.UserResolver                       { return w.userResolver }
func (w *Wiki) APIKeyService() *auth.APIKeyService                     { return w.apiKeys }
func (w *Wiki) Favorites() *favorites.FavoritesStore                   { return w.favorites }
func (w *Wiki) UserSettingsService() *usersettings.UserSettingsService { return w.userSettings }
func (w *Wiki) EmailTokenService() *auth.EmailTokenService             { return w.emailTokenService }

func (w *Wiki) Close() error {
	w.shutdownCancel()
	w.reloadWG.Wait()
	w.status.Finish()
	if w.auth != nil {
		if err := w.auth.Close(); err != nil {
			return err
		}
	} else if w.user != nil {
		if err := w.user.Close(); err != nil {
			return err
		}
	}
	if w.apiKeys != nil {
		if err := w.apiKeys.Close(); err != nil {
			return err
		}
	}
	if w.emailTokenService != nil {
		w.emailTokenService.Close()
	}
	if w.emailTokenStore != nil {
		if err := w.emailTokenStore.Close(); err != nil {
			w.log.Error("error closing email token store", "error", err)
		}
	}
	if w.links != nil {
		if err := w.links.Close(); err != nil {
			w.log.Error("error closing links", "error", err)
		}
	}
	if w.userSettings != nil {
		if err := w.userSettings.Close(); err != nil {
			w.log.Error("error closing user settings store", "error", err)
		}
	}
	if w.favorites != nil {
		if err := w.favorites.Close(); err != nil {
			w.log.Error("error closing favorites store", "error", err)
		}
	}
	return w.searchIndex.Close()
}
