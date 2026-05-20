# Go Micro Example

![Linter](https://github.com/sksmith/note-server/actions/workflows/lint.yml/badge.svg)
![Security](https://github.com/sksmith/note-server/actions/workflows/sec.yml/badge.svg)
![Test](https://github.com/sksmith/note-server/actions/workflows/test.yml/badge.svg)

This sample project was created as a collection of the various things I've learned about best
practices building microservices using Go. I structured the project using a hexagonal style abstracting
away business logic from dependencies like the RESTful API, the Postgres database, and RabbitMQ message queue.

## Structure

The Go community generally likes application directory structures to be as simple as possible which is
totally admirable and applicable for a small simple microservice. I could probably have kept everything
for this project in a single directory and focused on making sure it met twelve factors. But I'm a big
fan of [Domain Driven Design](https://martinfowler.com/bliki/DomainDrivenDesign.html), and how it gels so
nicely with [Hexagonal Architecture](https://alistair.cockburn.us/hexagonal-architecture/) and I wanted
to see how a Go microservice might look structured using them.

The layout is *domain-grouped under `internal/`*: each domain owns
its service, repository, HTTP transport, messaging adapters, DTOs,
and mocks in a single directory. Cross-cutting concerns live under
`internal/platform/`. The composition root (config load, dependency
wiring, HTTP listener, signal handling, shutdown) lives in
`internal/app/`, and `cmd/server/main.go` is a thin entry point that
delegates to it.

```text
cmd/
  server/main.go            # thin entry: load config, build app.Server, run
  demo/                     # DSN-015 demo orchestrator
  stub-catalog/             # DSN-018 stub upstream for the catalog client demo

internal/
  inventory/                # inventory + reservation domain
  user/                     # user domain
  auth/                     # JWT signer + /auth/token transport + auth middleware
  catalog/                  # outbound REST client (driven adapter)

  platform/                 # cross-cutting; no domain knowledge
    cache/                  # Redis + in-memory cache
    observability/          # tracing, correlation IDs
    ratelimit/              # token-bucket limiter (Redis + Lua)
    secrets/                # secrets provider abstraction
    idempotency/            # Kafka idempotency applier (+ rest/ for HTTP)
    events/                 # event envelope + JSON Schema registry
    httpx/                  # generic HTTP middleware, problem+json, pagination,
                            # rate-limit middleware, render helpers, Scrub
    messaging/
      kafka/                # franz-go producer/consumer
      amqp/                 # RabbitMQ publisher/subscriber primitives
    persistence/            # pgx pool, Conn/Transaction interfaces, mocks,
                            # migrations/

  app/                      # composition root
    server.go               # app.Server, New/NewWithDeps/Run/Cleanup
    routes.go               # router construction
    health.go, env.go,
    docs.go                 # /live, /ready, /admin/env, /openapi.yaml + /docs

  testutil/                 # test-only HTTP/logging helpers

api/
  client/v1/                # generated Go client (oapi-codegen)

config/                     # viper-backed config + defaults
deploy/                     # Kustomize base for Kubernetes deployment (OPS-004)
docs/                       # human-authored ADRs, error model, lifecycle notes
plan/                       # (gitignored) working tickets and matrix
scripts/, web/              # ops scripts, frontend source
```

![structure diagram](inventory.jpg)

> The structure diagram predates the DSN-028 restructure and shows
> the old `core/` / `api/` / `db/` / `queue/` layout. The semantics
> (hexagonal boundaries between domain, transport, and persistence)
> are preserved — only the directory layout changed.

## Running the Application Locally

This project requires that you have Docker, Go and Make installed locally. If you do, you can start
the application first by starting the docker-compose file, then start the application using the
supplied Makefile.

```shell
docker-compose -f ./scripts/docker-compose.yml up -d
make run
```

If you want to create a deployable executable and run it:

```shell
make build
./bin/inventory
```

### Run Docker Compose

```shell
docker-compose up
```

This brings up Postgres, RabbitMQ, the app, and a one-shot **demo
orchestrator** (DSN-015). After the app's `/ready` returns 200, the
orchestrator runs every registered capability step against the live
app and prints a summary table on its way out.

`make demo` is a convenience wrapper that runs the same stack with
`--abort-on-container-exit --exit-code-from demo`, so the whole stack
tears down when the demo finishes and your shell gets the demo's exit
code. Use `make demo-down` to wipe volumes if a step left state behind.

#### What you'll see

The `demo` service log ends with a table like:

```text
demo summary
────────────────────────────────────────────────────────────────────────
CAPABILITY     STEP                              STATUS    LATENCY TRACE
DSN-026        REST create + read via gener…     pass         32ms abc123…
────────────────────────────────────────────────────────────────────────
```

One row per registered step. `pass` everywhere means every advertised
capability fired end-to-end; the demo container exits 0. Any `fail`
includes the underlying error reason on the next line and the demo
container exits non-zero (the app keeps running for further poking).

Each capability ticket (DSN-016 through DSN-027) plugs into the
orchestrator by appending a `Step` to
[cmd/demo/steps.go](cmd/demo/steps.go). If the API breaks, the demo
breaks — it's a contract test as much as a demo.

## Application Features

### RESTful API

This application uses the wonderful [go-chi](https://github.com/go-chi/chi) for routing
[beautifuly documentation](https://github.com/go-chi/chi/blob/master/_examples/rest/main.go) served as the main 
inspiration for how to structure the API. Seriously, I was so impressed.

In Java I like to generate the controller layer using Open API so that the contract and implementation always match 
exactly. I couldn't quite find an equivalent solution I liked.

Truth be told, if I were doing inter-microservice communication I would strongly consider using gRPC rather than a 
RESTful API.

#### Pagination

List endpoints accept `?limit=…&offset=…`. `limit` defaults to `50`,
must be a positive integer, and is capped at `200`. `offset` defaults
to `0` and must be a non-negative integer. Invalid input returns a
`400 application/problem+json` with an `errors[]` extension naming the
offending field — values are never silently coerced.

Responses include an [RFC 8288](https://www.rfc-editor.org/rfc/rfc8288)
`Link` header. A `rel="next"` link is emitted when the server returned
a full page (`len(results) == limit`); a `rel="prev"` link is emitted
when `offset > 0`.

#### OpenAPI spec + Swagger UI

The application is annotated with [`swaggo/swag`](https://github.com/swaggo/swag);
`make openapi` regenerates [`internal/app/openapi.yaml`](internal/app/openapi.yaml) from the
handler comments. Generated artifacts are committed and a CI drift check
([.github/workflows/openapi.yml](.github/workflows/openapi.yml)) fails the
build when the spec or the Go client are stale.

| Endpoint        | Purpose                                                                |
| --------------- | ---------------------------------------------------------------------- |
| `/openapi.yaml` | OpenAPI 3.1 source-of-truth, embedded into the binary via `go:embed`.  |
| `/docs`         | Swagger UI (offline, bundled via `swaggest/swgui/v5emb`).              |

Both routes are gated by `docs.enabled` (default `true`). Set
`docs.enabled: false` (or `GME_DOCS_ENABLED=false`) in `prod` to keep the
spec internal.

Typed clients are generated alongside the spec:

| Target              | Command           | Output                |
| ------------------- | ----------------- | --------------------- |
| Go client           | `make clients-go` | `api/client/v1/`      |
| TypeScript types    | `make clients-ts` | `web/src/api/`        |
| Both                | `make clients`    | both                  |

The TS step shells out to `npx openapi-typescript@7` — it needs Node
locally and is intentionally out of CI's Go-only matrix. Reviewers spot
TS drift by hand for now.

### TLS termination

By default the HTTP server binds plaintext and TLS is terminated
upstream by an ingress controller, service-mesh sidecar, or local-dev
reverse proxy. The Caddy service in
[docker-compose.yml](docker-compose.yml) demonstrates this locally:
hit `https://localhost:8443` and Caddy reverse-proxies plaintext to
the app on the compose network.

The service can also serve HTTPS itself for single-host deployments
without a terminator:

| env var | default | meaning |
| --- | --- | --- |
| `GME_TLS_ENABLED` | `false` | When `true`, serve HTTPS via `ListenAndServeTLS`. |
| `GME_TLS_CERTFILE` | *empty* | Path to a PEM cert (required when `tls.enabled=true`). |
| `GME_TLS_KEYFILE` | *empty* | Path to the matching PEM private key. |

The in-process TLS profile is TLS 1.2+ with the Mozilla "intermediate"
AEAD cipher list. An [HSTS middleware](internal/platform/httpx/hsts.go)
sets `Strict-Transport-Security: max-age=31536000; includeSubDomains`
on responses to requests that arrived over TLS — either directly (
`r.TLS` non-nil) or via `X-Forwarded-Proto: https` from a terminator.
Plaintext responses get no header.

See [docs/deployment.md](docs/deployment.md) for the full deployment
posture and production checklist.

### Container image

The image is two-stage: a digest-pinned `golang:1.26-alpine` builder
compiles a static binary, and a digest-pinned
`gcr.io/distroless/static-debian12:nonroot` runtime stage holds only
the binary, the bundled `config.yml` defaults, and the migration
files. Pinning both stages by `@sha256:...` (SEC-011) means a rebuild
fetches the exact bytes CI saw last green; bump the digests in the
same PR that bumps the Go minor version.

The runtime stage runs as UID `65532:65532`
(distroless `nonroot`) declared explicitly via `USER` so admission
controllers (`runAsNonRoot`, Kyverno) can assert the value without
resolving base-image metadata. No shell, package manager, or libc
ships in the final image, so the attack surface for a compromised
process is just the Go binary and the kernel.

OCI labels (`org.opencontainers.image.source`, `revision`, `version`,
`created`, `licenses`, `title`, `description`) are set from
`make docker` build args so registry UIs and provenance tools can
trace any image back to its commit. `HEALTHCHECK` is intentionally
omitted — distroless has nothing to exec — and liveness/readiness
are wired to the `/live` and `/ready` endpoints (DSN-002) via the
Kubernetes pod spec instead.

### Rate limiting & request-size caps

Two layers of throttling protect the API, both backed by the same
distributed token-bucket limiter
([internal/platform/ratelimit](internal/platform/ratelimit/limiter.go),
DSN-021b) running a Redis Lua script for atomic
read-refill-subtract-write. Each layer uses its own Redis-key prefix
and its own metric scope label so the two buckets are independent —
a brute-force attacker exhausting their auth budget does not
consume the global budget.

| Layer | Routes covered | Default | Redis prefix / scope |
| --- | --- | --- | --- |
| `/auth/token` (SEC-002b / DSN-021b) | `POST /auth/token` only | 1 req/s, burst 5 | `rl:auth:` / `auth` |
| Global per-IP (SEC-007) | Every user-facing route under `/auth` and `/api/v1` | 50 req/s, burst 100 | `rl:global:` / `global` |

Probe and metrics endpoints (`/live`, `/ready`, `/metrics`) sit
outside both groups so Kubernetes and Prometheus scrapes are never
throttled. Buckets are keyed per source IP (`X-Forwarded-For` is
honoured when present, with `RemoteAddr` as the fallback) — a
production deployment behind a real load balancer should configure
the LB to write a trusted forwarded header.

Behaviour on every protected route:

| Scenario | Response |
| --- | --- |
| Under rate | Request forwarded. `X-RateLimit-Remaining` set on the response. |
| Over rate | 429 `application/problem+json`. `Retry-After` (seconds, minimum 1) and `X-RateLimit-Remaining` set. |
| Redis unreachable | Limiter degrades open — request forwarded with a `ratelimit_errors_total` increment. The alternative (deny-on-error) turns a Redis blip into an outage. |
| `GME_REDIS_URL` empty | Limiters not installed; all routes run un-throttled. The 1-MiB body cap still applies (it doesn't need Redis). |

Request body sizes are capped by an HTTP middleware in
[internal/platform/httpx/maxbytes.go](internal/platform/httpx/maxbytes.go).
Requests whose `Content-Length` exceeds the configured limit are
rejected upfront with 413 `application/problem+json`; chunked or
unknown-length bodies are truncated by `http.MaxBytesReader` and
surface as decode errors to the handler. The cap is always on —
it does not depend on Redis.

Config (env vars):

| env var | default | meaning |
| --- | --- | --- |
| `GME_RATELIMIT_AUTHRATEPERSECOND` | `1.0` | Token refill rate for `/auth/token`, per second. |
| `GME_RATELIMIT_AUTHBURST` | `5` | Bucket capacity for `/auth/token`. |
| `GME_RATELIMIT_GLOBALRATEPERSECOND` | `50.0` | Token refill rate for the global per-IP throttle. |
| `GME_RATELIMIT_GLOBALBURST` | `100` | Bucket capacity for the global per-IP throttle. |
| `GME_RATELIMIT_MAXREQUESTBODYBYTES` | `1048576` | Maximum request body size in bytes. `0` or negative disables the cap. |

Prometheus metrics: `ratelimit_allowed_total{scope}`,
`ratelimit_denied_total{scope}`, `ratelimit_errors_total`,
`ratelimit_eval_duration_ms`. `scope` is `auth` or `global`, so
dashboards can split the two layers without joining on Redis keys.

### Redis user cache

`internal/user` reads users through a Redis cache (DSN-021c), replacing
the previous in-process `hashicorp/golang-lru` cache. Same
cache-aside shape as the inventory read-path:

- `Get` checks the cache first; miss falls through to Postgres and
  populates the cache with a short TTL (default 60s).
- `Create` populates the cache with the new row.
- `Delete` invalidates the cached entry.
- TTL is the safety net: admin/role revocations propagate
  automatically when the cached row expires, without needing
  explicit cache-bust on the user-management endpoints.

The cache is opt-in via `usrrepo.dbRepo.SetCache(c, ttl)`; cmd/main
wires it when `GME_REDIS_URL` is set. An empty URL or a Redis outage
falls back to direct DB reads — auth still works, just slower.

| env var | default | meaning |
| --- | --- | --- |
| `GME_REDIS_USERCACHETTLSECONDS` | `60` | TTL for cached user rows, in seconds. |

Key shape: `user:{username}:v1`. Bumping the `v1` suffix drops every
cached row when the cached shape changes.

### Redis cache (inventory read-path)

`GET /api/v1/inventory/{sku}` reads through a Redis cache
([internal/platform/cache](internal/platform/cache/cache.go), DSN-020). The cache sits at the
*service* layer, not the handler — non-HTTP consumers (queue-driven
flows, future gRPC) hit the same path.

Cache-aside pattern:

- Miss → DB read → populate cache with a per-key TTL (default 5
  minutes) → return.
- Hit → return cached value, repository never touched.
- Successful write (Produce, fulfilled reservation) invalidates the
  per-SKU key via the `publishInventory` post-commit hook. The TTL is
  the safety net for a missed invalidation.

The cache is best-effort: an empty `redis.url` or a Redis outage
degrades to the DB path transparently — every request still succeeds,
just slower. A `cache_requests_total{prefix,outcome=hit|miss|error}`
counter surfaces the degradation so operators can spot a cache that's
silently down.

Key shape: `inv:product:{sku}:v1`. Bumping the `v1` suffix is the
global invalidation lever — drop every entry without touching Redis
directly when the cached shape changes.

Config (env vars):

| env var | default | meaning |
| --- | --- | --- |
| `GME_REDIS_URL` | empty | Redis connection URL (`redis://host:port/db`). Empty disables the cache. |
| `GME_REDIS_CACHETTLMINUTES` | `5` | TTL for cached ProductInventory entries. |

`/ready` adds Redis to its dependency map when the client is wired,
so a probe fails fast if Redis is unreachable. It also reports AMQP
under `amqp.inventory` and `amqp.product`: each queue tracks the
timestamp of its most-recent successful redial and reports unready
on startup until the first session arrives, and again if the
redial loop fails to obtain a session for more than 10 seconds
(TST-004).

### REST idempotency (Idempotency-Key)

Mutating routes (`PUT /api/v1/inventory/{sku}/productionEvent`, `PUT
/api/v1/reservation`) require an `Idempotency-Key` request header
(DSN-019). The middleware
([internal/platform/idempotency/rest](internal/platform/idempotency/rest/middleware.go)) caches the
response under the `(key, sha256(body))` pair with a configurable
TTL (default 24h, matching Stripe's contract):

| Scenario | Response |
| --- | --- |
| First request with a key | Handler runs; status + body + curated headers recorded. |
| Same key, same body, within TTL | Cached response replayed byte-for-byte. `Idempotent-Replay: true` is set so the client can tell. |
| Same key, different body | 409 `application/problem+json` (Stripe-style conflict). |
| Key missing | 400 `application/problem+json` (header is required on mutating routes). |
| TTL expired | Treated as a first request — handler re-runs. |
| Handler returned 5xx | Not cached — the next retry gets a fresh attempt. |

The middleware is pluggable behind a small `Store` interface. When
`GME_REDIS_URL` is set, DSN-021a backs the store with Redis so
cross-replica retries replay correctly; otherwise it falls back to
an in-memory store that's sufficient for single-replica deployments.
The contract is in [internal/platform/idempotency/rest/store.go](internal/platform/idempotency/rest/store.go).

Two layers of dedupe sit on top of each other deliberately: the
middleware guards safe retries by replaying the original *response*,
while the inventory service's existing `request_id` guard guarantees
that even if the middleware mis-fires (process restart, replica
failover before DSN-021 lands) a duplicate production won't post.

Config (env vars):

| env var | default | meaning |
| --- | --- | --- |
| `GME_IDEMPOTENCY_TTLMINUTES` | `1440` | Retention window for cached responses, in minutes. |

Prometheus counters: `rest_idempotency_hits_total`,
`rest_idempotency_saves_total`,
`rest_idempotency_conflicts_total`,
`rest_idempotency_missing_header_total`.

### Outbound REST client (catalog)

The inventory `GET /api/v1/inventory/{sku}` response is optionally
enriched by an outbound call to a stub "catalog" upstream
([internal/catalog](internal/catalog/client.go), DSN-018). The client wraps
`*http.Client` with explicit timeouts, bounded retries with
exponential backoff + jitter (5xx and transport errors only — 4xx is
treated as a non-recoverable caller bug), `otelhttp.Transport` for
trace propagation, and `X-Request-Id` header injection from the
inbound request's correlation context (DSN-005).

The upstream itself is a tiny stub at
[cmd/stub-catalog](cmd/stub-catalog/main.go) wired into
[docker-compose.yml](docker-compose.yml); the `make demo` orchestrator
hits the enriched endpoint and asserts the catalog block is present.

Catalog failures degrade gracefully: a timeout, 5xx-after-retries,
404, or upstream outage leaves the inventory response intact — the
`catalog` JSON field is simply omitted. Enrichment is best-effort,
never a hard dependency of the request.

Config (env vars):

| env var | default | meaning |
| --- | --- | --- |
| `GME_CATALOG_BASEURL` | empty | Catalog base URL. Empty disables the client entirely. |
| `GME_CATALOG_TIMEOUTMS` | `3000` | Total deadline for one Lookup including retries. |
| `GME_CATALOG_PERATTEMPTMS` | `1000` | Per-attempt HTTP timeout. |
| `GME_CATALOG_MAXATTEMPTS` | `3` | Total attempts (1 initial + N-1 retries). |

Prometheus metrics emitted by the client:
`catalog_client_requests_total{outcome}`,
`catalog_client_retries_total`, `catalog_client_failures_total`,
`catalog_client_lookup_duration_ms`.

### Authentication

Endpoints under `/api/v1` are protected by the
[Authenticate middleware](api/middleware.go), which requires a Bearer JWT. Callers exchange
credentials at `/auth/token` (the only endpoint that accepts HTTP Basic) for a short-lived JWT,
then present that token on subsequent requests. Users are stored in the database with
bcrypt-hashed passwords and locally cached using
[golang-lru](https://github.com/hashicorp/golang-lru). In a production setting if I actually
wanted caching I'd either use a remote cache like Redis, or a distributed local cache like
groupcache to prevent stale or out of sync data.

#### Getting a JWT

`POST /auth/token` with HTTP Basic credentials returns a short-lived bearer token:

```sh
curl -u alice:s3cret -X POST http://localhost:8080/auth/token
# {"access_token":"eyJhbGciOi...","token_type":"Bearer","expires_in":900}

curl -H "Authorization: Bearer eyJhbGciOi..." http://localhost:8080/api/v1/inventory
```

Issued tokens carry `sub`, `iss`, `aud`, `iat`, `exp`, `jti`, and a `roles` claim
(`["admin"]` for admins, `[]` otherwise). bcrypt is invoked **only** at token issuance — subsequent
requests verify the JWT signature, which is much faster than bcrypt at default cost.

Signing config (env vars; see [.env.example](.env.example)):

| env var | required | default | meaning |
| --- | --- | --- | --- |
| `GME_JWT_SIGNING_KEY` | yes in `prod` | random ephemeral key | HMAC-SHA256 signing key, minimum 32 bytes. |
| `GME_JWT_TTL_SECONDS` | no | 900 (15 min) | Token lifetime. |

In `prod` profile the application refuses to start if `GME_JWT_SIGNING_KEY` is missing or shorter
than 32 bytes. In `local` / `dev` an ephemeral key is generated, with a loud WARN that issued
tokens will not survive a restart.

#### Bootstrap admin

On startup the application ensures an `admin` user exists. Behavior depends on the running profile:

- **`local` / `dev` (default):** if no admin exists, one is created. If `BOOTSTRAP_ADMIN_PASSWORD` is set, that
  password is used; otherwise a 32-character random password is generated and printed to the log **once** with a
  warning. Capture it on first run — it will not be shown again.
- **`prod`:** the application refuses to start if no admin exists and `BOOTSTRAP_ADMIN_PASSWORD` is unset. Set the
  env var (typically via your secret manager) and start.

Older databases that ran the seed `admin:admin` migration are detected and replaced on startup. Newly-created
databases never carry the seed credential.

```sh
# local: pick your own password
BOOTSTRAP_ADMIN_PASSWORD=please-change-me go run ./cmd

# local: let it generate one and watch the logs
go run ./cmd
# {"level":"warn","username":"admin","password":"<base64 token>","message":"bootstrap admin user created with auto-generated password..."}
```

### Metrics

This application outputs prometheus metrics using middleware I plugged into the go-chi router. If you're running
locally check them out at [http://localhost:8080/metrics](http://localhost:8080/metrics). Every URL automatically
gets a hit count and a latency metric added. You can find the configurations [here](api/middleware.go).

In addition to the per-URL metrics, the auth middleware exposes two counters:

| Metric | Meaning |
| --- | --- |
| `auth_basic_requests_total` | Retained from the SEC-002 migration; stays flat at zero now that Basic Auth is no longer accepted on protected routes (SEC-002c). |
| `auth_jwt_requests_total` | Successful Bearer JWT requests. |

Neither counter increments on auth failure.

### Logging

I ended up going with [zerolog](https://github.com/rs/zerolog) for logging in this project. I really like its API and 
their benchmarks look really great too! You can get structured logging or nice human-readable logging by
[changing some configs](config.yml#L10)

Every request flows through `api.CorrelationLogger`, which honours an inbound `X-Request-Id`
(generates one if absent) and binds `request_id` — plus `trace_id` / `span_id` when an OTel span
is recording — onto a child logger attached to the request context. Downstream code uses
`log.Ctx(ctx)` to pick up the correlated logger; the AMQP layer ferries `request_id` across
queue boundaries via the `x-request-id` header. See [docs/observability.md](docs/observability.md)
for the full picture.

#### Sensitive-value redaction (SEC-010)

The request logger emits the URI to give operators a useful trace, but the URI
sometimes carries credentials in query parameters (`?token=…`, `?code=…`, OAuth
callbacks, etc.). The `Logging` middleware runs every URI through
[`httpx.RedactURI`](internal/platform/httpx/redact.go) before logging it,
replacing the values for keys in `httpx.SensitiveQueryParams` with `[REDACTED]`.
The set is documented in `redact.go` and covers OAuth/OIDC parameters
(`token`, `access_token`, `refresh_token`, `id_token`, `code`, `state`),
explicit credentials (`password`, `secret`, `api_key`), and session
identifiers — case-insensitive. Non-sensitive query keys round-trip unchanged
so log lines stay diffable.

Authorization headers and cookies are not logged anywhere in this service —
the request logger only emits the URI and the `Origin` header. If a future
change starts logging additional headers, route them through the same
redaction helper.

### Configuration

This project uses [viper](https://github.com/spf13/viper) for handling externalized configurations. At the moment it
only reads from the local config.yml but the plan is to make it compatible with
[Spring Cloud Config](https://cloud.spring.io/spring-cloud-config), and [etcd](https://etcd.io).

#### Secrets and credentials

Credentials never live in tracked files. The four sensitive viper keys — `db.user`, `db.pass`, `rabbitmq.user`,
`rabbitmq.pass` — are blank in `config.yml` and read from env vars at startup. Names are upper-snake with the `GME_`
prefix:

| viper key | env var |
| --- | --- |
| `db.user` | `GME_DB_USER` |
| `db.pass` | `GME_DB_PASS` |
| `rabbitmq.user` | `GME_RABBITMQ_USER` |
| `rabbitmq.pass` | `GME_RABBITMQ_PASS` |

Additionally, `BOOTSTRAP_ADMIN_PASSWORD` (no prefix) seeds the admin user — see [Bootstrap admin](#bootstrap-admin).

Three convenient ways to supply these locally:

1. **Inline:** `GME_DB_PASS=postgres go run ./cmd`
2. **`.env` file** in the repo root (gitignored). Sourced manually (`set -a; source .env; set +a`) or via tools like
   `direnv`. See [.env.example](.env.example) for the shape.
3. **`config.local.yml`** in the repo root (gitignored). Same schema as `config.yml`; merged on top at startup.

In production, the env vars are populated by a **secrets provider** at process startup. Selection is via
`GME_SECRETS_PROVIDER`:

| `GME_SECRETS_PROVIDER` | Reads from | Use case |
| --- | --- | --- |
| unset / `env` | shell env, `.env` file | dev, CI, `go run` |
| `file` | `GME_SECRETS_DIR` (default `/vault/secrets`) | Vault Agent injector in K8s |

The application itself talks to no external secret store — the Vault Agent sidecar renders templates into a
shared tmpfs and the app reads files. See [docs/adr/0001-secrets-management.md](docs/adr/0001-secrets-management.md)
for the architectural decision and the rotation playbook.

The required-secret list lives in [internal/platform/secrets/provider.go](internal/platform/secrets/provider.go) (`secrets.Required`); the
provider's `Load` fails fast at startup if any are missing.

#### Local stack credentials

[docker-compose.yml](docker-compose.yml) parameterises the Postgres / pgAdmin / RabbitMQ admin credentials with the
`${VAR:-default}` pattern, so the literal passwords no longer live in tracked YAML. Defaults remain dev-friendly
(`postgres`, `guest`, `admin`) so `docker-compose up -d` still works out of the box for new contributors.

### Testing

I chose not to go with any of the test frameworks when putting this project together. I felt like using interfaces and 
injecting dependencies would be enough to allow me to mock what I need to. There's a fair bit of boilerplate code 
required to mock, say, the inventory repository but not having to pull in and learn yet another dependency for testing 
seemed like a fair tradeoff.

The testing in this project is pretty bare-bones and mostly just proof-of-concept. If you want to see some tests, 
though, they're in [api](api). I personally prefer more integration tests that test an application front-to-back for 
features rather than tons and tons of tightly-coupled unit tests.

The default `go test ./...` runs the unit tests only. To run the full local
check matrix in one go, use `make verify`. See [Make targets](#make-targets)
below for what each step does.

The integration tests in [cmd/integration_test.go](cmd/integration_test.go)
require Postgres and RabbitMQ (see [docker-compose.yml](docker-compose.yml))
and are gated behind the `integration` build tag. Run them with:

```shell
docker-compose up -d
go test -tags=integration ./cmd/...
```

### Make targets

`make verify` is the entry point most contributors want — it chains every
local check and fails fast on the first one. The individual targets are also
exposed so you can run a single step in isolation:

| Target | What it does |
| --- | --- |
| `make tools` | Installs `golangci-lint`, `gosec`, `govulncheck` (pinned), and `gofumpt` into `$(go env GOPATH)/bin`. Run once before `make verify`, and re-run when bumping versions. |
| `make precommit-install` | Installs the local pre-commit hooks defined in [.pre-commit-config.yaml](.pre-commit-config.yaml) — `gofumpt`, `golangci-lint`, `gitleaks` (OPS-003). Requires the `pre-commit` CLI (`brew install pre-commit` on macOS, `pip install pre-commit` elsewhere) and `$(go env GOPATH)/bin` on `PATH` so the hooks can find `gofumpt` and `golangci-lint` after `make tools`. |
| `make fmt` | Runs `gofmt -l .` and exits non-zero if any file needs formatting. Does not modify files — fix with `gofmt -w <file>`. The `lint` step additionally enforces gofumpt's stricter rules via golangci-lint's formatter pipeline. |
| `make vet` | Runs `go vet ./...`. |
| `make lint` | Runs `golangci-lint run` with the project defaults. |
| `make sec` | Runs `gosec ./...` (CWE-tagged static analysis). |
| `make vuln` | Runs `govulncheck ./...` against the [Go vulnerability database](https://pkg.go.dev/vuln). Fails on any finding — stdlib findings are cleared by bumping `go.mod`'s `toolchain` directive. |
| `make test-race` | Runs `go test -race -count=1 -timeout 60s ./...`. |
| `make verify` | Runs `fmt`, `vet`, `lint`, `sec`, `vuln`, `test-race`, and `openapi-check` in order — seven checks, fail-fast. |
| `make test` | Runs `go test -cover ./...` without race detection — quick smoke check. |
| `make build` | Builds the binary into `./bin/go-micro-example` with version metadata baked in. |
| `make run` | Runs the application via `go run ./cmd/.`. Requires Postgres and RabbitMQ. |
| `make docker` | Builds the Docker image. |
| `make demo` | Brings up the full docker-compose stack and runs the DSN-015 orchestrator end-to-end. Tears everything down on exit and propagates the demo container's exit code. |
| `make demo-down` | `docker compose down -v` — tears down the demo stack and wipes volumes. |
| `make openapi` | Regenerates `api/openapi.yaml` + `api/openapi.json` from handler annotations (DSN-026). |
| `make clients` | Regenerates Go (`api/client/v1`) and TS (`web/src/api`) clients from the spec. |
| `make openapi-check` | CI drift gate: regenerates spec + Go client and fails on diff. |

If `make tools` has not been run yet, `lint`, `sec`, and `vuln` will
fail with `command not found`; install the tools first.

### Database Migrations

I'm using the [migrate](https://github.com/golang-migrate/migrate) project to manage database migrations.

```shell
migrate create -ext sql -dir internal/platform/persistence/migrations -seq create_products_table

migrate -database postgres://postgres:postgres@localhost:5432/smfg-db?sslmode=disable -path internal/platform/persistence/migrations up

migrate -source file://internal/platform/persistence/migrations -database postgres://localhost:5432/database down
```

## 12 Factors

One of the goals of this service was to ensure all [12 principals](https://12factor.net/) of a 12-factor app are adhered 
to. This was a nice way to make sure the app I built offered most of what you need out of a Spring Boot application.

### I. Codebase

The application is stored in my git repository.

### II. Dependencies

Go handles this for us through its dependency management system (yay!)

### III. Config

See the [configuration section](#Configuration) section above.

### IV. Backing Services

The application connects to all external dependencies (in this case, RabbitMQ, and Postgres) via URLs which it gets from 
remote configuration.

### V. Build, release, run

The application can easily be plugged into any CI/CD pipeline. This is mostly thanks to Go making this easy through 
great command line tools.

### VI. Processes

This app is not *strictly* stateless. There is a cache in the user repository. This was a design choice I made in the 
interest of seeing what setting up a local cache in go might look like. In a more real-world application you would 
probably want an external cache (like Redis), or a distributed cache (like 
[Group Cache](https://github.com/golang/groupcache) - which is really cool!)

This app is otherwise stateless and threadsafe.

### VII. Port Binding

The application binds to a supplied port on startup.

### VIII. Concurrency

Other than maintaining an instance-based cache (see Process above), the application will scale horizontally without 
issue. The database dependency would need to scale vertically unless you started using sharding, or a distributed data 
store like [Cosmos DB](https://docs.microsoft.com/en-us/azure/cosmos-db/distribute-data-globally).

### IX. Disposability

One of the wonderful things about Go is how *fast* it starts up. This application can start up and shut down in a 
fraction of the time that similar Spring Boot microservices. In addition, they use a much smaller footprint. This is 
perfect for services that need to be highly elastic on demand.

### X. Dev/Prod Parity

Docker makes standing up a prod-like environment on your local environment a breeze. This application has
[a docker-compose file](scripts/docker-compose.yml) that starts up a local instance of rabbit and postgres. This 
obviously doesn't account for ensuring your dev and stage environments are up to snuff but at least that's a good start 
for local development.

### XI. Logs

Logs in the application are written to the stdout allowing for logscrapers like 
[logstash](https://www.elastic.co/logstash) to consume and parse the logs. Through configuration the logs can output as 
plain text for ease of reading during local development and then switched after deployment into json structured logs for 
automatic parsing.

### XII. Admin Processes

Database migration is automated in the project using [migrate](https://github.com/golang-migrate/migrate).

## Contributing

- [Error handling conventions](docs/errors.md) — sentinels, wrapping
  with `%w`, `errors.Is`/`errors.As`, log shape. Enforced by
  `errorlint` and `errname` in CI.
- [Process lifecycle](docs/lifecycle.md) — startup ordering and the
  graceful-shutdown sequence (DSN-001).
- [Observability](docs/observability.md) — Prometheus metrics + the
  OpenTelemetry tracing setup (DSN-004).
- [Messaging contracts](docs/messaging.md) — domain-event envelope,
  schema registry, compatibility policy (DSN-012).

## TODO

- [ ] Recreate architecture diagram
- [ ] Add godoc
- [ ] Return 204 no content if data already exists
- [ ] Cleanup TODOs
