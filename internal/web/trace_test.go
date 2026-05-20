package web

import (
	"context"
	"strings"
	"testing"
)

func TestRenderSpanTree_FlatTwoChildren(t *testing.T) {
	spans := []jaegerSpan{
		{
			SpanID:        "root",
			OperationName: "POST /api/v1/inventory",
			StartTime:     1000,
			Duration:      5000,
			Tags:          []jaegerKVPair{{Key: "span.kind", Value: "server"}},
		},
		{
			SpanID:        "c1",
			OperationName: "pgx.Exec INSERT",
			StartTime:     1500,
			Duration:      2000,
			References:    []jaegerRef{{RefType: "CHILD_OF", SpanID: "root"}},
			Tags:          []jaegerKVPair{{Key: "span.kind", Value: "client"}},
		},
		{
			SpanID:        "c2",
			OperationName: "kafka.produce",
			StartTime:     4000,
			Duration:      500,
			References:    []jaegerRef{{RefType: "CHILD_OF", SpanID: "root"}},
			Tags:          []jaegerKVPair{{Key: "span.kind", Value: "producer"}},
		},
	}

	out := RenderSpanTree(spans, 30)

	for _, want := range []string{
		"SERVER", "CLIENT", "PRODUCER",
		"POST /api/v1/inventory", "pgx.Exec INSERT", "kafka.produce",
		"├─", "└─",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("RenderSpanTree missing %q\n%s", want, out)
		}
	}
}

func TestRenderSpanTree_OverflowMarker(t *testing.T) {
	spans := make([]jaegerSpan, 35)
	for i := range spans {
		spans[i] = jaegerSpan{
			SpanID:        spanIDFor(i),
			OperationName: "op",
			StartTime:     int64(i),
			Duration:      10,
		}
		if i > 0 {
			spans[i].References = []jaegerRef{{RefType: "CHILD_OF", SpanID: spanIDFor(0)}}
		}
	}
	out := RenderSpanTree(spans, 10)
	if !strings.Contains(out, "…+") {
		t.Errorf("expected overflow marker for >limit spans; got\n%s", out)
	}
}

func TestRenderSpanTree_Empty(t *testing.T) {
	if got := RenderSpanTree(nil, 30); got != "" {
		t.Errorf("nil spans should render empty, got %q", got)
	}
}

func spanIDFor(i int) string {
	return string(rune('a'+i%26)) + string(rune('a'+(i/26)%26))
}

func TestTraceRenderer_DisabledReturnsNote(t *testing.T) {
	r := newTraceRenderer("")
	tree, note := r.Render(context.Background(), "any-trace")
	if tree != "" || note == "" {
		t.Errorf("disabled renderer: want empty tree + non-empty note, got tree=%q note=%q", tree, note)
	}
}

func TestTraceRenderer_NoTraceIDNote(t *testing.T) {
	r := newTraceRenderer("http://jaeger.example.invalid")
	tree, note := r.Render(context.Background(), "")
	if tree != "" || !strings.Contains(note, "no trace id") {
		t.Errorf("missing trace id should produce note; got tree=%q note=%q", tree, note)
	}
}
