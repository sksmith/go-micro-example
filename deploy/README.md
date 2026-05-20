# Deployment

Kustomize base for running `go-micro-example` on Kubernetes (OPS-004).
The base is intentionally deployable on its own; real environments
wrap it in their own `deploy/overlays/<env>/` with patches for
replica counts, the image tag, ConfigMap values, and any
cluster-specific labels.

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
│   └── local/                          # the only overlay; `make k8s-up` renders this
│       ├── kustomization.yaml
│       ├── namespace.yaml
│       ├── postgres.yaml
│       ├── rabbitmq.yaml
│       ├── otel-collector.yaml
│       ├── metrics-server.yaml         # applied to kube-system out-of-band
│       ├── kube-network-policies.yaml  # NetworkPolicy enforcer (kube-system)
│       ├── external-secrets-operator.yaml  # ESO bundle (server-side applied)
│       ├── vault.yaml                  # dev-mode Vault
│       ├── secretstore.yaml            # ClusterSecretStore vault-backend
│       ├── bootstrap-job.yaml          # seeds Vault kv paths
│       ├── pgadmin.yaml                # Postgres UI
│       ├── jaeger.yaml                 # traces UI
│       ├── loki.yaml                   # log store
│       ├── promtail.yaml               # log shipper
│       ├── grafana.yaml                # logs + metrics dashboards
│       ├── prometheus.yaml             # annotation-scrape Prometheus
│       └── headlamp.yaml               # Kubernetes UI
└── kind/
    └── cluster.yaml         # kind cluster spec used by `make k8s-up`
```

## Local workflow

Two make targets, all-or-nothing:

```sh
make k8s-up    # build image → kind up → load image → apply overlay → wait Ready
make k8s-down  # kind delete cluster — everything in the cluster goes with it
```

`k8s-up` blocks on `kubectl rollout status` for the app Deployment,
which (via the readiness probe) means migrations completed and AMQP
redial succeeded. Cold bring-up is ~3–5 min (ESO + Vault add the
bulk of that vs. a plain overlay).

Prereqs: `kind`, `kubectl`, `docker`. The make target prints an
install hint if any of them is missing.

Verify the stack is healthy:

```sh
kubectl -n go-micro-example get pods
# go-micro-example-…   1/1   Running
# postgres-…           1/1   Running
# rabbitmq-…           1/1   Running
# vault-…              1/1   Running
# jaeger-…             1/1   Running
# loki-…               1/1   Running
# promtail-…           1/1   Running
# grafana-…            1/1   Running
# prometheus-…         1/1   Running
# pgadmin-…            1/1   Running
# headlamp-…           1/1   Running
# otel-collector-…     1/1   Running
# vault-bootstrap-…    0/1   Completed

kubectl -n go-micro-example logs deploy/go-micro-example \
  | grep -E 'executing migrations|ready to publish|listening'
