package runtime

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MOMIEDEJIYI/ployenv/internal/platform"
)

// Both of python's indexes are endpoint-only (no archive mirror carries them),
// so the seams are what let these tests run offline.
// Prereleases on NuGet carry a -b<build>/-rc<n> suffix; legacy four-component
// versions (3.5.2.1) are real releases and must survive the filter.
const pythonNugetIndexJSON = `{"versions":["3.11.9","3.12.0-b3","3.12.0-rc1","3.12.10","3.13.0","3.5.2.1"]}`

func TestPythonNugetVersionsSkipsPrereleases(t *testing.T) {
	srv := staticServer(t, map[string]string{"/python/index.json": pythonNugetIndexJSON})
	override(t, &pythonNugetBase, srv.URL)

	got, err := pythonNugetVersions()
	if err != nil {
		t.Fatalf("pythonNugetVersions: %v", err)
	}
	// The published order is preserved; anything that is not a plain release is
	// not an installable pick.
	want := []string{"3.11.9", "3.12.10", "3.13.0", "3.5.2.1"}
	if !equalStrings(got, want) {
		t.Fatalf("pythonNugetVersions = %v, want %v", got, want)
	}
}

func TestPythonNugetPlanUsesTheNugetLayout(t *testing.T) {
	srv := staticServer(t, map[string]string{})
	writeConfig(t, map[string]string{"python": srv.URL})
	override(t, &pythonNugetBase, srv.URL)

	plan := pythonNugetPlan("3.12.10")
	if plan.Exact != "3.12.10" || plan.File != "python.3.12.10.nupkg" {
		t.Fatalf("plan = %+v", plan)
	}
	// A nupkg needs the NuGet normalisation, not a plain flatten.
	if plan.Layout != LayoutNuGet {
		t.Fatalf("Layout = %v, want LayoutNuGet", plan.Layout)
	}
	if want := srv.URL + "/python/3.12.10/python.3.12.10.nupkg"; plan.URLFor(srv.URL) != want {
		t.Fatalf("URLFor = %q, want %q", plan.URLFor(srv.URL), want)
	}
	if len(plan.Bases) != 2 || plan.Bases[0] != srv.URL {
		t.Fatalf("Bases = %v, want the configured mirror first", plan.Bases)
	}
	if plan.Bases[1] != "https://nuget.cdn.azure.cn/v3-flatcontainer" {
		t.Fatalf("fallback base = %q, want the nuget mirror", plan.Bases[1])
	}
}

func TestPythonAssetSuffixMatchesTheHostPlatform(t *testing.T) {
	suffix := pythonAssetSuffix()
	if !strings.HasSuffix(suffix, "install_only.tar.gz") {
		t.Fatalf("pythonAssetSuffix() = %q, want the install_only tarball", suffix)
	}
	if arch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[platform.Arch()]; arch != "" {
		if !strings.HasPrefix(suffix, arch+"-") {
			t.Fatalf("pythonAssetSuffix() = %q, want the %s architecture prefix", suffix, arch)
		}
	}
	if osToken := map[string]string{
		"windows": "pc-windows-msvc",
		"darwin":  "apple-darwin",
		"linux":   "unknown-linux-gnu",
	}[platform.OS()]; osToken != "" {
		if !strings.Contains(suffix, osToken) {
			t.Fatalf("pythonAssetSuffix() = %q, want the %s target triple", suffix, osToken)
		}
	}
}

