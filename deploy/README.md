# Deployment

Kustomize base for running `go-micro-example` on a Kubernetes
cluster (OPS-004). The base is intentionally deployable on its own;
real environments wrap it in `deploy/overlays/<env>/` with patches
for replica counts, the image tag, ConfigMap values (collector
endpoint, CORS origins, etc.), and any cluster-specific labels.

```text
deploy/
└── base/
    ├── kustomization.yaml   # stitches resources, namespace, labels
    ├── deployment.yaml      # pod spec + security context + probes
    ├── service.yaml         # ClusterIP
    ├── configmap.yaml       # non-secret GME_* / OTEL_* env vars
    ├── externalsecret.yaml  # ESO → Vault → in-cluster Secret
    ├── networkpolicy.yaml   # default-deny + per-dependency allows
    └── hpa.yaml             # CPU-based autoscaler
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
