package netx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// apiGitHub is a test stand-in for api.github.com: the token seam is what makes
// the origin rule testable without reaching the network.
func apiGitHub(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	oldHost, oldScheme := githubAPIHost, githubAPIScheme
	githubAPIHost, githubAPIScheme = strings.TrimPrefix(ts.URL, "http://"), "http"
	t.Cleanup(func() { githubAPIHost, githubAPIScheme = oldHost, oldScheme })
	return ts
}

// A stale ambient token used to turn a request that works anonymously into a hard
// failure: api.github.com answers 401 for a bad credential and 200 without one.
func TestARejectedAmbientTokenFallsBackToAnonymous(t *testing.T) {
	var seen []string
	ts := apiGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		seen = append(seen, auth)
		if auth != "" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	t.Setenv("GITHUB_TOKEN", "stale")

	var v map[string]bool
	if err := GetJSON(ts.URL+"/x.json", &v); err != nil {
		t.Fatalf("GetJSON: %v", err)
	}
	if !v["ok"] {
		t.Errorf("decoded = %v, want the anonymous response", v)
	}
	if len(seen) != 2 {
		t.Fatalf("requests = %d, want 2 (with the token, then without)", len(seen))
	}
	if seen[0] == "" {
		t.Error("the first request must carry the ambient token")
	}
	if seen[1] != "" {
		t.Errorf("the retry must not carry a credential, got %q", seen[1])
	}
}

// Without a token there is nothing to fall back from: one request, one error.
func TestUnauthorizedWithoutATokenIsNotRetried(t *testing.T) {
	n := 0
	ts := apiGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		n++
		w.WriteHeader(http.StatusUnauthorized)
	})
	t.Setenv("GITHUB_TOKEN", "")

	if _, err := GetText(ts.URL + "/x"); err == nil {
		t.Fatal("a 401 must still be an error")
	}
	if n != 1 {
		t.Errorf("requests = %d, want 1 (nothing to retry without a token)", n)
	}
}
