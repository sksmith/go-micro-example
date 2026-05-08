package api_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/sksmith/go-micro-example/api"
	"github.com/sksmith/go-micro-example/core"
	"github.com/sksmith/go-micro-example/core/user"
)

// readCounter scrapes /metrics and returns the value of the named
// Prometheus counter. Returns -1 when the counter is absent so a test
// can distinguish missing from zero.
func readCounter(t *testing.T, ts *httptest.Server, name string) float64 {
	t.Helper()
	res, err := http.Get(ts.URL + api.MetricsEndpoint)
	if err != nil {
		t.Fatalf("scrape /metrics: %v", err)
	}
	defer res.Body.Close()
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
	r, usrSvc, signer := newTestRouterWithSigner()
	usrSvc.LoginFunc = func(ctx context.Context, username, password string) (user.User, error) {
		if username == "alice" && password == "pw" {
			return user.User{Username: "alice", IsAdmin: true}, nil
		}
		return user.User{}, core.ErrNotFound
	}
	ts := httptest.NewServer(r)
	defer ts.Close()

	basicBefore := readCounter(t, ts, "auth_basic_requests_total")
	jwtBefore := readCounter(t, ts, "auth_jwt_requests_total")
	if basicBefore < 0 || jwtBefore < 0 {
		t.Fatalf("expected counters to exist, basic=%v jwt=%v", basicBefore, jwtBefore)
	}

	// Successful Basic Auth → basic counter should advance, jwt should not.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+api.ApiPath+api.InventoryPath, nil)
	req.SetBasicAuth("alice", "pw")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()

	// Failed Basic Auth → no counter movement.
	req2, _ := http.NewRequest(http.MethodGet, ts.URL+api.ApiPath+api.InventoryPath, nil)
	req2.SetBasicAuth("alice", "wrong")
	res2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	res2.Body.Close()

	// Successful Bearer → jwt counter should advance, basic should not.
	tok, _, _ := signer.Issue(user.User{Username: "alice", IsAdmin: true})
	req3, _ := http.NewRequest(http.MethodGet, ts.URL+api.ApiPath+api.InventoryPath, nil)
	req3.Header.Set("Authorization", "Bearer "+tok)
	res3, err := http.DefaultClient.Do(req3)
	if err != nil {
		t.Fatal(err)
	}
	res3.Body.Close()

	// Failed Bearer → no counter movement.
	req4, _ := http.NewRequest(http.MethodGet, ts.URL+api.ApiPath+api.InventoryPath, nil)
	req4.Header.Set("Authorization", "Bearer not-a-real-token")
	res4, err := http.DefaultClient.Do(req4)
	if err != nil {
		t.Fatal(err)
	}
	res4.Body.Close()

	basicAfter := readCounter(t, ts, "auth_basic_requests_total")
	jwtAfter := readCounter(t, ts, "auth_jwt_requests_total")

	if basicAfter-basicBefore != 1 {
		t.Errorf("auth_basic_requests_total moved by %v, want 1", basicAfter-basicBefore)
	}
	if jwtAfter-jwtBefore != 1 {
		t.Errorf("auth_jwt_requests_total moved by %v, want 1", jwtAfter-jwtBefore)
	}
}
