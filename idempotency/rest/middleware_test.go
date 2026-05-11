package rest_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sksmith/go-micro-example/idempotency/rest"
)

// newHandler returns an http.Handler that increments calls each time
// it runs, then writes the supplied status + body. Used to detect
// whether the middleware skipped the handler on a replay.
func newHandler(calls *int32, status int, body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(calls, 1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-Id", "rid-123")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
}

func wrap(t *testing.T, store rest.Store, required bool, ttl time.Duration, h http.Handler) http.Handler {
	t.Helper()
	return rest.Middleware(rest.Config{Store: store, TTL: ttl, Required: required})(h)
}

func do(t *testing.T, h http.Handler, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/resource", strings.NewReader(body))
	if key != "" {
		req.Header.Set(rest.Header, key)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestMiddlewareFirstWriteRunsHandlerAndStores(t *testing.T) {
	store := rest.NewMemoryStore()
	var calls int32
	h := wrap(t, store, true, 0, newHandler(&calls, http.StatusCreated, `{"id":1}`))

	rec := do(t, h, "key-A", `{"sku":"abc"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d, want 201", rec.Code)
	}
	if rec.Body.String() != `{"id":1}` {
		t.Errorf("body=%q", rec.Body.String())
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("handler ran %d times, want 1", got)
	}
	if store.Size() != 1 {
		t.Errorf("store size=%d, want 1", store.Size())
	}
}

func TestMiddlewareReplaysSecondRequestByteForByte(t *testing.T) {
	store := rest.NewMemoryStore()
	var calls int32
	h := wrap(t, store, true, 0, newHandler(&calls, http.StatusCreated, `{"id":1}`))

	_ = do(t, h, "key-A", `{"sku":"abc"}`)
	rec := do(t, h, "key-A", `{"sku":"abc"}`)

	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("handler ran %d times, want 1 (replay should skip handler)", calls)
	}
	if rec.Code != http.StatusCreated {
		t.Errorf("replay status=%d, want 201", rec.Code)
	}
	if rec.Body.String() != `{"id":1}` {
		t.Errorf("replay body=%q", rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("replay Content-Type=%q, want application/json", got)
	}
	if got := rec.Header().Get("Idempotent-Replay"); got != "true" {
		t.Errorf("replay header Idempotent-Replay=%q, want true", got)
	}
	if got := rec.Header().Get(rest.HashHeader); got == "" {
		t.Errorf("replay header %s missing", rest.HashHeader)
	}
}

func TestMiddlewareConflictOnSameKeyDifferentBody(t *testing.T) {
	store := rest.NewMemoryStore()
	var calls int32
	h := wrap(t, store, true, 0, newHandler(&calls, http.StatusCreated, `{"id":1}`))

	_ = do(t, h, "key-A", `{"sku":"abc"}`)
	rec := do(t, h, "key-A", `{"sku":"different"}`)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d, want 409", rec.Code)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("handler ran %d times, want 1 (conflict should not re-run)", calls)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Errorf("Content-Type=%q, want application/problem+json", got)
	}
}

func TestMiddlewareExpiredKeyBehavesAsFirstWrite(t *testing.T) {
	store := rest.NewMemoryStore()
	var calls int32
	h := wrap(t, store, true, 10*time.Millisecond, newHandler(&calls, http.StatusCreated, `{"id":1}`))

	_ = do(t, h, "key-A", `{"sku":"abc"}`)
	time.Sleep(20 * time.Millisecond)
	rec := do(t, h, "key-A", `{"sku":"abc"}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d, want 201", rec.Code)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("handler ran %d times, want 2 (expired cache should re-run)", calls)
	}
}

func TestMiddlewareMissingKeyRequiredReturns400(t *testing.T) {
	store := rest.NewMemoryStore()
	var calls int32
	h := wrap(t, store, true, 0, newHandler(&calls, http.StatusCreated, `{"id":1}`))

	rec := do(t, h, "", `{"sku":"abc"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", rec.Code)
	}
	if calls != 0 {
		t.Errorf("handler ran %d times; should be 0 when required header missing", calls)
	}
}

func TestMiddlewareMissingKeyOptionalPassesThrough(t *testing.T) {
	store := rest.NewMemoryStore()
	var calls int32
	h := wrap(t, store, false, 0, newHandler(&calls, http.StatusCreated, `{"id":1}`))

	rec := do(t, h, "", `{"sku":"abc"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d, want 201", rec.Code)
	}
	if calls != 1 {
		t.Errorf("handler ran %d times, want 1", calls)
	}
}

func TestMiddlewareDoesNotCache5xx(t *testing.T) {
	store := rest.NewMemoryStore()
	var calls int32
	h := wrap(t, store, true, 0, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, "boom", http.StatusInternalServerError)
	}))

	_ = do(t, h, "key-A", `{"sku":"abc"}`)
	_ = do(t, h, "key-A", `{"sku":"abc"}`)

	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("handler ran %d times, want 2 (5xx must not be cached)", got)
	}
	if store.Size() != 0 {
		t.Errorf("store size=%d, want 0 (5xx must not be cached)", store.Size())
	}
}

func TestMiddlewareDegradesOpenOnStoreLookupError(t *testing.T) {
	bad := &failingStore{lookupErr: errors.New("redis down")}
	var calls int32
	h := wrap(t, bad, true, 0, newHandler(&calls, http.StatusCreated, `{"id":1}`))

	rec := do(t, h, "key-A", `{"sku":"abc"}`)
	if rec.Code != http.StatusCreated {
		t.Errorf("status=%d, want 201 (lookup failure should not block)", rec.Code)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("handler ran %d times, want 1", calls)
	}
}

func TestMiddlewareHashesBodyConsistently(t *testing.T) {
	store := rest.NewMemoryStore()
	var calls int32
	body := `{"sku":"abc","qty":7}`
	// Wrap an inner handler that asserts it still sees the full body
	// (the middleware buffered + reset r.Body).
	h := rest.Middleware(rest.Config{Store: store, Required: true})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		buf, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("handler read body: %v", err)
		}
		if string(buf) != body {
			t.Errorf("handler body=%q, want %q", string(buf), body)
		}
		w.WriteHeader(http.StatusCreated)
	}))

	req := httptest.NewRequest(http.MethodPost, "/x", bytes.NewBufferString(body))
	req.Header.Set(rest.Header, "key-A")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d, want 201", rec.Code)
	}
}

// failingStore is a Store whose Lookup/Save can be configured to
// return errors. Used to assert the middleware degrades open.
type failingStore struct {
	lookupErr error
	saveErr   error
}

func (f *failingStore) Lookup(_ context.Context, _ string) (rest.Entry, bool, error) {
	return rest.Entry{}, false, f.lookupErr
}

func (f *failingStore) Save(_ context.Context, _ string, _ rest.Entry, _ time.Duration) error {
	return f.saveErr
}

func TestMemoryStoreRejectsExpiredEntryOnLookup(t *testing.T) {
	s := rest.NewMemoryStore()
	_ = s.Save(context.Background(), "k", rest.Entry{Status: 201}, 5*time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	_, found, err := s.Lookup(context.Background(), "k")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if found {
		t.Error("expected expired entry to report not found")
	}
	if s.Size() != 0 {
		t.Errorf("expired entry should have been swept; size=%d", s.Size())
	}
}
