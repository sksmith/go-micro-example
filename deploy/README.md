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
│   └── local/
│       ├── kustomization.yaml  # K8S-002/003 overlay (image + deps)
│       ├── namespace.yaml      # K8S-003 self-contained namespace
│       ├── secret.yaml         # K8S-003 plain dev Secret
│       ├── postgres.yaml       # K8S-003 in-cluster Postgres
│       └── rabbitmq.yaml       # K8S-003 in-cluster RabbitMQ
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
session established via Service name `rabbitmq`). NetworkPolicy
enforcement is intrinsic: if the egress rules were wrong, neither
side would connect.

The pod image is also distroless (no shell), so the conventional
`kubectl exec … sh -c '…'` connectivity probe doesn't apply here —
the log evidence above is the substitute.

> **TST-005 caveat.** After the first ~10 s, `/ready` flips to 503
> because the AMQP staleness check at
> `internal/inventory/transport_queue.go` doesn't refresh on a
> healthy long-lived session — `sessionOK()` only fires on
> (re)connect. The pod still serves traffic; readiness probes will
> mark it NotReady and Service rotation will drop it. Track in
> [TST-005](../plan/TST-005-amqp-readiness-stable-session.md);
> until it's fixed, `make k8s-local-up`'s `kubectl rollout status`
> succeeds on the initial roll but the pod won't *stay* Ready.

`make k8s-validate-local` runs the dry-run-apply against the overlay
without needing the image — useful in CI and for sanity-checking
overlay edits.
