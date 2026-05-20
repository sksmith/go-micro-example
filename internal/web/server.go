// Package web serves the DSN-027 server-rendered HTMX UI mounted at
// /ui. The UI is a portfolio piece for the template — every capability
// the project demonstrates (Kafka, outbound REST, Redis cache,
// idempotency, rate limiting, RabbitMQ) gets a clickable "Try it"
// card whose response lands in a single scope pane on the right.
//
// The UI runs entirely off stdlib html/template + a vendored HTMX
// bundle so the demo works offline and there is no SPA toolchain to
// maintain. Static assets (CSS, the HTMX bundle, self-host font
// directory) and templates ride along inside the binary via
// embed.FS — the SEC-011 read-only filesystem refuses disk reads.
//
// Mounted by internal/app/routes.go only when cfg.UI.Enabled is true.
package web

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
)

// Mount returns the chi sub-router that should be mounted at /ui. The
// caller owns the URL prefix; the handler renders absolute paths
// (/ui/static/..., /ui/auth/token, etc.) so the router need not be
// nested under another prefix.
//
// jaegerQueryURL is forwarded to the trace renderer; empty disables
// the ASCII span hierarchy and the scope pane renders the response
// pieces without it. tokenIssuer mints a bearer JWT given HTTP Basic
// credentials; the /ui/auth/token handler relays the result into an
// HttpOnly cookie scoped to /ui so the other cards send the bearer
// automatically without storing the JWT in JS-accessible state.
func Mount(jaegerQueryURL string, tokenIssuer TokenIssuer) http.Handler {
	tmpl := mustParseTemplates()
	staticFS := mustSubFS("static")
	trace := newTraceRenderer(jaegerQueryURL)

	r := chi.NewRouter()

	r.Get("/", func(w http.ResponseWriter, req *http.Request) {
		page := indexData(req)
		renderHTML(w, tmpl, "index.gohtml", page)
	})

	// Static assets — CSS, vendored HTMX, fonts — served from the
	// embedded FS so the same binary is hermetic. http.FileServer
	// handles caching headers via etag/last-modified; we hard-code
	// a generous Cache-Control because the assets are versioned by
	// build (any change ships a new image).
	r.Get("/static/*", staticHandler(staticFS))

	r.Post("/auth/token", authHandler(tokenIssuer, tmpl))
	r.Post("/auth/logout", logoutHandler(tmpl))

	r.Post("/cards/{card}/try", cardTryHandler(tmpl, trace))

	// Refresh just the trace pane for a previously-issued request.
	// Jaeger's OTel-collector pipeline batches spans on a ~1s
	// interval, so the very first scope render after a card fires
	// often shows "trace not yet flushed to jaeger — retry shortly".
	// Reloading the whole card would issue a new request and a new
	// trace ID; this endpoint takes the original trace ID, re-queries
	// Jaeger, and returns just the inner trace fragment so HTMX can
	// swap it in place.
	r.Get("/trace/{traceID}", traceRefreshHandler(tmpl, trace))

	return r
}

// TokenIssuer is the surface the /ui/auth/token handler needs from
// the AuthApi. The concrete type lives in internal/auth and is
// adapted at the composition root so this package stays free of
// inventory/auth dependencies.
type TokenIssuer interface {
	IssueFromBasic(username, password string) (accessToken string, ttlSeconds int64, err error)
}

// staticHandler serves the embedded static FS. The route pattern
// "/static/*" places the captured asset path in chi's "*" url param,
// so the handler stays prefix-agnostic — it works the same whether
// the parent router mounts /ui directly or nests it under another
// prefix in the future.
func staticHandler(staticFS fs.FS) http.HandlerFunc {
	fileServer := http.FileServer(http.FS(staticFS))
	return func(w http.ResponseWriter, req *http.Request) {
		asset := chi.URLParam(req, "*")
		w.Header().Set("Cache-Control", "public, max-age=600")
		req2 := req.Clone(req.Context())
		req2.URL.Path = "/" + asset
		fileServer.ServeHTTP(w, req2)
	}
}

