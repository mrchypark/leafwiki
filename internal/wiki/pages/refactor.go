package pages

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/perber/wiki/internal/core/pagevisibility"
	sharederrors "github.com/perber/wiki/internal/core/shared/errors"
	"github.com/perber/wiki/internal/core/tree"
	"github.com/perber/wiki/internal/links"
	"github.com/perber/wiki/internal/wiki/pagesave"
)

const (
	RefactorKindRename = "rename"
	RefactorKindMove   = "move"
)

// RefactorPreviewInput is the input for PreviewPageRefactorUseCase.
type RefactorPreviewInput struct {
	PageID      string
	Kind        string
	Title       string
	Slug        string
	Content     *string
	NewParentID *string
}

// RefactorPreview is the result of a refactor preview operation.
type RefactorPreview struct {
	Kind          string                 `json:"kind"`
	PageID        string                 `json:"pageId"`
	OldPath       string                 `json:"oldPath"`
	NewPath       string                 `json:"newPath"`
	AffectedPages []RefactorAffectedPage `json:"affectedPages"`
	Counts        RefactorPreviewCounts  `json:"counts"`
	Warnings      []string               `json:"warnings"`
}

// RefactorPreviewCounts holds aggregated counts for the preview.
type RefactorPreviewCounts struct {
	AffectedPages int `json:"affectedPages"`
	MatchedLinks  int `json:"matchedLinks"`
}

// RefactorAffectedPage describes a page that has links affected by the refactor.
type RefactorAffectedPage struct {
	FromPageID   string   `json:"fromPageId"`
	FromTitle    string   `json:"fromTitle"`
	FromPath     string   `json:"fromPath"`
	MatchedPaths []string `json:"matchedPaths"`
	Warnings     []string `json:"warnings"`
}

// RefactorApplyInput extends the preview with apply options.
type RefactorApplyInput struct {
	UserID     string
	Version    string
	Tags       []string
	Properties map[string]string
	Draft      *bool
	RefactorPreviewInput
	RewriteLinks bool
}

// PreviewPageRefactorUseCase computes what would change if a refactor is applied.
type PreviewPageRefactorUseCase struct {
	tree  *tree.TreeService
	slug  *tree.SlugService
	links *links.LinkService
	log   *slog.Logger
}

// NewPreviewPageRefactorUseCase constructs a PreviewPageRefactorUseCase.
func NewPreviewPageRefactorUseCase(
	t *tree.TreeService,
	s *tree.SlugService,
	l *links.LinkService,
	log *slog.Logger,
) *PreviewPageRefactorUseCase {
	return &PreviewPageRefactorUseCase{tree: t, slug: s, links: l, log: log}
}

// Execute computes the refactor preview without making changes.
func (uc *PreviewPageRefactorUseCase) Execute(_ context.Context, in RefactorPreviewInput) (*RefactorPreview, error) {
	page, err := uc.tree.GetPage(in.PageID)
	if err != nil {
		return nil, err
	}

	oldPath := page.CalculatePath()
	newPath, err := uc.computeTargetPath(page, in)
	if err != nil {
		return nil, err
	}

	excludeIDs := subtreeIDSet(page.PageNode)
	// For renames that change the title, also surface pages that reference the
	// old title via [[OldTitle]] wiki-link sentinels.
	sentinelTitle := ""
	if in.Kind == RefactorKindRename && in.Title != page.Title {
		sentinelTitle = page.Title
	}
	affectedPages, matchedLinks, err := uc.getAffectedPages(oldPath, page.Title, page.ID, excludeIDs, sentinelTitle)
	if err != nil {
		return nil, err
	}

	return &RefactorPreview{
		Kind:          in.Kind,
		PageID:        in.PageID,
		OldPath:       oldPath,
		NewPath:       newPath,
		AffectedPages: affectedPages,
		Counts: RefactorPreviewCounts{
			AffectedPages: len(affectedPages),
			MatchedLinks:  matchedLinks,
		},
		Warnings: collectPreviewWarnings(affectedPages),
	}, nil
}

