package runtime

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/MOMIEDEJIYI/ployenv/internal/logx"
	"github.com/MOMIEDEJIYI/ployenv/internal/netx"
	"github.com/MOMIEDEJIYI/ployenv/internal/version"
)

// rustDist serves the release manifest, the component archives and a .sha256
// sidecar next to each of them.
var rustDist = "https://static.rust-lang.org/dist"

// rustMirrors carry the same layout. Only .tar.gz mirrors are usable: the
// standard library has no xz decoder, and some mirrors publish xz alone.
var rustMirrors = []string{
	"https://mirrors.ustc.edu.cn/rust-static/dist",
}

// rustComponents are the three packages that make a usable toolchain. The
// combined "rust" archive is four times larger because it also carries the
// documentation, so it is deliberately not used.
var rustComponents = []string{"rustc", "cargo", "rust-std"}

// rustSpec manages Rust from the official distribution server, assembled from
// its per-component archives. Like every other runtime it adds nothing to the
// environment except its bin/ directory: no RUSTUP_HOME and no CARGO_HOME, so a
// user's ~/.cargo stays theirs (cargo creates it on first use).
var rustSpec = &Spec{
	Name:     "rust",
	Title:    "Rust (rustc + cargo)",
	Kind:     "language",
	Aliases:  []string{"rustlang"},
	Official: func() string { return rustDist },
	ExeTools: []string{"rustc", "rustdoc", "cargo"},
	Primary:  "rustc",
	ToolPath: rustToolPath,
	PathDirs: func(dir string) []string { return []string{filepath.Join(dir, "bin")} },
	Verify:   rustVerify,
	List:     rustList,
	Plan:     rustPlan,
}

// rustTripleFor maps a platform to the Rust target triple. Unknown targets get
// "", so a caller can report that instead of building a URL that cannot exist.
func rustTripleFor(osName, arch string) string {
	switch osName {
	case "windows":
		switch arch {
		case "amd64":
			return "x86_64-pc-windows-msvc"
		case "arm64":
			return "aarch64-pc-windows-msvc"
		}
	case "darwin":
		switch arch {
		case "amd64":
			return "x86_64-apple-darwin"
		case "arm64":
			return "aarch64-apple-darwin"
		}
	default:
		switch arch {
		case "amd64":
			return "x86_64-unknown-linux-gnu"
		case "arm64":
			return "aarch64-unknown-linux-gnu"
		}
	}
	return ""
}

// rustToolPath keeps every tool under bin/, on every platform.
func rustToolPathFor(osName, tool string) string {
	switch tool {
	case "rustc", "rustdoc", "cargo":
		return filepath.Join("bin", exeFor(osName, tool))
	}
	return ""
}

func rustToolPath(tool, _ string) string { return rustToolPathFor(hostOS(), tool) }

// rustChannel is what the stable channel manifest is read for: the version it
// pins and the date whose directory carries the same archives.
type rustChannel struct {
	Version string
	Date    string
}

var (
	rustVersionLine = regexp.MustCompile(`(?m)^version = "([0-9]+\.[0-9]+\.[0-9]+)`)
	rustDateLine    = regexp.MustCompile(`(?m)^date = "([0-9]{4}-[0-9]{2}-[0-9]{2})"`)
)

// rustChannelInfo reads the stable channel. The manifest is a large TOML
// document, but only two lines matter: the [pkg.rustc] version and the
// document's own date. Parsing the rest would add a TOML grammar for nothing.
// The version always comes from the official manifest: a mirror can lag by a
// release, and resolving to a version whose archives the mirror does not have
// yet is exactly the case that looks like a broken install.
func rustChannelInfo() (rustChannel, error) {
	txt, err := netx.GetTextRetry(rustDist + "/channel-rust-stable.toml")
	if err != nil {
		return rustChannel{}, err
	}
	start := strings.Index(txt, "[pkg.rustc]")
	if start < 0 {
		return rustChannel{}, fmt.Errorf("the stable manifest has no [pkg.rustc] table")
	}
	m := rustVersionLine.FindStringSubmatch(txt[start:])
	if m == nil {
		return rustChannel{}, fmt.Errorf("the stable manifest has no rustc version")
	}
	ch := rustChannel{Version: m[1]}
	if d := rustDateLine.FindStringSubmatch(txt); d != nil {
		ch.Date = d[1]
	}
	return ch, nil
}

