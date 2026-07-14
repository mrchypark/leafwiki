package pages

import (
	"context"
	"log/slog"
	"time"

	"github.com/perber/wiki/internal/core/pagevisibility"
	"github.com/perber/wiki/internal/core/tree"
	httpmetrics "github.com/perber/wiki/internal/http/metrics"
	"github.com/perber/wiki/internal/wiki/pagesave"
)

// MovePageInput is the input for MovePageUseCase.
type MovePageInput struct {
	UserID            string
	ID                string
	Version           string
	ParentID          string
	PathPreconditions *tree.PathPreconditions
}

// MovePageUseCase moves a page to a new parent, updating links and recording revisions.
type MovePageUseCase struct {
	tree         *tree.TreeService
	orchestrator *pagesave.PageSaveOrchestrator
	log          *slog.Logger
	metrics      *httpmetrics.HTTPMetrics
}

// NewMovePageUseCase constructs a MovePageUseCase.
func NewMovePageUseCase(
	t *tree.TreeService,
	o *pagesave.PageSaveOrchestrator,
	log *slog.Logger,
	metrics ...*httpmetrics.HTTPMetrics,
) *MovePageUseCase {
	var m *httpmetrics.HTTPMetrics
	if len(metrics) > 0 {
		m = metrics[0]
	}
	return &MovePageUseCase{tree: t, orchestrator: o, log: log, metrics: m}
}

// Execute moves the page and fires post-save side effects for the whole subtree.
func (uc *MovePageUseCase) Execute(_ context.Context, in MovePageInput) (err error) {
	started := time.Now()
	defer func() {
		uc.metrics.ObservePageSaveWorkflow(string(pagesave.PageOperationMove), err, started)
	}()

	if in.ID == "root" || in.ID == "" {
		return newPageRootOperationError("move")
	}

	in.Version = sanitizeClientVersion(in.Version)

	beforePage, err := uc.tree.GetPage(in.ID)
	if err != nil {
		return err
	}
	subtreeIDs, affectedTitles := collectSubtreeIDsAndTitles(beforePage.PageNode)
	if len(subtreeIDs) == 0 {
		subtreeIDs = []string{in.ID}
	}
	oldPath := beforePage.CalculatePath()
	wasDraft := pagevisibility.IsInDraftSubtree(beforePage.PageNode)

	if err := uc.tree.MoveNodeWithPreconditions(in.UserID, in.ID, in.ParentID, in.Version, in.PathPreconditions); err != nil {
		return err
	}
	afterNode, err := uc.tree.SnapshotPageNode(in.ID)
	if err != nil {
		return err
	}

	event := pagesave.PageSaveEvent{
		Operation:       pagesave.PageOperationMove,
		UserID:          in.UserID,
		OldPath:         oldPath,
		DraftChanged:    wasDraft != pagevisibility.IsInDraftSubtree(afterNode),
		AffectedPageIDs: subtreeIDs,
		AffectedTitles:  affectedTitles,
	}

	pages, errs := uc.tree.GetPages(subtreeIDs)
	for i, p := range pages {
		if errs[i] != nil {
			uc.log.Warn("failed to get page after move", "pageID", subtreeIDs[i], "error", errs[i])
			continue
		}
		event.AffectedPages = append(event.AffectedPages, p)
	}

	uc.orchestrator.Run(event)

	return nil
}