func (uc *PreviewPageRefactorUseCase) computeTargetPath(page *tree.Page, in RefactorPreviewInput) (string, error) {
	switch in.Kind {
	case RefactorKindRename:
		ve := sharederrors.NewValidationErrors()
		if in.Title == "" {
			ve.Add("title", "Title must not be empty")
		}
		if err := uc.slug.IsValidSlug(in.Slug); err != nil {
			ve.Add("slug", err.Error())
		}
		if ve.HasErrors() {
			return "", ve
		}
		parentPath := ""
		if page.Parent != nil {
			parentPath = page.Parent.CalculatePath()
		}
		if parentPath == "" {
			return "/" + in.Slug, nil
		}
		return parentPath + "/" + in.Slug, nil

	case RefactorKindMove:
		parentID := ""
		if in.NewParentID != nil {
			parentID = *in.NewParentID
		}
		parentPath, err := uc.resolveParentPath(parentID)
		if err != nil {
			return "", err
		}
		if parentPath == "" {
			return "/" + page.Slug, nil
		}
		return parentPath + "/" + page.Slug, nil

	default:
		return "", fmt.Errorf("unsupported refactor kind: %s", in.Kind)
	}
}

func (uc *PreviewPageRefactorUseCase) resolveParentPath(parentID string) (string, error) {
	if parentID == "" || parentID == "root" {
		return "", nil
	}
	parent, err := uc.tree.GetPage(parentID)
	if err != nil {
		return "", err
	}
	return parent.CalculatePath(), nil
}

func (uc *PreviewPageRefactorUseCase) getAffectedPages(oldPath string, pageTitle string, targetID string, excludeIDs map[string]struct{}, sentinelTitle string) ([]RefactorAffectedPage, int, error) {
	if uc.links == nil {
		return nil, 0, nil
	}
	matches, err := uc.links.GetRefactorMatchesForPrefix(oldPath)
	if err != nil {
		return nil, 0, err
	}

	grouped := make(map[string]*RefactorAffectedPage)
	totalMatches := 0
	for _, match := range matches {
		if _, excluded := excludeIDs[match.FromPageID]; excluded {
			continue
		}
		fromPath := ""
		if page, err := uc.tree.GetPage(match.FromPageID); err == nil && page != nil {
			fromPath = page.CalculatePath()
		}
		item, ok := grouped[match.FromPageID]
		if !ok {
			item = &RefactorAffectedPage{
				FromPageID: match.FromPageID,
				FromTitle:  match.FromTitle,
				FromPath:   fromPath,
			}
			grouped[match.FromPageID] = item
		}
		if !containsString(item.MatchedPaths, match.ToPath) {
			item.MatchedPaths = append(item.MatchedPaths, match.ToPath)
		}
		totalMatches++
	}

	// For title-changing renames, also include pages that reference the old
	// title via [[OldTitle]] wiki-link sentinels (not matched by path prefix).
	if sentinelTitle != "" {
		// If another published page shares the old title, [[OldTitle]] belongs to
		// that public candidate rather than exclusively to the refactor target.
		// Draft duplicates do not participate in public title resolution.
		if !hasOtherPublishedTitleMatch(uc.tree, sentinelTitle, targetID) {
			sentinelIDs, err := uc.links.GetRefactorSourcePageIDsForWikiLinkTitle(sentinelTitle)
			if err != nil {
				return nil, 0, err
			}
			wikiLinkSyntax := "[[" + sentinelTitle + "]]"
			for _, id := range sentinelIDs {
				if _, excluded := excludeIDs[id]; excluded {
					continue
				}
				if _, already := grouped[id]; already {
					// Already found via path-prefix query; wiki-link annotation
					// is handled in the items loop below.
					continue
				}
				fromPath := ""
				fromTitle := ""
				if p, err := uc.tree.GetPage(id); err == nil && p != nil {
					fromPath = p.CalculatePath()
					fromTitle = p.Title
				}
				grouped[id] = &RefactorAffectedPage{
					FromPageID:   id,
					FromTitle:    fromTitle,
					FromPath:     fromPath,
					MatchedPaths: []string{wikiLinkSyntax},
				}
				totalMatches++
			}
		}
	}

	engine := links.NewMarkdownRefactorEngine()
	items := make([]RefactorAffectedPage, 0, len(grouped))
	for _, item := range grouped {
		sourcePage, err := uc.tree.GetPage(item.FromPageID)
		if err != nil {
			return nil, 0, err
		}

		// Replace raw route paths in matchedPaths with their wiki-link syntax
		// when the page content uses [[Title]] or [[path/hint]] instead of
		// a standard markdown link.
		wikiLinks := engine.FindWikiLinksForPath(sourcePage.Content, oldPath, pageTitle)
		for _, wl := range wikiLinks {
			if !containsString(item.MatchedPaths, wl) {
				item.MatchedPaths = append(item.MatchedPaths, wl)
			}
			// Remove the raw route path entry that the wiki-link replaces.
			item.MatchedPaths = removeString(item.MatchedPaths, oldPath)
		}

		rules := []links.RewriteRule{{OldPath: oldPath, NewPath: oldPath}}
		result := engine.Rewrite(sourcePage.Content, sourcePage.CalculatePath(), rules)
		for _, w := range result.Warnings {
			if !containsString(item.Warnings, w.Message) {
				item.Warnings = append(item.Warnings, w.Message)
			}
		}
		sort.Strings(item.MatchedPaths)
		sort.Strings(item.Warnings)
		item.MatchedPaths = ensureStrings(item.MatchedPaths)
		item.Warnings = ensureStrings(item.Warnings)
		items = append(items, *item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].FromTitle == items[j].FromTitle {
			return items[i].FromPath < items[j].FromPath
		}
		return items[i].FromTitle < items[j].FromTitle
	})
	return items, totalMatches, nil
}

