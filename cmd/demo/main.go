// Command demo is the orchestrator from DSN-015: it waits for the
// app's /ready probe to succeed, runs each registered demo step
// against the running app, prints a summary table, and exits non-zero
// on any failure.
//
// Each capability ticket (DSN-016 through DSN-027) registers a step in
// the Steps slice. The runner is intentionally small — it's the
// thing a reader sees fire end-to-end after 'docker-compose up'.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	cfg := loadConfig()
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Deadline)
	defer cancel()

	fmt.Printf("demo: waiting for %s%s (max %s)\n", cfg.BaseURL, cfg.ReadyPath, cfg.Deadline)
	if err := waitReady(ctx, cfg); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "demo: app never became ready: %v\n", err)
		os.Exit(2)
	}
	fmt.Printf("demo: %s%s returned 200; running %d step(s)\n\n", cfg.BaseURL, cfg.ReadyPath, len(Steps))

	results := make([]Result, 0, len(Steps))
	for _, step := range Steps {
		results = append(results, runStep(ctx, cfg, step))
	}

	printSummary(os.Stdout, results)

	for _, r := range results {
		if r.Status != StatusPass {
			os.Exit(1)
		}
	}
}

// Config captures the runtime knobs read from env vars.
type Config struct {
	BaseURL        string
	ReadyPath      string
	AdminUser      string
	AdminPass      string
	Deadline       time.Duration
	PerStepTimeout time.Duration
	PollInterval   time.Duration

	// DSN-016 Kafka step. Empty Brokers skips the step gracefully.
	KafkaBrokers       string
	KafkaCommandsTopic string
	KafkaEventsTopic   string
	KafkaDemoGroup     string
}

func loadConfig() Config {
	return Config{
		BaseURL:            envOr("DEMO_BASE_URL", "http://localhost:8080"),
		ReadyPath:          envOr("DEMO_READY_PATH", "/ready"),
		AdminUser:          envOr("DEMO_ADMIN_USER", "admin"),
		AdminPass:          envOr("DEMO_ADMIN_PASS", "admin"),
		Deadline:           envDuration("DEMO_DEADLINE", 90*time.Second),
		PerStepTimeout:     envDuration("DEMO_STEP_TIMEOUT", 15*time.Second),
		PollInterval:       envDuration("DEMO_POLL_INTERVAL", 2*time.Second),
		KafkaBrokers:       envOr("DEMO_KAFKA_BROKERS", ""),
		KafkaCommandsTopic: envOr("DEMO_KAFKA_COMMANDS_TOPIC", "inventory.commands.v1"),
		KafkaEventsTopic:   envOr("DEMO_KAFKA_EVENTS_TOPIC", "inventory.product-quantity-changed.v1"),
		KafkaDemoGroup:     envOr("DEMO_KAFKA_DEMO_GROUP", "demo-watcher"),
	}
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func envDuration(name string, def time.Duration) time.Duration {
	if v := os.Getenv(name); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func waitReady(ctx context.Context, cfg Config) error {
	url := cfg.BaseURL + cfg.ReadyPath
	hc := &http.Client{Timeout: 2 * time.Second}
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		res, err := hc.Do(req)
		if err == nil && res.StatusCode == http.StatusOK {
			_ = res.Body.Close()
			return nil
		}
		if res != nil {
			_ = res.Body.Close()
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("readiness timeout: %w", ctx.Err())
		case <-time.After(cfg.PollInterval):
		}
	}
}

func runStep(parent context.Context, cfg Config, step Step) Result {
	ctx, cancel := context.WithTimeout(parent, cfg.PerStepTimeout)
	defer cancel()
	fmt.Printf("→ %s: %s\n", step.Capability, step.Name)

	start := time.Now()
	traceID, err := step.Run(ctx, cfg)
	dur := time.Since(start)

	r := Result{Capability: step.Capability, Name: step.Name, Latency: dur, TraceID: traceID}
	if err != nil {
		r.Status = StatusFail
		r.Reason = err.Error()
		fmt.Printf("   ✗ %s (%s)\n", err, dur.Round(time.Millisecond))
	} else {
		r.Status = StatusPass
		fmt.Printf("   ✓ %s\n", dur.Round(time.Millisecond))
	}
	return r
}

// Result is one row of the summary table.
type Result struct {
	Capability string
	Name       string
	Status     string
	Latency    time.Duration
	TraceID    string
	Reason     string
}

const (
	StatusPass = "pass"
	StatusFail = "fail"
)

func printSummary(w *os.File, results []Result) {
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "demo summary")
	_, _ = fmt.Fprintln(w, strings.Repeat("─", 72))
	_, _ = fmt.Fprintf(w, "%-14s %-32s %-6s %10s %s\n", "CAPABILITY", "STEP", "STATUS", "LATENCY", "TRACE")
	for _, r := range results {
		trace := r.TraceID
		if trace == "" {
			trace = "—"
		}
		_, _ = fmt.Fprintf(w, "%-14s %-32s %-6s %10s %s\n",
			r.Capability,
			truncate(r.Name, 32),
			r.Status,
			r.Latency.Round(time.Millisecond),
			trace,
		)
		if r.Status == StatusFail && r.Reason != "" {
			_, _ = fmt.Fprintf(w, "%-14s   reason: %s\n", "", r.Reason)
		}
	}
	_, _ = fmt.Fprintln(w, strings.Repeat("─", 72))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