func authHandler(issuer TokenIssuer, tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if issuer == nil {
			http.Error(w, "auth unavailable", http.StatusServiceUnavailable)
			return
		}
		if err := req.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		username := strings.TrimSpace(req.PostForm.Get("username"))
		password := req.PostForm.Get("password")
		if username == "" || password == "" {
			http.Error(w, "username + password required", http.StatusBadRequest)
			return
		}
		token, ttl, err := issuer.IssueFromBasic(username, password)
		if err != nil {
			log.Ctx(req.Context()).Debug().Err(err).Msg("ui auth: token issuance failed")
			http.Error(w, "invalid credentials", http.StatusUnauthorized)
			return
		}

		// HttpOnly cookie scoped to /ui keeps the JWT off
		// document.cookie; HTMX picks it up automatically on
		// subsequent requests because cookies are scoped by path,
		// not by JS access.
		setAuthCookie(w, req, token, int(ttl))

		// Re-render the auth card in the "signed-in" shape so HTMX
		// can swap it in place. Going through html/template keeps
		// the username escape on the safe side of the boundary.
		renderHTML(w, tmpl, "card_auth.gohtml", IndexPage{AuthState: "ready"})
	}
}

func logoutHandler(tmpl *template.Template) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		setAuthCookie(w, req, "", -1)
		renderHTML(w, tmpl, "card_auth.gohtml", IndexPage{AuthState: "locked"})
	}
}

// setAuthCookie writes (or clears) the ui_token cookie with the
// canonical security attributes. Centralising the call so the
// Secure/HttpOnly/SameSite trio can't drift between the issue and
// logout paths. Secure is opportunistic: true under TLS or behind an
// `X-Forwarded-Proto: https` ingress; false on plain HTTP so a
// developer browser at http://localhost still receives the cookie.
// HttpOnly + SameSite=Strict are the load-bearing properties — they
// keep the JWT off document.cookie and away from cross-site forms.
//
// #nosec G124 -- Secure is opportunistic; HttpOnly+SameSite carry the security guarantee
//
//nolint:gosec // G124 false positive: Secure is conditional on TLS
func setAuthCookie(w http.ResponseWriter, req *http.Request, value string, maxAge int) {
	c := &http.Cookie{
		Name:     "ui_token",
		Value:    value,
		Path:     "/ui",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   maxAge,
		Secure:   req.TLS != nil || req.Header.Get("X-Forwarded-Proto") == "https",
	}
	http.SetCookie(w, c)
}

func isHTTPS(req *http.Request) bool {
	if req.TLS != nil {
		return true
	}
	return req.Header.Get("X-Forwarded-Proto") == "https"
}

func indexData(req *http.Request) IndexPage {
	authState := "locked"
	if c, err := req.Cookie("ui_token"); err == nil && c.Value != "" {
		authState = "ready"
	}
	return IndexPage{
		Title:       "operator console",
		ServerTime:  time.Now().UTC().Format("2006-01-02 15:04:05 UTC"),
		Cards:       capabilityCards(),
		AuthState:   authState,
		Year:        time.Now().UTC().Year(),
		RunID:       runID(),
		Description: "Every input/output surface, exercised end-to-end.",
	}
}

// IndexPage is the view model for index.gohtml. Exported so render
// tests in this package can construct deterministic input.
type IndexPage struct {
	Title       string
	Description string
	ServerTime  string
	AuthState   string
	Year        int
	RunID       string
	Cards       []CapabilityCard
}

// CapabilityCard renders one inspector-row entry on the left rail.
type CapabilityCard struct {
	Slug       string
	Ticket     string
	Title      string
	Body       string
	Method     string
	Path       string
	NeedsAuth  bool
	BannerLine string
}

