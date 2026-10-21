package runtime

import (
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// rustManifest is trimmed from channel-rust-stable.toml. cargo carries a
// different version on purpose: the rustc table is the one that names the
// toolchain, and a parser that simply took the first "version" line would get
// this wrong.
const rustManifest = `manifest-version = "2"
date = "2026-10-13"

[pkg.cargo]
version = "1.99.0 (0c1a1b2c3 2026-10-01)"
git_commit_hash = "0000000000000000000000000000000000000000"

[[pkg.cargo.target.x86_64-pc-windows-msvc]]
available = true
url = "https://static.rust-lang.org/dist/2026-10-13/cargo-1.99.0-x86_64-pc-windows-msvc.tar.gz"
hash = "1111111111111111111111111111111111111111111111111111111111111111"

[pkg.rustc]
version = "1.99.1 (58cf8f9c3 2026-10-13)"
git_commit_hash = "58cf8f9c35b355199727627d07ae34a4e2846d67"

[[pkg.rustc.target.x86_64-pc-windows-msvc]]
available = true
url = "https://static.rust-lang.org/dist/2026-10-13/rustc-1.99.1-x86_64-pc-windows-msvc.tar.gz"
hash = "2222222222222222222222222222222222222222222222222222222222222222"
`

const rustSHA = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"

// rustDistServer points the distribution root at a local server and drops the
// mirrors, so no test reaches the network.
func rustDistServer(t *testing.T, extra map[string]string) *httptest.Server {
	t.Helper()
	routes := map[string]string{
		"/channel-rust-stable.toml": rustManifest,
	}
	for k, v := range extra {
		routes[k] = v
	}
	srv := staticServer(t, routes)
	override(t, &rustDist, srv.URL)
	old := rustMirrors
	rustMirrors = nil
	t.Cleanup(func() { rustMirrors = old })
	return srv
}

func TestRustChannelReadsTheRustcVersion(t *testing.T) {
	rustDistServer(t, nil)

	ch, err := rustChannelInfo()
	if err != nil {
		t.Fatalf("rustChannelInfo: %v", err)
	}
	if ch.Version != "1.99.1" {
		t.Errorf("Version = %q, want 1.99.1 (the cargo table must not win)", ch.Version)
	}
	if ch.Date != "2026-10-13" {
		t.Errorf("Date = %q, want 2026-10-13", ch.Date)
	}
}

func TestRustListsOnlyTheStableChannel(t *testing.T) {
	rustDistServer(t, nil)

	for _, filter := range []string{"", "latest", "stable", "all", "1.99", "1.99.1"} {
		rels, err := rustList(filter)
		if err != nil {
			t.Fatalf("rustList(%q): %v", filter, err)
		}
		if len(rels) != 1 || rels[0].Version != "1.99.1" {
			t.Errorf("rustList(%q) = %v, want the stable 1.99.1", filter, rels)
		}
	}
	rels, err := rustList("1.98")
	if err != nil {
		t.Fatalf("rustList(1.98): %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("rustList(1.98) = %v, want nothing: there is no full version index", rels)
	}
	for _, unsupported := range []string{"beta", "nightly"} {
		if _, err := rustList(unsupported); err == nil || !strings.Contains(err.Error(), "not supported") {
			t.Errorf("rustList(%s) error = %v, want the not-supported message", unsupported, err)
		}
	}
	if _, err := rustList("lts"); err == nil || !strings.Contains(err.Error(), "no LTS channel") {
		t.Errorf("rustList(lts) error = %v, want the no-LTS message", err)
	}
}

func TestRustTriplesCoverEveryPublishedTarget(t *testing.T) {
	cases := map[string]string{
		"windows/amd64": "x86_64-pc-windows-msvc",
		"windows/arm64": "aarch64-pc-windows-msvc",
		"darwin/amd64":  "x86_64-apple-darwin",
		"darwin/arm64":  "aarch64-apple-darwin",
		"linux/amd64":   "x86_64-unknown-linux-gnu",
		"linux/arm64":   "aarch64-unknown-linux-gnu",
		"linux/386":     "",
	}
	for key, want := range cases {
		parts := strings.SplitN(key, "/", 2)
		if got := rustTripleFor(parts[0], parts[1]); got != want {
			t.Errorf("rustTripleFor(%s) = %q, want %q", key, got, want)
		}
	}
}

func TestRustPlanFetchesThreeComponentsWithTheirSidecars(t *testing.T) {
	files := map[string]string{}
	for _, comp := range []string{"rustc", "cargo", "rust-std"} {
		name := fmt.Sprintf("%s-1.99.1-x86_64-pc-windows-msvc.tar.gz", comp)
		files["/"+name+".sha256"] = rustSHA + "  " + name + "\n"
	}
	rustDistServer(t, files)
	withOS(t, "windows")
	withArch(t, "amd64")

	plan, err := rustSpec.Plan("1.99.1")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.Exact != "1.99.1" || plan.Layout != LayoutRust {
		t.Fatalf("Plan = %+v, want 1.99.1 with LayoutRust", plan)
	}
	if len(plan.Parts) != 3 {
		t.Fatalf("got %d parts, want rustc+cargo+rust-std", len(plan.Parts))
	}
	if plan.File != "" || plan.URLFor != nil {
		t.Error("a multi-archive plan must not also describe a single archive")
	}
	for _, part := range plan.Parts {
		if !strings.Contains(part.File, "1.99.1-x86_64-pc-windows-msvc.tar.gz") {
			t.Errorf("part %q does not name the version and triple", part.File)
		}
		if part.SHA256 != rustSHA {
			t.Errorf("part %q SHA256 = %q, want the sidecar digest", part.File, part.SHA256)
		}
		if got := part.URLFor("https://example.test/dist/"); got != "https://example.test/dist/"+part.File {
			t.Errorf("URLFor = %q", got)
		}
	}
	// The dated directory is a fallback root for the same archives.
	last := plan.Bases[len(plan.Bases)-1]
	if !strings.HasSuffix(last, "/2026-10-13") {
		t.Errorf("last base = %q, want the dated directory", last)
	}
}

func TestRustPlanRejectsAnUnknownArchitecture(t *testing.T) {
	rustDistServer(t, nil)
	withOS(t, "linux")
	withArch(t, "386")

	if _, err := rustSpec.Plan("1.99.1"); err == nil || !strings.Contains(err.Error(), "no distribution") {
		t.Errorf("Plan error = %v, want an unsupported-platform message", err)
	}
}

func TestRustToolPathsLiveUnderBin(t *testing.T) {
	for _, tool := range []string{"rustc", "rustdoc", "cargo"} {
		if got := rustToolPathFor("windows", tool); got != filepath.Join("bin", tool+".exe") {
			t.Errorf("rustToolPathFor(windows, %s) = %q", tool, got)
		}
		if got := rustToolPathFor("linux", tool); got != filepath.Join("bin", tool) {
			t.Errorf("rustToolPathFor(linux, %s) = %q", tool, got)
		}
	}
	if got := rustToolPathFor("windows", "rustfmt"); got != "" {
		t.Errorf("an unshipped tool must have no path, got %q", got)
	}
	if got := rustSpec.PathEntries(filepath.Join("C:", "data", "versions", "rust", "1.99.1")); len(got) != 1 || !strings.HasSuffix(got[0], "bin") {
		t.Errorf("PathEntries = %v, want the bin directory", got)
	}
	if entries := rustSpec.EnvEntries(filepath.Join("C:", "data", "versions", "rust", "1.99.1")); len(entries) != 0 {
		t.Errorf("EnvEntries = %v, want nothing (no RUSTUP_HOME / CARGO_HOME)", entries)
	}
	if got := Lookup("rustlang"); got != rustSpec {
		t.Errorf("Lookup(rustlang) = %v, want the rust spec", got)
	}
}

// The dated directory holds the release the channel points at today. An older
// version lives under its own date, which this manifest does not name, so
// guessing it would only add a 404 to every download attempt.
func TestRustPlanUsesDatedBasesOnlyForTheChannelVersion(t *testing.T) {
	rustDistServer(t, nil)
	withOS(t, "windows")
	withArch(t, "amd64")

	current, err := rustSpec.Plan("1.99.1")
	if err != nil {
		t.Fatalf("Plan(current): %v", err)
	}
	if !strings.HasSuffix(current.Bases[len(current.Bases)-1], "/2026-10-13") {
		t.Errorf("current version bases = %v, want the dated fallback last", current.Bases)
	}

	older, err := rustSpec.Plan("1.98.0")
	if err != nil {
		t.Fatalf("Plan(older): %v", err)
	}
	for _, b := range older.Bases {
		if strings.HasSuffix(b, "/2026-10-13") {
			t.Errorf("an older version must not use today's dated directory: %v", older.Bases)
		}
	}
}
