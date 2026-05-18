package app_test

import (
	"bytes"
	"github.com/sksmith/go-micro-example/internal/app"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func TestCorrelationLogger_PropagatesXRequestID(t *testing.T) {
	var buf bytes.Buffer
	original := log.Logger
	log.Logger = zerolog.New(&buf).Level(zerolog.TraceLevel)
	t.Cleanup(func() { log.Logger = original })

	r, _ := newTestRouter()
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)

	req, err := http.NewRequest(http.MethodGet, ts.URL+app.LivenessEndpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Request-Id", "test-123")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()

	got := buf.String()
	if !strings.Contains(got, "test-123") {
		t.Errorf("expected log output to contain inbound X-Request-Id %q, got:\n%s", "test-123", got)
	}
	if !strings.Contains(got, `"request_id"`) {
		t.Errorf("expected log output to contain request_id field, got:\n%s", got)
	}
}

func TestCorrelationLogger_GeneratesIDWhenAbsent(t *testing.T) {
	var buf bytes.Buffer
	original := log.Logger
	log.Logger = zerolog.New(&buf).Level(zerolog.TraceLevel)
	t.Cleanup(func() { log.Logger = original })

	r, _ := newTestRouter()
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)

	res, err := http.Get(ts.URL + app.LivenessEndpoint)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()

	got := buf.String()
	if !strings.Contains(got, `"request_id"`) {
		t.Errorf("expected request_id field even when X-Request-Id absent, got:\n%s", got)
	}
}