func capabilityCards() []CapabilityCard {
	return []CapabilityCard{
		{
			Slug:       "rest-create",
			Ticket:     "DSN-026",
			Title:      "REST create",
			Body:       "PUT a new product through the inventory API and read it back.",
			Method:     "PUT",
			Path:       "/api/v1/inventory",
			NeedsAuth:  true,
			BannerLine: "inventory.create",
		},
		{
			Slug:       "kafka-publish",
			Ticket:     "DSN-016",
			Title:      "Kafka publish",
			Body:       "Emit a RecordProduction command, watch the outbound product-quantity-changed event land.",
			Method:     "PUT",
			Path:       "/api/v1/inventory/{sku}/productionEvent",
			NeedsAuth:  true,
			BannerLine: "kafka.produce",
		},
		{
			Slug:       "rest-outbound",
			Ticket:     "DSN-018",
			Title:      "Outbound REST",
			Body:       "Hit the catalog client; observe retries, deadlines, and OTel client span.",
			Method:     "GET",
			Path:       "/api/v1/inventory/{sku}",
			NeedsAuth:  true,
			BannerLine: "catalog.lookup",
		},
		{
			Slug:       "redis-cache",
			Ticket:     "DSN-020",
			Title:      "Redis cache",
			Body:       "Read the same SKU twice; the second response is a cache hit on the response cache.",
			Method:     "GET",
			Path:       "/api/v1/inventory/{sku}",
			NeedsAuth:  true,
			BannerLine: "cache.aside",
		},
		{
			Slug:       "idempotency",
			Ticket:     "DSN-021a",
			Title:      "Idempotency",
			Body:       "Repeat a mutating PUT with the same Idempotency-Key — byte-identical response, only one effect.",
			Method:     "PUT",
			Path:       "/api/v1/inventory",
			NeedsAuth:  true,
			BannerLine: "idem.replay",
		},
		{
			Slug:       "rate-limit",
			Ticket:     "DSN-021b",
			Title:      "Rate limit",
			Body:       "Hammer /auth/token until the Redis-backed bucket says 429.",
			Method:     "POST",
			Path:       "/auth/token",
			NeedsAuth:  false,
			BannerLine: "rl:auth",
		},
		{
			Slug:       "user-cache",
			Ticket:     "DSN-021c",
			Title:      "User cache",
			Body:       "Authenticate twice; second auth resolves the user row from Redis instead of Postgres.",
			Method:     "POST",
			Path:       "/auth/token",
			NeedsAuth:  false,
			BannerLine: "cache.user",
		},
		{
			Slug:       "rabbitmq",
			Ticket:     "DSN-024",
			Title:      "RabbitMQ",
			Body:       "Publish an inventory update; observe the AMQP envelope land on the exchange.",
			Method:     "PUT",
			Path:       "/api/v1/inventory/{sku}/productionEvent",
			NeedsAuth:  true,
			BannerLine: "amqp.publish",
		},
		{
			Slug:       "grpc",
			Ticket:     "DSN-022",
			Title:      "gRPC",
			Body:       "Pending DSN-022. Listed here so the surface is visible from day one.",
			Method:     "RPC",
			Path:       "inventory.v1.Inventory/GetProduct",
			NeedsAuth:  true,
			BannerLine: "grpc.invoke",
		},
	}
}

func renderHTML(w http.ResponseWriter, tmpl *template.Template, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Error().Err(err).Str("template", name).Msg("ui render failed")
		// Headers are likely already flushed; best we can do is
		// truncate without panicking.
	}
}

//go:embed templates static
var assets embed.FS

func mustSubFS(dir string) fs.FS {
	sub, err := fs.Sub(assets, dir)
	if err != nil {
		panic(fmt.Errorf("web.mustSubFS(%q): %w", dir, err))
	}
	return sub
}

func mustParseTemplates() *template.Template {
	funcMap := template.FuncMap{
		"upper": strings.ToUpper,
		"join":  strings.Join,
		// tracePane wraps the three fields the trace_pane partial
		// reads so scope.gohtml can construct one inline. html/template
		// has no struct-literal syntax of its own.
		"tracePane": func(traceID, spanTree, note string) TracePane {
			return TracePane{TraceID: traceID, SpanTree: spanTree, Note: note}
		},
	}
	t, err := template.New("ui").Funcs(funcMap).ParseFS(assets,
		"templates/*.gohtml",
	)
	if err != nil {
		panic(fmt.Errorf("web.mustParseTemplates: %w", err))
	}
	return t
}

// runID is a stable per-process identifier shown as the watermark in
// the bottom-right corner. Generated once at package init from the
// startup time so a refresh keeps the same value but a redeploy gets
// a fresh one — useful when running multiple stacks side by side.
var runIDValue = fmt.Sprintf("r-%x", time.Now().UnixNano()&0xffffff)

func runID() string { return runIDValue }
