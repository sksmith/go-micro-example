# Builder pinned to a multi-arch index digest so CI rebuilds against
# the same toolchain layer every run (SEC-011). Bump the digest in the
# same PR that bumps the Go minor version — the value comes from
# `docker buildx imagetools inspect golang:1.26-alpine --format '{{.Manifest.Digest}}'`.
FROM golang:1.26-alpine@sha256:91eda9776261207ea25fd06b5b7fed8d397dd2c0a283e77f2ab6e91bfa71079d AS builder

WORKDIR /app

COPY . .

ARG VER=NOT_SUPPLIED
ARG SHA1=NOT_SUPPLIED
ARG NOW=NOT_SUPPLIED

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags "-X github.com/sksmith/go-micro-example/config.AppVersion=$VER \
    -X github.com/sksmith/go-micro-example/config.Sha1Version=$SHA1 \
    -X github.com/sksmith/go-micro-example/config.BuildTime=$NOW" \
    -o ./go-micro-example ./cmd/server

# distroless static-debian12:nonroot ships /etc/passwd with a 65532
# nonroot user, /etc/ssl/certs/ca-certificates.crt, and defaults USER
# to nonroot — so the apk ca-certs install and the cert copy that
# `FROM scratch` needed both disappear (SEC-011).
FROM gcr.io/distroless/static-debian12:nonroot@sha256:d093aa3e30dbadd3efe1310db061a14da60299baff8450a17fe0ccc514a16639

WORKDIR /app

COPY --from=builder /app/go-micro-example /usr/bin/
# config.yml ships defaults only. Secrets are sourced from env vars /
# Vault (SEC-004); nothing credential-bearing is baked into the image.
COPY --from=builder /app/config.yml /app
# Migrations ship with the image so 'db.migrate=true' can find them on
# startup at the configured path (DSN-028 Phase 4).
COPY --from=builder /app/internal/platform/persistence/migrations /app/internal/platform/persistence/migrations

# Re-declare the UID/GID explicitly even though the base already
# defaults to nonroot. Stating it lets image-policy tooling (Kyverno,
# OPA, runAsNonRoot admission) assert the value without resolving
# the base image's metadata.
USER 65532:65532

ARG VER
ARG SHA1
ARG NOW

LABEL org.opencontainers.image.source="https://github.com/sksmith/go-micro-example" \
      org.opencontainers.image.revision="${SHA1}" \
      org.opencontainers.image.version="${VER}" \
      org.opencontainers.image.created="${NOW}" \
      org.opencontainers.image.licenses="NOASSERTION" \
      org.opencontainers.image.title="go-micro-example" \
      org.opencontainers.image.description="Go microservice template"

# HEALTHCHECK intentionally omitted. distroless ships no shell, curl,
# or wget, so the canonical `HEALTHCHECK CMD ...` form has nothing to
# exec. In Kubernetes the app is probed against /live and /ready on
# :8080 via the pod spec (DSN-002 endpoints) — that's the supported
# liveness/readiness path. For ad-hoc `docker run`, `curl` from the
# host against the published port serves the same role.

ENTRYPOINT ["go-micro-example"]
