//go:build darwin || linux

package links

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/perber/wiki/internal/core/tree"
)

func TestLinkService_IndexAllPagesContext_SerializesIncrementalLinkUpdate(t *testing.T) {
	svc, ts, _ := setupLinkService(t)
	if _, err := ts.CreateNode("system", nil, "Old", "old", pageNodeKind()); err != nil {
		t.Fatalf("CreateNode old: %v", err)
	}
	newTargetID, err := ts.CreateNode("system", nil, "New", "new", pageNodeKind())
	if err != nil {
		t.Fatalf("CreateNode new: %v", err)
	}
	sourceID, err := ts.CreateNode("system", nil, "Source", "source", pageNodeKind())
	if err != nil {
		t.Fatalf("CreateNode source: %v", err)
	}
	oldContent := "[Old](/old)"
	if err := ts.UpdateNode("system", *sourceID, "Source", "source", &oldContent, tree.VersionUnchecked, nil, nil, false); err != nil {
		t.Fatalf("UpdateNode source: %v", err)
	}
	source, err := ts.GetPage(*sourceID)
	if err != nil {
		t.Fatalf("GetPage source: %v", err)
	}

	// Replace the source file with a FIFO. Reindex clears the store, snapshots
	// the tree, then blocks reading this page before it can write stale links.
	sourcePath := filepath.Join(svc.storageDir, "root", "source.md")
	if err := os.Remove(sourcePath); err != nil {
		t.Fatalf("Remove source: %v", err)
	}
	if err := syscall.Mkfifo(sourcePath, 0o600); err != nil {
		t.Fatalf("Mkfifo source: %v", err)
	}

	reindexDone := make(chan error, 1)
	go func() {
		reindexDone <- svc.IndexAllPages()
	}()

	type writerResult struct {
		file *os.File
		err  error
	}
	writerReady := make(chan writerResult, 1)
	go func() {
		file, openErr := os.OpenFile(sourcePath, os.O_WRONLY, 0)
		writerReady <- writerResult{file: file, err: openErr}
	}()
	writer := <-writerReady
	if writer.err != nil {
		t.Fatalf("OpenFile FIFO writer: %v", writer.err)
	}
	defer func() { _ = writer.file.Close() }()

	if svc.reconcileMu.TryLock() {
		svc.reconcileMu.Unlock()
		t.Fatal("reindex did not hold the reconciliation boundary while snapshotting")
	}

	updateDone := make(chan error, 1)
	go func() {
		updateDone <- svc.UpdateLinksForPage(source, "[New](/new)")
	}()

	// Keep the already-open FIFO handles for the blocked reindex, but restore
	// the path that later current-state validation reads.
	if err := os.Remove(sourcePath); err != nil {
		t.Fatalf("Remove source FIFO: %v", err)
	}
	if err := os.WriteFile(sourcePath, []byte(source.RawContent), 0o600); err != nil {
		t.Fatalf("Restore source: %v", err)
	}
	if _, err := writer.file.WriteString(source.RawContent); err != nil {
		t.Fatalf("WriteString FIFO: %v", err)
	}
	if err := writer.file.Close(); err != nil {
		t.Fatalf("Close FIFO writer: %v", err)
	}
	if err := <-reindexDone; err != nil {
		t.Fatalf("IndexAllPages: %v", err)
	}
	if err := <-updateDone; err != nil {
		t.Fatalf("UpdateLinksForPage: %v", err)
	}

	out, err := svc.GetOutgoingLinksForPage(*sourceID)
	if err != nil {
		t.Fatalf("GetOutgoingLinksForPage: %v", err)
	}
	if out.Count != 1 || out.Outgoings[0].Broken || out.Outgoings[0].ToPageID != *newTargetID {
		t.Fatalf("reindex overwrote incremental link update: %#v", out)
	}
}

func TestLinkService_IndexAllPagesContext_SerializesDraftDeletionAfterReindex(t *testing.T) {
	svc, ts, _ := setupLinkService(t)
	if _, err := ts.CreateNode("system", nil, "Target", "target", pageNodeKind()); err != nil {
		t.Fatalf("CreateNode target: %v", err)
	}
	sourceID, err := ts.CreateNode("system", nil, "Source", "source", pageNodeKind())
	if err != nil {
		t.Fatalf("CreateNode source: %v", err)
	}
	content := "[Target](/target)"
	if err := ts.UpdateNode("system", *sourceID, "Source", "source", &content, tree.VersionUnchecked, nil, nil, false); err != nil {
		t.Fatalf("UpdateNode source: %v", err)
	}
	source, err := ts.GetPage(*sourceID)
	if err != nil {
		t.Fatalf("GetPage source: %v", err)
	}

	sourcePath := filepath.Join(svc.storageDir, "root", "source.md")
	if err := os.Remove(sourcePath); err != nil {
		t.Fatalf("Remove source: %v", err)
	}
	if err := syscall.Mkfifo(sourcePath, 0o600); err != nil {
		t.Fatalf("Mkfifo source: %v", err)
	}

	reindexDone := make(chan error, 1)
	go func() {
		reindexDone <- svc.IndexAllPages()
	}()

	type writerResult struct {
		file *os.File
		err  error
	}
	writerReady := make(chan writerResult, 1)
	go func() {
		file, openErr := os.OpenFile(sourcePath, os.O_WRONLY, 0)
		writerReady <- writerResult{file: file, err: openErr}
	}()
	writer := <-writerReady
	if writer.err != nil {
		t.Fatalf("OpenFile FIFO writer: %v", writer.err)
	}
	defer func() { _ = writer.file.Close() }()

	deleteStarted := make(chan struct{})
	deleteDone := make(chan error, 1)
	go func() {
		close(deleteStarted)
		// Draft and delete side effects both remove the source through this API.
		deleteDone <- svc.DeleteOutgoingLinksForPage(*sourceID)
	}()
	<-deleteStarted

	select {
	case err := <-deleteDone:
		if err != nil {
			t.Fatalf("DeleteOutgoingLinksForPage: %v", err)
		}
		t.Fatal("draft deletion did not wait for the active reindex")
	case <-time.After(100 * time.Millisecond):
	}

	// Restore the filesystem path while the open FIFO handles continue to
	// release the blocked reindex below.
	if err := os.Remove(sourcePath); err != nil {
		t.Fatalf("Remove source FIFO: %v", err)
	}
	if err := os.WriteFile(sourcePath, []byte(source.RawContent), 0o600); err != nil {
		t.Fatalf("Restore source: %v", err)
	}
	if _, err := writer.file.WriteString(source.RawContent); err != nil {
		t.Fatalf("WriteString FIFO: %v", err)
	}
	if err := writer.file.Close(); err != nil {
		t.Fatalf("Close FIFO writer: %v", err)
	}
	if err := <-reindexDone; err != nil {
		t.Fatalf("IndexAllPages: %v", err)
	}
	if err := <-deleteDone; err != nil {
		t.Fatalf("DeleteOutgoingLinksForPage: %v", err)
	}

	out, err := svc.GetOutgoingLinksForPage(*sourceID)
	if err != nil {
		t.Fatalf("GetOutgoingLinksForPage: %v", err)
	}
	if out.Count != 0 {
		t.Fatalf("reindex overwrote final draft deletion: %#v", out)
	}
}