# INF executing migrations
# DBG ready to publish messages exchange=inventory.exchange
# INF listening port=8080
# INF listening for messages queue=product.queue
```

The app pod image is distroless (no shell), so the conventional
`kubectl exec … sh -c '…'` connectivity probe doesn't apply — the
log evidence above is the substitute.

## Reaching the UIs

Each UI is a Service in `go-micro-example`. Port-forward whichever
one you want; every command below is namespaced with
`-n go-micro-example`.

| UI         | Port-forward                                | Login                       |
| ---------- | ------------------------------------------- | --------------------------- |
| App        | `port-forward svc/go-micro-example 8080:80` | bootstrap `admin` / `admin` |
| Vault      | `port-forward svc/vault 8200:8200`          | token `root` (1)            |
| RabbitMQ   | `port-forward svc/rabbitmq 15672:15672`     | `guest` / `guest`           |
| pgAdmin    | `port-forward svc/pgadmin 9090:80`          | (2)                         |
| Jaeger     | `port-forward svc/jaeger 16686:16686`       | —                           |
| Grafana    | `port-forward svc/grafana 3000:3000`        | `admin` / `admin`           |
| Prometheus | `port-forward svc/prometheus 9090:9090`     | —                           |
| Headlamp   | `port-forward svc/headlamp 8001:80`         | token (3)                   |

1. Dev-mode root token; never reuse outside this cluster.
2. Sign in as `admin@example.com` / `admin`; the preregistered
   `go-micro-example` server prompts for the Postgres password
   (`postgres`) on first connect.
3. Mint a fresh token with
   `kubectl -n go-micro-example create token headlamp --duration=24h`
   and paste it into the login screen.

**Dev-only credentials.** Every default above is hard-coded for
local development. None of them belongs anywhere near a real
cluster.

## Cluster prerequisites (for a real cluster)

| Capability | Why |
| --- | --- |
| Kubernetes ≥ 1.27 | `autoscaling/v2` HPA, NetworkPolicy v1, ExternalSecret v1 |
| metrics-server | The HPA reads pod CPU from `metrics.k8s.io` |
| [External Secrets Operator](https://external-secrets.io) | Materialises the `go-micro-example-secrets` Secret from Vault |
| A `ClusterSecretStore` named `vault-backend` | Resolves `go-micro-example/{db,rabbitmq,jwt}` paths |
| In-cluster Postgres, RabbitMQ, (optional) Redis, OTel collector | Service names match the labels the NetworkPolicy uses |

If your dependencies don't carry the labels the NetworkPolicy
selects on (`app.kubernetes.io/name: postgresql`, `rabbitmq`, etc.),
either re-label them or patch `networkpolicy.yaml` in an overlay.
The base intentionally uses label-selectors (not namespace-selectors)
so it's portable across cluster layouts.

## What's baked into the base

### Pod security (SEC-011 + OPS-004)

- `runAsNonRoot: true`, `runAsUser: 65532` (matches the distroless
  image's nonroot UID).
- `readOnlyRootFilesystem: true` with an `emptyDir` at `/tmp` so the
  Go runtime has somewhere to spill profile data.
- `allowPrivilegeEscalation: false`, all capabilities dropped.
- `seccompProfile: RuntimeDefault`.
- `terminationGracePeriodSeconds: 30` — matches the
  `http.Server.Shutdown` + AMQP/Kafka drain budget from DSN-001.

### Probes (DSN-002 + TST-004)

- **Liveness** on `/live`: simple liveness signal.
- **Readiness** on `/ready`: aggregates the per-dependency pingers
  (Postgres, Redis, AMQP via TST-004). 503 keeps the pod out of
  Service rotation until every dep is healthy.
- **Startup** probe on `/ready` with a 150 s budget so DB migrations
  and the initial AMQP redial have time to finish before liveness
  starts judging.

### Network policy

Default-deny egress with explicit allow-lists:

- kube-dns (UDP/TCP 53) — required for in-cluster DNS resolution.
- Postgres (TCP 5432)
- RabbitMQ (TCP 5672)
- Redis (TCP 6379) — only if your overlay sets `GME_REDIS_URL`
- OpenTelemetry collector (TCP 4317, OTLP/gRPC)

Ingress: from `ingress-nginx` and `prometheus` on TCP 8080. Tune in
an overlay if you run a different ingress controller or scraper.

**NetworkPolicy enforcement on kind.** The default kind CNI (kindnet)
does *not* enforce NetworkPolicy — the policy lands as a contract
but every pod's egress stays default-allow. `make k8s-up` installs
[`kube-network-policies`](https://github.com/kubernetes-sigs/kube-network-policies)
as a DaemonSet in `kube-system` so denial paths actually drop
packets. Verify directly with a labelled probe pod:

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

`kube-network-policies` v1.0.0 enforces in-cluster pod-to-pod /
pod-to-Service flows. It does not enforce pod-to-external traffic
and has port-granularity gaps on multi-port destination Services.
Full faithfulness to the spec needs a heavier CNI (Calico).

### Autoscaling

`HorizontalPodAutoscaler` v2 with a 70 % CPU utilisation target
between 2 and 10 replicas. A custom `http_requests_per_second`
metric is commented out — uncomment after wiring the
[Prometheus Adapter](https://github.com/kubernetes-sigs/prometheus-adapter)
to expose it through the external metrics API.

Drive a scale-up event with the bundled load script:

```sh
# Terminal 1 — watch the HPA
kubectl -n go-micro-example get hpa -w

# Terminal 2 — fire concurrent wgets at the app for ~3 minutes
hack/k8s-loadtest.sh 180 12
```

The script spawns an in-cluster busybox pod labelled
`app.kubernetes.io/component: loadtest`, which the overlay's
NetworkPolicy patch admits on :8080. Within ~30 s the HPA target
crosses 70 % and `REPLICAS` climbs from 2 toward 10. Scale-down is
intentionally slow — the OPS-004 HPA sets
`scaleDown.stabilizationWindowSeconds: 300`, so replicas hold their
peak for five minutes after load stops.

### Secrets

The Deployment's `envFrom` references a Secret named
`go-micro-example-secrets`. The `ExternalSecret` resource creates it
by pulling from Vault — see `externalsecret.yaml` for the path
layout. The local overlay installs ESO + a dev-mode Vault and seeds
the kv paths via the `vault-bootstrap` Job, so the base
ExternalSecret resolves locally without manual setup.

## Smoke tests against the running stack

`make k8s-up` produces a Ready pod; the recipes below exercise the
trace pipeline, the metrics scrape, the logs flow, and the
ExternalSecret unhappy path.

### Traces (OTel collector + Jaeger)

The overlay patches the app's `OTEL_EXPORTER_OTLP_ENDPOINT` to the
in-cluster collector Service and cranks `OTEL_TRACES_SAMPLER_ARG`
to `1.0` so every request produces spans. The collector forwards to
both `debug` (stdout) and `otlp/jaeger` (UI).

```sh
kubectl -n go-micro-example port-forward svc/go-micro-example 8080:80 &

# Bootstrap admin / admin — Basic creds against /auth/token, not JSON.
TOKEN=$(curl -sS -u admin:admin -X POST http://localhost:8080/auth/token \
  | jq -r .access_token)

