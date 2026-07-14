package links

import (
	"context"
	"errors"

	"github.com/perber/wiki/internal/core/auth"
	"github.com/perber/wiki/internal/core/pagevisibility"
	sharederrors "github.com/perber/wiki/internal/core/shared/errors"
	"github.com/perber/wiki/internal/core/tree"
	corelinks "github.com/perber/wiki/internal/links"
)

var ErrLinkServiceUnavailable = sharederrors.NewLocalizedError(
	ErrCodeLinkUnavailable,
	"Link service is unavailable",
	"link service is unavailable",
	nil,
)

// ─── GetLinkStatusUseCase ────────────────────────────────────────────────────

type GetLinkStatusInput struct {
	PageID       string
	User         *auth.User
	AuthDisabled bool
}

type GetLinkStatusOutput struct {
	Status *corelinks.LinkStatusResult
}

type GetLinkStatusUseCase struct {
	links *corelinks.LinkService
	tree  *tree.TreeService
}

func NewGetLinkStatusUseCase(l *corelinks.LinkService, t *tree.TreeService) *GetLinkStatusUseCase {
	return &GetLinkStatusUseCase{links: l, tree: t}
}

func (uc *GetLinkStatusUseCase) Execute(_ context.Context, in GetLinkStatusInput) (*GetLinkStatusOutput, error) {
	if uc.links == nil {
		return nil, ErrLinkServiceUnavailable
	}
	node, err := uc.tree.SnapshotPageNode(in.PageID)
	if err != nil {
		if errors.Is(err, tree.ErrPageNotFound) {
			return nil, linkPageNotFoundError(err)
		}
		return nil, err
	}
	if pagevisibility.IsInDraftSubtree(node) {
		if !pagevisibility.CanView(node, in.User, in.AuthDisabled) {
			return nil, linkPageNotFoundError(nil)
		}
		return &GetLinkStatusOutput{Status: &corelinks.LinkStatusResult{
			Backlinks:       []corelinks.BacklinkResultItem{},
			BrokenIncoming:  []corelinks.BacklinkResultItem{},
			Outgoings:       []corelinks.OutgoingResultItem{},
			BrokenOutgoings: []corelinks.OutgoingResultItem{},
		}}, nil
	}
	status, err := uc.links.GetLinkStatusForPage(in.PageID, node.CalculatePath())
	if err != nil {
		return nil, err
	}
	filterLinkStatus(status, uc.tree)
	return &GetLinkStatusOutput{Status: status}, nil
}

func linkPageNotFoundError(err error) error {
	return sharederrors.NewLocalizedError(
		ErrCodeLinkPageNotFound,
		"Page not found",
		"page not found",
		err,
	)
}

func filterLinkStatus(status *corelinks.LinkStatusResult, treeService *tree.TreeService) {
	if status == nil {
		return
	}
	published := make(map[string]struct{})
	for _, pageID := range pagevisibility.AllPublishedPageIDs(treeService) {
		published[pageID] = struct{}{}
	}
	status.Backlinks = filterPublicBacklinks(status.Backlinks, published)
	status.BrokenIncoming = filterPublicBacklinks(status.BrokenIncoming, published)
	status.Outgoings = filterPublicOutgoings(status.Outgoings, published)
	status.BrokenOutgoings = filterPublicOutgoings(status.BrokenOutgoings, published)
	status.Counts = corelinks.LinkStatusCounts{
		Backlinks:       len(status.Backlinks),
		BrokenIncoming:  len(status.BrokenIncoming),
		Outgoings:       len(status.Outgoings),
		BrokenOutgoings: len(status.BrokenOutgoings),
	}
}

func filterPublicBacklinks(items []corelinks.BacklinkResultItem, published map[string]struct{}) []corelinks.BacklinkResultItem {
	visible := make([]corelinks.BacklinkResultItem, 0, len(items))
	for _, item := range items {
		if _, ok := published[item.FromPageID]; ok {
			visible = append(visible, item)
		}
	}
	return visible
}

func filterPublicOutgoings(items []corelinks.OutgoingResultItem, published map[string]struct{}) []corelinks.OutgoingResultItem {
	visible := make([]corelinks.OutgoingResultItem, 0, len(items))
	for _, item := range items {
		if item.ToPageID != "" {
			if _, ok := published[item.ToPageID]; !ok {
				continue
			}
		}
		visible = append(visible, item)
	}
	return visible
}

// ─── GetBacklinksUseCase ─────────────────────────────────────────────────────

type GetBacklinksInput struct {
	PageID string
}

type GetBacklinksOutput struct {
	Result *corelinks.BacklinkResult
}

type GetBacklinksUseCase struct {
	links *corelinks.LinkService
}

func NewGetBacklinksUseCase(l *corelinks.LinkService) *GetBacklinksUseCase {
	return &GetBacklinksUseCase{links: l}
}

func (uc *GetBacklinksUseCase) Execute(_ context.Context, in GetBacklinksInput) (*GetBacklinksOutput, error) {
	if uc.links == nil {
		return nil, ErrLinkServiceUnavailable
	}
	result, err := uc.links.GetBacklinksForPage(in.PageID)
	if err != nil {
		return nil, err
	}
	return &GetBacklinksOutput{Result: result}, nil
}

// ─── GetOutgoingLinksUseCase ─────────────────────────────────────────────────

type GetOutgoingLinksInput struct {
	PageID string
}

type GetOutgoingLinksOutput struct {
	Result *corelinks.OutgoingResult
}

type GetOutgoingLinksUseCase struct {
	links *corelinks.LinkService
}

func NewGetOutgoingLinksUseCase(l *corelinks.LinkService) *GetOutgoingLinksUseCase {
	return &GetOutgoingLinksUseCase{links: l}
}

func (uc *GetOutgoingLinksUseCase) Execute(_ context.Context, in GetOutgoingLinksInput) (*GetOutgoingLinksOutput, error) {
	if uc.links == nil {
		return nil, ErrLinkServiceUnavailable
	}
	result, err := uc.links.GetOutgoingLinksForPage(in.PageID)
	if err != nil {
		return nil, err
	}
	return &GetOutgoingLinksOutput{Result: result}, nil
}

// ─── ReindexLinksUseCase ─────────────────────────────────────────────────────

type ReindexLinksUseCase struct {
	links *corelinks.LinkService
}

func NewReindexLinksUseCase(l *corelinks.LinkService) *ReindexLinksUseCase {
	return &ReindexLinksUseCase{links: l}
}

func (uc *ReindexLinksUseCase) Execute(_ context.Context) error {
	if uc.links == nil {
		return nil
	}
	return uc.links.IndexAllPages()
}
