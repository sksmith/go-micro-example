package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// runCacheRead exercises DSN-020. The step:
//  1. Creates a fresh SKU so the inventory cache is guaranteed cold
//     for this key.
//  2. Reads /metrics, captures the current cache hit + miss counters
//     scoped to inv:product.
//  3. Sends two GETs for the SKU in quick succession.
//  4. Reads /metrics again. Asserts the miss counter went up by 1
//     (first call seeded the cache) AND the hit counter went up by
//     at least 1 (second call read from cache).
//
// Failure modes the assertion catches: cache misconfigured (both
// counters stay at zero), invalidation broken (miss count higher than
// expected), or the inventory handler bypassed the cache entirely
// (hit count never increments).
func runCacheRead(ctx context.Context, cfg Config) (string, error) {
	tok, err := fetchToken(ctx, cfg)
	if err != nil {
		return "", fmt.Errorf("token: %w", err)
	}

	sku := fmt.Sprintf("cache-sku-%d", nowNanos())
	if err := createProduct(ctx, cfg, tok, sku); err != nil {
		return "", fmt.Errorf("seed product: %w", err)
	}

	hitsBefore, missesBefore, err := readCacheCounters(ctx, cfg)
	if err != nil {
		return "", fmt.Errorf("read metrics (before): %w", err)
	}

	// Two reads back-to-back. The first should be a miss; the second
	// a hit. (CreateProduct also triggers a cache invalidation via
	// publishInventory at zero quantity, but the cache is empty for
	// this SKU anyway — the first GET below is the seed.)
	if _, _, err := getInventory(ctx, cfg, tok, sku); err != nil {
		return "", fmt.Errorf("first GET: %w", err)
	}
	if _, _, err := getInventory(ctx, cfg, tok, sku); err != nil {
		return "", fmt.Errorf("second GET: %w", err)
	}

	hitsAfter, missesAfter, err := readCacheCounters(ctx, cfg)
	if err != nil {
		return "", fmt.Errorf("read metrics (after): %w", err)
	}

	hitDelta := hitsAfter - hitsBefore
	missDelta := missesAfter - missesBefore
	if missDelta < 1 {
		return "", fmt.Errorf("cache miss counter only moved by %g; want >=1 (cache-aside should have recorded a miss on the first GET)", missDelta)
	}
	if hitDelta < 1 {
		return "", fmt.Errorf("cache hit counter only moved by %g; want >=1 (second GET should have hit the cache)", hitDelta)
	}
	return "", nil
}

// readCacheCounters scrapes /metrics and returns the current hit and
// miss counters scoped to the inv:product prefix. The metric family
// shape comes from core/cache: cache_requests_total{prefix,outcome}.
func readCacheCounters(ctx context.Context, cfg Config) (hits, misses float64, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.BaseURL+"/metrics", nil)
	if err != nil {
		return 0, 0, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		return 0, 0, fmt.Errorf("GET /metrics: status %d body=%s", res.StatusCode, body)
	}
	scanner := bufio.NewScanner(res.Body)
	// Some metric label orderings (Prometheus stores labels
	// alphabetically by name) put outcome before prefix; tolerate
	// either by matching on label substrings.
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "cache_requests_total{") {
			continue
		}
		if !strings.Contains(line, `prefix="inv:product"`) {
			continue
		}
		val, parseErr := parseMetricValue(line)
		if parseErr != nil {
			return 0, 0, parseErr
		}
		switch {
		case strings.Contains(line, `outcome="hit"`):
			hits = val
		case strings.Contains(line, `outcome="miss"`):
			misses = val
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, err
	}
	return hits, misses, nil
}

// parseMetricValue plucks the float64 value from a Prometheus text
// line — everything after the last whitespace. Comments and labels
// are ignored by the caller's filter.
func parseMetricValue(line string) (float64, error) {
	idx := strings.LastIndexByte(line, ' ')
	if idx < 0 {
		return 0, fmt.Errorf("malformed metric line: %s", line)
	}
	return strconv.ParseFloat(strings.TrimSpace(line[idx+1:]), 64)
}