func rustList(filter string) ([]Release, error) {
	f := strings.ToLower(strings.TrimSpace(filter))
	switch f {
	case "beta", "nightly":
		return nil, fmt.Errorf("rust@%s is not supported; only the stable channel is installed", f)
	case "lts":
		return nil, fmt.Errorf("rust has no LTS channel; use latest or an explicit version")
	}
	// An exact version needs no index: every release sits at the same flat path,
	// so asking the manifest about it would only make an offline install (and a
	// unit test) depend on the network for nothing.
	if version.IsExact(f) {
		return []Release{{Version: strings.TrimPrefix(f, "v")}}, nil
	}
	ch, err := rustChannelInfo()
	if err != nil {
		return nil, err
	}
	// There is no full version index upstream, so the only answer is the
	// channel's current version; older versions install by explicit request.
	if f == "" || f == "latest" || f == "stable" || f == "all" {
		return []Release{{Version: ch.Version}}, nil
	}
	if !version.Matches(f, ch.Version) {
		return nil, nil
	}
	return []Release{{Version: ch.Version}}, nil
}

// rustBases orders the download roots: a configured mirror, the official root,
// then the dated directories that carry the same archives. The dated form is
// only a fallback for the release the channel currently points at: an older
// version lives under its own date, which today's manifest does not name, so
// guessing it would only produce a 404.
func rustBases(ch rustChannel, exact string) []string {
	flat := downloadBases("rust", rustDist, rustMirrors...)
	bases := append([]string{}, flat...)
	if ch.Date != "" && exact == ch.Version {
		for _, b := range flat {
			dated := strings.TrimRight(b, "/") + "/" + ch.Date
			dup := false
			for _, have := range bases {
				if strings.TrimRight(have, "/") == dated {
					dup = true
					break
				}
			}
			if !dup {
				bases = append(bases, dated)
			}
		}
	}
	return bases
}

// rustChecksum reads the .sha256 sidecar of one component archive. The file is
// "<hex>  <name>", so the digest is the first 64-hex field.
func rustChecksum(bases []string, urlFor func(string) string) string {
	for _, base := range bases {
		txt, err := netx.GetText(urlFor(base) + ".sha256")
		if err != nil {
			logx.Verbosef("no SHA-256 from %s: %v", netx.ShortURL(base), err)
			continue
		}
		for _, field := range strings.Fields(txt) {
			if len(field) == 64 && isHexString(field) {
				return strings.ToLower(field)
			}
		}
	}
	return ""
}

func isHexString(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return len(s) > 0
}

func rustPlan(ver string) (Plan, error) {
	exact := strings.TrimPrefix(strings.TrimSpace(ver), "v")
	ch := rustChannel{}
	if exact == "" || version.IsChannel(exact) {
		var err error
		ch, err = rustChannelInfo()
		if err != nil {
			return Plan{}, err
		}
		exact = ch.Version
	}
	triple := rustTripleFor(hostOS(), hostArch())
	if triple == "" {
		return Plan{}, fmt.Errorf("Rust publishes no distribution for %s/%s", hostOS(), hostArch())
	}
	// An explicit version skips the manifest entirely, so an offline install and
	// a unit test never touch the network; the dated fallback is only known for
	// the channel version, which is why rustBases receives ch.
	bases := rustBases(ch, exact)

	parts := make([]Part, 0, len(rustComponents))
	for _, comp := range rustComponents {
		name := fmt.Sprintf("%s-%s-%s.tar.gz", comp, exact, triple)
		file := name
		urlFor := func(base string) string { return strings.TrimRight(base, "/") + "/" + file }
		parts = append(parts, Part{File: name, URLFor: urlFor, SHA256: rustChecksum(bases, urlFor)})
		if parts[len(parts)-1].SHA256 == "" {
			logx.Warnf("no SHA-256 published for %s; installing it without an integrity check", name)
		}
	}
	return Plan{Exact: exact, Bases: bases, Parts: parts, Layout: LayoutRust}, nil
}

func rustVerify(dir string) (string, error) {
	out, err := captureOutput(filepath.Join(dir, rustToolPath("rustc", "")), "--version")
	if err != nil {
		return "", err
	}
	// "rustc 1.99.1 (58cf8f9c3 2026-10-13)"
	fields := strings.Fields(out)
	if len(fields) >= 2 {
		return fields[1], nil
	}
	return "", nil
}