// ─── ApplyPageRefactorUseCase ────────────────────────────────────────────────

// ApplyPageRefactorUseCase applies a rename or move with optional link rewriting.
type ApplyPageRefactorUseCase struct {
	tree         *tree.TreeService
	slug         *tree.SlugService
	links        *links.LinkService
	orchestrator *pagesave.PageSaveOrchestrator
	log          *slog.Logger
	preview      *PreviewPageRefactorUseCase
}

// NewApplyPageRefactorUseCase constructs an ApplyPageRefactorUseCase.
func NewApplyPageRefactorUseCase(
	t *tree.TreeService,
	s *tree.SlugService,
	l *links.LinkService,
	o *pagesave.PageSaveOrchestrator,
	log *slog.Logger,
) *ApplyPageRefactorUseCase {
	return &ApplyPageRefactorUseCase{
		tree:         t,
		slug:         s,
		links:        l,
		orchestrator: o,
		log:          log,
		preview:      NewPreviewPageRefactorUseCase(t, s, l, log),
	}
}

// Execute applies the refactor operation to the page tree.
func (uc *ApplyPageRefactorUseCase) Execute(ctx context.Context, in RefactorApplyInput) (*tree.Page, error) {
	plan, err := uc.buildApplyPlan(in)
	if err != nil {
		return nil, err
	}

	snapshots := captureSnapshots(plan.page)

	var rewriteRules []links.RewriteRule
	if in.RewriteLinks {
		rule := links.RewriteRule{OldPath: plan.oldPath, NewPath: plan.newPath}
		if in.Kind == RefactorKindRename && in.Title != plan.page.Title {
			rule.OldTitle = plan.page.Title
			rule.NewTitle = in.Title
		}
		rewriteRules = []links.RewriteRule{rule}
	}

	var updated *tree.Page
	switch in.Kind {
	case RefactorKindRename:
		updateUC := NewUpdatePageUseCase(uc.tree, uc.slug, uc.orchestrator, uc.log)
		result, err := updateUC.Execute(ctx, UpdatePageInput{
			UserID:     in.UserID,
			ID:         in.PageID,
			Version:    in.Version,
			Title:      in.Title,
			Slug:       in.Slug,
			Content:    in.Content,
			Tags:       in.Tags,
			Properties: in.Properties,
			Draft:      in.Draft,
			PathPreconditions: &tree.PathPreconditions{
				ExpectedSourcePath: plan.oldPath,
			},
		})
		if err != nil {
			return nil, err
		}
		updated = result.Page

	case RefactorKindMove:
		parentID := ""
		if in.NewParentID != nil {
			parentID = *in.NewParentID
		}
		moveUC := NewMovePageUseCase(uc.tree, uc.orchestrator, uc.log)
		if err := moveUC.Execute(ctx, MovePageInput{
			UserID: in.UserID, ID: in.PageID, Version: in.Version, ParentID: parentID,
			PathPreconditions: &tree.PathPreconditions{
				ExpectedSourcePath:            plan.oldPath,
				ExpectedDestinationParentPath: plan.destinationParentPath,
			},
		}); err != nil {
			return nil, err
		}
		updated, err = uc.tree.GetPage(in.PageID)
		if err != nil {
			return nil, err
		}

	default:
		return nil, fmt.Errorf("unsupported refactor kind: %s", in.Kind)
	}

	rewriteIncoming := in.RewriteLinks && !pagevisibility.IsInDraftSubtree(updated.PageNode)
	if rewriteIncoming {
		if err := uc.rewriteAffectedPages(in.UserID, plan.affectedPageIDs, rewriteRules); err != nil {
			return nil, err
		}
	}
	if err := uc.rewritePathChangedSubtree(in.UserID, snapshots, plan.oldPath, plan.newPath); err != nil {
		return nil, err
	}
	return uc.tree.GetPage(updated.ID)
}

