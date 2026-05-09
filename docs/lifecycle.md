# Process lifecycle

## Startup

[cmd/main.go](../cmd/main.go) builds dependencies in this order
and fails fast (`log.Fatal`) on any step:

1. `secrets.LoadFromEnv` populates `GME_*` env vars from the
   configured provider (env / Vault Agent files — DSN-006).
2. `config.Load` reads YAML + binds env via viper.
3. `db.ConnectDb` opens the pgx pool and (optionally) runs
   `migrate.Up`.
4. `queue.NewInventoryQueue` and `queue.NewProductQueue` start
   their redial loops; both threaded with the signal-aware
   context so they unwind on shutdown.
5. `user.Bootstrap` ensures an admin exists.
6. `auth.NewSigner` constructs the JWT signer (with `strict=true`
   in `prod`).
7. The chi router is mounted; `srv.ListenAndServe` runs in a
   goroutine.

## Shutdown (DSN-001)

`signal.NotifyContext(SIGINT, SIGTERM)` drives an ordered
teardown. The shutdown deadline defaults to **30 seconds** and is
overridable with `GME_SHUTDOWN_TIMEOUT_SECONDS` (positive integer,
matching the `GME_JWT_TTL_SECONDS` pattern). Make sure the
deadline stays **below** the orchestrator's grace period — for
Kubernetes that's `terminationGracePeriodSeconds`, default 30s,
so set both together if you change one.

When a signal arrives:

1. **HTTP** — `srv.Shutdown(timeoutCtx)` stops the listener and
   waits for in-flight requests to finish. If the timeout
   expires, `srv.Close()` drops remaining idle connections.
2. **Database** — `pool.Close()` returns connections to Postgres
   gracefully (pgxpool blocks until borrowed connections are
   released).
3. **Queue (deferred to TST-003)** — the redial loops in
   [queue/queue.go](../queue/queue.go) cooperate with the cancelled
   context, so `NewInventoryQueue` / `NewProductQueue` exit when
   the next session boundary is reached. They do **not** yet
   "finish in-flight deliveries then exit" — that's TST-003's
   scope, since it requires the queue to grow a `Close` method
   alongside the test suite.

## Verifying graceful shutdown locally

```sh
# Terminal A
make run

# Terminal B — fire a slow-ish request, then Ctrl-C terminal A
# while it's in flight. Expect the curl to return 200, not
# "connection reset", and Terminal A should log
# "HTTP server stopped cleanly" before exiting.
curl -i http://localhost:8080/health
```

Automated coverage lives in
[cmd/shutdown_test.go](../cmd/shutdown_test.go):

- `TestShutdownHTTPDrainsInFlight` — request that's already
  running when shutdown starts must complete with 200.
- `TestShutdownHTTPHonorsDeadline` — a hanging request must not
  block shutdown indefinitely; `Close()` falls back when the
  deadline expires.
- `TestResolveShutdownTimeout` — env-var parsing branches.

## Health probes (DSN-002)

The router exposes two endpoints for orchestrators to poll:

| Endpoint | Status | Purpose |
| ----------- | -------- | --------- |
| `/live` | always 200 | **Liveness.** The process is up enough to handle a request. Failing this means kubelet should restart the pod. Does not depend on any external system. |
| `/ready` | 200 if every registered `Pinger` returns nil within 1s, 503 otherwise | **Readiness.** The pod is ready to accept traffic. Currently checks the pgx pool. AMQP is intentionally absent — the queue subsystem doesn't yet expose a non-blocking connectivity check (see follow-up). |

Each `/ready` dep gets its own per-check 1s deadline so a wedged
backend can't hang the probe past kubelet's `timeoutSeconds`.
Failures are listed in the response body, one `name: reason` per
line.

### Configuring `/ready` deps

`api.ConfigureRouter` takes a `map[string]api.Pinger` parameter.
[cmd/main.go](../cmd/main.go) currently passes `{"db": dbPool}`;
add new keys here as new external dependencies arrive (cache,
search, etc.).

`Pinger` is the minimal `Ping(ctx context.Context) error`
interface — `*pgxpool.Pool` satisfies it directly.

## Known gaps

- Queue consumer drain (see TST-003).
- No structured "shutdown phase" metric. If you're debugging a
  slow shutdown, the four `log.Info` lines emitted by
  `shutdown` / `shutdownHTTP` are your timeline.
- AMQP readiness — the queue's redial loop doesn't surface
  connection state. Tracked alongside TST-003.
