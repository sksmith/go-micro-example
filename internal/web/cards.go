package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// ScopeView is the right-pane response view rendered after a card
// fires. The fields map 1:1 to the spec in DSN-027: enough to give a
// reader the full HTTP picture plus the ASCII span tree from Jaeger.
type ScopeView struct {
	Method          string
	URL             string
	RequestHeaders  []KeyValue
	RequestBody     string
	Status          int
	StatusText      string
	StatusKind      string // "ok" | "warn" | "err" — drives the accent colour
	ResponseHeaders []KeyValue
	ResponseBody    string
	LatencyMs       int64
	RequestID       string
	TraceID         string
	SpanTree        string // pre-rendered ASCII
	SpanTreeNote    string // muted note shown alongside the tree (e.g. "jaeger disabled")
	Card            string
}

type KeyValue struct {
	K string
	V string
}

func cardTryHandler(tmpl *template.Template, trace *traceRenderer) http.HandlerFunc {
	cards := indexCards()
	return func(w http.ResponseWriter, req *http.Request) {
		slug := chi.URLParam(req, "card")
		card, ok := cards[slug]
		if !ok {
			http.Error(w, "unknown card", http.StatusNotFound)
			return
		}

		// Read the bearer cookie. Mutating cards bail with a locked
		// scope view if the user hasn't signed in yet.
		token := ""
		if c, err := req.Cookie("ui_token"); err == nil {
			token = c.Value
		}
		if card.NeedsAuth && token == "" {
			view := lockedScope(card)
			renderScope(w, tmpl, view)
			return
		}

		// Each "Try it" runs a small canned scenario per card so the
		// reader gets a meaningful response without a textbox. The
		// scenarios stay close to the real handler — they just use
		// fixed inputs.
		view := runCardScenario(req.Context(), card, token, trace, demoBaseURL(req))
		renderScope(w, tmpl, view)
	}
}

func indexCards() map[string]CapabilityCard {
	out := make(map[string]CapabilityCard, 16)
	for _, c := range capabilityCards() {
		out[c.Slug] = c
	}
	return out
}

func lockedScope(card CapabilityCard) ScopeView {
	return ScopeView{
		Card:         card.Title,
		Method:       card.Method,
		URL:          card.Path,
		Status:       http.StatusUnauthorized,
		StatusText:   "locked",
		StatusKind:   "warn",
		ResponseBody: "{\n  \"detail\": \"sign in via the Authenticate card first\"\n}",
		SpanTreeNote: "no trace — request never left the browser",
	}
}

func renderScope(w http.ResponseWriter, tmpl *template.Template, view ScopeView) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, "scope.gohtml", view); err != nil {
		http.Error(w, "scope render failed", http.StatusInternalServerError)
	}
}

// TracePane is the view model for trace_pane.gohtml. The same partial
// renders both inline inside scope.gohtml and as a standalone HTMX
// swap target so the user can re-query Jaeger without re-firing the
// underlying request.
type TracePane struct {
	TraceID  string
	SpanTree string
	Note     string
}

// traceRefreshHandler re-queries Jaeger for an existing trace ID and
// returns just the trace-pane fragment so an HTMX swap can update
// the scope view in place. The trace ID comes from the chi URL
// param; isHexID guards the path before it reaches Jaeger.
func traceRefreshHandler(tmpl *template.Template, trace *traceRenderer) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		traceID := chi.URLParam(req, "traceID")
		if !isHexID(traceID) {
			http.Error(w, "bad trace id", http.StatusBadRequest)
			return
		}
		tree, note := trace.Render(req.Context(), traceID)
		pane := TracePane{TraceID: traceID, SpanTree: tree, Note: note}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := tmpl.ExecuteTemplate(w, "trace_pane.gohtml", pane); err != nil {
			http.Error(w, "trace render failed", http.StatusInternalServerError)
		}
	}
}

// isHexID guards the /ui/trace/{traceID} path from arbitrary input
// before it reaches the Jaeger Query API. Jaeger accepts 16- and
// 32-char lowercase hex; we accept 1–32 to leave room for older
// 64-bit IDs that aren't zero-padded.
func isHexID(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
}

func demoBaseURL(req *http.Request) string {
	scheme := "http"
	if isHTTPS(req) {
		scheme = "https"
	}
	return scheme + "://" + req.Host
}

