package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

// TestShutdownHTTPDrainsInFlight is the regression test for
// DSN-001's central guarantee: a request that's already on the
// wire when shutdown begins must finish, not get dropped. We
// start a server with a slow handler, fire a request that sleeps
// 200ms, kick off shutdown 50ms in, and assert the request
// completes with 200 and shutdownHTTP returns within the deadline.
func TestShutdownHTTPDrainsInFlight(t *testing.T) {
	handlerStarted := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		close(handlerStarted)
		select {
		case <-time.After(200 * time.Millisecond):
		case <-r.Context().Done():
			t.Errorf("handler context cancelled mid-request: %v", r.Context().Err())
		}
		w.WriteHeader(http.StatusOK)
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: time.Second}

	serveDone := make(chan struct{})
	go func() {
		_ = srv.Serve(ln)
		close(serveDone)
	}()

	// Fire the slow request. Use a separate client with a generous
	// timeout so we don't false-fail on the test's own deadline.
	client := &http.Client{Timeout: 5 * time.Second}
	resp := make(chan *http.Response, 1)
	reqErr := make(chan error, 1)
	go func() {
		r, err := client.Get("http://" + ln.Addr().String() + "/slow")
		if err != nil {
			reqErr <- err
			return
		}
		resp <- r
	}()

	// Wait for the handler to actually start before shutting down,
	// otherwise the test reduces to "Shutdown rejects new conns"
	// instead of "Shutdown drains in-flight."
	select {
	case <-handlerStarted:
	case <-time.After(time.Second):
		t.Fatal("handler never started")
	}

	shutdownStart := time.Now()
	shutdownHTTP(srv, 2*time.Second)
	shutdownDur := time.Since(shutdownStart)

	select {
	case r := <-resp:
		defer func() { _ = r.Body.Close() }()
		if r.StatusCode != http.StatusOK {
			t.Errorf("got status %d, want 200", r.StatusCode)
		}
	case err := <-reqErr:
		t.Fatalf("in-flight request failed: %v", err)
	case <-time.After(time.Second):
		t.Fatal("in-flight request never completed")
	}

	if shutdownDur > 2*time.Second {
		t.Errorf("shutdownHTTP took %s, exceeded its own 2s deadline", shutdownDur)
	}

	<-serveDone
}

// TestShutdownHTTPHonorsDeadline asserts that a request that runs
// past the shutdown timeout doesn't keep the server alive forever.
// shutdownHTTP must return within ~timeout, even if the handler
// is still going.
func TestShutdownHTTPHonorsDeadline(t *testing.T) {
	handlerStarted := make(chan struct{})
	hangCtx, cancelHang := context.WithCancel(context.Background())
	t.Cleanup(cancelHang)

	mux := http.NewServeMux()
	var once sync.Once
	mux.HandleFunc("/hang", func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(handlerStarted) })
		<-hangCtx.Done()
		w.WriteHeader(http.StatusOK)
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: time.Second}

	go func() { _ = srv.Serve(ln) }()

	go func() {
		// Fire the hanging request and drop the response; we only
		// care about the server's behaviour.
		req, _ := http.NewRequestWithContext(hangCtx, http.MethodGet, "http://"+ln.Addr().String()+"/hang", nil)
		resp, err := http.DefaultClient.Do(req)
		if err == nil && resp != nil {
			_ = resp.Body.Close()
		}
	}()

	select {
	case <-handlerStarted:
	case <-time.After(time.Second):
		t.Fatal("handler never started")
	}

	const deadline = 100 * time.Millisecond
	shutdownStart := time.Now()
	shutdownHTTP(srv, deadline)
	shutdownDur := time.Since(shutdownStart)

	// Allow generous slack — CI runners are slow — but the upper
	// bound has to be well under "forever."
	if shutdownDur > 2*time.Second {
		t.Errorf("shutdownHTTP took %s with a %s deadline; expected forced close", shutdownDur, deadline)
	}

	// Belt-and-braces: the listener should be closed.
	if _, err := net.DialTimeout("tcp", ln.Addr().String(), 100*time.Millisecond); err == nil {
		t.Error("listener still accepting connections after shutdownHTTP returned")
	} else if !isConnRefused(err) {
		// Either connection refused (good) or something equivalent
		// is fine; we just don't want a successful dial.
		t.Logf("dial after shutdown: %v (acceptable)", err)
	}
}

func TestResolveShutdownTimeout(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want time.Duration
	}{
		{name: "unset uses default", env: "", want: defaultShutdownTimeout},
		{name: "valid integer is honored", env: "5", want: 5 * time.Second},
		{name: "non-numeric falls back to default", env: "thirty", want: defaultShutdownTimeout},
		{name: "zero is rejected (default)", env: "0", want: defaultShutdownTimeout},
		{name: "negative is rejected (default)", env: "-1", want: defaultShutdownTimeout},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("GME_SHUTDOWN_TIMEOUT_SECONDS", test.env)
			if got := resolveShutdownTimeout(); got != test.want {
				t.Errorf("got=%s want=%s", got, test.want)
			}
		})
	}
}

func isConnRefused(err error) bool {
	if err == nil {
		return false
	}
	var netErr *net.OpError
	return errors.As(err, &netErr)
}
