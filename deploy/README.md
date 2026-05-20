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
│   ├── local/                   # K8S-002/003/004/006 (plain Secret)
│   │   ├── kustomization.yaml
│   │   ├── namespace.yaml
│   │   ├── secret.yaml          # plain dev Secret
│   │   ├── postgres.yaml
│   │   ├── rabbitmq.yaml
│   │   ├── metrics-server.yaml
│   │   ├── kube-network-policies.yaml  # NetworkPolicy enforcer
│   │   └── otel-collector.yaml  # K8S-006 OTel collector
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

`make k8s-validate-local` runs the dry-run-apply against the overlay
without needing the image — useful in CI and for sanity-checking
overlay edits.
