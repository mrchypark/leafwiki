package pages

import (
	"context"
	"log/slog"

	"github.com/perber/wiki/internal/core/pagevisibility"
	sharederrors "github.com/perber/wiki/internal/core/shared/errors"
	"github.com/perber/wiki/internal/core/tree"
	"github.com/perber/wiki/internal/wiki/pagesave"
)

// UpdatePageInput is the input for UpdatePageUseCase.
type UpdatePageInput struct {
	UserID              string
	ID                  string
	Version             string
	Title               string
	Slug                string
	Content             *string
	Tags                []string
	Properties          map[string]string
	Draft               *bool
	PreserveFrontmatter bool
	PathPreconditions   *tree.PathPreconditions
}

// UpdatePageOutput is the output of UpdatePageUseCase.
type UpdatePageOutput struct {
	Page *tree.Page
}

// UpdatePageUseCase updates an existing page's content and/or structure.
type UpdatePageUseCase struct {
	tree         *tree.TreeService
	slug         *tree.SlugService
	orchestrator *pagesave.PageSaveOrchestrator
	log          *slog.Logger
}

// NewUpdatePageUseCase constructs an UpdatePageUseCase.
func NewUpdatePageUseCase(
	t *tree.TreeService,
	s *tree.SlugService,
	o *pagesave.PageSaveOrchestrator,
	log *slog.Logger,
) *UpdatePageUseCase {
	if log == nil {
		log = slog.Default()
	}
	return &UpdatePageUseCase{tree: t, slug: s, orchestrator: o, log: log}
}

// Execute validates, updates the node, and fires post-save side effects.
func (uc *UpdatePageUseCase) Execute(_ context.Context, in UpdatePageInput) (*UpdatePageOutput, error) {
	ve := sharederrors.NewValidationErrors()
	if in.Title == "" {
		ve.Add("title", "Title must not be empty")
	}
	if err := uc.slug.IsValidSlug(in.Slug); err != nil {
		ve.Add("slug", err.Error())
	}
	if ve.HasErrors() {
		return nil, ve
	}

	in.Version = sanitizeClientVersion(in.Version)

	before, err := uc.tree.GetPage(in.ID)
	if err != nil {
		return nil, err
	}

	slugChanged := in.Slug != before.Slug
	oldPath := before.CalculatePath()
	// Snapshot mutable fields before UpdateNode mutates the live tree node.
	oldTitle := before.Title
	oldContent := before.Content
	oldDraft := before.Draft
	wasPublic := !pagevisibility.IsInDraftSubtree(before.PageNode)

	var subtreeIDs, affectedTitles []string
	draftWillChange := in.Draft != nil && *in.Draft != oldDraft
	if slugChanged || draftWillChange {
		subtreeIDs, affectedTitles = collectSubtreeIDsAndTitles(before.PageNode)
		if len(subtreeIDs) == 0 {
			subtreeIDs = []string{in.ID}
		}
	}

	if err = uc.tree.UpdateNodeWithDraftWithPreconditions(in.UserID, in.ID, in.Title, in.Slug, in.Content, in.Version, in.Tags, in.Properties, in.PreserveFrontmatter, in.Draft, in.PathPreconditions); err != nil {
		uc.reconcileFailedDraftTransition(in.UserID, in.ID, wasPublic, oldPath, oldTitle, subtreeIDs, affectedTitles)
		return nil, err
	}

	after, err := uc.tree.GetPage(in.ID)
	if err != nil {
		return nil, err
	}

	contentChanged := oldContent != after.Content
	titleChanged := oldTitle != after.Title
	draftChanged := oldDraft != after.Draft

	event := pagesave.PageSaveEvent{
		Operation:      pagesave.PageOperationUpdate,
		UserID:         in.UserID,
		After:          after,
		OldPath:        oldPath,
		OldTitle:       oldTitle,
		ContentChanged: contentChanged,
		SlugChanged:    slugChanged,
		TitleChanged:   titleChanged,
		DraftChanged:   draftChanged,
	}

	if slugChanged || draftChanged {
		event.AffectedPageIDs = append(event.AffectedPageIDs, subtreeIDs...)
		event.AffectedTitles = append(event.AffectedTitles, affectedTitles...)
		// After is the authoritative updated snapshot and must always be included,
		// even if the bulk subtree read partially fails.
		event.AffectedPages = append(event.AffectedPages, after)
		pages, errs := uc.tree.GetPages(subtreeIDs)
		for i, p := range pages {
			if errs[i] != nil {
				uc.log.Warn("failed to get page for affected list", "pageID", subtreeIDs[i], "error", errs[i])
				continue
			}
			if p == nil || p.ID == after.ID {
				continue
			}
			event.AffectedPages = append(event.AffectedPages, p)
		}
	}

	uc.orchestrator.Run(event)

	return &UpdatePageOutput{Page: after}, nil
}

func (uc *UpdatePageUseCase) reconcileFailedDraftTransition(userID, pageID string, wasPublic bool, oldPath, oldTitle string, pageIDs, titles []string) {
	if !wasPublic || len(pageIDs) == 0 || uc.orchestrator == nil {
		return
	}
	node, err := uc.tree.SnapshotPageNode(pageID)
	if err != nil {
		uc.log.Warn("failed to inspect page after update error", "pageID", pageID, "error", err)
		return
	}
	if !pagevisibility.IsInDraftSubtree(node) {
		return
	}

	pages, errs := uc.tree.GetPages(pageIDs)
	affected := make([]*tree.Page, 0, len(pages))
	var after *tree.Page
	for i, page := range pages {
		if errs[i] != nil {
			uc.log.Warn("failed to get page for update error reconciliation", "pageID", pageIDs[i], "error", errs[i])
			continue
		}
		if page == nil {
			continue
		}
		affected = append(affected, page)
		if page.ID == pageID {
			after = page
		}
	}

	uc.orchestrator.Run(pagesave.PageSaveEvent{
		Operation:          pagesave.PageOperationUpdate,
		UserID:             userID,
		After:              after,
		DraftChanged:       true,
		ReconciliationOnly: true,
		OldPath:            oldPath,
		OldTitle:           oldTitle,
		AffectedPages:      affected,
		AffectedPageIDs:    append([]string(nil), pageIDs...),
		AffectedTitles:     append([]string(nil), titles...),
	})
}
