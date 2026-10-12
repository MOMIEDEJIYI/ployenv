// Package netx wraps the HTTP client used to fetch version indexes and
// download archives. It honors the ambient proxy settings and records every
// request so failures can be diagnosed after the fact.
package netx

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/MOMIEDEJIYI/ployenv/internal/logx"
)

const userAgent = "ployenv (+https://github.com/MOMIEDEJIYI/ployenv)"

const (
	// Metadata requests are small, so a hung index endpoint must fail fast
	// instead of stalling a command for half an hour (NFR-4, BUG-007).
	metadataTimeout = 45 * time.Second
	// Archives are large and get a generous budget.
	downloadTimeout = 30 * time.Minute
)

// A fresh connection per request is deliberate: pooled connections misbehave on
// some corporate proxies and CDNs, answering 404 for unrelated later paths.
var client = &http.Client{
	Transport: &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		ForceAttemptHTTP2:   true,
		DisableKeepAlives:   true,
		MaxIdleConns:        1,
		MaxIdleConnsPerHost: 1,
		IdleConnTimeout:     10 * time.Second,
	},
	// No client-wide timeout: every request carries its own deadline, so
	// metadata and downloads can use different budgets (BUG-007).
}

// newRequest builds a GET that expires after timeout. The caller owns the
// returned cancel function and must call it once the body is closed.
func newRequest(url string, timeout time.Duration) (*http.Request, context.CancelFunc, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "*/*")
	return req, cancel, nil
}

// ShortURL truncates long URLs for readable log lines.
func ShortURL(u string) string {
	if len(u) > 100 {
		return u[:100] + "..."
	}
	return u
}

// GetJSON fetches a JSON document into v.
func GetJSON(url string, v any) error {
	req, cancel, err := newRequest(url, metadataTimeout)
	if err != nil {
		return err
	}
	defer cancel()
	logx.Capture("GET %s", url)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("GET %s returned HTTP %d %s", ShortURL(url), resp.StatusCode, strings.TrimSpace(string(body)))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}

// retrySleep is a seam so tests can exercise the retry loop without waiting out
// the real backoff (2 s + 4 s would otherwise dominate the suite runtime).
var retrySleep = time.Sleep

// GetJSONRetry retries transient upstream failures (indexes sometimes 5xx).
func GetJSONRetry(url string, v any) error {
	var err error
	for attempt := 1; attempt <= 3; attempt++ {
		if err = GetJSON(url, v); err == nil {
			return nil
		}
		logx.Verbosef("request failed (attempt %d): %v", attempt, err)
		if attempt < 3 {
			retrySleep(time.Duration(attempt*2) * time.Second)
		}
	}
	return err
}
