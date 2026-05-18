// Package idempotency provides a small, transport-agnostic helper
// for at-least-once consumers that need at-most-once handler
// invocations (DSN-017). The pattern is a single Postgres table —
// processed_events (event_id, consumer_group) — with a unique
// constraint on the pair. Applier.Apply:
//
//   - INSERT ... ON CONFLICT DO NOTHING. The unique constraint
//     serializes concurrent deliveries: the second INSERT blocks on
//     the first's lock, then either succeeds (the first rolled back)
//     or returns rowcount=0 (the first committed).
//   - If rowcount=0, the (event_id, consumer_group) pair was already
//     processed — skip the handler and return nil.
//   - Otherwise run the handler. On handler error, DELETE the dedupe
//     row so the next delivery can re-attempt, then surface the
//     error to the caller's retry/DLT logic (DSN-016).
//
// The dedupe row commits BEFORE the handler runs, not inside the
// same transaction as the handler's side effects. This trades a
// known edge case — a process crash AFTER INSERT but BEFORE DELETE
// leaves a stuck row that skips the next retry — for not having to
// thread a database transaction through every service method the
// handler might touch.
//
// The helper is reused across DSN-016 (Kafka, now), and the
// forthcoming DSN-019 / DSN-023 / DSN-025 tickets — the same dedupe
// table backs all transports, partitioned by consumer_group.
package idempotency

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
)

var (
	metricsOnce  sync.Once
	appliedTotal prometheus.Counter
	skippedTotal prometheus.Counter
)

func ensureMetrics() {
	metricsOnce.Do(func() {
		appliedTotal = prometheus.NewCounter(prometheus.CounterOpts{
			Name: "idempotency_handler_applied_total",
			Help: "Handler invocations that ran (first-time delivery for the (event_id, group) pair).",
		})
		skippedTotal = prometheus.NewCounter(prometheus.CounterOpts{
			Name: "idempotency_handler_skipped_total",
			Help: "Handler invocations skipped because the (event_id, group) pair was already processed.",
		})
		prometheus.MustRegister(appliedTotal, skippedTotal)
	})
}

// Pool is the slice of *pgxpool.Pool that the Applier needs. Keeping
// the surface narrow makes the package easy to swap in tests.
type Pool interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Applier is the dedupe-then-do helper. Construct one per consumer
// group; share it across handler invocations.
type Applier struct {
	pool          Pool
	consumerGroup string
}

// NewApplier returns an Applier scoped to the given consumer group.
// The group is the dedupe partition: two independent consumers can
// each apply the same event once, but a redelivery within the same
// group skips.
func NewApplier(pool Pool, consumerGroup string) *Applier {
	ensureMetrics()
	if consumerGroup == "" {
		// Empty group would coalesce all callers into one dedupe
		// scope, which is almost certainly a bug.
		panic("idempotency: consumerGroup is required")
	}
	return &Applier{pool: pool, consumerGroup: consumerGroup}
}

// Apply runs fn at most once per (eventID, consumerGroup) pair. The
// dedupe row is committed before fn runs; on fn error the row is
// removed so the next delivery can re-attempt. See the package doc
// for the crash-mid-handler caveat.
func (a *Applier) Apply(ctx context.Context, eventID string, fn func(ctx context.Context) error) error {
	if eventID == "" {
		return errors.New("idempotency: eventID is required")
	}

	tag, err := a.pool.Exec(
		ctx,
		"INSERT INTO processed_events (event_id, consumer_group) VALUES ($1, $2) ON CONFLICT DO NOTHING",
		eventID, a.consumerGroup,
	)
	if err != nil {
		return fmt.Errorf("insert processed_events: %w", err)
	}
	if tag.RowsAffected() == 0 {
		skippedTotal.Inc()
		log.Ctx(ctx).Debug().Str("event_id", eventID).Str("consumer_group", a.consumerGroup).Msg("idempotency: skipping duplicate")
		return nil
	}

	if err := fn(ctx); err != nil {
		// Roll back the dedupe row so the retry can re-attempt.
		// Best effort: if DELETE fails too, surface the original
		// handler error — the next delivery will see the row and
		// skip, but the consumer's bounded-retry/DLT logic will
		// route it correctly.
		if _, delErr := a.pool.Exec(
			ctx,
			"DELETE FROM processed_events WHERE event_id = $1 AND consumer_group = $2",
			eventID, a.consumerGroup,
		); delErr != nil {
			log.Ctx(ctx).Warn().Err(delErr).Str("event_id", eventID).Msg("failed to roll back processed_events row after handler error")
		}
		return fmt.Errorf("handler: %w", err)
	}

	appliedTotal.Inc()
	return nil
}

// PrunedRows reports how many rows the most recent CleanupOnce
// invocation deleted. Exported for tests; production code should
// rely on the metric instead.
type CleanupResult struct {
	Pruned int64
}

// CleanupOnce deletes processed_events rows older than the configured
// retention window. Returns the number of rows removed.
func (a *Applier) CleanupOnce(ctx context.Context, window time.Duration) (CleanupResult, error) {
	cutoff := time.Now().Add(-window)
	tag, err := a.pool.Exec(
		ctx,
		"DELETE FROM processed_events WHERE processed_at < $1",
		cutoff,
	)
	if err != nil {
		return CleanupResult{}, fmt.Errorf("cleanup processed_events: %w", err)
	}
	pruned := tag.RowsAffected()
	if pruned > 0 {
		log.Ctx(ctx).Debug().Int64("pruned", pruned).Time("cutoff", cutoff).Msg("idempotency cleanup")
	}
	return CleanupResult{Pruned: pruned}, nil
}

// Cleanup runs CleanupOnce on a ticker until ctx is canceled.
// Intended to be started in a goroutine from cmd/main.
func (a *Applier) Cleanup(ctx context.Context, every, window time.Duration) {
	if every <= 0 {
		every = time.Hour
	}
	if window <= 0 {
		window = 30 * 24 * time.Hour
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := a.CleanupOnce(ctx, window); err != nil {
				log.Ctx(ctx).Warn().Err(err).Msg("idempotency cleanup tick failed")
			}
		}
	}
}

// PoolFromPgxpool is a tiny adapter so callers can pass a
// *pgxpool.Pool directly to NewApplier without importing the interface.
func PoolFromPgxpool(p *pgxpool.Pool) Pool { return p }
