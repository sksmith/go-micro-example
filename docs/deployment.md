# Deployment

This document describes how go-micro-example expects to be deployed,
with a particular focus on TLS termination — the topic SEC-005 was
filed against.

## TLS termination

**The service binds plaintext.** In production, TLS is terminated
upstream by the ingress controller (or a sidecar like Envoy / Linkerd
proxy) and the service listens on a private port that only the
terminator can reach. The reasons for this split:

- Certificate rotation is the terminator's responsibility, not the
  application's. Rotating an in-process listener requires a restart
  or a hot-reload path; rotating at the ingress is a routine ops task.
- The same posture lets us run mTLS between proxies (or service-mesh
  sidecars) without complicating application code.
- Decoupling lets the app survive cert-store and TLS-library churn
  without code changes.

### Standalone HTTPS (optional)

For deployments without a terminator — a single-host VM, a one-off
appliance, or a developer who wants to test HTTPS without compose —
the service can serve HTTPS directly:

```yaml
tls:
  enabled: true
  certFile: /etc/go-micro-example/tls/server.crt
  keyFile:  /etc/go-micro-example/tls/server.key
```

Or via env:

```
GME_TLS_ENABLED=true
GME_TLS_CERTFILE=/etc/go-micro-example/tls/server.crt
GME_TLS_KEYFILE=/etc/go-micro-example/tls/server.key
```

When `tls.enabled=true`:

- The HTTP server uses `ListenAndServeTLS` with the configured cert/key.
- The `tls.Config` enforces TLS 1.2+ and the Mozilla "intermediate"
  AEAD cipher list (TLS 1.3 cipher suites and curves are fixed by Go).
- The service fails to start if `tls.certFile` or `tls.keyFile` is
  empty — a misconfiguration that would otherwise silently serve
  cleartext.

`tls.enabled=false` (the default) is the production posture; pair it
with a TLS terminator out in front of the service.

## HSTS

The service unconditionally mounts an HSTS middleware
([internal/platform/httpx/hsts.go](../internal/platform/httpx/hsts.go))
that emits `Strict-Transport-Security` on responses to requests that
arrived over TLS. The detection is:

- `r.TLS != nil` — request hit the in-process TLS listener directly, **or**
- `X-Forwarded-Proto: https` — a TLS terminator handled the handshake
  and forwarded the request as plaintext.

Plaintext requests get no header (browsers discard HSTS over HTTP, and
emitting it makes local-dev logs noisier without buying any security).

The default value is `max-age=31536000; includeSubDomains` —
Mozilla's intermediate profile and the minimum the HSTS preload list
requires. `preload` is **not** enabled by default; turn it on only
once every subdomain has been verified HTTPS-only.

## Local development

`docker-compose.yml` ships a [Caddy](https://caddyserver.com/)
service that mirrors the prod topology: Caddy listens on `:443` with
a self-signed cert (`tls internal`) and reverse-proxies plaintext to
the app on the compose network. The app continues to expose its
plain `:8080` listener for direct debugging.

```sh
docker compose up --build
# https://localhost:8443/   (Caddy → app, self-signed cert; -k or trust the CA)
# http://localhost:8080/    (app direct; no TLS)
```

The Caddyfile lives at
[scripts/caddy/Caddyfile](../scripts/caddy/Caddyfile). The Caddy
container persists its locally-generated CA in a named volume so the
cert stays stable across restarts — `docker compose down -v` wipes
it.

## Production checklist

- TLS terminator (ingress / sidecar) handles certs and sits in front
  of the service.
- The service binds on a private port; only the terminator can reach
  it.
- The terminator forwards `X-Forwarded-Proto: https` so the HSTS
  middleware engages.
- `tls.enabled` is left at its default (`false`) unless the
  deployment intentionally serves HTTPS directly.
- The terminator's TLS profile is at least as strict as the in-process
  one: TLS 1.2+, AEAD ciphers only, modern curves.