type applyRefactorPlan struct {
	page                  *tree.Page
	oldPath               string
	newPath               string
	destinationParentPath string
	affectedPageIDs       []string
}

func (uc *ApplyPageRefactorUseCase) buildApplyPlan(in RefactorApplyInput) (*applyRefactorPlan, error) {
	page, err := uc.tree.GetPage(in.PageID)
	if err != nil {
		return nil, err
	}

	oldPath := page.CalculatePath()
	newPath := ""
	destinationParentPath := ""
	if in.Kind == RefactorKindMove {
		parentID := ""
		if in.NewParentID != nil {
			parentID = *in.NewParentID
		}
		destinationParentPath, err = uc.preview.resolveParentPath(parentID)
		if err != nil {
			return nil, err
		}
		newPath = "/" + page.Slug
		if destinationParentPath != "" {
			newPath = destinationParentPath + newPath
		}
	} else {
		newPath, err = uc.preview.computeTargetPath(page, in.RefactorPreviewInput)
		if err != nil {
			return nil, err
		}
	}

	plan := &applyRefactorPlan{
		page:                  page,
		oldPath:               oldPath,
		newPath:               newPath,
		destinationParentPath: destinationParentPath,
	}

	if !in.RewriteLinks || uc.links == nil {
		return plan, nil
	}

	pageIDs, err := uc.links.GetRefactorSourcePageIDsForPrefix(oldPath)
	if err != nil {
		return nil, err
	}

	excludeIDs := subtreeIDSet(page.PageNode)
	plan.affectedPageIDs = make([]string, 0, len(pageIDs))
	for _, pageID := range pageIDs {
		if _, excluded := excludeIDs[pageID]; excluded {
			continue
		}
		plan.affectedPageIDs = append(plan.affectedPageIDs, pageID)
	}

	// For renames that change the title, also find pages that reference the old
	// title via a sentinel (wikilink:OldTitle). These are stored as sentinels and
	// are not found by the prefix-based lookup above.
	if in.Kind == RefactorKindRename && in.Title != plan.page.Title {
		// Another published same-title page owns or shares [[OldTitle]] resolution;
		// including those links would silently rewrite references that do not
		// exclusively identify this target. Draft duplicates are not candidates.
		if !hasOtherPublishedTitleMatch(uc.tree, plan.page.Title, plan.page.ID) {
			sentinelIDs, err := uc.links.GetRefactorSourcePageIDsForWikiLinkTitle(plan.page.Title)
			if err != nil {
				return nil, err
			}
			for _, id := range sentinelIDs {
				if _, excluded := excludeIDs[id]; excluded {
					continue
				}
				if !containsString(plan.affectedPageIDs, id) {
					plan.affectedPageIDs = append(plan.affectedPageIDs, id)
				}
			}
		}
	}

	return plan, nil
}

