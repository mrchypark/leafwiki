package pagesave

import "github.com/perber/wiki/internal/core/tree"

// PageOperationType identifies which page mutation triggered a save event.
type PageOperationType string

const (
	PageOperationCreate  PageOperationType = "create"
	PageOperationUpdate  PageOperationType = "update"
	PageOperationMove    PageOperationType = "move"
	PageOperationDelete  PageOperationType = "delete"
	PageOperationRestore PageOperationType = "restore"
)

// PageSaveEvent carries all context a side effect needs to react to a page mutation.
type PageSaveEvent struct {
	Operation PageOperationType
	UserID    string

	// Before is the page state prior to the operation; nil for Create.
	Before *tree.Page
	// After is the page state after the operation; nil for Delete.
	After *tree.Page

	ContentChanged bool
	SlugChanged    bool
	TitleChanged   bool
	DraftChanged   bool
	// ReconciliationOnly marks an event that repairs best-effort projections
	// after a failed mutation left a safe, partially committed current state.
	ReconciliationOnly bool

	// OldPath is the path of the page before the mutation (CalculatePath on a live node
	// returns the new path after UpdateNode/MoveNode mutates the tree in place).
	OldPath string
	// OldTitle is the title before an update/rename; empty for other operations.
	OldTitle string

	// AffectedPages contains the successfully loaded detached snapshots for an
	// operation's affected set (e.g. a moved/deleted subtree).
	AffectedPages []*tree.Page
	// AffectedPageIDs is the authoritative affected set when loading one of the
	// detached page snapshots fails after the tree mutation has committed.
	AffectedPageIDs []string
	// AffectedTitles contains the deduplicated, non-empty titles captured from
	// the authoritative subtree before a mutation changes or removes it.
	AffectedTitles []string

	// Summary is passed to content revisions (e.g. "page created", "page copied").
	Summary string
}