// runCardScenario performs the canned request for the chosen card and
// returns a populated ScopeView. The function is deliberately
// synchronous and per-card — a small switch is easier to read than a
// generic dispatcher when each card has slightly different inputs.
func runCardScenario(ctx context.Context, card CapabilityCard, token string, trace *traceRenderer, baseURL string) ScopeView {
	hc := &http.Client{Timeout: 5 * time.Second}

	view := ScopeView{
		Card:   card.Title,
		Method: card.Method,
		URL:    baseURL + card.Path,
	}

	switch card.Slug {
	case "rest-create":
		body := []byte(`{"sku":"DEMO-SKU-001","name":"Operator Console Widget","upc":"0000000001"}`)
		view.RequestBody = prettyJSON(body)
		view.URL = baseURL + "/api/v1/inventory"
		view.Method = http.MethodPut
		req := newDemoRequest(ctx, http.MethodPut, view.URL, body, token, "")
		view.RequestHeaders = headerKVs(req.Header)
		executeAndFill(hc, req, &view, trace)

	case "kafka-publish", "rabbitmq":
		body := []byte(`{"requestId":"ui-demo","quantity":5}`)
		view.URL = baseURL + "/api/v1/inventory/DEMO-SKU-001/productionEvent"
		view.Method = http.MethodPut
		view.RequestBody = prettyJSON(body)
		req := newDemoRequest(ctx, http.MethodPut, view.URL, body, token, "ui-"+card.Slug)
		view.RequestHeaders = headerKVs(req.Header)
		executeAndFill(hc, req, &view, trace)

	case "rest-outbound", "redis-cache":
		view.URL = baseURL + "/api/v1/inventory/DEMO-SKU-001"
		req := newDemoRequest(ctx, http.MethodGet, view.URL, nil, token, "")
		view.RequestHeaders = headerKVs(req.Header)
		executeAndFill(hc, req, &view, trace)

	case "idempotency":
		body := []byte(`{"sku":"DEMO-IDEM-001","name":"Idempotent Widget","upc":"0000000002"}`)
		view.URL = baseURL + "/api/v1/inventory"
		view.Method = http.MethodPut
		view.RequestBody = prettyJSON(body)
		req := newDemoRequest(ctx, http.MethodPut, view.URL, body, token, "ui-idem-fixed-key")
		view.RequestHeaders = headerKVs(req.Header)
		executeAndFill(hc, req, &view, trace)

	case "rate-limit", "user-cache":
		view.URL = baseURL + "/auth/token"
		req := newDemoRequest(ctx, http.MethodPost, view.URL, nil, "", "")
		req.SetBasicAuth("admin", "admin")
		view.RequestHeaders = headerKVs(req.Header)
		executeAndFill(hc, req, &view, trace)

	case "grpc":
		view.URL = "inventory.v1.Inventory/GetProduct"
		view.Status = http.StatusNotImplemented
		view.StatusText = "pending DSN-022"
		view.StatusKind = "warn"
		view.ResponseBody = "{\n  \"detail\": \"gRPC surface lands with DSN-022; placeholder shown so the card grid is dense from day one.\"\n}"
		view.SpanTreeNote = "no trace — feature not wired"

	default:
		view.Status = http.StatusInternalServerError
		view.StatusText = "no scenario"
		view.StatusKind = "err"
	}

	return view
}

// newDemoRequest builds an *http.Request against the same server
// hosting the UI. The URL is always derived from req.Host so the
// gosec G107 dynamic-URL flag is a false positive — the call goes to
// the very binary that just rendered the page.
func newDemoRequest(ctx context.Context, method, url string, body []byte, token, idemKey string) *http.Request {
	var reader *bytes.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	var req *http.Request
	if reader != nil {
		req, _ = http.NewRequestWithContext(ctx, method, url, reader) //#nosec G107,G704 -- same-origin demo request to req.Host
	} else {
		req, _ = http.NewRequestWithContext(ctx, method, url, nil) //#nosec G107,G704 -- same-origin demo request to req.Host
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if idemKey != "" {
		req.Header.Set("Idempotency-Key", idemKey)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}

// executeAndFill performs the canned scenario's HTTP request and
// fills the ScopeView with the response. The destination URL is
// always derived from req.Host (the same server hosting the UI), so
// the gosec G107/G704 SSRF flag on hc.Do is a false positive — the
// UI exercises the *same* service it's served from.
func executeAndFill(hc *http.Client, req *http.Request, view *ScopeView, trace *traceRenderer) {
	start := time.Now()
	resp, err := hc.Do(req) //#nosec G107,G704 -- same-origin demo request to req.Host

	view.LatencyMs = time.Since(start).Milliseconds()
	if err != nil {
		view.Status = 0
		view.StatusText = "transport error"
		view.StatusKind = "err"
		view.ResponseBody = "{\n  \"error\": " + jsonQuote(err.Error()) + "\n}"
		view.SpanTreeNote = "no trace — request never reached the server"
		return
	}
	defer func() { _ = resp.Body.Close() }()

	view.Status = resp.StatusCode
	view.StatusText = http.StatusText(resp.StatusCode)
	view.StatusKind = statusKind(resp.StatusCode)
	view.ResponseHeaders = headerKVs(resp.Header)
	view.RequestID = resp.Header.Get("X-Request-Id")
	view.TraceID = resp.Header.Get("X-Trace-Id")

	body := readLimited(resp, 1<<14)
	view.ResponseBody = prettyJSON(body)

	if trace != nil {
		tree, note := trace.Render(req.Context(), view.TraceID)
		view.SpanTree = tree
		view.SpanTreeNote = note
	}
}

func statusKind(code int) string {
	switch {
	case code >= 200 && code < 300:
		return "ok"
	case code >= 400 && code < 500:
		return "warn"
	case code >= 500:
		return "err"
	}
	return "muted"
}

func readLimited(resp *http.Response, max int64) []byte {
	buf := &bytes.Buffer{}
	_, _ = buf.ReadFrom(http.MaxBytesReader(nil, resp.Body, max))
	return buf.Bytes()
}

// prettyJSON re-encodes a JSON body with 2-space indentation. Falls
// back to the raw string if the body isn't valid JSON — the scope
// pane should still show what came back, even on a 500 with an HTML
// error page.
func prettyJSON(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, b, "", "  "); err == nil {
		return pretty.String()
	}
	return string(b)
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func headerKVs(h http.Header) []KeyValue {
	out := make([]KeyValue, 0, len(h))
	for k, v := range h {
		// Don't leak Authorization in the visible scope pane.
		// Showing "<redacted>" makes the redaction explicit
		// rather than silently dropping the row.
		val := strings.Join(v, ", ")
		if strings.EqualFold(k, "Authorization") {
			val = "<redacted>"
		}
		out = append(out, KeyValue{K: k, V: val})
	}
	return out
}

// Render is exposed so render_test can drive the scope template
// directly without standing up a chi router.
func (s *ScopeView) RenderHTML(tmpl *template.Template) (string, error) {
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "scope.gohtml", s); err != nil {
		return "", fmt.Errorf("execute scope: %w", err)
	}
	return buf.String(), nil
}
