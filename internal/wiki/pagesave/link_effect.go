package pagesave

import (
	"log/slog"
	"strings"

	"github.com/perber/wiki/internal/core/pagevisibility"
	"github.com/perber/wiki/internal/core/tree"
	"github.com/perber/wiki/internal/links"
)

// LinkIndexSideEffect updates the link index after every page mutation.
type LinkIndexSideEffect struct {
	svc *links.LinkService
	log *slog.Logger
}

// NewLinkIndexSideEffect creates a LinkIndexSideEffect.
func NewLinkIndexSideEffect(svc *links.LinkService, log *slog.Logger) *LinkIndexSideEffect {
	if log == nil {
		log = slog.Default()
	}
	return &LinkIndexSideEffect{svc: svc, log: log}
}

func (e *LinkIndexSideEffect) Apply(event PageSaveEvent) {
	if e.svc == nil {
		return
	}
	switch event.Operation {
	case PageOperationCreate:
		e.updateAndHeal(event.After)

	case PageOperationRestore:
		// Content was restored to a previous version; update outgoing links and heal incoming.
		e.updateAndHeal(event.After)

	case PageOperationUpdate:
		if event.DraftChanged || event.SlugChanged {
			var titles []string
			if event.DraftChanged || event.TitleChanged {
				titles = affectedLinkTitles(event)
			}
			if err := e.svc.ReconcileLinksForAffectedPages(event.AffectedPageIDs, event.AffectedPages, titles); err != nil {
				e.log.Warn("failed to reconcile links for updated pages", "error", err)
			}
		} else {
			if event.After != nil {
				e.updateAndHeal(event.After)
			}
		}
		if !event.DraftChanged && !event.SlugChanged && event.TitleChanged {
			e.reconcileChangedTitles(event)
		}

	case PageOperationMove:
		var titles []string
		if event.DraftChanged {
			titles = affectedLinkTitles(event)
		}
		if err := e.svc.ReconcileLinksForAffectedPages(event.AffectedPageIDs, event.AffectedPages, titles); err != nil {
			e.log.Warn("failed to reconcile links for moved pages", "error", err)
		}

	case PageOperationDelete:
		if err := e.svc.ReconcileLinksForAffectedPages(event.AffectedPageIDs, event.AffectedPages, affectedLinkTitles(event)); err != nil {
			e.log.Warn("failed to reconcile links for deleted pages", "error", err)
		}
	}
}

func (e *LinkIndexSideEffect) reconcileChangedTitles(event PageSaveEvent) {
	for _, title := range affectedLinkTitles(event) {
		if err := e.svc.ReconcileWikiLinksForTitle(title); err != nil {
			e.log.Warn("failed to reconcile wiki links after title visibility change", "title", title, "error", err)
		}
	}
}

func affectedLinkTitles(event PageSaveEvent) []string {
	titles := make([]string, 0, len(event.AffectedTitles)+len(event.AffectedPages)+2)
	titles = append(titles, event.AffectedTitles...)
	titles = append(titles, event.OldTitle)
	if event.After != nil {
		titles = append(titles, event.After.Title)
	}
	for _, page := range event.AffectedPages {
		if page != nil {
			titles = append(titles, page.Title)
		}
	}

	unique := titles[:0]
	seen := make(map[string]struct{}, len(titles))
	for _, title := range titles {
		title = strings.TrimSpace(title)
		key := strings.ToLower(title)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, title)
	}
	return unique
}

func (e *LinkIndexSideEffect) healExact(p *tree.Page) {
	if p == nil || pagevisibility.IsInDraftSubtree(p.PageNode) {
		return
	}
	if err := e.svc.HealLinksForExactPath(p); err != nil {
		e.log.Warn("failed to heal links for page", "pageID", p.ID, "error", err)
	}
	if err := e.svc.HealWikiLinksForPage(p); err != nil {
		e.log.Warn("failed to heal wiki links for page", "pageID", p.ID, "error", err)
	}
}

func (e *LinkIndexSideEffect) updateAndHeal(p *tree.Page) {
	if p == nil {
		return
	}
	if pagevisibility.IsInDraftSubtree(p.PageNode) {
		if err := e.svc.RemoveLinksForDraftPage(p); err != nil {
			e.log.Warn("failed to remove draft page links", "pageID", p.ID, "error", err)
		}
		return
	}
	if err := e.svc.UpdateLinksForPage(p, p.Content); err != nil {
		e.log.Warn("failed to update links for page", "pageID", p.ID, "error", err)
	}
	e.healExact(p)
}
