# Deployment

Kustomize base for running `go-micro-example` on a Kubernetes
cluster (OPS-004). The base is intentionally deployable on its own;
real environments wrap it in `deploy/overlays/<env>/` with patches
for replica counts, the image tag, ConfigMap values (collector
endpoint, CORS origins, etc.), and any cluster-specific labels.

```text
deploy/
├── base/
│   ├── kustomization.yaml   # stitches resources, namespace, labels
│   ├── deployment.yaml      # pod spec + security context + probes
│   ├── service.yaml         # ClusterIP
│   ├── configmap.yaml       # non-secret GME_* / OTEL_* env vars
│   ├── externalsecret.yaml  # ESO → Vault → in-cluster Secret
│   ├── networkpolicy.yaml   # default-deny + per-dependency allows
│   └── hpa.yaml             # CPU-based autoscaler
├── overlays/
│   ├── local/                   # K8S-002/003/004/006/009/010/011/012/013 (plain Secret)
│   │   ├── kustomization.yaml
│   │   ├── namespace.yaml
│   │   ├── secret.yaml          # plain dev Secret
│   │   ├── postgres.yaml
│   │   ├── rabbitmq.yaml
│   │   ├── pgadmin.yaml         # K8S-009 pgAdmin UI for Postgres
│   │   ├── metrics-server.yaml
│   │   ├── kube-network-policies.yaml  # NetworkPolicy enforcer
│   │   ├── otel-collector.yaml  # K8S-006 OTel collector
│   │   ├── jaeger.yaml          # K8S-010 Jaeger UI backend
│   │   ├── loki.yaml            # K8S-011 Loki single-binary
│   │   ├── promtail.yaml        # K8S-011 Promtail log shipper
│   │   ├── grafana.yaml         # K8S-011/012 Grafana UI
│   │   ├── prometheus.yaml      # K8S-012 annotation-scrape Prometheus
│   │   └── headlamp.yaml        # K8S-013 Kubernetes UI
│   └── local-eso/               # K8S-005 (ESO + dev Vault)
│       ├── kustomization.yaml
│       ├── external-secrets-operator.yaml
│       ├── vault.yaml           # in-cluster Vault dev mode
│       ├── secretstore.yaml     # ClusterSecretStore vault-backend
│       └── bootstrap-job.yaml   # seeds Vault kv paths
└── kind/
    └── cluster.yaml         # K8S-001 local Tier 1 validation gate
```

## Quick start

```sh
# Render to stdout (sanity-check the output):
kustomize build deploy/base

# Apply to the current kube-context:
kubectl apply -k deploy/base
```

The base targets the `go-micro-example` namespace. Create it first
(or override via `kustomize edit set namespace`):

```sh
kubectl create namespace go-micro-example
```

## Cluster prerequisites

