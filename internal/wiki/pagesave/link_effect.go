package pagesave

import (
	"log/slog"
	"strings"

	"github.com/perber/wiki/internal/core/pagevisibility"
	"github.com/perber/wiki/internal/core/tree"
	httpmetrics "github.com/perber/wiki/internal/http/metrics"
	"github.com/perber/wiki/internal/links"
)

// LinkIndexSideEffect updates the link index after every page mutation.
type LinkIndexSideEffect struct {
	svc     *links.LinkService
	log     *slog.Logger
	metrics *httpmetrics.HTTPMetrics
}

// NewLinkIndexSideEffect creates a LinkIndexSideEffect.
func NewLinkIndexSideEffect(svc *links.LinkService, log *slog.Logger, metrics ...*httpmetrics.HTTPMetrics) *LinkIndexSideEffect {
	if log == nil {
		log = slog.Default()
	}
	var m *httpmetrics.HTTPMetrics
	if len(metrics) > 0 {
		m = metrics[0]
	}
	return &LinkIndexSideEffect{svc: svc, log: log, metrics: m}
}

func (e *LinkIndexSideEffect) Name() string {
	return "links"
}

func (e *LinkIndexSideEffect) Apply(event PageSaveEvent) {
	if e.svc == nil {
		return
	}
	switch event.Operation {
	case PageOperationCreate:
		e.updateAndHeal(event.After, event.Operation)

	case PageOperationRestore:
		// Content was restored to a previous version; update outgoing links and heal incoming.
		e.updateAndHeal(event.After, event.Operation)

	case PageOperationUpdate:
		if event.DraftChanged || event.SlugChanged {
			var titles []string
			if event.DraftChanged || event.TitleChanged {
				titles = affectedLinkTitles(event)
			}
			if err := e.svc.ReconcileLinksForAffectedPages(event.AffectedPageIDs, event.AffectedPages, titles); err != nil {
				e.log.Warn("failed to reconcile links for updated pages", "error", err)
				e.recordFailure(event.Operation)
			}
		} else {
			if event.After != nil {
				e.updateAndHeal(event.After, event.Operation)
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
			e.recordFailure(event.Operation)
		}

	case PageOperationDelete:
		if err := e.svc.ReconcileLinksForAffectedPages(event.AffectedPageIDs, event.AffectedPages, affectedLinkTitles(event)); err != nil {
			e.log.Warn("failed to reconcile links for deleted pages", "error", err)
			e.recordFailure(event.Operation)
		}
	}
}

func (e *LinkIndexSideEffect) reconcileChangedTitles(event PageSaveEvent) {
	for _, title := range affectedLinkTitles(event) {
		if err := e.svc.ReconcileWikiLinksForTitle(title); err != nil {
			e.log.Warn("failed to reconcile wiki links after title visibility change", "title", title, "error", err)
			e.recordFailure(event.Operation)
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

func (e *LinkIndexSideEffect) healExact(p *tree.Page, operation PageOperationType) {
	if p == nil || pagevisibility.IsInDraftSubtree(p.PageNode) {
		return
	}
	if err := e.svc.HealLinksForExactPath(p); err != nil {
		e.log.Warn("failed to heal links for page", "pageID", p.ID, "error", err)
		e.recordFailure(operation)
	}
	if err := e.svc.HealWikiLinksForPage(p); err != nil {
		e.log.Warn("failed to heal wiki links for page", "pageID", p.ID, "error", err)
		e.recordFailure(operation)
	}
}

func (e *LinkIndexSideEffect) updateAndHeal(p *tree.Page, operation PageOperationType) {
	if p == nil {
		return
	}
	if pagevisibility.IsInDraftSubtree(p.PageNode) {
		if err := e.svc.RemoveLinksForDraftPage(p); err != nil {
			e.log.Warn("failed to remove draft page links", "pageID", p.ID, "error", err)
			e.recordFailure(operation)
		}
		return
	}
	if err := e.svc.UpdateLinksForPage(p, p.Content); err != nil {
		e.log.Warn("failed to update links for page", "pageID", p.ID, "error", err)
		e.recordFailure(operation)
	}
	e.healExact(p, operation)
}

func (e *LinkIndexSideEffect) recordFailure(operation PageOperationType) {
	e.metrics.IncPageSaveSideEffectFailure(string(operation), e.Name())
}
