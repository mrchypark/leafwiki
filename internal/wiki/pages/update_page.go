package pages

import (
	"context"
	"log/slog"
	"time"

	sharederrors "github.com/perber/wiki/internal/core/shared/errors"
	"github.com/perber/wiki/internal/core/tree"
	httpmetrics "github.com/perber/wiki/internal/http/metrics"
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
	Kind                *tree.NodeKind
	Tags                []string
	Properties          map[string]string
	PreserveFrontmatter bool
	Draft               *bool
	// DraftAllowed is set only for authenticated editor/admin requests.
	DraftAllowed bool
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
	metrics      *httpmetrics.HTTPMetrics
}

// NewUpdatePageUseCase constructs an UpdatePageUseCase.
func NewUpdatePageUseCase(
	t *tree.TreeService,
	s *tree.SlugService,
	o *pagesave.PageSaveOrchestrator,
	log *slog.Logger,
	metrics *httpmetrics.HTTPMetrics,
) *UpdatePageUseCase {
	return &UpdatePageUseCase{tree: t, slug: s, orchestrator: o, log: log, metrics: metrics}
}

// Execute validates, updates the node, and fires post-save side effects.
func (uc *UpdatePageUseCase) Execute(_ context.Context, in UpdatePageInput) (out *UpdatePageOutput, err error) {
	started := time.Now()
	defer func() {
		uc.metrics.ObservePageSaveWorkflow(string(pagesave.PageOperationUpdate), err, started)
	}()

	in.Version = sanitizeClientVersion(in.Version)
	if in.Draft != nil {
		return uc.transitionDraft(in)
	}

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

	before, err := uc.tree.GetPage(in.ID)
	if err != nil {
		return nil, err
	}

	slugChanged := in.Slug != before.Slug
	oldPath := before.CalculatePath()
	// Snapshot mutable fields before UpdateNode mutates the live tree node.
	oldTitle := before.Title
	oldContent := before.Content

	var subtreeIDs []string
	if slugChanged {
		subtreeIDs = collectSubtreeIDs(before.PageNode)
		if len(subtreeIDs) == 0 {
			subtreeIDs = []string{in.ID}
		}
	}

	if err = uc.tree.UpdateNode(in.UserID, in.ID, in.Title, in.Slug, in.Content, in.Version, in.Tags, in.Properties, in.PreserveFrontmatter); err != nil {
		return nil, err
	}

	after, err := uc.tree.GetPage(in.ID)
	if err != nil {
		return nil, err
	}

	contentChanged := oldContent != after.Content
	titleChanged := oldTitle != after.Title

	event := pagesave.PageSaveEvent{
		Operation:      pagesave.PageOperationUpdate,
		UserID:         in.UserID,
		After:          after,
		OldPath:        oldPath,
		OldTitle:       oldTitle,
		ContentChanged: contentChanged,
		SlugChanged:    slugChanged,
		TitleChanged:   titleChanged,
	}

	if slugChanged {
		pages, errs := uc.tree.GetPages(subtreeIDs)
		for i, p := range pages {
			if errs[i] != nil {
				uc.log.Warn("failed to get page for affected list", "pageID", subtreeIDs[i], "error", errs[i])
				continue
			}
			event.AffectedPages = append(event.AffectedPages, p)
		}
	}

	uc.orchestrator.Run(event)

	return &UpdatePageOutput{Page: after}, nil
}

func (uc *UpdatePageUseCase) transitionDraft(in UpdatePageInput) (*UpdatePageOutput, error) {
	ve := sharederrors.NewValidationErrors()
	if !in.DraftAllowed {
		ve.Add("draft", "Drafts require authentication")
	}
	if in.Title != "" || in.Slug != "" || in.Content != nil || in.Kind != nil || in.Tags != nil || in.Properties != nil || in.PreserveFrontmatter {
		ve.Add("draft", "Draft state must be changed separately from page content or structure")
	}
	if ve.HasErrors() {
		return nil, ve
	}
	before, err := uc.tree.GetPage(in.ID)
	if err != nil {
		return nil, err
	}
	beforeNode := *before.PageNode
	before.PageNode = &beforeNode
	if err := uc.tree.SetDraft(in.UserID, in.ID, *in.Draft, in.Version); err != nil {
		return nil, err
	}
	page, err := uc.tree.GetPage(in.ID)
	if err != nil {
		return nil, err
	}
	uc.orchestrator.Run(pagesave.PageSaveEvent{
		Operation: pagesave.PageOperationUpdate,
		UserID:    in.UserID,
		Before:    before,
		After:     page,
	})
	return &UpdatePageOutput{Page: page}, nil
}
