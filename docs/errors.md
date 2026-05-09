# Error handling conventions

This project uses stdlib `errors` and `fmt.Errorf` only. There are
no `pkg/errors`, `cockroachdb/errors`, or similar third-party error
libraries. Stack traces are not part of the error chain; if you
need traceability, add structured fields at the log boundary.

The conventions below are enforced by `golangci-lint` (`errorlint`,
`errname`) where possible, by review otherwise.

## Sentinels

A sentinel is a package-level `var ErrXxx = errors.New(...)`.
Callers compare against it with `errors.Is`. Use a sentinel when
the **caller can take a different action** based on the kind of
failure — typically distinguishing 4xx from 5xx in the API layer,
or "not found" from "real error" in the service layer.

```go
// core/repo.go
var ErrNotFound = errors.New("core: record not found")

// core/user/service.go
var ErrInvalidCredentials = errors.New("invalid credentials")
var ErrInvalidInput       = errors.New("invalid input")
```

Naming: the variable is `ErrXxx`. The error message is lowercase
with no trailing punctuation, and prefixed with the package name
if the message would be ambiguous out of context (`core: record
not found`, not `record not found`).

## Validation errors

Service-layer input validation returns errors that **wrap
`ErrInvalidInput`**. The API layer uses `errors.Is` to map them
to HTTP 400, and the wrapped message becomes the client-facing
detail.

```go
// service
if pr.Quantity < 1 {
    return fmt.Errorf("quantity must be greater than zero: %w", ErrInvalidInput)
}

// api handler
if errors.Is(err, inventory.ErrInvalidInput) {
    Render(w, r, BadRequestResponse(err))
    return
}
```

Without this pattern, validation errors fall through to
`ErrInternalServer` and the user gets a 500 for what is plainly
their fault. ERR-001 B1's wrong-password-becomes-500 was the
egregious case.

## Wrapping

Use `fmt.Errorf("<what we were doing>: %w", err)` to add context
when an error crosses a layer boundary that the caller can't
reconstruct.

**Service methods wrap repo errors.** The repo speaks SQL; the
service speaks domain operations. A SQL error bubbling out of
`s.repo.SaveProduct` becomes `save product: <sql error>` so the
log line shows what step failed.

```go
if err = s.repo.SaveProduct(ctx, product, opts); err != nil {
    return fmt.Errorf("save product: %w", err)
}
```

**Repos return raw.** They already say "FROM products" in the
SQL; wrapping with "GetProduct: ..." adds nothing. Sentinel
mapping (e.g. `pgx.ErrNoRows` → `core.ErrNotFound`) happens at
the repo boundary.

**Don't wrap thin pass-throughs.** A service method that just
calls one repo method and returns the result doesn't need
wrapping — the repo already has the context.

**Don't double-log.** If you wrap and return, don't also
`log.Error()` the same error at the same layer. Pick one. The
boundary that converts the error to a response is where the log
goes.

## Comparing errors

Always use `errors.Is` (for sentinels) or `errors.As` (for
typed errors). **Never** use `==` or `!=`:

```go
// ❌
if err == pgx.ErrNoRows { ... }

// ✅
if errors.Is(err, pgx.ErrNoRows) { ... }
```

This is enforced by `errorlint`. The `==` form silently breaks
the moment any layer between the source and the check decides
to wrap.

## Logging errors

Use `log.Error().Err(err).Msg("...")` (zerolog's verbose form).
The shortcut `log.Err(err).Send()` is banned: it produces a log
entry with no context message.

Every error log entry must:
- Use `log.Error().Err(err)` form (uniform with non-error logs).
- Have a `.Msg("what was being attempted")`.
- Include the relevant identifying fields (`Str("sku", ...)`,
  `Str("requestId", ...)`, etc.).

```go
// ❌
log.Err(err).Send()

// ✅
log.Error().Err(err).Str("sku", product.Sku).Msg("failed to create product")
```

A grep for `log.Err(` should return nothing in non-test code.

## Error response builders

API helpers that *build* an `*ErrResponse` are named after the
HTTP status, not with an `Err` prefix:

- `BadRequestResponse(err)` — 400
- `ErrInternalServer` — 500 (a static `*ErrResponse` value,
  legitimately `Err`-prefixed because it's a sentinel response)
- `ErrNotFound` — 404 (same)

`Err`-prefixed identifiers should be sentinel error *values*, not
functions, so that `errors.Is(x, ErrFoo)` is the only thing
"`ErrFoo`" ever means.

## What we deliberately don't do

- **Stack traces in errors.** `pkg/errors` and `cockroachdb/errors`
  attach stack traces; we removed `pkg/errors` in DEP-005 and
  don't carry stacks in error chains. If you need traceability,
  attach structured fields to the log entry at the boundary, or
  rely on the wrapping chain (`fmt.Errorf` `%w`) to describe
  *what* failed.
- **Custom error types beyond `*ValidationError`.** Sentinels
  cover most needs. A typed error makes sense only when the
  caller wants to extract structured detail (a field name, a
  retry-after duration). For everything else, sentinel + wrap.
- **`errors.Join`.** Only useful for batch operations that should
  surface every failure at once (the `secrets.FileProvider` does
  this with a hand-rolled message); fine when needed, not the
  default.

## When in doubt

Read [core/inventory/service.go](../core/inventory/service.go) —
`Produce`, `CreateProduct`, `Reserve`, and `FillReserves` are the
canonical examples of the pattern after ERR-001.
