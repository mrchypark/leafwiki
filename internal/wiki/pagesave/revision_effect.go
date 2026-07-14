package pagesave

import (
	"log/slog"

	"github.com/perber/wiki/internal/core/pagevisibility"
	"github.com/perber/wiki/internal/core/revision"
	httpmetrics "github.com/perber/wiki/internal/http/metrics"
)

// RevisionSideEffect records revision history entries after page mutations.
type RevisionSideEffect struct {
	svc     *revision.Service
	log     *slog.Logger
	metrics *httpmetrics.HTTPMetrics
}

// NewRevisionSideEffect creates a RevisionSideEffect.
func NewRevisionSideEffect(svc *revision.Service, log *slog.Logger, metrics ...*httpmetrics.HTTPMetrics) *RevisionSideEffect {
	if log == nil {
		log = slog.Default()
	}
	var m *httpmetrics.HTTPMetrics
	if len(metrics) > 0 {
		m = metrics[0]
	}
	return &RevisionSideEffect{svc: svc, log: log, metrics: m}
}

func (e *RevisionSideEffect) Name() string {
	return "revision"
}

func (e *RevisionSideEffect) Apply(event PageSaveEvent) {
	if e.svc == nil || event.ReconciliationOnly {
		return
	}
	switch event.Operation {
	case PageOperationCreate:
		if event.After != nil {
			e.recordContent(event.After.ID, event.UserID, event.Summary, event.Operation)
		}

	case PageOperationUpdate:
		becamePublic := event.Before != nil && pagevisibility.IsInDraftSubtree(event.Before.PageNode) && event.After != nil && !pagevisibility.IsInDraftSubtree(event.After.PageNode)
		if event.DraftChanged || becamePublic {
			// Draft saves never enter public history. When a draft becomes public,
			// capture each newly visible page exactly once as its published baseline.
			summary := event.Summary
			if summary == "" && becamePublic {
				summary = "page published"
			}
			for _, pageID := range revisionAffectedPageIDs(event) {
				e.recordPublishedBaseline(pageID, event.UserID, summary, event.Operation)
			}
			return
		}
		if event.SlugChanged {
			for _, pageID := range revisionAffectedPageIDs(event) {
				if event.ContentChanged && event.After != nil && pageID == event.After.ID {
					// Root page with content change: record content revision instead.
					continue
				}
				e.recordStructure(pageID, event.UserID, event.Operation)
			}
		} else if event.TitleChanged && !event.ContentChanged {
			if event.After != nil {
				e.recordStructure(event.After.ID, event.UserID, event.Operation)
			}
		}
		if event.ContentChanged && event.After != nil {
			e.recordContent(event.After.ID, event.UserID, event.Summary, event.Operation)
		}

	case PageOperationMove:
		for _, pageID := range revisionAffectedPageIDs(event) {
			e.recordStructure(pageID, event.UserID, event.Operation)
		}

	case PageOperationDelete:
		// No revision entry on delete; data cleanup is handled by the use case.

	case PageOperationRestore:
		// RestoreRevision already writes a RevisionTypeRestore entry internally.
	}
}

func revisionAffectedPageIDs(event PageSaveEvent) []string {
	ids := event.AffectedPageIDs
	if len(ids) == 0 {
		ids = make([]string, 0, len(event.AffectedPages)+1)
		for _, page := range event.AffectedPages {
			if page != nil {
				ids = append(ids, page.ID)
			}
		}
		if event.After != nil {
			ids = append(ids, event.After.ID)
		}
	}

	unique := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, pageID := range ids {
		if pageID == "" {
			continue
		}
		if _, ok := seen[pageID]; ok {
			continue
		}
		seen[pageID] = struct{}{}
		unique = append(unique, pageID)
	}
	return unique
}

func (e *RevisionSideEffect) recordContent(pageID, userID, summary string, operation PageOperationType) {
	if _, _, err := e.svc.RecordContentUpdate(pageID, userID, summary); err != nil {
		e.log.Warn("failed to record content revision", "pageID", pageID, "error", err)
		e.metrics.IncPageSaveSideEffectFailure(string(operation), e.Name())
	}
}

func (e *RevisionSideEffect) recordPublishedBaseline(pageID, userID, summary string, operation PageOperationType) {
	if _, err := e.svc.RecordPublishedBaseline(pageID, userID, summary); err != nil {
		e.log.Warn("failed to record published baseline", "pageID", pageID, "error", err)
		e.metrics.IncPageSaveSideEffectFailure(string(operation), e.Name())
	}
}

func (e *RevisionSideEffect) recordStructure(pageID, userID string, operation PageOperationType) {
	if _, _, err := e.svc.RecordStructureChange(pageID, userID, ""); err != nil {
		e.log.Warn("failed to record structure revision", "pageID", pageID, "error", err)
		e.metrics.IncPageSaveSideEffectFailure(string(operation), e.Name())
	}
}