| Capability | Why |
| --- | --- |
| Kubernetes ≥ 1.27 | `autoscaling/v2` HPA, NetworkPolicy v1, ExternalSecret v1 |
| metrics-server | The HPA reads pod CPU from `metrics.k8s.io` |
| [External Secrets Operator](https://external-secrets.io) | Materialises the `go-micro-example-secrets` Secret from Vault |
| A `ClusterSecretStore` named `vault-backend` | Resolves `go-micro-example/db`, `go-micro-example/rabbitmq`, `go-micro-example/jwt` paths |
| In-cluster Postgres, RabbitMQ, (optional) Redis, OTel collector | Service names match the labels the NetworkPolicy uses |

If your dependencies don't carry the labels the NetworkPolicy
selects on (`app.kubernetes.io/name: postgresql`, `rabbitmq`, etc.),
either re-label them or patch `networkpolicy.yaml` in an overlay.
The base intentionally uses label-selectors (not
namespace-selectors) so it's portable across cluster layouts.

## What's baked into the base

### Pod security (SEC-011 + OPS-004)

- `runAsNonRoot: true`, `runAsUser: 65532` (matches the distroless
  image's nonroot UID).
- `readOnlyRootFilesystem: true` with an `emptyDir` at `/tmp` so
  the Go runtime has somewhere to spill profile data.
- `allowPrivilegeEscalation: false`, all capabilities dropped.
- `seccompProfile: RuntimeDefault`.
- `terminationGracePeriodSeconds: 30` — matches the
  `http.Server.Shutdown` + AMQP/Kafka drain budget from DSN-001.

### Probes (DSN-002 + TST-004)

- **Liveness** on `/live`: simple liveness signal — the process
  is running and the HTTP server is responsive.
- **Readiness** on `/ready`: aggregates the per-dependency
  pingers (Postgres, Redis, AMQP via TST-004). 503 keeps the pod
  out of Service rotation until every dep is healthy.
- **Startup** probe on `/ready` with a 150 s budget so DB
  migrations and the initial AMQP redial have time to finish
  before liveness starts judging.

### Network policy

Default-deny egress with explicit allow-lists:

- kube-dns (UDP/TCP 53) — required for in-cluster DNS resolution
- Postgres (TCP 5432)
- RabbitMQ (TCP 5672)
- Redis (TCP 6379) — only if your overlay sets `GME_REDIS_URL`
- OpenTelemetry collector (TCP 4317, OTLP/gRPC)

Ingress: from `ingress-nginx` and `prometheus` on TCP 8080. Tune
in an overlay if you run a different ingress controller or scraper.

### Autoscaling

`HorizontalPodAutoscaler` v2 with a 70 % CPU utilisation target
between 2 and 10 replicas. A custom `http_requests_per_second`
metric is commented out — uncomment after wiring the
[Prometheus Adapter](https://github.com/kubernetes-sigs/prometheus-adapter)
to expose it through the external metrics API.

### Secrets

The Deployment's `envFrom` references a Secret named
`go-micro-example-secrets`. The `ExternalSecret` resource creates
it by pulling from Vault — see `externalsecret.yaml` for the path
layout. If your cluster doesn't run ESO, swap that file for a
manually-managed Secret with the same keys.

## Building an overlay

Bring up a `local` or `staging` overlay by copying the base
labels and patching what's different:

```sh
mkdir -p deploy/overlays/staging
cat > deploy/overlays/staging/kustomization.yaml <<'YAML'
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
namespace: go-micro-example
resources:
  - ../../base
images:
  - name: ghcr.io/sksmith/go-micro-example
    newTag: v1.2.3-staging
patches:
  - target:
      kind: ConfigMap
      name: go-micro-example-config
    patch: |-
      - op: replace
        path: /data/OTEL_EXPORTER_OTLP_ENDPOINT
        value: http://otel-collector.observability:4317
      - op: replace
        path: /data/GME_CORS_ALLOWEDORIGINS
        value: https://staging.example.com
YAML
```

`kustomize build deploy/overlays/staging` renders the patched
output.

## Validating before apply

```sh
# Schema validation against the upstream OpenAPI spec.
# Skip schemas it can't resolve (e.g. third-party CRDs):
kustomize build deploy/base | kubeconform -strict -ignore-missing-schemas -summary

# Dry-run against your live cluster (catches admission-policy
# rejections that schema validation alone misses):
kustomize build deploy/base | kubectl apply --dry-run=server -f -
```

### Local Tier 1 validation against a real apiserver (K8S-001)

`kubeconform` validates schemas offline. To also exercise the
apiserver's admission path — admission controllers, RBAC, label
selector mismatches, kustomize/apiserver drift — stand up a local
[`kind`](https://kind.sigs.k8s.io/) cluster and dry-run-apply the
base against it:

```sh
make k8s-up         # creates a single-node kind cluster pinned to
                    # the kube version in deploy/kind/cluster.yaml
make k8s-validate   # renders the base, filters ExternalSecret, and
                    # `kubectl apply --dry-run=server`s the rest
make k8s-down       # tears the cluster down
```

`make k8s-validate` filters out the `ExternalSecret` resource
because the external-secrets.io CRDs are not installed in this
Tier 1 cluster — K8S-005 wires External Secrets Operator and
removes the filter. Everything else in the base goes through the
apiserver unchanged.

**Local prereqs:** `kind`, `kubectl`, and
[`yq`](https://github.com/mikefarah/yq) on `PATH`. The make targets
print an install hint if any of them is missing.

CI runs the same gate via `.github/workflows/k8s-validate.yml`,
path-filtered to PRs that touch `deploy/**`. It uses
[`helm/kind-action`](https://github.com/helm/kind-action) (pinned
by commit SHA) to provision the cluster, then invokes both
`make k8s-validate` (base) and `make k8s-validate-local` (overlay).

### Run a Ready pod locally (K8S-002 + K8S-003)

The local overlay (`deploy/overlays/local`) layers four things on
top of the base:

- An image rewrite + `imagePullPolicy: Never` so the kubelet uses a
  locally-built `go-micro-example:dev` image (K8S-002).
- A `$patch: delete` that strips the base `ExternalSecret` — the
  external-secrets.io CRDs aren't installed at this tier (K8S-005's
  territory), and the overlay supplies a plain `Secret` of the same
  name instead.
- A plain `Secret` (`go-micro-example-secrets`) carrying the dev
  credentials the Deployment's `envFrom` expects (matches the
  docker-compose defaults).
- In-cluster `postgres` and `rabbitmq` Deployments + Services with
  the `app.kubernetes.io/name` labels the base NetworkPolicy
  selects on. RabbitMQ loads its exchanges/queues/bindings from
  `scripts/rabbitmq/definitions.json` via a kustomize-generated
  ConfigMap, so the DSN-015 topology is in place on first boot.

One-shot bring-up and tear-down:

```sh
make k8s-local-up    # k8s-up + docker-load + apply + rollout status
make k8s-local-down  # delete overlay + k8s-down
```

`k8s-local-up` blocks on `kubectl rollout status` until the app
Deployment is Available, which (via the readiness probe in
DSN-002 + TST-004) means migrations completed and AMQP redial
succeeded.

Verify the dependencies are up and the app talked to both of them:

```sh
kubectl -n go-micro-example get pods
# go-micro-example-…  …/1   Running   # see TST-005 below for /1 caveat
# postgres-…          1/1   Running
# rabbitmq-…          1/1   Running

kubectl -n go-micro-example logs deploy/go-micro-example \
  | grep -E 'executing migrations|ready to publish messages|listening'
# INF executing migrations
# DBG ready to publish messages exchange=inventory.exchange
# DBG ready to publish messages exchange=reservation.exchange
# INF listening port=8080
# INF listening for messages queue=product.queue
# DBG ready to publish messages exchange=product.dlt.exchange
```

The "executing migrations" log proves the Postgres egress allow-list
works (pgx connected via Service name `postgres`), and the "ready to
publish messages" lines prove the RabbitMQ allow-list works (AMQP
session established via Service name `rabbitmq`).

The pod image is also distroless (no shell), so the conventional
`kubectl exec … sh -c '…'` connectivity probe doesn't apply here —
the log evidence above is the substitute.

#### RabbitMQ Management UI (K8S-008)

The local overlay uses the `rabbitmq:4.0-management-alpine` image,
which ships the
[Management Plugin](https://www.rabbitmq.com/management.html) on
port 15672. The Service already exposes it — reach the UI with:

```sh
make k8s-local-ui-rabbitmq
# RabbitMQ Management UI: http://localhost:15672  (guest / guest)
```

The target port-forwards `svc/rabbitmq 15672:15672` in the
foreground; Ctrl-C tears the forward down. On macOS it also `open`s
a browser tab.

What to expect on the Overview page after `make k8s-local-up`:

- **Exchanges** → four DSN-015 exchanges (`inventory.exchange`,
  `reservation.exchange`, `product.exchange`, `product.dlt.exchange`)
  plus the AMQP-default exchanges. These come from
  `scripts/rabbitmq/definitions.json` via the `load_definitions`
  directive (same source of truth the docker-compose stack reads),
  so seeing all four is proof the ConfigMap mount worked.
- **Queues** → `inventory.queue`, `reservation.queue`,
  `product.queue`, `product.dlt.queue`.
- **Connections** → ≥ 1 connection from the app pod, with channels
  open for publishing (`inventory`/`reservation`/`product` exchanges)
  and consuming (`product.queue`). The chi handler and the
  `ProductQueue` consumer each open their own channel — expect the
  per-connection channel count to climb past one as the app warms.

**Dev-only posture.** The management plugin is enabled in the local
image for browsing convenience; the OPS-004 base does not pull this
tag. Real deployments either disable the plugin (`rabbitmq-plugins
disable rabbitmq_management`) or restrict it to a management
network — exposing :15672 publicly is a security incident.

#### pgAdmin for Postgres (K8S-009)

`docker-compose.yml` ships a pgAdmin alongside Postgres so developers
can browse rows, inspect migrations, and run ad-hoc SQL without a
desktop client. The local overlay mirrors that — `pgadmin.yaml`
defines a Deployment + Service + ConfigMap (`servers.json`) that
preconfigures a server entry pointing at the in-cluster `postgres`
Service.

```sh
make k8s-local-ui-pgadmin
# pgAdmin: http://localhost:9090  (admin@example.com / admin)
# Postgres password (when prompted for the 'go-micro-example' server): postgres
```

The target port-forwards `svc/pgadmin 9090:80`, prints the admin
credentials and the Postgres password, and (on macOS) opens a
browser tab. Sign in with `admin@example.com` / `admin`. The tree
on the left should already show **Local → go-micro-example** —
expand it, supply `postgres` as the password on first connect, and
browse **Databases → go-micro-example-db → Schemas → public →
Tables** to confirm the migrations applied (`products`,
`product_inventory`, `production_events`, `users`,
`idempotency_keys`, plus a few migration-tracking tables).

The pgAdmin pod is intentionally stateless: `/var/lib/pgadmin`
mounts an `emptyDir`, so saved queries vanish with the cluster.
`make k8s-local-down` removes the pgAdmin Deployment, Service, and
ConfigMap along with the rest of the overlay.

#### NetworkPolicy enforcement on kind

The default kind CNI (kindnet) **does not enforce NetworkPolicy on
its own**. Without an enforcer the OPS-004 policy lands as a
contract but is a silent no-op locally — every pod's egress stays
default-allow. `make k8s-local-up` therefore also installs
[`kube-network-policies`](https://github.com/kubernetes-sigs/kube-network-policies)
as a DaemonSet in `kube-system`; it syncs nftables rules against
the cluster's NetworkPolicies so denial paths actually drop packets.

To verify enforcement directly, drive a probe pod labelled like the
app and try a port that isn't on the egress allow-list:

```sh
kubectl -n go-micro-example run netpol-probe --restart=Never \
  --image=busybox:1.37 \
  --labels='app.kubernetes.io/name=go-micro-example' \
  --command -- /bin/sh -c '
    nc -nzv -w 3 postgres 5432   # ALLOW (on egress list)
    nc -nzv -w 3 postgres 1234   # DENY  (no rule matches)
  '
kubectl -n go-micro-example logs netpol-probe
kubectl -n kube-system logs ds/kube-network-policies | grep "Packet denied"
```

The enforcer log shows `"Packet denied by egress policy"` for the
postgres:1234 attempt — proof the policy rules are being evaluated
end-to-end.

**Scope caveat.** `kube-network-policies` v1.0.0 enforces in-cluster
pod-to-pod / pod-to-Service flows. It does **not** enforce
pod-to-external (egress to an IP not owned by a cluster pod) and
has port-granularity gaps on multi-port destination Services
(traffic to a destination pod on a different port than the
allow-list specifies is still permitted today). Full faithfulness
to the spec requires a heavier CNI like Calico — a future overlay
variant.

### HPA scale validation (K8S-004)

`make k8s-local-up` also installs
[`metrics-server`](https://github.com/kubernetes-sigs/metrics-server)
into `kube-system` so `kubectl top pods` returns real numbers and the
`HorizontalPodAutoscaler` from the OPS-004 base has a metrics source.
The manifest is vendored from the upstream `v0.8.1` release with one
change: `--kubelet-insecure-tls` is added to the controller's args so
metrics-server accepts kind's self-signed kubelet certs.

Drive a scale-up event with the bundled load script:

```sh
# Terminal 1 — watch the HPA
kubectl -n go-micro-example get hpa -w

# Terminal 2 — drive load for 3 minutes
make k8s-local-loadtest
# (override defaults with LOADTEST_DURATION / LOADTEST_PARALLEL)
```

The script spawns a single in-cluster busybox pod that fires
concurrent wgets at `http://go-micro-example/live`. The pod carries
`app.kubernetes.io/component: loadtest` — the overlay's
NetworkPolicy patch admits that label so the load traffic can reach
the app on :8080. Within ~30 s the HPA target should cross 70 % and
`REPLICAS` climbs from 2 toward `maxReplicas: 10`.

Scale-down is intentionally slow — the OPS-004 HPA sets
`scaleDown.stabilizationWindowSeconds: 300`, so replicas hold their
peak for five minutes after load stops before stepping back down.

### Trace verification against an in-cluster OTel collector (K8S-006)

The local overlay also runs an OpenTelemetry Collector
(`otel/opentelemetry-collector-contrib:0.152.0`) with a `debug`
exporter — no Jaeger/Tempo backend, just stdout. The overlay
patches the base ConfigMap to point the app's
`OTEL_EXPORTER_OTLP_ENDPOINT` at the collector Service and cranks
`OTEL_TRACES_SAMPLER_ARG` to `1.0` so every request produces spans.

The collector lives in the `go-micro-example` namespace labelled
`app.kubernetes.io/name: opentelemetry-collector`, which is exactly
the label the base NetworkPolicy egress allow-list selects on (port
4317 / OTLP-gRPC) — so the app pod can reach it without an overlay
patch on the NetworkPolicy.

End-to-end smoke test (assumes `make k8s-local-up` already ran):

```sh
kubectl -n go-micro-example port-forward svc/go-micro-example 8080:80 &

# Bootstrap admin / admin (BOOTSTRAP_ADMIN_PASSWORD in the K8S-003
# Secret) — note Basic creds against /auth/token, not JSON.
TOKEN=$(curl -sS -u admin:admin -X POST http://localhost:8080/auth/token \
  | jq -r .access_token)

# Seed a product, then read it back so HTTP + service + pgx all fire.
curl -sS -X PUT http://localhost:8080/api/v1/inventory \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"sku":"test1sku","upc":"upc1","name":"smoke test"}'

curl -sS -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/api/v1/inventory/test1sku

kubectl -n go-micro-example logs deploy/otel-collector --tail=200
```

In the collector log you should see a trace with three spans
stitched together by parent ID:

- **SERVER** `/api/v1/inventory/{sku}` — `otelchi` (HTTP root)
- **INTERNAL** `GetProductInventory` — `inventory.Service` (DSN-004b)
- **CLIENT** `query SELECT … FROM products p, product_inventory pi …` — `otelpgx`

Exercising the AMQP propagation (DSN-004a) takes one more curl —
`PUT /api/v1/inventory/{sku}/productionEvent` publishes an
`inventory.exchange` event, and the resulting trace adds a PRODUCER
span (`amqp.publish inventory.exchange`) under the SERVER root:

```sh
curl -sS -X PUT \
  http://localhost:8080/api/v1/inventory/test1sku/productionEvent \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -H "Idempotency-Key: smoke-$(date +%s)" \
  -d '{"requestId":"smoke-1","quantity":7}'
```

The app doesn't itself consume `inventory.queue`, so CONSUMER spans
don't fire end-to-end in this minimal demo. Publishing into
`product.queue` (which the app *does* subscribe to) would show
those — left as a future addition if/when the demo orchestrator
gets ported to k8s.

**Sampling caveat.** With `OTEL_TRACES_SAMPLER_ARG=1.0` and the
default OTLP batch processor queue size (~2 K spans), high-volume
routes can saturate the exporter and drop spans for other requests
that happen during the same window. The K8S-004 load test driving
`/live` at 24 concurrent wgets/wave will mask `/api/v1/*` spans
emitted in parallel. For trace verification, run the smoke test
above when no load driver is active, or lower
`OTEL_TRACES_SAMPLER_ARG` in the overlay.

#### Browsing traces in Jaeger (K8S-010)

The collector's `debug` exporter is great for "do spans arrive?"
but useless for navigation. The local overlay also runs a
[Jaeger](https://www.jaegertracing.io/) all-in-one with in-memory
storage, hooked into the same collector pipeline as a second
exporter target. The same smoke test now produces a clickable
trace.

```sh
make k8s-local-ui-jaeger
# Jaeger UI: http://localhost:16686
```

The target port-forwards `svc/jaeger 16686:16686` in the foreground;
Ctrl-C tears the forward down. On macOS it also `open`s a browser
tab.

Once the curls above have run, open Jaeger and set:

- **Service:** `go-micro-example`
- **Operation:** `(All)` or the specific HTTP route

Hit *Find Traces*. The list shows the recent traces; clicking one
opens the Gantt view with the same span hierarchy that prints in
the collector log:

- **SERVER** `/api/v1/inventory/{sku}` — `otelchi`
- **INTERNAL** `GetProductInventory` — `inventory.Service`
- **CLIENT** `query …` — `otelpgx`
- **PRODUCER** `amqp.publish inventory.exchange` — `otelamqp`
  (only on the `productionEvent` path)

The OTel collector keeps the `debug` exporter alongside the new
`otlp/jaeger` exporter, so the K8S-006 grep-on-stdout workflow still
works for log-only verification.

**Persistence.** Jaeger all-in-one stores traces in memory — they
survive page reloads, browser refreshes, and the lifetime of the
Jaeger pod, but vanish when the pod restarts. A real deployment
would pair Jaeger with Cassandra / OpenSearch storage, or migrate
to Grafana Tempo if the team standardises on a single Grafana
stack for logs / metrics / traces (a possible K8S-011/012
consolidation).

### Logs UI: Loki + Grafana (K8S-011)

`kubectl logs deploy/go-micro-example` is fine for one-pod
debugging but doesn't scale — there's no way to follow a
`request_id` through the inventory queue's consumer or correlate
across pods at the same timestamp. The local overlay runs the
canonical Grafana log stack so the DSN-005 structured-log fields
actually pay off in a UI:

- **Loki** (single-binary, in-memory filesystem storage) on
  `svc/loki:3100`.
- **Promtail** (DaemonSet) tails `/var/log/pods/**` on the node and
  ships every container's logs to Loki. RBAC is read-only against
  pods/services/endpoints.
- **Grafana** (`svc/grafana:3000`, admin / admin) with a
  provisioned Loki datasource and a starter dashboard.

```sh
make k8s-local-ui-grafana
# Grafana: http://localhost:3000  (admin / admin)
```

The target port-forwards `svc/grafana 3000:3000` and (on macOS)
opens a browser tab.

What to verify on first visit:

- **Dashboards → go-micro-example logs** — renders the
  `{namespace="go-micro-example"}` panel; recent app log lines
  appear within seconds of pod startup.
- **Explore → Loki** — type
  `{namespace="go-micro-example"} |= "listening"` and confirm the
  startup-banner line appears. Add `| json` to extract
  `request_id` / `trace_id` from a structured JSON line.
- **Datasources → Loki → Test** — reports "Data source connected
  and labels found".

Stopping Loki (`kubectl -n go-micro-example scale deploy/loki
--replicas=0`) and watching Grafana's Explore page raise a clean
"datasource unreachable" banner confirms the integration is live
and not pre-rendered.

**Dev-only posture.** Single-replica Loki, in-memory ring, and
filesystem storage on an `emptyDir` mean logs vanish on pod
restart. Promtail mounts the host's `/var/log/pods` directly, which
real clusters typically restrict via PodSecurity. A production
deployment would use the Grafana Loki helm chart with object
storage, run several replicas, and replace Promtail with Grafana
Alloy or vector.dev.

### Metrics UI: Prometheus + Grafana (K8S-012)

The OPS-004 base sets `prometheus.io/scrape: "true"`,
`prometheus.io/port: "8080"`, and `prometheus.io/path: "/metrics"`
on the app pod template, but no scraper was running in the cluster
— so the chi middleware's `url_hit_count` / `url_latency` series,
the SEC-002b Basic-Auth counter, and the Go runtime metrics were
all unindexed. The local overlay now lands a single-replica
Prometheus that picks the app up via those annotations, plus a
Grafana datasource and starter dashboard that visualise the
result.

```sh
make k8s-local-ui-prometheus
# Prometheus: http://localhost:9090  (/targets, /graph)

make k8s-local-ui-grafana
# Grafana: http://localhost:3000  (admin / admin)
```

First-visit checks:

- **Prometheus → Status → Targets** — the `kubernetes-pods` job
  shows the app pod as `UP`, scraped at
  `<pod-ip>:8080/metrics`. No ServiceMonitor / PodMonitor required.
- **Prometheus → Graph** — type
  `sum(rate(url_hit_count[1m])) by (url)` and execute. After a
  short burst of traffic (`make k8s-local-loadtest`) the panel
  fills in.
- **Grafana → Dashboards → go-micro-example metrics** — renders
  the RPS, p99 latency, goroutines, heap, GC pause, and the
  SEC-002b Basic-Auth counter (which should remain at 0
  post-SEC-002c).

The Prometheus pod carries
`app.kubernetes.io/name: prometheus`, which the base
NetworkPolicy's ingress allow-list already admits on TCP 8080 —
so the scrape path works without an overlay patch.

**Dev-only posture.** Single replica, in-memory TSDB on an
emptyDir, 2-hour retention. No Alertmanager, no Prometheus
Adapter, no Thanos. Production would either use
`kube-prometheus-stack` (Operator + Alertmanager + Grafana +
recording rules) or hand a long-term-storage backend like Mimir.

The HPA custom-metric path (`http_requests_per_second` via the
Prometheus Adapter against `external.metrics.k8s.io`) is a
separate follow-up — K8S-004 stubs it out in `deploy/base/hpa.yaml`.

### Kubernetes UI: Headlamp (K8S-013)

Operating the local cluster as a stack of `kubectl get/describe/logs`
commands works but burns terminal time on workflows that are
obviously visual ("which pod is restarting, what event explains
it, what does its deployment look like"). The local overlay runs
[Headlamp](https://headlamp.dev) — an open-source Kubernetes UI —
so the operator has a clickable view of the same state.

```sh
make k8s-local-ui-headlamp
# Headlamp: http://localhost:8001
# Token (paste into the Headlamp login screen):
# eyJhbGciOi…
```

The target mints a fresh 24-hour ServiceAccount token (no static
credentials in the image), prints it to stdout, port-forwards
`svc/headlamp 8001:80`, and (on macOS) opens a browser tab. Paste
the token into the login screen.

What to verify on first visit:

- **Workloads → Pods**, namespace dropdown set to
  `go-micro-example`: shows app + postgres + rabbitmq +
  otel-collector pods alongside any UI pods you've enabled
  (jaeger, loki, promtail, grafana, prometheus, pgadmin, headlamp
  itself).
- Clicking the app **Deployment** opens the spec / events /
  replicas panes. The **Logs** tab streams output without
  switching to a terminal — that's the `pods/log` Role doing its
  job.
- The **Events** view across the namespace flags any restart /
  pull / scheduling problems without `kubectl describe`.

**RBAC scope.** The Headlamp ServiceAccount is bound to the
built-in `view` ClusterRole (read-only across the cluster) plus a
narrow Role in the `go-micro-example` namespace granting
`pods/exec`, `pods/log`, and `pods/portforward`. Deliberately *not*
`cluster-admin` — what you see in the UI matches what a real
read-only developer would have in a staging cluster.

Real installs swap the SA-token login for OIDC / SSO; that's out
of scope for the dev loop. Alternatives: `k9s` (terminal-only,
no port-forward needed), Lens (desktop), the official Kubernetes
Dashboard (similar feature set, heavier RBAC out of the box).

### Real ExternalSecret flow against in-cluster Vault (K8S-005)

The K8S-003 overlay sidesteps the base `ExternalSecret` by shipping
a plain Secret of the same name. The K8S-005 overlay
(`deploy/overlays/local-eso/`) restores the real ESO flow against a
throwaway in-cluster Vault, so the secret-resolution path is
actually exercised locally:

- Vault runs in `go-micro-example` (dev mode, hard-coded root token).
- A `ClusterSecretStore` named `vault-backend` points at the
  in-cluster Vault Service.
- A one-shot Job seeds `kv/go-micro-example/{db,rabbitmq,jwt}` with
  the same dev credentials docker-compose uses.
- The base `ExternalSecret` resolves through ESO and materialises
  the `go-micro-example-secrets` Secret the app's `envFrom`
  references.
- `refreshInterval` is patched from 1 h down to 30 s in the overlay
  so the unhappy-path demo (below) is interactive.

```sh
make k8s-eso-up    # kind + ESO + Vault + bootstrap + app
make k8s-eso-down  # tear everything down
```

`k8s-eso-up` is heavier than `k8s-local-up` — it pulls the ESO
bundle and the Vault image, and waits on three rollouts plus the
bootstrap Job — so first-time bring-up runs ~3–5 minutes on a cold
machine.

Verify the path end-to-end:

```sh
kubectl get clustersecretstore vault-backend
# vault-backend   Valid   ReadWrite   True

kubectl -n go-micro-example get externalsecret go-micro-example-secrets
# go-micro-example-secrets   ClusterSecretStore   vault-backend   30s   SecretSynced   True

kubectl -n go-micro-example get secret go-micro-example-secrets
# go-micro-example-secrets   Opaque   5   ← materialised by ESO
```

Demonstrate the unhappy path (Vault unreachable → ExternalSecret
flips to `SecretSyncedError` within one refresh interval):

```sh
kubectl -n go-micro-example scale deploy/vault --replicas=0
sleep 45
kubectl -n go-micro-example get externalsecret go-micro-example-secrets \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")].reason}'
# SecretSyncedError
```

Restoring Vault is a two-step recovery because dev-mode storage is
in-memory — the kv state vanishes on restart:

```sh
kubectl -n go-micro-example scale deploy/vault --replicas=1
make k8s-eso-reseed   # deletes the completed bootstrap Job and
                      # reapplies the manifest, then force-syncs ESO
# ExternalSecret returns to SecretSynced within ~5 seconds
```

The already-materialised Secret stays present throughout, so the
app pod doesn't restart while ESO catches up.

**Dev-only posture.** The Vault is in dev mode with the root token
hard-coded; the `ClusterSecretStore` authenticates with that token
via a Kubernetes Secret. A real cluster swaps to Kubernetes auth (or
AppRole) and runs Vault on durable storage. Copying this overlay
into a staging environment would be a security incident.

#### Vault UI (K8S-007)

The Vault binary ships a browser UI at `/ui`. Reach it with:

```sh
make k8s-eso-ui-vault
# Vault UI:    http://localhost:8200/ui
# Root token:  root  (dev-mode only)
```

The target port-forwards `svc/vault 8200:8200` in the foreground —
Ctrl-C tears the forward down. On macOS it also `open`s a browser
tab at the UI URL.

Log in with method `Token` and paste `root`. The seeded kv paths
(populated by `bootstrap-job.yaml`) live on the `kv/` mount:

- `kv/go-micro-example/db`        — Postgres user / password
- `kv/go-micro-example/rabbitmq`  — RabbitMQ user / password
- `kv/go-micro-example/jwt`       — JWT signing key

If the Vault pod restarted, the kv tree is empty (dev-mode storage
is in-memory). Run `make k8s-eso-reseed` first; the same Job that
populates it on first boot reseeds it.

**Dev token only.** `root` here is the hard-coded
`VAULT_DEV_ROOT_TOKEN_ID` from `vault.yaml` — it exists because the
in-cluster Vault is throwaway. Production Vault auth (Kubernetes
auth, AppRole, OIDC) is K8S-005's territory; the UI is just a window
into the same throwaway store.

`make k8s-validate-local` runs the dry-run-apply against the overlay
without needing the image — useful in CI and for sanity-checking
overlay edits.
