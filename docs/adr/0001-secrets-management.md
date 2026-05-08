# ADR 0001 — Secrets management via Vault Agent injector

- **Status:** Accepted (DSN-006).
- **Date:** 2026-05-08.
- **Related:** SEC-004 (env-var sourcing), DSN-006 (this decision).

## Context

After SEC-004, the four credential-bearing config keys (`db.user`,
`db.pass`, `rabbitmq.user`, `rabbitmq.pass`) plus `GME_JWT_SIGNING_KEY`
are populated from the process environment. SEC-004 satisfied "no
secrets in tracked files" but punted on the upstream question — where
do those env vars actually come from in production? DSN-006 picks one
answer and bakes it into the runtime so operators are not left to
improvise.

The four candidates we considered:

1. **Vault Agent injector (sidecar in Kubernetes).** Vault Agent runs
   as a sidecar in the pod, authenticates via the Kubernetes service
   account (Vault `auth/kubernetes` method), renders Vault templates
   into a tmpfs volume shared with the app container, and (optionally)
   keeps the rendered files fresh as Vault leases rotate.
2. **Direct Vault SDK calls from the app.** Embed the
   `hashicorp/vault/api` client, authenticate from the app, fetch
   secrets at startup (and re-fetch periodically).
3. **AWS Secrets Manager + AWS SDK.** Resolve secrets via the AWS
   SDK at startup, using IRSA / instance profile auth.
4. **Kubernetes Secrets sourced from External Secrets Operator (ESO).**
   ESO syncs from Vault / AWS / GCP into a `Secret` resource; the pod
   mounts it as env vars or files.

## Decision

We adopt **option 1 — Vault Agent injector**. The Go binary itself
knows nothing about Vault. Authentication, leasing, retry, template
rendering, and secret rotation all live in the sidecar.

The application reads secrets from files in a directory whose path is
configurable via `GME_SECRETS_DIR` (defaulting to `/vault/secrets`,
the Agent injector's standard mount point). A
`secrets.FileProvider` implementation in `core/secrets/` reads each
file, trims a single trailing newline (which Vault templates produce
by default), and exports the value as the matching `GME_*` env var
**before** viper resolves config — so the existing SEC-004 plumbing
keeps working unchanged.

For dev / CI / `go run`, the same package ships a
`secrets.EnvProvider` that does nothing but verify required variables
are already in the environment, matching the SEC-004 contract. The
provider is selected at startup via `GME_SECRETS_PROVIDER`:

| `GME_SECRETS_PROVIDER` | Reads from | Use case |
| --- | --- | --- |
| unset / `env` | shell env, `.env` file | dev, CI, `go run` |
| `file` | `GME_SECRETS_DIR` (default `/vault/secrets`) | Vault Agent injector in K8s |

## Why Vault Agent injector and not the alternatives

- **vs. direct Vault SDK calls.** The app would need Vault auth
  config, retry, lease renewal, and rotation logic baked into Go
  code. That is exactly the work the Agent already does, in a
  CNCF-blessed sidecar. Keeping it out of the binary keeps the binary
  simpler and lets us replace Vault later without touching the app.
- **vs. AWS Secrets Manager.** Cloud-coupling without a clear win.
  The team has Vault running for other services and Vault is portable
  across clouds; AWS SM would lock us in. If we move off Vault later,
  swapping the sidecar (e.g. for ESO) is a deploy-time change, not a
  code change.
- **vs. External Secrets Operator.** ESO ends up writing to
  Kubernetes `Secret` resources, which sit at rest in etcd (encrypted
  if so configured, but still). The Agent injector renders into
  in-memory tmpfs — never persisted. Lower blast radius if a node is
  compromised.

## Consequences

- **For the runtime.** The Go process gains exactly one new
  dependency: the `secrets.Provider` interface plus two
  implementations. No Vault client code; no retry; no rotation
  logic. About 130 lines of Go, fully unit-tested.
- **For deployment.** The K8s manifest gains the standard Vault
  Agent injector annotations on the pod spec, and a Vault role +
  policy + Kubernetes auth binding granted to the service account.
  Concrete YAML belongs to OPS-004 (K8s manifests) and is **not** in
  scope of this ADR or DSN-006 — DSN-006 just makes the app
  injector-compatible.
- **For rotation.** Vault Agent re-renders the template files when a
  lease ticks. Two postures are possible:
    1. **Restart-on-rotate.** The current minimal posture. App reads
       env vars once at startup; a rotation requires a pod restart,
       which is cheap on K8s and matches the project's no-graceful-
       shutdown limitation tracked in DSN-001. Document this in the
       rotation playbook (next section).
    2. **Reload-without-restart.** Watch the rendered files and
       re-resolve. Out of scope for DSN-006; revisit if rotation
       cadence becomes painful.
  We pick (1) on purpose: it keeps DSN-006 a single-PR change. The
  rotation playbook below tells operators what to do.
- **For dev workflow.** Unchanged. Devs continue to set `GME_*` env
  vars from a `.env` file or shell. `GME_SECRETS_PROVIDER` is unset
  by default and selects EnvProvider, which only verifies presence.
- **For tests.** A test can either set env vars directly (the EnvProvider
  path) or write files to a temp dir and point a `FileProvider` at it.
  No Vault test fixture is required.

## Rotation playbook (restart-on-rotate)

1. Operator updates the secret in Vault (`vault kv put kv/<path>
   db_pass=<new>`).
2. Vault Agent re-renders the template within its lease window
   (default ≤ 1 minute).
3. Operator triggers a rolling restart of the deployment:
   ```sh
   kubectl rollout restart deploy/go-micro-example
   ```
4. The new pods read the freshly rendered files at startup. Old
   pods finish in-flight requests and terminate. Zero-downtime
   provided the deployment has more than one replica and a
   `maxUnavailable` < replicas.
5. The startup health check (built into `secrets.LoadFromEnv` →
   `EnvProvider.Load` / `FileProvider.Load`) refuses to start any
   pod that cannot resolve every required secret.

## Out of scope (filed elsewhere)

- **Concrete K8s manifests** with Vault Agent annotations →
  **OPS-004**.
- **Reload-without-restart** for rotation → potential follow-up if
  cadence demands it; not filed today.
- **Bootstrap admin password** (`BOOTSTRAP_ADMIN_PASSWORD`) is
  intentionally not part of `secrets.Required`. It is a one-shot,
  optional input read directly via `os.Getenv` in `cmd/main.go`. If
  we later want it in Vault too, dropping it into `secrets.Required`
  is one line.
- **JWT signing key** (`GME_JWT_SIGNING_KEY`) is *also* deliberately
  not in `secrets.Required`. The `auth.NewSigner` strict flag
  enforces presence in `prod` profile and tolerates an ephemeral key
  elsewhere. Adding it to `Required` would force prod-style
  enforcement onto every profile.
