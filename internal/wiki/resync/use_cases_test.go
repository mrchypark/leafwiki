package wikiresync_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sharederrors "github.com/perber/wiki/internal/core/shared/errors"
	httpmetrics "github.com/perber/wiki/internal/http/metrics"
	. "github.com/perber/wiki/internal/wiki/resync"
)

func metricsBody(t *testing.T, metrics *httpmetrics.HTTPMetrics) string {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	metrics.HTTPHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from /metrics, got %d: %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func TestTriggerResyncUseCase_Execute_LaunchesTrigger(t *testing.T) {
	job := NewResyncJob()
	called := false
	metrics := httpmetrics.NewHTTPMetrics()
	uc := NewTriggerResyncUseCase(job, func() { called = true }, metrics)

	if err := uc.Execute(context.Background()); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !called {
		t.Error("trigger was not called")
	}
	if !strings.Contains(metricsBody(t, metrics), `leafwiki_resync_runs_total{result="accepted"} 1`) {
		t.Error("accepted resync metric was not incremented")
	}
}

func TestTriggerResyncUseCase_Execute_ReturnsLocalizedErrorWhenAlreadyRunning(t *testing.T) {
	job := NewResyncJob()
	job.Start() // simulate running

	metrics := httpmetrics.NewHTTPMetrics()
	uc := NewTriggerResyncUseCase(job, func() { t.Error("trigger must not be called") }, metrics)
	err := uc.Execute(context.Background())

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	loc, ok := sharederrors.AsLocalizedError(err)
	if !ok {
		t.Fatalf("expected LocalizedError, got %T", err)
	}
	if loc.Code != ErrCodeResyncAlreadyRunning {
		t.Errorf("expected code %q, got %q", ErrCodeResyncAlreadyRunning, loc.Code)
	}
	if strings.Contains(metricsBody(t, metrics), `leafwiki_resync_runs_total{result="accepted"}`) {
		t.Error("accepted resync metric was incremented for an already running job")
	}
}

func TestGetResyncStatusUseCase_Execute_ReturnsJobStatus(t *testing.T) {
	job := NewResyncJob()
	job.Start()
	job.SetPhase(PhaseTags)
	job.Finish(errors.New("something went wrong"))

	uc := NewGetResyncStatusUseCase(job)
	out := uc.Execute(context.Background())

	if out.Status.Running {
		t.Error("finished job should not be running")
	}
	if !out.Status.Done {
		t.Error("expected done=true")
	}
	if out.Status.Error != "something went wrong" {
		t.Errorf("unexpected error message: %q", out.Status.Error)
	}
}
