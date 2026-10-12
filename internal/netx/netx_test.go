package netx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestShortURLLeavesShortURLsAlone(t *testing.T) {
	short := "https://example.com/a.json"
	if got := ShortURL(short); got != short {
		t.Errorf("ShortURL(%q) = %q, want it unchanged", short, got)
	}
	long := "https://example.com/" + strings.Repeat("x", 200)
	got := ShortURL(long)
	if len(got) != 103 || !strings.HasSuffix(got, "...") {
		t.Errorf("ShortURL(long) = %q (len %d), want 100 chars + \"...\"", got, len(got))
	}
	if !strings.HasPrefix(got, long[:100]) {
		t.Errorf("ShortURL(long) did not keep the first 100 characters: %q", got)
	}
}

func TestNewRequestSetsHeadersAndDeadline(t *testing.T) {
	req, cancel, err := newRequest("https://example.com/x", time.Minute)
	if err != nil {
		t.Fatalf("newRequest returned an error: %v", err)
	}
	defer cancel()
	if req.Method != http.MethodGet {
		t.Errorf("method = %s, want GET", req.Method)
	}
	if got := req.Header.Get("User-Agent"); got != userAgent {
		t.Errorf("User-Agent = %q, want %q", got, userAgent)
	}
	if got := req.Header.Get("Accept"); got != "*/*" {
		t.Errorf("Accept = %q, want */*", got)
	}
	if _, ok := req.Context().Deadline(); !ok {
		t.Error("the request has no deadline, so a hung endpoint would stall the command")
	}
}

func TestNewRequestRejectsBadURL(t *testing.T) {
	req, cancel, err := newRequest("http://[::1", time.Second)
	if err == nil {
		t.Fatalf("expected an error for a malformed URL, got request %v", req)
	}
	if req != nil || cancel != nil {
		t.Error("a failed newRequest must not hand back a request or a cancel func")
	}
}

func TestGetJSONDecodesBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != userAgent {
			t.Errorf("server saw User-Agent %q, want %q", got, userAgent)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"ployenv","count":2}`))
	}))
	defer srv.Close()

	var got struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	if err := GetJSON(srv.URL+"/index.json", &got); err != nil {
		t.Fatalf("GetJSON returned an error: %v", err)
	}
	if got.Name != "ployenv" || got.Count != 2 {
		t.Errorf("decoded %+v, want {ployenv 2}", got)
	}
}

func TestGetJSONReportsHTTPStatusAndBodyExcerpt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("  upstream is down  "))
	}))
	defer srv.Close()

	err := GetJSON(srv.URL+"/index.json", &map[string]any{})
	if err == nil {
		t.Fatal("expected an error for HTTP 503")
	}
	msg := err.Error()
	if !strings.Contains(msg, "HTTP 503") {
		t.Errorf("the error should name the status: %v", err)
	}
	if !strings.Contains(msg, "upstream is down") {
		t.Errorf("the error should quote the body (trimmed): %v", err)
	}
}

func TestGetJSONRejectsMalformedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json at all"))
	}))
	defer srv.Close()
	if err := GetJSON(srv.URL, &map[string]any{}); err == nil {
		t.Fatal("expected a decoding error for a non-JSON body")
	}
}

// GetJSONRetry must survive transient 5xx and stop once the body decodes. The
// backoff seam keeps this instant instead of 6 seconds.
func TestGetJSONRetryRecoversFromTransientFailures(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	restore := retrySleep
	slept := time.Duration(0)
	retrySleep = func(d time.Duration) { slept += d }
	defer func() { retrySleep = restore }()

	var got map[string]bool
	if err := GetJSONRetry(srv.URL, &got); err != nil {
		t.Fatalf("GetJSONRetry should have recovered on the third attempt: %v", err)
	}
	if !got["ok"] {
		t.Errorf("decoded %v, want ok=true", got)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("the server was called %d times, want 3", got)
	}
	if slept != 6*time.Second {
		t.Errorf("backoff slept %v across the two failures, want 2s+4s=6s", slept)
	}
}

func TestGetJSONRetryGivesUpAfterThreeAttempts(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	restore := retrySleep
	retrySleep = func(time.Duration) {}
	defer func() { retrySleep = restore }()

	err := GetJSONRetry(srv.URL, &map[string]any{})
	if err == nil {
		t.Fatal("expected the last error to be returned")
	}
	if !strings.Contains(err.Error(), "HTTP 502") {
		t.Errorf("the returned error should be the upstream one: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Errorf("the server was called %d times, want 3", got)
	}
}

// A caller decoding into a struct must see exactly the JSON document; this also
// pins that no ambient state (proxy env, client timeouts) leaks into the request.
func TestGetJSONIntoStructWithNestedValues(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"lts": "22.11.0", "versions": []string{"22.11.0", "20.9.0"}})
	}))
	defer srv.Close()

	var got struct {
		LTS      string   `json:"lts"`
		Versions []string `json:"versions"`
	}
	if err := GetJSON(srv.URL, &got); err != nil {
		t.Fatalf("GetJSON returned an error: %v", err)
	}
	if got.LTS != "22.11.0" || len(got.Versions) != 2 || got.Versions[1] != "20.9.0" {
		t.Errorf("decoded %+v, want lts=22.11.0 with two versions", got)
	}
}