# Seed a product, then read it back so HTTP + service + pgx all fire.
curl -sS -X PUT http://localhost:8080/api/v1/inventory \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"sku":"test1sku","upc":"upc1","name":"smoke test"}'
curl -sS -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/api/v1/inventory/test1sku

# Publish an AMQP event for the PRODUCER span (DSN-004a).
curl -sS -X PUT \
  http://localhost:8080/api/v1/inventory/test1sku/productionEvent \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -H "Idempotency-Key: smoke-$(date +%s)" \
  -d '{"requestId":"smoke-1","quantity":7}'

kubectl -n go-micro-example logs deploy/otel-collector --tail=200
```

The collector log shows a trace stitched together by parent ID:

- **SERVER** `/api/v1/inventory/{sku}` — `otelchi`
- **INTERNAL** `GetProductInventory` — `inventory.Service` (DSN-004b)
- **CLIENT** `query SELECT … FROM products p, product_inventory pi …` — `otelpgx`
- **PRODUCER** `amqp.publish inventory.exchange` — `otelamqp` (only on the `productionEvent` path)

Port-forward Jaeger and browse the same trace in a Gantt view (see
the Service table above).

### Metrics (Prometheus + Grafana)

Prometheus discovers pods annotated `prometheus.io/scrape: "true"`
— the OPS-004 base sets that on the app, so it shows up on
`/targets` as `UP` with no per-target config. Grafana provisions
the datasource and a starter dashboard.

```sh
kubectl -n go-micro-example port-forward svc/prometheus 9090:9090 &
# Then: sum(rate(url_hit_count[1m])) by (url)  in the expression browser.
kubectl -n go-micro-example port-forward svc/grafana 3000:3000 &
# Then: Dashboards → go-micro-example metrics
```

Dashboard panels: HTTP RPS by route, p99 latency by route,
goroutines, heap in use, GC pause p99, and the SEC-002b Basic-Auth
counter (should remain at 0 post-SEC-002c).

### Logs (Loki + Promtail + Grafana)

Promtail tails `/var/log/pods/**` via hostPath and ships to Loki.
Grafana exposes a Loki datasource so DSN-005's structured
`request_id` / `trace_id` fields are queryable.

```sh
kubectl -n go-micro-example port-forward svc/grafana 3000:3000
# Then: Explore → Loki → {namespace="go-micro-example"}
# Add `| json` to extract DSN-005's request_id / trace_id.
# Or: Dashboards → go-micro-example logs.
```

Stopping Loki (`kubectl -n go-micro-example scale deploy/loki
--replicas=0`) and watching Grafana raise a "datasource
unreachable" banner confirms the integration is live.

### ExternalSecret flow (ESO + Vault)

Vault runs in dev mode (in-memory KV, root token `root`). The
bootstrap Job seeds `kv/go-micro-example/{db,rabbitmq,jwt}` after
Vault becomes Ready, and the base ExternalSecret materialises
`go-micro-example-secrets`:

```sh
kubectl get clustersecretstore vault-backend
# vault-backend   Valid   ReadWrite   True

kubectl -n go-micro-example get externalsecret go-micro-example-secrets
# go-micro-example-secrets   ClusterSecretStore   vault-backend   30s   SecretSynced   True

kubectl -n go-micro-example get secret go-micro-example-secrets
# go-micro-example-secrets   Opaque   5
```

Demonstrate the unhappy path (Vault unreachable → ExternalSecret
flips to `SecretSyncedError` within one refresh interval — patched
down to 30 s in this overlay):

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
kubectl -n go-micro-example wait --for=condition=ready pod -l app.kubernetes.io/name=vault
kubectl -n go-micro-example delete job vault-bootstrap
kubectl kustomize --load-restrictor=LoadRestrictionsNone deploy/overlays/local \
  | yq 'select(.metadata.name == "vault-bootstrap" and .kind == "Job")' \
  | kubectl apply -f -
kubectl -n go-micro-example wait --for=condition=complete job/vault-bootstrap --timeout=60s
kubectl -n go-micro-example annotate externalsecret go-micro-example-secrets force-sync=$(date +%s) --overwrite
```

The already-materialised Secret stays present throughout, so the
app pod doesn't restart while ESO catches up. If that's too much
ceremony, `make k8s-down && make k8s-up` is the all-or-nothing
escape hatch.

**Dev-only posture.** The Vault is in dev mode with the root token
hard-coded; the `ClusterSecretStore` authenticates via that token.
A real cluster swaps to Kubernetes auth (or AppRole) and runs Vault
on durable storage. Copying this overlay into a staging environment
would be a security incident.

## Building a real overlay

Bring up a `staging` overlay by copying the base labels and
patching what's different:

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
output. Validate before applying:

```sh
# Schema validation against the upstream OpenAPI spec.
kustomize build deploy/overlays/staging | kubeconform -strict -ignore-missing-schemas -summary

# Dry-run against your live cluster (catches admission-policy
# rejections that schema validation alone misses):
kustomize build deploy/overlays/staging | kubectl apply --dry-run=server -f -
```

CI runs the same kind-based dry-run gate against the base + local
overlay — see `.github/workflows/k8s-validate.yml`.
