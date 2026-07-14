package pagesave

import (
	"log/slog"
	"sync"

	"github.com/perber/wiki/internal/core/tree"
	httpmetrics "github.com/perber/wiki/internal/http/metrics"
	"github.com/perber/wiki/internal/properties"
)

// PropertiesSideEffect updates the properties index after every page mutation.
type PropertiesSideEffect struct {
	svc     *properties.PropertiesService
	tree    *tree.TreeService
	log     *slog.Logger
	metrics *httpmetrics.HTTPMetrics
	mu      sync.Mutex
}

func NewPropertiesSideEffect(svc *properties.PropertiesService, treeService *tree.TreeService, log *slog.Logger, metrics ...*httpmetrics.HTTPMetrics) *PropertiesSideEffect {
	if log == nil {
		log = slog.Default()
	}
	var m *httpmetrics.HTTPMetrics
	if len(metrics) > 0 {
		m = metrics[0]
	}
	return &PropertiesSideEffect{svc: svc, tree: treeService, log: log, metrics: m}
}

func (e *PropertiesSideEffect) Name() string {
	return "properties"
}

func (e *PropertiesSideEffect) Apply(event PageSaveEvent) {
	if e.svc == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, state := range loadProjectionPages(e.tree, projectionPageIDs(event, false)) {
		if state.err != nil {
			e.log.Warn("failed to load current page for property index", "pageID", state.id, "error", state.err)
			e.recordFailure(event.Operation)
			continue
		}
		if state.remove {
			if err := e.svc.DeletePropertiesForPage(state.id); err != nil {
				e.log.Warn("failed to delete page property index", "pageID", state.id, "error", err)
				e.recordFailure(event.Operation)
			}
			continue
		}
		if err := e.svc.SetPropertiesForPage(state.id, properties.ExtractPropertiesFromContent(state.page.RawContent)); err != nil {
			e.log.Warn("failed to reconcile property index", "pageID", state.id, "error", err)
			e.recordFailure(event.Operation)
		}
	}
}

func (e *PropertiesSideEffect) recordFailure(operation PageOperationType) {
	e.metrics.IncPageSaveSideEffectFailure(string(operation), e.Name())
}
