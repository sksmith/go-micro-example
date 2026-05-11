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

The starting point of the application is under the [cmd](cmd/main.go) directory. The "domain"
core of the application where all business logic should reside is under the [core](core)
directory. The other directories listed there are each of the external dependencies for the project.

![structure diagram](inventory.jpg)

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
`make openapi` regenerates [`api/openapi.yaml`](api/openapi.yaml) from the
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

### Redis cache (inventory read-path)

`GET /api/v1/inventory/{sku}` reads through a Redis cache
([core/cache](core/cache/cache.go), DSN-020). The cache sits at the
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
so a probe fails fast if Redis is unreachable.

### REST idempotency (Idempotency-Key)

Mutating routes (`PUT /api/v1/inventory/{sku}/productionEvent`, `PUT
/api/v1/reservation`) require an `Idempotency-Key` request header
(DSN-019). The middleware
([idempotency/rest](idempotency/rest/middleware.go)) caches the
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
The contract is in [idempotency/rest/store.go](idempotency/rest/store.go).

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
([core/catalog](core/catalog/client.go), DSN-018). The client wraps
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

The required-secret list lives in [core/secrets/provider.go](core/secrets/provider.go) (`secrets.Required`); the
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
| `make tools` | Installs `golangci-lint` and `gosec` into `$(go env GOPATH)/bin`. Run once before `make verify`, and re-run when bumping versions. |
| `make fmt` | Runs `gofmt -l .` and exits non-zero if any file needs formatting. Does not modify files — fix with `gofmt -w <file>`. |
| `make vet` | Runs `go vet ./...`. |
| `make lint` | Runs `golangci-lint run` with the project defaults. |
| `make sec` | Runs `gosec ./...` (CWE-tagged static analysis). |
| `make test-race` | Runs `go test -race -count=1 -timeout 60s ./...`. |
| `make verify` | Runs `fmt`, `vet`, `lint`, `sec`, and `test-race` in order. |
| `make test` | Runs `go test -cover ./...` without race detection — quick smoke check. |
| `make build` | Builds the binary into `./bin/go-micro-example` with version metadata baked in. |
| `make run` | Runs the application via `go run ./cmd/.`. Requires Postgres and RabbitMQ. |
| `make docker` | Builds the Docker image. |
| `make demo` | Brings up the full docker-compose stack and runs the DSN-015 orchestrator end-to-end. Tears everything down on exit and propagates the demo container's exit code. |
| `make demo-down` | `docker compose down -v` — tears down the demo stack and wipes volumes. |
| `make openapi` | Regenerates `api/openapi.yaml` + `api/openapi.json` from handler annotations (DSN-026). |
| `make clients` | Regenerates Go (`api/client/v1`) and TS (`web/src/api`) clients from the spec. |
| `make openapi-check` | CI drift gate: regenerates spec + Go client and fails on diff. |

If `make tools` has not been run yet, `lint` and `sec` will fail with
`command not found`; install the tools first.

### Database Migrations

I'm using the [migrate](https://github.com/golang-migrate/migrate) project to manage database migrations.

```shell
migrate create -ext sql -dir db/migrations -seq create_products_table

migrate -database postgres://postgres:postgres@localhost:5432/smfg-db?sslmode=disable -path db/migrations up

migrate -source file://db/migrations -database postgres://localhost:5432/database down
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
