package links

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/perber/wiki/internal/core/pagevisibility"
	"github.com/perber/wiki/internal/core/tree"
)

type LinkService struct {
	storageDir  string
	treeService *tree.TreeService
	store       *LinksStore
	reconcileMu sync.Mutex
}

func NewLinkService(storageDir string, treeService *tree.TreeService, store *LinksStore) *LinkService {
	return &LinkService{
		storageDir:  storageDir,
		treeService: treeService,
		store:       store,
	}
}

func (b *LinkService) IndexAllPages() error {
	return b.IndexAllPagesContext(context.Background())
}

func (b *LinkService) IndexAllPagesContext(ctx context.Context) error {
	b.reconcileMu.Lock()
	defer b.reconcileMu.Unlock()
	return b.indexAllPagesContext(ctx)
}

func (b *LinkService) indexAllPagesContext(ctx context.Context) error {
	if !b.treeService.IsLoaded() {
		return nil
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	if err := b.clearLinks(); err != nil {
		return err
	}

	var ids []string
	if err := b.treeService.WalkNodes(func(id string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		ids = append(ids, id)
		return nil
	}); err != nil {
		return err
	}

	pages, errs := b.treeService.GetPages(ids)
	for i, page := range pages {
		if err := ctx.Err(); err != nil {
			return err
		}
		if errs[i] != nil {
			return errs[i]
		}
		if pagevisibility.IsInDraftSubtree(page.PageNode) {
			continue
		}
		targets := collectTargetsFromContent(b.treeService, page.CalculatePath(), page.Content)
		if err := b.store.AddLinks(page.ID, page.Title, targets); err != nil {
			return err
		}
	}

	return nil
}

func (b *LinkService) ClearLinks() error {
	b.reconcileMu.Lock()
	defer b.reconcileMu.Unlock()
	return b.clearLinks()
}

func (b *LinkService) clearLinks() error {
	return b.store.Clear()
}

func (b *LinkService) GetBacklinksForPage(pageID string) (*BacklinkResult, error) {
	pageTitle := ""
	if page, err := b.treeService.GetPage(pageID); err == nil {
		pageTitle = page.Title
	}

	backlinks, err := b.store.GetBacklinksForPage(pageID)
	if err != nil {
		return nil, err
	}
	backlinks, err = b.mergeAmbiguousWikiLinksIntoBacklinks(pageID, pageTitle, backlinks)
	if err != nil {
		return nil, err
	}
	return toBacklinkResult(b.treeService, backlinks), err
}

func (b *LinkService) GetOutgoingLinksForPage(pageID string) (*OutgoingResult, error) {
	outgoingLinks, err := b.store.GetOutgoingLinksForPage(pageID)
	return toOutgoingLinkResult(b.treeService, outgoingLinks), err
}

func (b *LinkService) GetRefactorMatchesForPrefix(oldPrefix string) ([]RefactorLinkMatch, error) {
	return b.store.GetRefactorMatchesForPrefix(oldPrefix)
}

func (b *LinkService) GetRefactorSourcePageIDsForPrefix(oldPrefix string) ([]string, error) {
	return b.store.GetRefactorSourcePageIDsForPrefix(oldPrefix)
}

func (b *LinkService) GetRefactorSourcePageIDsForWikiLinkTitle(title string) ([]string, error) {
	return b.store.GetRefactorSourcePageIDsForWikiLinkTitle(title)
}

func (b *LinkService) UpdateRewrittenLinksAndHealForPages(pages []*tree.Page, rules []RewriteRule) error {
	b.reconcileMu.Lock()
	defer b.reconcileMu.Unlock()
	return b.updateRewrittenLinksAndHealForPages(pages, rules)
}

func (b *LinkService) updateRewrittenLinksAndHealForPages(pages []*tree.Page, rules []RewriteRule) error {
	outgoingByPageID, err := b.store.GetOutgoingLinksForPages(pageIDsForPages(pages))
	if err != nil {
		return err
	}

	updates := make([]PageLinkUpdate, 0, len(pages))
	for _, page := range pages {
		if page == nil {
			continue
		}
		current, same := b.currentPageForLinkWrite(page, page.Content)
		if current == nil || pagevisibility.IsInDraftSubtree(current.PageNode) {
			if err := b.deleteOutgoingLinksForPage(page.ID); err != nil {
				return err
			}
			continue
		}
		if !same {
			continue
		}
		pagePath := normalizeWikiPath(current.CalculatePath())
		targets := rewriteResolvedTargets(pagePath, outgoingByPageID[current.ID], rules, b.treeService)
		updates = append(updates, PageLinkUpdate{
			FromPageID: current.ID,
			FromTitle:  current.Title,
			ToPath:     pagePath,
			Targets:    targets,
		})
	}

	if len(updates) == 0 {
		return nil
	}

	return b.store.ReplaceLinksAndHeal(updates)
}

func (b *LinkService) GetLinkStatusForPage(pageID string, pagePath string) (*LinkStatusResult, error) {
	pagePath = normalizeWikiPath(pagePath)
	page, err := b.treeService.GetPage(pageID)
	if err != nil {
		return nil, err
	}

	// 1) Valid inbound backlinks
	validBacklinks, err := b.store.GetBacklinksForPage(pageID)
	if err != nil {
		return nil, err
	}
	validBacklinks, err = b.mergeAmbiguousWikiLinksIntoBacklinks(pageID, page.Title, validBacklinks)
	if err != nil {
		return nil, err
	}
	validBacklinksResult := toBacklinkResult(b.treeService, validBacklinks)

	// 2) Broken inbound
	brokenIncoming, err := b.store.GetBrokenIncomingForPath(pagePath)
	if err != nil {
		return nil, err
	}
	brokenIncomingResult := toBacklinkResult(b.treeService, brokenIncoming)

	// 3) Outgoings
	outgoings, err := b.store.GetOutgoingLinksForPage(pageID)
	if err != nil {
		return nil, err
	}
	// Split outgoing in broken/non-broken
	okOut := make([]OutgoingResultItem, 0, len(outgoings))
	brokenOut := make([]OutgoingResultItem, 0)
	for _, outgoing := range outgoings {
		item := toOutgoingResultItem(b.treeService, outgoing)
		if outgoing.Broken && b.isAmbiguousWikilinkOutgoing(outgoing) {
			item.Broken = false
			okOut = append(okOut, item)
			continue
		}
		if item.Broken {
			brokenOut = append(brokenOut, item)
		} else {
			okOut = append(okOut, item)
		}
	}

	return &LinkStatusResult{
		Backlinks:       validBacklinksResult.Backlinks,
		BrokenIncoming:  brokenIncomingResult.Backlinks,
		Outgoings:       okOut,
		BrokenOutgoings: brokenOut,
		Counts: LinkStatusCounts{
			Backlinks:       len(validBacklinksResult.Backlinks),
			BrokenIncoming:  len(brokenIncomingResult.Backlinks),
			Outgoings:       len(okOut),
			BrokenOutgoings: len(brokenOut),
		},
	}, nil
}

func (b *LinkService) mergeAmbiguousWikiLinksIntoBacklinks(pageID string, pageTitle string, backlinks []Backlink) ([]Backlink, error) {
	if pageTitle == "" {
		return backlinks, nil
	}

	matches := publishedPagesByTitle(b.treeService, pageTitle)
	if len(matches) <= 1 {
		return backlinks, nil
	}

	isMatchingPage := false
	for _, match := range matches {
		if match != nil && match.ID == pageID {
			isMatchingPage = true
			break
		}
	}
	if !isMatchingPage {
		return backlinks, nil
	}

	ambiguousRefs, err := b.store.GetBrokenIncomingForPath(wikilinkSentinel(pageTitle))
	if err != nil {
		return nil, err
	}
	if len(ambiguousRefs) == 0 {
		return backlinks, nil
	}

	seen := make(map[string]struct{}, len(backlinks))
	merged := make([]Backlink, 0, len(backlinks)+len(ambiguousRefs))
	for _, backlink := range backlinks {
		key := backlink.FromPageID + "\x00" + backlink.ToPageID
		seen[key] = struct{}{}
		merged = append(merged, backlink)
	}

	for _, backlink := range ambiguousRefs {
		backlink.ToPageID = pageID
		backlink.Broken = false
		key := backlink.FromPageID + "\x00" + backlink.ToPageID
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		merged = append(merged, backlink)
	}

	return merged, nil
}

func (b *LinkService) isAmbiguousWikilinkOutgoing(outgoing Outgoing) bool {
	if !outgoing.Broken || !IsWikilinkSentinel(outgoing.ToPath) {
		return false
	}

	return len(publishedPagesByTitle(b.treeService, WikilinkTitleFromSentinel(outgoing.ToPath))) > 1
}

func (b *LinkService) UpdateLinksForPage(page *tree.Page, content string) error {
	b.reconcileMu.Lock()
	defer b.reconcileMu.Unlock()
	return b.updateLinksForPage(page, content)
}

func (b *LinkService) updateLinksForPage(page *tree.Page, content string) error {
	if page == nil {
		return nil
	}
	current, _ := b.currentPageForLinkWrite(page, content)
	if current == nil || pagevisibility.IsInDraftSubtree(current.PageNode) {
		return b.deleteOutgoingLinksForPage(page.ID)
	}
	targets := collectTargetsFromContent(b.treeService, current.CalculatePath(), current.Content)
	return b.store.AddLinks(current.ID, current.Title, targets)
}

func (b *LinkService) UpdateLinksAndHealForPages(pages []*tree.Page) error {
	b.reconcileMu.Lock()
	defer b.reconcileMu.Unlock()
	return b.updateLinksAndHealForPages(pages)
}

func (b *LinkService) updateLinksAndHealForPages(pages []*tree.Page) error {
	updates := make([]PageLinkUpdate, 0, len(pages))
	for _, page := range pages {
		if page == nil {
			continue
		}
		current, _ := b.currentPageForLinkWrite(page, page.Content)
		if current == nil || pagevisibility.IsInDraftSubtree(current.PageNode) {
			if err := b.deleteOutgoingLinksForPage(page.ID); err != nil {
				return err
			}
			continue
		}
		pagePath := normalizeWikiPath(current.CalculatePath())
		targets := collectTargetsFromContent(b.treeService, pagePath, current.Content)
		updates = append(updates, PageLinkUpdate{
			FromPageID: current.ID,
			FromTitle:  current.Title,
			ToPath:     pagePath,
			Targets:    targets,
		})
	}

	if len(updates) == 0 {
		return nil
	}

	return b.store.ReplaceLinksAndHeal(updates)
}

// DeleteOutgoingLinksForPage removes all outgoing link records for a page.
func (b *LinkService) DeleteOutgoingLinksForPage(pageID string) error {
	b.reconcileMu.Lock()
	defer b.reconcileMu.Unlock()
	return b.deleteOutgoingLinksForPage(pageID)
}

func (b *LinkService) deleteOutgoingLinksForPage(pageID string) error {
	return b.store.DeleteOutgoingLinks(pageID)
}

// MarkIncomingLinksBrokenForPage marks all incoming links pointing to pageID as broken.
func (b *LinkService) MarkIncomingLinksBrokenForPage(pageID string) error {
	b.reconcileMu.Lock()
	defer b.reconcileMu.Unlock()
	_, err := b.markIncomingLinksBrokenForPage(pageID)
	return err
}

func (b *LinkService) markIncomingLinksBrokenForPage(pageID string) ([]string, error) {
	return b.store.MarkIncomingLinksBroken(pageID)
}

// RemoveLinksForDraftPage removes both sides of a page's link relationships
// only while the current tree still places it in a draft subtree.
func (b *LinkService) RemoveLinksForDraftPage(page *tree.Page) error {
	if page == nil {
		return nil
	}
	b.reconcileMu.Lock()
	defer b.reconcileMu.Unlock()

	current, _ := b.currentPageForLinkWrite(page, page.Content)
	if current == nil || !pagevisibility.IsInDraftSubtree(current.PageNode) {
		return nil
	}
	if err := b.deleteOutgoingLinksForPage(current.ID); err != nil {
		return err
	}
	_, err := b.markIncomingLinksBrokenForPage(current.ID)
	return err
}

// ReconcileLinksForAffectedPages breaks links to the page identities that
// moved or disappeared, then refreshes surviving pages from the current tree.
// Using IDs avoids invalidating a path that a newer page now owns.
func (b *LinkService) ReconcileLinksForAffectedPages(pageIDs []string, pages []*tree.Page, titles []string) error {
	b.reconcileMu.Lock()
	defer b.reconcileMu.Unlock()

	ids := affectedLinkPageIDs(pageIDs, pages)
	currentPages, readErrs := b.treeService.GetPages(ids)
	publicPages := make([]*tree.Page, 0, len(ids))
	shallowPublicPages := make([]*tree.Page, 0, len(ids))
	invalidateIDs := make([]string, 0, len(ids))
	removeIDs := make([]string, 0, len(ids))
	var resultErr error

	for i, pageID := range ids {
		page := currentPages[i]
		readErr := readErrs[i]
		if readErr != nil {
			node, snapshotErr := b.treeService.SnapshotPageNode(pageID)
			switch {
			case errors.Is(readErr, tree.ErrPageNotFound), errors.Is(snapshotErr, tree.ErrPageNotFound):
				// The page was deleted; remove both sides of its link relationships.
				invalidateIDs = append(invalidateIDs, pageID)
				removeIDs = append(removeIDs, pageID)
			case snapshotErr != nil:
				resultErr = errors.Join(resultErr, fmt.Errorf("read affected page %s: %w", pageID, errors.Join(readErr, snapshotErr)))
			case pagevisibility.IsInDraftSubtree(node):
				// Draft pages are excluded from the public link index even when their
				// content file cannot be read.
				invalidateIDs = append(invalidateIDs, pageID)
				removeIDs = append(removeIDs, pageID)
			default:
				invalidateIDs = append(invalidateIDs, pageID)
				shallowPublicPages = append(shallowPublicPages, &tree.Page{PageNode: node})
				resultErr = errors.Join(resultErr, fmt.Errorf("read affected page %s: %w", pageID, readErr))
			}
			continue
		}

		invalidateIDs = append(invalidateIDs, pageID)
		if pagevisibility.IsInDraftSubtree(page.PageNode) {
			removeIDs = append(removeIDs, pageID)
			continue
		}
		publicPages = append(publicPages, page)
	}

	paths, err := b.store.InvalidateLinksForPages(invalidateIDs, removeIDs)
	if err != nil {
		return errors.Join(resultErr, err)
	}
	for _, page := range shallowPublicPages {
		if err := b.healLinksForExactPath(page); err != nil {
			return errors.Join(resultErr, err)
		}
	}
	if err := b.replaceLinksAndHealForCurrentPages(publicPages); err != nil {
		return errors.Join(resultErr, err)
	}
	for _, path := range paths {
		if err := b.healCurrentPublishedPageForPath(path); err != nil {
			resultErr = errors.Join(resultErr, err)
		}
	}
	seenTitles := make(map[string]struct{}, len(titles)+len(publicPages))
	for _, title := range titles {
		title = strings.TrimSpace(title)
		key := strings.ToLower(title)
		if key == "" {
			continue
		}
		if _, ok := seenTitles[key]; ok {
			continue
		}
		seenTitles[key] = struct{}{}
		if err := b.store.MarkWikiLinksBrokenForTitle(title); err != nil {
			return errors.Join(resultErr, err)
		}
		if err := b.healWikiLinksForTitleIfUnambiguous(title); err != nil {
			return errors.Join(resultErr, err)
		}
	}
	for _, page := range publicPages {
		key := strings.ToLower(strings.TrimSpace(page.Title))
		if _, ok := seenTitles[key]; ok {
			continue
		}
		seenTitles[key] = struct{}{}
		if err := b.healWikiLinksForTitleIfUnambiguous(page.Title); err != nil {
			return errors.Join(resultErr, err)
		}
	}
	return resultErr
}

func affectedLinkPageIDs(pageIDs []string, pages []*tree.Page) []string {
	ids := make([]string, 0, len(pageIDs)+len(pages))
	seen := make(map[string]struct{}, cap(ids))
	for _, pageID := range pageIDs {
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

func (b *LinkService) replaceLinksAndHealForCurrentPages(pages []*tree.Page) error {
	updates := make([]PageLinkUpdate, 0, len(pages))
	for _, page := range pages {
		pagePath := normalizeWikiPath(page.CalculatePath())
		updates = append(updates, PageLinkUpdate{
			FromPageID: page.ID,
			FromTitle:  page.Title,
			ToPath:     pagePath,
			Targets:    collectTargetsFromContent(b.treeService, pagePath, page.Content),
		})
	}
	if len(updates) == 0 {
		return nil
	}
	return b.store.ReplaceLinksAndHeal(updates)
}

func (b *LinkService) healCurrentPublishedPageForPath(path string) error {
	if IsWikilinkSentinel(path) {
		return nil
	}
	normalizedPath := normalizeWikiPath(path)
	lookup, err := b.treeService.LookupPagePath(normalizedPath)
	if err != nil {
		return fmt.Errorf("resolve invalidated link path %s: %w", path, err)
	}
	if !lookup.Exists || len(lookup.Segments) == 0 {
		return nil
	}
	node := lookup.Segments[len(lookup.Segments)-1].VisibilityNode
	if node == nil || normalizeWikiPath(node.CalculatePath()) != normalizedPath || pagevisibility.IsInDraftSubtree(node) {
		return nil
	}
	return b.store.HealLinksForPath(normalizedPath, node.ID)
}

// ReconcileWikiLinksForTitle re-resolves [[Title]] records against the current
// published tree after a visibility change alters the candidate set.
func (b *LinkService) ReconcileWikiLinksForTitle(title string) error {
	b.reconcileMu.Lock()
	defer b.reconcileMu.Unlock()

	title = strings.TrimSpace(title)
	if title == "" {
		return nil
	}
	if err := b.store.MarkWikiLinksBrokenForTitle(title); err != nil {
		return err
	}
	return b.healWikiLinksForTitleIfUnambiguous(title)
}

func (b *LinkService) HealLinksForExactPath(page *tree.Page) error {
	if page == nil {
		return nil
	}
	b.reconcileMu.Lock()
	defer b.reconcileMu.Unlock()
	current, _ := b.currentPageForLinkWrite(page, page.Content)
	if current == nil || pagevisibility.IsInDraftSubtree(current.PageNode) {
		return nil
	}
	return b.healLinksForExactPath(current)
}

func (b *LinkService) healLinksForExactPath(page *tree.Page) error {
	toPath := normalizeWikiPath(page.CalculatePath())
	return b.store.HealLinksForPath(toPath, page.ID)
}

// HealWikiLinksForPage heals broken [[Title]] sentinel records that target
// this page's title, but only when exactly one page with that title exists.
// If the title is shared by multiple pages the link is ambiguous and must
// remain as a broken sentinel.
func (b *LinkService) HealWikiLinksForPage(page *tree.Page) error {
	if page == nil {
		return nil
	}
	b.reconcileMu.Lock()
	defer b.reconcileMu.Unlock()
	current, _ := b.currentPageForLinkWrite(page, page.Content)
	if current == nil || pagevisibility.IsInDraftSubtree(current.PageNode) {
		return nil
	}
	return b.healWikiLinksForTitleIfUnambiguous(current.Title)
}

// HealWikiLinksForTitleIfUnambiguous heals broken [[Title]] sentinels when
// exactly one page with that title now exists. Called after a page is deleted
// so that formerly ambiguous wikilinks become resolved if only one candidate
// remains.
func (b *LinkService) HealWikiLinksForTitleIfUnambiguous(title string) error {
	b.reconcileMu.Lock()
	defer b.reconcileMu.Unlock()
	return b.healWikiLinksForTitleIfUnambiguous(title)
}

func (b *LinkService) healWikiLinksForTitleIfUnambiguous(title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil
	}
	matches := publishedPagesByTitle(b.treeService, title)
	if len(matches) != 1 {
		return nil
	}
	return b.store.HealWikiLinksForTitle(title, matches[0].ID)
}

func (b *LinkService) Close() error {
	if b.store == nil {
		return nil
	}
	return b.store.Close()
}

func (b *LinkService) currentPageForLinkWrite(page *tree.Page, content string) (*tree.Page, bool) {
	if page == nil || page.PageNode == nil || b.treeService == nil {
		return nil, false
	}
	current, err := b.treeService.GetPage(page.ID)
	if err != nil {
		return nil, false
	}
	same := page.Version() == current.Version() &&
		page.Title == current.Title &&
		page.CalculatePath() == current.CalculatePath() &&
		page.Content == current.Content &&
		pagevisibility.IsInDraftSubtree(page.PageNode) == pagevisibility.IsInDraftSubtree(current.PageNode)
	if same {
		current.Content = content
	}
	return current, same
}

func pageIDsForPages(pages []*tree.Page) []string {
	ids := make([]string, 0, len(pages))
	for _, page := range pages {
		if page == nil {
			continue
		}
		ids = append(ids, page.ID)
	}
	return ids
}

func rewriteResolvedTargets(currentPath string, outgoings []Outgoing, rules []RewriteRule, treeService *tree.TreeService) []TargetLink {
	if len(outgoings) == 0 {
		return nil
	}

	paths := make([]string, 0, len(outgoings))
	for _, outgoing := range outgoings {
		if IsWikilinkSentinel(outgoing.ToPath) {
			// Title-based wiki-link sentinels are resolved by title, not path.
			// Skip path rewriting — they are healed separately by HealWikiLinksForPage.
			continue
		}
		targetPath := normalizeWikiPath(outgoing.ToPath)
		if rewritten, ok := applyRewriteRules(targetPath, rules); ok {
			targetPath = rewritten
		}
		paths = append(paths, targetPath)
	}

	return resolveTargetLinks(treeService, currentPath, paths)
}
