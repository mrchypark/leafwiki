package tags

import (
	"context"
	"sort"
	"strings"

	"github.com/perber/wiki/internal/core/auth"
	"github.com/perber/wiki/internal/core/pagevisibility"
	"github.com/perber/wiki/internal/core/tree"
	"github.com/perber/wiki/internal/http/dto"
	coretags "github.com/perber/wiki/internal/tags"
)

// ─── GetTagsUseCase ──────────────────────────────────────────────────────────

type GetTagsInput struct {
	Filter   string
	Selected []string
	Limit    int
}

type GetTagsOutput struct {
	Tags []coretags.TagCount
}

type GetTagsUseCase struct {
	svc  *coretags.TagsService
	tree *tree.TreeService
}

func NewGetTagsUseCase(svc *coretags.TagsService, treeService *tree.TreeService) *GetTagsUseCase {
	return &GetTagsUseCase{svc: svc, tree: treeService}
}

func (uc *GetTagsUseCase) Execute(_ context.Context, in GetTagsInput) (*GetTagsOutput, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	filter := strings.ToLower(strings.TrimSpace(in.Filter))
	selected := normalizeTags(in.Selected)

	pageIDs := publicPageIDs(uc.tree)
	tagsByPage, err := uc.svc.GetTagsForPages(pageIDs)
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int)
	selectedSet := make(map[string]struct{}, len(selected))
	for _, tag := range selected {
		selectedSet[tag] = struct{}{}
	}
	for _, pageID := range pageIDs {
		pageTags := tagsByPage[pageID]
		if !containsAllTags(pageTags, selectedSet) {
			continue
		}
		for _, tag := range pageTags {
			if _, isSelected := selectedSet[tag]; isSelected || !strings.HasPrefix(tag, filter) {
				continue
			}
			counts[tag]++
		}
	}
	tags := make([]coretags.TagCount, 0, len(counts))
	for tag, count := range counts {
		tags = append(tags, coretags.TagCount{Tag: tag, Count: count})
	}
	sort.Slice(tags, func(i, j int) bool {
		if tags[i].Count == tags[j].Count {
			return tags[i].Tag < tags[j].Tag
		}
		return tags[i].Count > tags[j].Count
	})
	if len(tags) > limit {
		tags = tags[:limit]
	}
	return &GetTagsOutput{Tags: tags}, nil
}

// ─── GetPagesByTagsUseCase ───────────────────────────────────────────────────

type GetPagesByTagsInput struct {
	Tags []string
}

type GetPagesByTagsOutput struct {
	Pages []*dto.TaggedPage
}

type GetPagesByTagsUseCase struct {
	svc          *coretags.TagsService
	treeService  *tree.TreeService
	userResolver *auth.UserResolver
}

func NewGetPagesByTagsUseCase(svc *coretags.TagsService, treeService *tree.TreeService, userResolver *auth.UserResolver) *GetPagesByTagsUseCase {
	return &GetPagesByTagsUseCase{svc: svc, treeService: treeService, userResolver: userResolver}
}

func (uc *GetPagesByTagsUseCase) Execute(_ context.Context, in GetPagesByTagsInput) (*GetPagesByTagsOutput, error) {
	normalized := normalizeTags(in.Tags)
	if len(normalized) == 0 {
		return &GetPagesByTagsOutput{Pages: []*dto.TaggedPage{}}, nil
	}

	pageIDs, err := uc.svc.GetPageIDsByTags(normalized)
	if err != nil {
		return nil, err
	}
	if len(pageIDs) == 0 {
		return &GetPagesByTagsOutput{Pages: []*dto.TaggedPage{}}, nil
	}

	tagsPerPage, err := uc.svc.GetTagsForPages(pageIDs)
	if err != nil {
		return nil, err
	}

	excerptsPerPage, err := uc.svc.GetExcerptsForPages(pageIDs)
	if err != nil {
		return nil, err
	}

	pages := make([]*dto.TaggedPage, 0, len(pageIDs))
	for _, id := range pageIDs {
		node, err := uc.treeService.FindPageByID(id)
		if err != nil || node == nil || pagevisibility.IsInDraftSubtree(node) {
			continue
		}
		pages = append(pages, dto.ToTaggedPage(node, tagsPerPage[id], excerptsPerPage[id], uc.userResolver))
	}

	return &GetPagesByTagsOutput{Pages: pages}, nil
}

func publicPageIDs(treeService *tree.TreeService) []string {
	if treeService == nil {
		return nil
	}
	allIDs := make([]string, 0)
	_ = treeService.WalkNodes(func(id string) error {
		allIDs = append(allIDs, id)
		return nil
	})
	ids := make([]string, 0, len(allIDs))
	for _, id := range allIDs {
		node, err := treeService.FindPageByID(id)
		if err == nil && node != nil && !pagevisibility.IsInDraftSubtree(node) {
			ids = append(ids, id)
		}
	}
	return ids
}

func containsAllTags(tags []string, selected map[string]struct{}) bool {
	if len(selected) == 0 {
		return true
	}
	found := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		found[tag] = struct{}{}
	}
	for tag := range selected {
		if _, ok := found[tag]; !ok {
			return false
		}
	}
	return true
}

func normalizeTags(tags []string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(tags))
	for _, t := range tags {
		n := strings.ToLower(strings.TrimSpace(t))
		if n == "" {
			continue
		}
		if _, exists := seen[n]; exists {
			continue
		}
		seen[n] = struct{}{}
		result = append(result, n)
	}
	return result
}
