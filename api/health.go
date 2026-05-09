package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
)

// readinessTimeout caps each downstream readiness check so the
// probe itself cannot stall a Kubernetes kubelet (which retries
// at periodSeconds and times out at timeoutSeconds; the probe
// must return well inside both).
const readinessTimeout = 1 * time.Second

// Pinger is the minimal surface a downstream dependency needs to
// expose for /ready to verify connectivity. *pgxpool.Pool
// satisfies it directly via Ping(context.Context) error.
//
// Heavy queries don't belong here — the readiness probe runs on
// every kubelet poll. A stdlib-style "Ping" is the contract.
type Pinger interface {
	Ping(ctx context.Context) error
}

// LivenessHandler returns 200 unconditionally. The process being
// able to accept the request and reply at all is the liveness
// signal; if pgx or AMQP are down, that's a *readiness* concern
// (kubelet should stop sending traffic) not a *liveness* one
// (kubelet should restart the pod).
func LivenessHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	}
}

// ReadinessHandler returns 200 when every supplied Pinger pings
// cleanly within readinessTimeout, 503 with a reason listing
// failing dependencies otherwise. Each Pinger is checked
// sequentially against its own per-check timeout context.
//
// AMQP is intentionally absent. The queue subsystem (queue/queue.go)
// runs its own redial loop and does not currently expose a
// non-blocking "are we connected?" query. Adding AMQP readiness
// requires the queue to grow a state-watcher; tracked as a
// follow-up to TST-003.
func ReadinessHandler(deps map[string]Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var failures []string
		for name, dep := range deps {
			ctx, cancel := context.WithTimeout(r.Context(), readinessTimeout)
			err := dep.Ping(ctx)
			cancel()
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					failures = append(failures, fmt.Sprintf("%s: timeout", name))
				} else {
					failures = append(failures, fmt.Sprintf("%s: %v", name, err))
				}
			}
		}

		if len(failures) > 0 {
			log.Ctx(r.Context()).Warn().Strs("failures", failures).Msg("readiness check failed")
			w.WriteHeader(http.StatusServiceUnavailable)
			for _, f := range failures {
				_, _ = fmt.Fprintln(w, f)
			}
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	}
}
