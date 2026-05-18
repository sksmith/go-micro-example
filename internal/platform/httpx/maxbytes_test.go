package httpx_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sksmith/go-micro-example/internal/platform/httpx"
)

func bodyEchoHandler(read *int32) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		atomic.AddInt32(read, int32(len(b)))
		w.WriteHeader(http.StatusNoContent)
	})
}

func TestMaxBytesShortCircuitsOnOversizeContentLength(t *testing.T) {
	var read int32
	h := httpx.MaxBytes(8)(bodyEchoHandler(&read))

	body := strings.NewReader("123456789") // 9 bytes — limit is 8
	req := httptest.NewRequest(http.MethodPost, "/x", body)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d, want 413", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type=%q, want application/problem+json", ct)
	}
	if atomic.LoadInt32(&read) != 0 {
		t.Errorf("handler read %d bytes; want 0 (short-circuit)", read)
	}
	var p map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("unmarshal problem: %v", err)
	}
	if p["status"].(float64) != float64(http.StatusRequestEntityTooLarge) {
		t.Errorf("problem.status=%v, want 413", p["status"])
	}
	if detail, _ := p["detail"].(string); !strings.Contains(detail, "8 bytes") {
		t.Errorf("problem.detail=%q, want limit value", detail)
	}
}

func TestMaxBytesAllowsUnderLimit(t *testing.T) {
	var read int32
	h := httpx.MaxBytes(1024)(bodyEchoHandler(&read))

	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("hello"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d, want 204", rec.Code)
	}
	if got := atomic.LoadInt32(&read); got != 5 {
		t.Errorf("handler read %d bytes; want 5", got)
	}
}

func TestMaxBytesCapsChunkedBody(t *testing.T) {
	// httptest.NewRequest with -1 ContentLength simulates a chunked
	// request: the pre-check can't reject upfront, but the wrapped
	// body must surface an error to the handler when the limit is
	// exceeded mid-read.
	h := httpx.MaxBytes(4)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		if err == nil {
			t.Error("expected MaxBytesReader to error on overflow; got nil")
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusRequestEntityTooLarge)
	}))

	body := bytes.NewReader([]byte("more-than-four"))
	req := httptest.NewRequest(http.MethodPost, "/x", body)
	req.ContentLength = -1 // mark as unknown length
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d, want 413", rec.Code)
	}
}

func TestMaxBytesSkipsBodylessMethods(t *testing.T) {
	var ran int32
	h := httpx.MaxBytes(1)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&ran, 1)
		w.WriteHeader(http.StatusOK)
	}))

	for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodDelete, http.MethodOptions} {
		req := httptest.NewRequest(m, "/x", nil)
		req.ContentLength = 999 // stray header; must be ignored
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("method=%s: status=%d, want 200", m, rec.Code)
		}
	}
	if atomic.LoadInt32(&ran) != 4 {
		t.Errorf("handler ran %d times; want 4", ran)
	}
}

func TestMaxBytesZeroLimitDisablesMiddleware(t *testing.T) {
	var read int32
	h := httpx.MaxBytes(0)(bodyEchoHandler(&read))

	huge := strings.Repeat("a", 4096)
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(huge))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d, want 204 (limit=0 disables middleware)", rec.Code)
	}
	if got := atomic.LoadInt32(&read); got != int32(len(huge)) {
		t.Errorf("read=%d, want %d", got, len(huge))
	}
}
