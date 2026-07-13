package pagesave

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	"github.com/perber/wiki/internal/core/pagevisibility"
	"github.com/perber/wiki/internal/core/tree"
	"github.com/perber/wiki/internal/search"
)

// SearchIndexSideEffect updates the search index after every page mutation.
type SearchIndexSideEffect struct {
	index *search.SQLiteIndex
	tree  *tree.TreeService
	log   *slog.Logger
	mu    sync.Mutex
}

func NewSearchIndexSideEffect(index *search.SQLiteIndex, treeService *tree.TreeService, log *slog.Logger) *SearchIndexSideEffect {
	if log == nil {
		log = slog.Default()
	}
	return &SearchIndexSideEffect{index: index, tree: treeService, log: log}
}

func (e *SearchIndexSideEffect) Apply(event PageSaveEvent) {
	if e.index == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, state := range loadProjectionPages(e.tree, projectionPageIDs(event, true)) {
		if state.err != nil {
			e.log.Warn("failed to load current page for search index", "pageID", state.id, "error", state.err)
			continue
		}
		if state.remove {
			if err := e.index.RemovePage(state.id); err != nil {
				e.log.Warn("failed to remove page from search index", "pageID", state.id, "error", err)
			}
			continue
		}
		if err := e.writeToIndex(state.page, state.page.RawContent); err != nil {
			e.log.Warn("failed to reconcile search index", "pageID", state.id, "error", err)
		}
	}
}

// IndexAllPages clears the search index and rebuilds it from the current tree state.
// Call this once at startup; runtime updates are handled via Apply.
func (e *SearchIndexSideEffect) IndexAllPages() error {
	return e.IndexAllPagesContext(context.Background())
}

func (e *SearchIndexSideEffect) IndexAllPagesContext(ctx context.Context) error {
	if e.index == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := e.index.Clear(); err != nil {
		return err
	}

	var ids []string
	if err := e.tree.WalkNodes(func(id string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		ids = append(ids, id)
		return nil
	}); err != nil {
		return err
	}

	pages, errs := e.tree.GetPages(ids)
	for i, page := range pages {
		if err := ctx.Err(); err != nil {
			return err
		}
		if errs[i] != nil {
			e.log.Warn("skipping page during search bootstrap", "pageID", ids[i], "error", errs[i])
			continue
		}
		if pagevisibility.IsInDraftSubtree(page.PageNode) {
			continue
		}
		if err := e.writeToIndex(page, page.RawContent); err != nil {
			e.log.Warn("failed to update search index during bootstrap", "pageID", page.ID, "error", err)
		}
	}
	return nil
}

func (e *SearchIndexSideEffect) writeToIndex(page *tree.Page, content string) error {
	path := strings.TrimPrefix(page.CalculatePath(), "/")
	filePath := path
	if filePath != "" {
		filePath += ".md"
	}
	return e.index.IndexPage(path, filePath, page.ID, page.Title, page.Kind, content)
}
