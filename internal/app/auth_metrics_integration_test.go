package app_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/sksmith/go-micro-example/internal/app"
	"github.com/sksmith/go-micro-example/internal/user"
)

// readCounter scrapes /metrics and returns the value of the named
// Prometheus counter. Returns -1 when the counter is absent so a test
// can distinguish missing from zero.
func readCounter(t *testing.T, ts *httptest.Server, name string) float64 {
	t.Helper()
	res, err := http.Get(ts.URL + app.MetricsEndpoint)
	if err != nil {
		t.Fatalf("scrape /metrics: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read /metrics: %v", err)
	}
	prefix := name + " "
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, prefix) {
			// Prometheus text format: "<name> <value>"
			value := strings.TrimSpace(strings.TrimPrefix(line, prefix))
			v, err := strconv.ParseFloat(value, 64)
			if err != nil {
				t.Fatalf("parse counter line %q: %v", line, err)
			}
			return v
		}
	}
	return -1
}

func TestAuthCountersMoveOnSuccessOnly(t *testing.T) {
	// SEC-002c: Basic Auth is rejected on protected routes, so
	// auth_basic_requests_total never increments. The counter is
	// retained for migration visibility and must stay flat at zero.
	r, _, signer := newTestRouterWithSigner()
	ts := httptest.NewServer(r)
	defer ts.Close()

	basicBefore := readCounter(t, ts, "auth_basic_requests_total")
	jwtBefore := readCounter(t, ts, "auth_jwt_requests_total")
	if basicBefore < 0 || jwtBefore < 0 {
		t.Fatalf("expected counters to exist, basic=%v jwt=%v", basicBefore, jwtBefore)
	}

	// Basic Auth attempt → rejected with 401, no counter movement.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+app.ApiPath+app.InventoryPath, nil)
	req.SetBasicAuth("alice", "pw")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("Basic Auth attempt should be 401, got %d", res.StatusCode)
	}

	// Successful Bearer → jwt counter should advance, basic should not.
	tok, _, _ := signer.Issue(user.User{Username: "alice", IsAdmin: true})
	req2, _ := http.NewRequest(http.MethodGet, ts.URL+app.ApiPath+app.InventoryPath, nil)
	req2.Header.Set("Authorization", "Bearer "+tok)
	res2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	_ = res2.Body.Close()

	// Failed Bearer → no counter movement.
	req3, _ := http.NewRequest(http.MethodGet, ts.URL+app.ApiPath+app.InventoryPath, nil)
	req3.Header.Set("Authorization", "Bearer not-a-real-token")
	res3, err := http.DefaultClient.Do(req3)
	if err != nil {
		t.Fatal(err)
	}
	_ = res3.Body.Close()

	basicAfter := readCounter(t, ts, "auth_basic_requests_total")
	jwtAfter := readCounter(t, ts, "auth_jwt_requests_total")

	if basicAfter-basicBefore != 0 {
		t.Errorf("auth_basic_requests_total moved by %v, want 0 post-SEC-002c", basicAfter-basicBefore)
	}
	if jwtAfter-jwtBefore != 1 {
		t.Errorf("auth_jwt_requests_total moved by %v, want 1", jwtAfter-jwtBefore)
	}
}
