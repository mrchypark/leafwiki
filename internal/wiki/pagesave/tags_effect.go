package pagesave

import (
	"log/slog"
	"sync"

	"github.com/perber/wiki/internal/core/tree"
	"github.com/perber/wiki/internal/tags"
)

// TagsSideEffect updates the tag index after every page mutation.
type TagsSideEffect struct {
	svc  *tags.TagsService
	tree *tree.TreeService
	log  *slog.Logger
	mu   sync.Mutex
}

func NewTagsSideEffect(svc *tags.TagsService, treeService *tree.TreeService, log *slog.Logger) *TagsSideEffect {
	if log == nil {
		log = slog.Default()
	}
	return &TagsSideEffect{svc: svc, tree: treeService, log: log}
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
			continue
		}
		if state.remove {
			if err := e.svc.DeletePageIndex(state.id); err != nil {
				e.log.Warn("failed to delete page tag index", "pageID", state.id, "error", err)
			}
			continue
		}
		if err := e.svc.IndexPageContent(state.id, state.page.RawContent); err != nil {
			e.log.Warn("failed to reconcile tag index", "pageID", state.id, "error", err)
		}
	}
}