func hasOtherPublishedTitleMatch(treeService *tree.TreeService, title, targetID string) bool {
	for _, match := range treeService.SnapshotPagesByTitle(title) {
		if match.ID == targetID || pagevisibility.IsInDraftSubtree(match) {
			continue
		}
		return true
	}
	return false
}

type pathChangeSnapshot struct {
	PageID  string
	OldPath string
}

func captureSnapshots(page *tree.Page) []pathChangeSnapshot {
	snapshots := make([]pathChangeSnapshot, 0)
	var collect func(*tree.PageNode)
	collect = func(node *tree.PageNode) {
		snapshots = append(snapshots, pathChangeSnapshot{PageID: node.ID, OldPath: node.CalculatePath()})
		for _, child := range node.Children {
			collect(child)
		}
	}
	collect(page.PageNode)
	return snapshots
}

func (uc *ApplyPageRefactorUseCase) rewriteAffectedPages(userID string, affectedPageIDs []string, rules []links.RewriteRule) error {
	engine := links.NewMarkdownRefactorEngine()
	compiledWikiRewrites := links.CompileWikiLinkRewrites(rules)

	uc.log.Debug("rewriting wiki-links in affected pages", "pages", len(affectedPageIDs), "rules", len(rules))

	var pageIDs []string
	var bulk []tree.BulkContentUpdate

	pagesByID := uc.loadPagesByID(affectedPageIDs, "failed to get page for link rewrite, skipping")

	for _, pageID := range affectedPageIDs {
		page, ok := pagesByID[pageID]
		if !ok {
			continue
		}
		mdResult := engine.Rewrite(page.Content, page.CalculatePath(), rules)
		wikiResult := engine.RewriteWikiLinksPrecompiled(mdResult.Content, compiledWikiRewrites)
		newContent := wikiResult.Content
		if mdResult.Count() == 0 && wikiResult.Count() == 0 || newContent == page.Content {
			continue
		}
		pageIDs = append(pageIDs, page.ID)
		bulk = append(bulk, tree.BulkContentUpdate{ID: page.ID, Content: newContent, ExpectedVersion: page.Version()})
	}

	if len(bulk) == 0 {
		return nil
	}

	errs := uc.tree.BulkUpdateContent(userID, bulk)
	updatedPageIDs := pageIDs[:0]

	for i, pageID := range pageIDs {
		if errs[i] != nil {
			uc.log.Warn("failed to rewrite links in page", "pageID", pageID, "error", errs[i])
			continue
		}
		updatedPageIDs = append(updatedPageIDs, pageID)
	}
	uc.runPostRewriteEffects(userID, updatedPageIDs)
	return nil
}

