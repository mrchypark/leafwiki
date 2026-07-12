package properties

import (
	"context"
	"sort"
	"strings"

	"github.com/perber/wiki/internal/core/auth"
	"github.com/perber/wiki/internal/core/pagevisibility"
	sharederrors "github.com/perber/wiki/internal/core/shared/errors"
	"github.com/perber/wiki/internal/core/tree"
	"github.com/perber/wiki/internal/http/dto"
	coreprop "github.com/perber/wiki/internal/properties"
)

// ─── Sentinel errors ─────────────────────────────────────────────────────────

var ErrPropertiesMissingKey = sharederrors.NewLocalizedError(
	ErrCodePropertiesMissingKey,
	"Query parameter 'key' is required",
	"query parameter key is required",
	nil,
)

var ErrPropertiesMissingValue = sharederrors.NewLocalizedError(
	ErrCodePropertiesMissingValue,
	"Query parameter 'value' is required",
	"query parameter value is required",
	nil,
)

// ─── GetPropertyKeysUseCase ──────────────────────────────────────────────────

type GetPropertyKeysInput struct {
	Filter string
	Limit  int
}

type GetPropertyKeysOutput struct {
	Keys []coreprop.PropertyKeyCount
}

type GetPropertyKeysUseCase struct {
	svc  *coreprop.PropertiesService
	tree *tree.TreeService
}

func NewGetPropertyKeysUseCase(svc *coreprop.PropertiesService, treeService *tree.TreeService) *GetPropertyKeysUseCase {
	return &GetPropertyKeysUseCase{svc: svc, tree: treeService}
}

func (uc *GetPropertyKeysUseCase) Execute(_ context.Context, in GetPropertyKeysInput) (*GetPropertyKeysOutput, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	filter := strings.ToLower(strings.TrimSpace(in.Filter))
	pageIDs := publicPropertyPageIDs(uc.tree)
	propertiesByPage, err := uc.svc.GetPropertiesForPages(pageIDs)
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int)
	for _, pageID := range pageIDs {
		for key := range propertiesByPage[pageID] {
			if strings.HasPrefix(strings.ToLower(key), filter) {
				counts[key]++
			}
		}
	}
	keys := make([]coreprop.PropertyKeyCount, 0, len(counts))
	for key, count := range counts {
		keys = append(keys, coreprop.PropertyKeyCount{Key: key, Count: count})
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Count == keys[j].Count {
			return keys[i].Key < keys[j].Key
		}
		return keys[i].Count > keys[j].Count
	})
	if len(keys) > limit {
		keys = keys[:limit]
	}
	return &GetPropertyKeysOutput{Keys: keys}, nil
}

// ─── GetPagesByPropertyUseCase ───────────────────────────────────────────────

type GetPagesByPropertyInput struct {
	Key   string
	Value string
}

type GetPagesByPropertyOutput struct {
	Pages []*dto.PropertyPage
}

type GetPagesByPropertyUseCase struct {
	svc          *coreprop.PropertiesService
	treeService  *tree.TreeService
	userResolver *auth.UserResolver
}

func NewGetPagesByPropertyUseCase(svc *coreprop.PropertiesService, treeService *tree.TreeService, userResolver *auth.UserResolver) *GetPagesByPropertyUseCase {
	return &GetPagesByPropertyUseCase{svc: svc, treeService: treeService, userResolver: userResolver}
}

func (uc *GetPagesByPropertyUseCase) Execute(_ context.Context, in GetPagesByPropertyInput) (*GetPagesByPropertyOutput, error) {
	if strings.TrimSpace(in.Key) == "" {
		return nil, ErrPropertiesMissingKey
	}
	if strings.TrimSpace(in.Value) == "" {
		return nil, ErrPropertiesMissingValue
	}

	pageIDs, err := uc.svc.GetPageIDsByProperty(in.Key, in.Value)
	if err != nil {
		return nil, err
	}
	if len(pageIDs) == 0 {
		return &GetPagesByPropertyOutput{Pages: []*dto.PropertyPage{}}, nil
	}

	propsPerPage, err := uc.svc.GetPropertiesForPages(pageIDs)
	if err != nil {
		return nil, err
	}

	pages := make([]*dto.PropertyPage, 0, len(pageIDs))
	for _, id := range pageIDs {
		node, err := uc.treeService.FindPageByID(id)
		if err != nil || node == nil || pagevisibility.IsInDraftSubtree(node) {
			continue
		}
		pages = append(pages, dto.ToPropertyPage(node, propsPerPage[id], uc.userResolver))
	}

	return &GetPagesByPropertyOutput{Pages: pages}, nil
}

func publicPropertyPageIDs(treeService *tree.TreeService) []string {
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
