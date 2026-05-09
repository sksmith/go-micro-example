package inventory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/sksmith/go-micro-example/db"
)

// TestRollbackLogsRollbackError is the regression test for ERR-001
// B4. The rollback helper used to log the *triggering* error under a
// "failed to rollback" message, hiding the actual rollback failure.
// This test writes the package logger to a buffer, calls rollback
// with a tx whose Rollback returns a sentinel, and asserts the log
// entry's "error" field is the rollback sentinel (and that the
// trigger is preserved under a separate field).
func TestRollbackLogsRollbackError(t *testing.T) {
	rollbackErr := errors.New("rollback failed: dead conn")
	triggerErr := errors.New("original failure")

	mockTx := db.NewMockTransaction()
	mockTx.RollbackFunc = func(ctx context.Context) error { return rollbackErr }

	var buf bytes.Buffer
	testLogger := zerolog.New(&buf)
	original := log.Logger
	log.Logger = testLogger
	t.Cleanup(func() { log.Logger = original })

	// rollback uses log.Ctx(ctx); attach the buffer-backed logger to
	// ctx so the test exercises the same code path production hits.
	ctx := testLogger.WithContext(context.Background())
	rollback(ctx, mockTx, triggerErr)

	if buf.Len() == 0 {
		t.Fatal("expected a log entry, got none")
	}
	if !strings.Contains(buf.String(), "failed to rollback") {
		t.Errorf("expected 'failed to rollback' message, got %q", buf.String())
	}

	// Parse the JSON log line and assert the field bindings.
	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("log line is not valid JSON: %v\nraw=%s", err, buf.String())
	}
	if got := entry["error"]; got != rollbackErr.Error() {
		t.Errorf("error field got=%v want=%v (the rollback error must dominate the log entry)", got, rollbackErr.Error())
	}
	if got := entry["trigger"]; got != triggerErr.Error() {
		t.Errorf("trigger field got=%v want=%v", got, triggerErr.Error())
	}
}

// TestRollbackNilTxIsNoop ensures the helper tolerates a nil tx,
// which happens when BeginTransaction itself fails and the deferred
// rollback fires anyway.
func TestRollbackNilTxIsNoop(t *testing.T) {
	var buf bytes.Buffer
	original := log.Logger
	log.Logger = zerolog.New(&buf)
	t.Cleanup(func() { log.Logger = original })

	rollback(context.Background(), nil, errors.New("trigger"))

	if buf.Len() != 0 {
		t.Errorf("expected no log output for nil tx, got %q", buf.String())
	}
}
