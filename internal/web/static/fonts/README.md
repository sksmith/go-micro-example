# DSN-027 UI fonts

This directory is the canonical home of the two self-hosted typefaces
the operator console uses. Until the woff2 binaries land the page
resolves the faces via `local()` calls in `ui.css`, which picks up
the user's installed copy of:

- display: JetBrains Mono → IBM Plex Mono → Berkeley Mono → Commit
  Mono → Menlo (system fallback)
- body: IBM Plex Serif → EB Garamond → Iowan Old Style → Georgia

When the binaries are added back, drop them in here as
`op-mono.woff2` / `op-serif.woff2` and re-add the `url(...) format("woff2")`
clause to `ui.css`. Both faces ship under SIL OFL / Apache 2.0.
