package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// traceRenderer turns a Jaeger Query API response into the fixed-
// width ASCII span tree the scope pane shows. When the renderer is
// constructed without a base URL — the most common configuration on
// a developer laptop — Render is a no-op that returns ("", note) so
// the template can show the muted "trace pane disabled" line.
type traceRenderer struct {
	baseURL string
	hc      *http.Client
}

func newTraceRenderer(baseURL string) *traceRenderer {
	if baseURL == "" {
		return &traceRenderer{}
	}
	return &traceRenderer{
		baseURL: strings.TrimRight(baseURL, "/"),
		hc:      &http.Client{Timeout: 2 * time.Second},
	}
}

// Render fetches the trace identified by traceID from the configured
// Jaeger Query API and walks its span tree, returning a multi-line
// ASCII rendering and an optional muted note (used when the trace
// can't be fetched, isn't ready yet, or the renderer is disabled).
//
// The tree is capped at 30 spans. Overflow is signalled by an
// explicit "…+N more" marker — silent truncation hides whether the
// trace is missing data or just deep.
func (t *traceRenderer) Render(ctx context.Context, traceID string) (string, string) {
	if t == nil || t.baseURL == "" {
		return "", "jaeger query disabled — set ui.jaegerQueryUrl"
	}
	if traceID == "" {
		return "", "no trace id on response"
	}

	// baseURL is operator-configured (ui.jaegerQueryUrl). The trace
	// ID comes from either the response X-Trace-Id header (server-
	// generated) or the /ui/trace/{traceID} chi param, which the
	// handler gates with isHexID — so the only chars that ever
	// reach this URL are [0-9a-f]. gosec G704 fires on the dynamic
	// URL regardless; the bounded character set keeps it safe.
	url := t.baseURL + "/api/traces/" + traceID
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil) //#nosec G107,G704 -- baseURL operator-configured, traceID validated via isHexID
	if err != nil {
		return "", fmt.Sprintf("trace lookup error: %v", err)
	}
	resp, err := t.hc.Do(req) //#nosec G107,G704 -- see above
	if err != nil {
		return "", fmt.Sprintf("trace lookup error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return "", "trace not yet flushed to jaeger — retry shortly"
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Sprintf("jaeger query returned %s", resp.Status)
	}

	var payload jaegerPayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Sprintf("trace decode error: %v", err)
	}
	if len(payload.Data) == 0 || len(payload.Data[0].Spans) == 0 {
		return "", "trace has no spans"
	}
	return RenderSpanTree(payload.Data[0].Spans, 30), ""
}

// jaegerPayload is the minimal slice of /api/traces/<id> we read.
// Only the fields used by RenderSpanTree are declared — Jaeger's
// response carries many more.
type jaegerPayload struct {
	Data []struct {
		Spans []jaegerSpan `json:"spans"`
	} `json:"data"`
}

type jaegerSpan struct {
	SpanID        string         `json:"spanID"`
	OperationName string         `json:"operationName"`
	StartTime     int64          `json:"startTime"`
	Duration      int64          `json:"duration"`
	References    []jaegerRef    `json:"references"`
	Tags          []jaegerKVPair `json:"tags"`
}

type jaegerRef struct {
	RefType string `json:"refType"`
	SpanID  string `json:"spanID"`
}

type jaegerKVPair struct {
	Key   string      `json:"key"`
	Value interface{} `json:"value"`
}

// Span is the renderer-facing struct exported so tests can construct
// fixtures without touching the jaegerSpan internals.
type Span struct {
	ID      string
	Parent  string
	Name    string
	Kind    string // SERVER | INTERNAL | CLIENT | PRODUCER | CONSUMER
	StartUs int64
	DurUs   int64
}

