package pagesave

import (
	"context"
	"errors"

	"github.com/perber/wiki/internal/core/pagevisibility"
	"github.com/perber/wiki/internal/core/tree"
)

type projectionPageState struct {
	id     string
	page   *tree.Page
	remove bool
	err    error
}

// IndexAllTagsAndPropertiesContext rebuilds both metadata projections from one
// tree snapshot while excluding incremental writers for either projection.
func IndexAllTagsAndPropertiesContext(ctx context.Context, tagsEffect *TagsSideEffect, propertiesEffect *PropertiesSideEffect) error {
	if tagsEffect == nil || tagsEffect.svc == nil || propertiesEffect == nil || propertiesEffect.svc == nil {
		return nil
	}
	tagsEffect.mu.Lock()
	defer tagsEffect.mu.Unlock()
	propertiesEffect.mu.Lock()
	defer propertiesEffect.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return err
	}
	if err := tagsEffect.svc.ClearIndex(); err != nil {
		return err
	}
	if err := propertiesEffect.svc.ClearIndex(); err != nil {
		return err
	}

	var ids []string
	if err := tagsEffect.tree.WalkNodes(func(pageID string) error {
		ids = append(ids, pageID)
		return nil
	}); err != nil {
		return err
	}
	pages, errs := tagsEffect.tree.GetPages(ids)
	for i, page := range pages {
		if err := ctx.Err(); err != nil {
			return err
		}
		if errs[i] != nil {
			tagsEffect.log.Warn("skipping page during metadata projection rebuild", "pageID", ids[i], "error", errs[i])
			continue
		}
		if pagevisibility.IsInDraftSubtree(page.PageNode) {
			continue
		}
		if err := tagsEffect.svc.IndexPageContent(page.ID, page.RawContent); err != nil {
			tagsEffect.log.Warn("failed to rebuild tag index for page", "pageID", page.ID, "error", err)
		}
		if err := propertiesEffect.svc.IndexPageContent(page.ID, page.RawContent); err != nil {
			propertiesEffect.log.Warn("failed to rebuild property index for page", "pageID", page.ID, "error", err)
		}
	}
	return nil
}

func projectionPageIDs(event PageSaveEvent, pathSensitive bool) []string {
	var pages []*tree.Page
	var authoritativeIDs []string
	switch event.Operation {
	case PageOperationDelete:
		pages = event.AffectedPages
		authoritativeIDs = event.AffectedPageIDs
	case PageOperationMove:
		if event.DraftChanged || pathSensitive {
			pages = event.AffectedPages
			authoritativeIDs = event.AffectedPageIDs
		}
	case PageOperationUpdate:
		if event.DraftChanged || pathSensitive && event.SlugChanged {
			pages = event.AffectedPages
			authoritativeIDs = event.AffectedPageIDs
		} else if event.After != nil {
			pages = []*tree.Page{event.After}
		}
	case PageOperationCreate, PageOperationRestore:
		if event.After != nil {
			pages = []*tree.Page{event.After}
		}
	}
	if len(pages) == 0 && event.Before != nil {
		pages = []*tree.Page{event.Before}
	}

	ids := make([]string, 0, len(authoritativeIDs)+len(pages))
	seen := make(map[string]struct{}, len(authoritativeIDs)+len(pages))
	for _, pageID := range authoritativeIDs {
		if pageID == "" {
			continue
		}
		if _, ok := seen[pageID]; ok {
			continue
		}
		seen[pageID] = struct{}{}
		ids = append(ids, pageID)
	}
	for _, page := range pages {
		if page == nil || page.ID == "" {
			continue
		}
		if _, ok := seen[page.ID]; ok {
			continue
		}
		seen[page.ID] = struct{}{}
		ids = append(ids, page.ID)
	}
	return ids
}

// loadProjectionPages resolves current states in one shallow, parallel tree
// read instead of trusting detached save events.
func loadProjectionPages(treeService *tree.TreeService, pageIDs []string) []projectionPageState {
	states := make([]projectionPageState, len(pageIDs))
	for i, pageID := range pageIDs {
		states[i].id = pageID
	}
	if treeService == nil {
		for i := range states {
			states[i].err = tree.ErrTreeNotLoaded
		}
		return states
	}

	pages, errs := treeService.GetPages(pageIDs)
	for i, page := range pages {
		switch {
		case errors.Is(errs[i], tree.ErrPageNotFound):
			states[i].remove = true
		case errs[i] != nil:
			node, snapshotErr := treeService.SnapshotPageNode(pageIDs[i])
			if errors.Is(snapshotErr, tree.ErrPageNotFound) || snapshotErr == nil && pagevisibility.IsInDraftSubtree(node) {
				states[i].remove = true
			} else {
				states[i].err = errs[i]
			}
		case pagevisibility.IsInDraftSubtree(page.PageNode):
			states[i].remove = true
		default:
			states[i].page = page
		}
	}
	return states
}
