//go:build darwin || linux

package revision

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/perber/wiki/internal/core/tree"
)

func TestRecordAssetChange_SkipsSnapshotWhenAncestorBecomesDraftDuringCapture(t *testing.T) {
	service, treeService, storageDir := newRevisionTestService(t)
	sectionKind := tree.NodeKindSection
	sectionID, err := treeService.CreateNode("tester", nil, "Section", "section", &sectionKind)
	if err != nil {
		t.Fatalf("CreateNode(section): %v", err)
	}
	pageKind := tree.NodeKindPage
	pageID, err := treeService.CreateNode("tester", sectionID, "Page", "page", &pageKind)
	if err != nil {
		t.Fatalf("CreateNode(page): %v", err)
	}
	content := "public content"
	if err := treeService.UpdateNode("tester", *pageID, "Page", "page", &content, tree.VersionUnchecked, nil, nil, false); err != nil {
		t.Fatalf("UpdateNode(page): %v", err)
	}

	assetDir := filepath.Join(storageDir, "assets", *pageID)
	if err := os.MkdirAll(assetDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(asset): %v", err)
	}
	assetPath := filepath.Join(assetDir, "private.txt")
	if err := syscall.Mkfifo(assetPath, 0o600); err != nil {
		t.Fatalf("Mkfifo(asset): %v", err)
	}

	type writerResult struct {
		file *os.File
		err  error
	}
	writerReady := make(chan writerResult, 1)
	go func() {
		file, openErr := os.OpenFile(assetPath, os.O_WRONLY, 0)
		writerReady <- writerResult{file: file, err: openErr}
	}()

	type revisionResult struct {
		revision *Revision
		created  bool
		err      error
	}
	revisionDone := make(chan revisionResult, 1)
	go func() {
		revision, created, recordErr := service.RecordAssetChange(*pageID, "tester", "asset")
		revisionDone <- revisionResult{revision: revision, created: created, err: recordErr}
	}()

	var writer *os.File
	select {
	case result := <-writerReady:
		if result.err != nil {
			t.Fatalf("OpenFile(fifo writer): %v", result.err)
		}
		writer = result.file
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for asset scan")
	}
	defer writer.Close()

	setRevisionTestDraft(t, treeService, *sectionID, true)
	secret := []byte("draft-only asset")
	if err := os.Remove(assetPath); err != nil {
		t.Fatalf("Remove(fifo): %v", err)
	}
	if err := os.WriteFile(assetPath, secret, 0o600); err != nil {
		t.Fatalf("WriteFile(asset): %v", err)
	}
	if _, err := writer.Write(secret); err != nil {
		t.Fatalf("Write(fifo): %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close(fifo): %v", err)
	}

	select {
	case result := <-revisionDone:
		if result.err != nil || result.created || result.revision != nil {
			t.Fatalf("draft transition revision = %#v, created=%v, err=%v", result.revision, result.created, result.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for revision capture")
	}

	revisions, err := service.ListRevisions(*pageID)
	if err != nil {
		t.Fatalf("ListRevisions: %v", err)
	}
	if len(revisions) != 0 {
		t.Fatalf("revisions after draft transition = %d, want 0", len(revisions))
	}
}

func TestRecordAssetChange_ReturnsVersionConflictWhenPublicLineageChangesDuringCapture(t *testing.T) {
	service, treeService, storageDir := newRevisionTestService(t)
	pageID := createRevisionTestPage(t, treeService, "Page", "page", "public content")
	assetDir := filepath.Join(storageDir, "assets", pageID)
	if err := os.MkdirAll(assetDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(asset): %v", err)
	}
	assetPath := filepath.Join(assetDir, "public.txt")
	if err := syscall.Mkfifo(assetPath, 0o600); err != nil {
		t.Fatalf("Mkfifo(asset): %v", err)
	}

	type writerResult struct {
		file *os.File
		err  error
	}
	writerReady := make(chan writerResult, 1)
	go func() {
		file, openErr := os.OpenFile(assetPath, os.O_WRONLY, 0)
		writerReady <- writerResult{file: file, err: openErr}
	}()

	type revisionResult struct {
		revision *Revision
		created  bool
		err      error
	}
	revisionDone := make(chan revisionResult, 1)
	go func() {
		revision, created, recordErr := service.RecordAssetChange(pageID, "tester", "asset")
		revisionDone <- revisionResult{revision: revision, created: created, err: recordErr}
	}()

	var writer *os.File
	select {
	case result := <-writerReady:
		if result.err != nil {
			t.Fatalf("OpenFile(fifo writer): %v", result.err)
		}
		writer = result.file
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for asset scan")
	}
	defer writer.Close()

	updatedContent := "new public content"
	if err := treeService.UpdateNode("tester", pageID, "Page", "page", &updatedContent, tree.VersionUnchecked, nil, nil, false); err != nil {
		t.Fatalf("UpdateNode(public): %v", err)
	}
	asset := []byte("public asset")
	if _, err := writer.Write(asset); err != nil {
		t.Fatalf("Write(fifo): %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close(fifo): %v", err)
	}

	select {
	case result := <-revisionDone:
		if !errors.Is(result.err, tree.ErrVersionConflict) || result.created || result.revision != nil {
			t.Fatalf("public lineage change revision = %#v, created=%v, err=%v", result.revision, result.created, result.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for revision capture")
	}

	revisions, err := service.ListRevisions(pageID)
	if err != nil {
		t.Fatalf("ListRevisions: %v", err)
	}
	if len(revisions) != 0 {
		t.Fatalf("revisions after public lineage change = %d, want 0", len(revisions))
	}
}