func TestPythonVersionFromAssetName(t *testing.T) {
	cases := map[string]string{
		"cpython-3.12.7+20241016-x86_64-pc-windows-msvc-install_only.tar.gz": "3.12.7",
		"cpython-3.12.7+20241016-aarch64-apple-darwin-install_only.tar.gz":   "3.12.7",
		// No build stamp: the standalone builds always carry one, so this is not
		// the shape ployenv understands.
		"cpython-3.12.7-x86_64-unknown-linux-gnu-install_only.tar.gz": "",
		// Another project's archive.
		"python-3.12.7+20241016-x86_64-unknown-linux-gnu-install_only.tar.gz": "",
		"cpython-+20241016-x86_64-unknown-linux-gnu-install_only.tar.gz":      "",
	}
	for name, want := range cases {
		if got := pythonVersionFromAsset(name); got != want {
			t.Errorf("pythonVersionFromAsset(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestPythonAssetListKeepsOneAssetPerVersion(t *testing.T) {
	suffix := pythonAssetSuffix()
	body := fmt.Sprintf(`[{"tag_name":"20241016","assets":[
	  {"name":"cpython-3.12.7+20241016-%[1]s","browser_download_url":"https://github.invalid/first.tar.gz"},
	  {"name":"cpython-3.12.7+20241017-%[1]s","browser_download_url":"https://github.invalid/newer-duplicate.tar.gz"},
	  {"name":"cpython-3.12.6+20241016-%[1]s","browser_download_url":"https://github.invalid/older.tar.gz"},
	  {"name":"cpython-3.12.5+20241016-x86_64-unknown-freebsd-install_only.tar.gz","browser_download_url":"https://github.invalid/other-platform.tar.gz"},
	  {"name":"python-3.12.4+20241016-%[1]s","browser_download_url":"https://github.invalid/not-cpython.tar.gz"}
	]}]`, suffix)
	srv := staticServer(t, map[string]string{"/": body})
	override(t, &pythonReleasesURL, srv.URL)

	got, err := pythonAssetList()
	if err != nil {
		t.Fatalf("pythonAssetList: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("pythonAssetList returned %d assets, want one per version: %+v", len(got), got)
	}
	if got[0].Version != "3.12.7" || got[0].Tag != "20241016" {
		t.Fatalf("first asset = %+v", got[0])
	}
	if got[0].URL != "https://github.invalid/first.tar.gz" {
		t.Fatalf("the newest asset of a version must not displace the first one: %+v", got[0])
	}
	if got[1].Version != "3.12.6" {
		t.Fatalf("second asset = %+v, want 3.12.6", got[1])
	}
}

func TestPythonOfficialSourceDependsOnThePlatform(t *testing.T) {
	nuget := staticServer(t, map[string]string{})
	standalone := staticServer(t, map[string]string{})
	override(t, &pythonNugetBase, nuget.URL)
	override(t, &pythonReleasesURL, standalone.URL)

	want := standalone.URL
	if platform.IsWindows() {
		// Windows has no standalone build; python comes from NuGet there.
		want = nuget.URL
	}
	if got := pythonOfficial(); got != want {
		t.Fatalf("pythonOfficial() = %q, want %q", got, want)
	}
}

func TestPythonEndpointsDefaultToTheDocumentedSources(t *testing.T) {
	if pythonNugetBase != "https://api.nuget.org/v3-flatcontainer" {
		t.Errorf("pythonNugetBase = %q", pythonNugetBase)
	}
	if pythonReleasesURL != "https://api.github.com/repos/astral-sh/python-build-standalone/releases" {
		t.Errorf("pythonReleasesURL = %q", pythonReleasesURL)
	}
	if adoptiumAPI != "https://api.adoptium.net/v3" {
		t.Errorf("adoptiumAPI = %q", adoptiumAPI)
	}
}

func TestPythonListAndPlanPerPlatform(t *testing.T) {
	nuget := staticServer(t, map[string]string{"/python/index.json": `{"versions":["3.12.10","3.11.9"]}`})
	override(t, &pythonNugetBase, nuget.URL)

	suffix := pythonAssetSuffix()
	assets := staticServer(t, map[string]string{"/": fmt.Sprintf(
		`[{"tag_name":"20241016","assets":[
		   {"name":"cpython-3.12.7+20241016-%[1]s","browser_download_url":"https://github.invalid/python-3.12.7.tar.gz"},
		   {"name":"cpython-3.11.9+20241016-%[1]s","browser_download_url":"https://github.invalid/python-3.11.9.tar.gz"}
		 ]}]`, suffix)})
	override(t, &pythonReleasesURL, assets.URL)

	// The archive mirror is what a user configures; it only ever carries the
	// standalone layout, so it is the download base, not the index.
	writeConfig(t, map[string]string{"python": "https://mirror.invalid/python-build-standalone"})

	list, err := pythonList("3.12")
	if err != nil {
		t.Fatalf("pythonList(3.12): %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("pythonList(3.12) returned %d releases, want 1: %+v", len(list), list)
	}

	if platform.IsWindows() {
		if list[0].Version != "3.12.10" || list[0].Note != "nuget python (includes pip)" {
			t.Fatalf("on Windows python comes from nuget, got %+v", list[0])
		}
		plan, err := pythonPlan(list[0].Version)
		if err != nil {
			t.Fatalf("pythonPlan: %v", err)
		}
		if plan.Layout != LayoutNuGet || plan.File != "python.3.12.10.nupkg" {
			t.Fatalf("plan = %+v, want the nuget package", plan)
		}
		return
	}

	if list[0].Version != "3.12.7" || list[0].Note != "cpython-3.12.7+20241016-"+suffix {
		t.Fatalf("on the Unix-likes python comes from the standalone builds, got %+v", list[0])
	}
	plan, err := pythonPlan(list[0].Version)
	if err != nil {
		t.Fatalf("pythonPlan: %v", err)
	}
	if plan.Layout != LayoutFlatten {
		t.Fatalf("Layout = %v, want LayoutFlatten", plan.Layout)
	}
	wantMirror := "https://mirror.invalid/python-build-standalone/20241016/" + plan.File
	if len(plan.Bases) != 2 || plan.Bases[0] != wantMirror {
		t.Fatalf("Bases = %v, want the configured mirror first (%q)", plan.Bases, wantMirror)
	}
	if plan.Bases[1] != "https://github.invalid/python-3.12.7.tar.gz" {
		t.Fatalf("Bases = %v, want the GitHub asset as the fallback", plan.Bases)
	}
	if got := plan.URLFor(plan.Bases[1]); got != plan.Bases[1] {
		t.Fatalf("URLFor = %q, want the base itself for the GitHub release", got)
	}

	// A version that no asset matches is an error, not an empty plan.
	if _, err := pythonPlan("9.9.9"); err == nil {
		t.Fatal("pythonPlan must fail for a version that does not exist")
	}

	// The channel keywords must not be treated as a version filter.
	all, err := pythonList("latest")
	if err != nil {
		t.Fatalf("pythonList(latest): %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("pythonList(latest) returned %d releases, want both assets", len(all))
	}
}
