package pagesave

import (
	"log/slog"
	"sync"

	"github.com/perber/wiki/internal/core/tree"
	httpmetrics "github.com/perber/wiki/internal/http/metrics"
	"github.com/perber/wiki/internal/tags"
)

// TagsSideEffect updates the tag index after every page mutation.
type TagsSideEffect struct {
	svc     *tags.TagsService
	tree    *tree.TreeService
	log     *slog.Logger
	metrics *httpmetrics.HTTPMetrics
	mu      sync.Mutex
}

func NewTagsSideEffect(svc *tags.TagsService, treeService *tree.TreeService, log *slog.Logger, metrics ...*httpmetrics.HTTPMetrics) *TagsSideEffect {
	if log == nil {
		log = slog.Default()
	}
	var m *httpmetrics.HTTPMetrics
	if len(metrics) > 0 {
		m = metrics[0]
	}
	return &TagsSideEffect{svc: svc, tree: treeService, log: log, metrics: m}
}

func (e *TagsSideEffect) Name() string {
	return "tags"
}

func (e *TagsSideEffect) Apply(event PageSaveEvent) {
	if e.svc == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, state := range loadProjectionPages(e.tree, projectionPageIDs(event, false)) {
		if state.err != nil {
			e.log.Warn("failed to load current page for tag index", "pageID", state.id, "error", state.err)
			e.recordFailure(event.Operation)
			continue
		}
		if state.remove {
			if err := e.svc.DeletePageIndex(state.id); err != nil {
				e.log.Warn("failed to delete page tag index", "pageID", state.id, "error", err)
				e.recordFailure(event.Operation)
			}
			continue
		}
		if err := e.svc.IndexPageContent(state.id, state.page.RawContent); err != nil {
			e.log.Warn("failed to reconcile tag index", "pageID", state.id, "error", err)
			e.recordFailure(event.Operation)
		}
	}
}

func (e *TagsSideEffect) recordFailure(operation PageOperationType) {
	e.metrics.IncPageSaveSideEffectFailure(string(operation), e.Name())
}