// RenderSpanTree converts a flat list of spans into the fixed-width
// ASCII tree shown in the scope pane. The limit caps the rendered
// span count; the remainder is announced via "…+N more". Exported
// because the trace-tree shape is the differentiator and worth
// unit-testing in isolation.
func RenderSpanTree(spans []jaegerSpan, limit int) string {
	if len(spans) == 0 {
		return ""
	}

	flat := make([]Span, len(spans))
	for i, s := range spans {
		flat[i] = toSpan(s)
	}

	sort.SliceStable(flat, func(i, j int) bool { return flat[i].StartUs < flat[j].StartUs })

	maxDur := int64(0)
	for _, s := range flat {
		if s.DurUs > maxDur {
			maxDur = s.DurUs
		}
	}
	if maxDur == 0 {
		maxDur = 1
	}

	// Index by ID to look up children. Anything whose Parent is
	// not in the set is treated as a root — Jaeger can return
	// orphan spans when sub-services trickle in.
	byID := make(map[string]int, len(flat))
	for i, s := range flat {
		byID[s.ID] = i
	}

	children := make(map[string][]int, len(flat))
	roots := make([]int, 0, 4)
	for i, s := range flat {
		if _, ok := byID[s.Parent]; ok && s.Parent != "" {
			children[s.Parent] = append(children[s.Parent], i)
		} else {
			roots = append(roots, i)
		}
	}

	var b strings.Builder
	rendered := 0
	for _, r := range roots {
		walkSpan(&b, flat, children, r, "", true, &rendered, limit, maxDur)
	}

	if extra := len(flat) - rendered; extra > 0 {
		fmt.Fprintf(&b, "…+%d more\n", extra)
	}
	return b.String()
}

func walkSpan(b *strings.Builder, flat []Span, children map[string][]int, idx int, prefix string, isLast bool, rendered *int, limit int, maxDur int64) {
	if *rendered >= limit {
		return
	}
	branch := "├─"
	nextPrefix := prefix + "│  "
	if isLast {
		branch = "└─"
		nextPrefix = prefix + "   "
	}
	if prefix == "" {
		branch = ""
		nextPrefix = "  "
	}
	s := flat[idx]
	bar := durationBar(s.DurUs, maxDur, 12)
	kind := s.Kind
	if kind == "" {
		kind = "INTERNAL"
	}
	fmt.Fprintf(b, "%s%s %-8s %s %6dµs  %s\n", prefix, branch, kind, bar, s.DurUs, s.Name)
	*rendered++

	kids := children[s.ID]
	for i, ki := range kids {
		walkSpan(b, flat, children, ki, nextPrefix, i == len(kids)-1, rendered, limit, maxDur)
	}
}

// durationBar renders a relative duration as a 12-cell mono-bar using
// Unicode eighth blocks for sub-cell precision. The leftmost
// cell is always at least partly filled when DurUs > 0 so the
// shortest span still shows a non-empty bar.
func durationBar(dur, max, width int64) string {
	if dur <= 0 || max <= 0 {
		return strings.Repeat(" ", int(width))
	}
	totalEighths := dur * width * 8 / max
	if totalEighths < 1 {
		totalEighths = 1
	}
	full := totalEighths / 8
	rem := totalEighths % 8
	var b strings.Builder
	for i := int64(0); i < full && i < width; i++ {
		b.WriteRune('█')
	}
	if full < width && rem > 0 {
		b.WriteRune([]rune("·▏▎▍▌▋▊▉")[rem])
		full++
	}
	for i := full; i < width; i++ {
		b.WriteRune(' ')
	}
	return b.String()
}

func toSpan(j jaegerSpan) Span {
	kind := "INTERNAL"
	for _, t := range j.Tags {
		if t.Key == "span.kind" {
			if s, ok := t.Value.(string); ok {
				kind = strings.ToUpper(s)
			}
		}
	}
	parent := ""
	for _, ref := range j.References {
		if ref.RefType == "CHILD_OF" {
			parent = ref.SpanID
			break
		}
	}
	return Span{
		ID:      j.SpanID,
		Parent:  parent,
		Name:    j.OperationName,
		Kind:    kind,
		StartUs: j.StartTime,
		DurUs:   j.Duration,
	}
}
