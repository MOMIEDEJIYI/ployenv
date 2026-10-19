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
	neturl "net/url"
	"os"
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

// GitHub API requests may authenticate with the ambient token. The two seams
// below keep that decision testable and keep the token away from every other
// host (mirrors, CDNs, release pages).
var (
	githubAPIHost   = "api.github.com"
	githubAPIScheme = "https"
)

// githubToken returns the token from the environment, or "" when unset. It is
// only ever sent over https to the API host above; ployenv never logs it.
func githubToken() string { return strings.TrimSpace(os.Getenv("GITHUB_TOKEN")) }

// wantsToken reports whether a request to rawurl would carry the ambient token.
func wantsToken(rawurl string) bool {
	u, err := neturl.Parse(rawurl)
	if err != nil || u.Host != githubAPIHost || u.Scheme != githubAPIScheme {
		return false
	}
	return githubToken() != ""
}

// newRequest builds a GET that expires after timeout. The caller owns the
// returned cancel function and must call it once the body is closed.
func newRequest(url string, timeout time.Duration) (*http.Request, context.CancelFunc, error) {
	return newRequestFor(url, timeout, true)
}

// newRequestFor is newRequest with the token decision spelled out, so a rejected
// token can be retried anonymously.
func newRequestFor(url string, timeout time.Duration, withToken bool) (*http.Request, context.CancelFunc, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "*/*")
	// api.github.com rate-limits anonymous callers hard; an ambient token lifts
	// the quota a lot. Only that origin gets it (never a mirror or CDN).
	if withToken && wantsToken(url) {
		req.Header.Set("Authorization", "Bearer "+githubToken())
	}
	return req, cancel, nil
}

// ShortURL truncates long URLs for readable log lines.
func ShortURL(u string) string {
	if len(u) > 100 {
		return u[:100] + "..."
	}
	return u
}

// getBytes fetches a small metadata document and returns its body.
func getBytes(url string) ([]byte, error) {
	body, status, err := getBytesWith(url, true)
	if err == nil {
		return body, nil
	}
	// A stale ambient token must not turn a request that works anonymously into a
	// failure: api.github.com answers 401 for a bad credential but 200 without one.
	if (status == http.StatusUnauthorized || status == http.StatusForbidden) && wantsToken(url) {
		logx.Verbosef("api.github.com rejected the ambient GITHUB_TOKEN (HTTP %d); retrying anonymously", status)
		if body, _, anonErr := getBytesWith(url, false); anonErr == nil {
			return body, nil
		}
	}
	return nil, err
}

// getBytesWith performs one GET, reporting the status code alongside the error so
// the caller can tell a rejected credential from any other failure.
func getBytesWith(url string, withToken bool) ([]byte, int, error) {
	req, cancel, err := newRequestFor(url, metadataTimeout, withToken)
	if err != nil {
		return nil, 0, err
	}
	defer cancel()
	logx.Capture("GET %s", url)
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return nil, resp.StatusCode, fmt.Errorf("GET %s returned HTTP %d %s", ShortURL(url), resp.StatusCode, strings.TrimSpace(string(body)))
	}
	body, err := io.ReadAll(resp.Body)
	return body, resp.StatusCode, err
}

// GetJSON fetches a JSON document into v.
func GetJSON(url string, v any) error {
	body, err := getBytes(url)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, v)
}

// GetText fetches a document as text. Some upstream indexes are XML rather than
// JSON (Maven's maven-metadata.xml), so the body is returned as-is and the
// caller parses it.
func GetText(url string) (string, error) {
	body, err := getBytes(url)
	if err != nil {
		return "", err
	}
	return string(body), nil
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

// GetTextRetry is GetJSONRetry for a text document.
func GetTextRetry(url string) (string, error) {
	var (
		out string
		err error
	)
	for attempt := 1; attempt <= 3; attempt++ {
		if out, err = GetText(url); err == nil {
			return out, nil
		}
		logx.Verbosef("request failed (attempt %d): %v", attempt, err)
		if attempt < 3 {
			retrySleep(time.Duration(attempt*2) * time.Second)
		}
	}
	return "", err
}