func (uc *ApplyPageRefactorUseCase) rewritePathChangedSubtree(userID string, snapshots []pathChangeSnapshot, oldPath, newPath string) error {
	engine := links.NewMarkdownRefactorEngine()
	rules := []links.RewriteRule{{OldPath: oldPath, NewPath: newPath}}

	var updatedIDs []string
	var bulk []tree.BulkContentUpdate

	pageIDs := make([]string, 0, len(snapshots))
	for _, snap := range snapshots {
		pageIDs = append(pageIDs, snap.PageID)
	}
	pagesByID := uc.loadPagesByID(pageIDs, "failed to get subtree page for link rewrite, skipping")

	for _, snap := range snapshots {
		current, ok := pagesByID[snap.PageID]
		if !ok {
			continue
		}
		currentPath := current.CalculatePath()
		// First, fix relative links whose base path changed because the page moved.
		relResult := engine.RewriteRelativeLinksForPathChange(current.Content, snap.OldPath, currentPath, rules)
		// Then, fix absolute links within the moved subtree (e.g. /old/sub → /new/sub).
		// RewriteRelativeLinksForPathChange skips absolute links, so they need a
		// second pass. Using the new current path is safe here: relative links were
		// already corrected in the first pass and will not match the old-path rules.
		// Skip pass 2 when the content cannot contain any matching absolute links.
		finalContent := relResult.Content
		if strings.Contains(relResult.Content, oldPath) {
			finalContent = engine.Rewrite(relResult.Content, currentPath, rules).Content
		}
		if finalContent == current.Content {
			continue
		}
		updatedIDs = append(updatedIDs, current.ID)
		bulk = append(bulk, tree.BulkContentUpdate{ID: current.ID, Content: finalContent, ExpectedVersion: current.Version()})
	}

	if len(bulk) == 0 {
		return nil
	}

	errs := uc.tree.BulkUpdateContent(userID, bulk)
	updatedPageIDs := updatedIDs[:0]

	for i, pageID := range updatedIDs {
		if errs[i] != nil {
			uc.log.Warn("failed to rewrite relative links in subtree page", "pageID", pageID, "error", errs[i])
			continue
		}
		updatedPageIDs = append(updatedPageIDs, pageID)
	}
	uc.runPostRewriteEffects(userID, updatedPageIDs)
	return nil
}

func (uc *ApplyPageRefactorUseCase) runPostRewriteEffects(userID string, pageIDs []string) {
	if uc.orchestrator == nil || len(pageIDs) == 0 {
		return
	}
	pagesByID := uc.loadPagesByID(pageIDs, "failed to get rewritten page for post-save effects, skipping")
	for _, pageID := range pageIDs {
		page, ok := pagesByID[pageID]
		if !ok {
			continue
		}
		uc.orchestrator.Run(pagesave.PageSaveEvent{
			Operation:      pagesave.PageOperationUpdate,
			UserID:         userID,
			After:          page,
			ContentChanged: true,
		})
	}
}

// ─── Helpers ────────────────────────────────────────────────────────────────

func subtreeIDSet(node *tree.PageNode) map[string]struct{} {
	ids := make(map[string]struct{})
	for _, id := range collectSubtreeIDs(node) {
		ids[id] = struct{}{}
	}
	return ids
}

func containsString(values []string, target string) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}

func removeString(values []string, target string) []string {
	out := values[:0]
	for _, v := range values {
		if v != target {
			out = append(out, v)
		}
	}
	return out
}

func ensureStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func (uc *ApplyPageRefactorUseCase) loadPagesByID(ids []string, warningMessage string) map[string]*tree.Page {
	if len(ids) == 0 {
		return map[string]*tree.Page{}
	}

	pages, errs := uc.tree.GetPages(ids)
	loaded := make(map[string]*tree.Page, len(ids))
	for i, id := range ids {
		if errs[i] != nil {
			uc.log.Warn(warningMessage, "pageID", id, "error", errs[i])
			continue
		}
		if pages[i] == nil {
			continue
		}
		loaded[id] = pages[i]
	}

	return loaded
}

func collectPreviewWarnings(pages []RefactorAffectedPage) []string {
	var warnings []string
	for _, p := range pages {
		for _, w := range p.Warnings {
			if !containsString(warnings, w) {
				warnings = append(warnings, w)
			}
		}
	}
	sort.Strings(warnings)
	return ensureStrings(warnings)
}
