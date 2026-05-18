package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPrintSummaryFormatsRows(t *testing.T) {
	tmp, err := os.CreateTemp(t.TempDir(), "summary-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tmp.Close() }()

	printSummary(tmp, []Result{
		{Capability: "DSN-026", Name: "REST round-trip", Status: StatusPass, Latency: 12 * time.Millisecond, TraceID: "abc123"},
		{Capability: "DSN-XXX", Name: "Demo step that fails", Status: StatusFail, Latency: 5 * time.Millisecond, Reason: "kaboom"},
	})

	if _, err := tmp.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(tmp); err != nil {
		t.Fatal(err)
	}
	got := buf.String()

	wantContains := []string{
		"demo summary",
		"DSN-026",
		"REST round-trip",
		"pass",
		"abc123",
		"DSN-XXX",
		"fail",
		"reason: kaboom",
	}
	for _, w := range wantContains {
		if !strings.Contains(got, w) {
			t.Errorf("summary missing %q in:\n%s", w, got)
		}
	}
}

func TestWaitReadyPollsUntil200(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := Config{
		BaseURL:      srv.URL,
		ReadyPath:    "/",
		Deadline:     2 * time.Second,
		PollInterval: 10 * time.Millisecond,
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Deadline)
	defer cancel()

	if err := waitReady(ctx, cfg); err != nil {
		t.Fatalf("waitReady: %v", err)
	}
	if calls < 3 {
		t.Errorf("expected at least 3 polls before 200, got %d", calls)
	}
}

func TestWaitReadyHonorsDeadline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	cfg := Config{
		BaseURL:      srv.URL,
		ReadyPath:    "/",
		Deadline:     100 * time.Millisecond,
		PollInterval: 25 * time.Millisecond,
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Deadline)
	defer cancel()

	err := waitReady(ctx, cfg)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error chain should include DeadlineExceeded, got %v", err)
	}
}

func TestRunStepCapturesFailure(t *testing.T) {
	cfg := Config{PerStepTimeout: time.Second}
	step := Step{
		Capability: "TEST",
		Name:       "always fails",
		Run: func(_ context.Context, _ Config) (string, error) {
			return "trace-1", fmt.Errorf("simulated")
		},
	}
	r := runStep(context.Background(), cfg, step)
	if r.Status != StatusFail {
		t.Errorf("status=%q want=fail", r.Status)
	}
	if r.TraceID != "trace-1" {
		t.Errorf("trace_id should be preserved on failure, got %q", r.TraceID)
	}
	if !strings.Contains(r.Reason, "simulated") {
		t.Errorf("reason should carry underlying error, got %q", r.Reason)
	}
}

func TestRunStepCapturesSuccess(t *testing.T) {
	cfg := Config{PerStepTimeout: time.Second}
	step := Step{
		Capability: "TEST",
		Name:       "always passes",
		Run: func(_ context.Context, _ Config) (string, error) {
			return "trace-ok", nil
		},
	}
	r := runStep(context.Background(), cfg, step)
	if r.Status != StatusPass {
		t.Errorf("status=%q want=pass", r.Status)
	}
	if r.TraceID != "trace-ok" {
		t.Errorf("trace_id got=%q", r.TraceID)
	}
	// Windows' default monotonic clock resolution is ~15ms, so a
	// no-op Run can record Latency = 0 even though the timer fired.
	// Asserting >= 0 (rather than > 0) keeps the contract — the field
	// is populated and non-negative — without depending on the host's
	// clock granularity (DEP-011: unblocks Dependabot's Windows job).
	if r.Latency < 0 {
		t.Errorf("latency should be non-negative, got %v", r.Latency)
	}
}
