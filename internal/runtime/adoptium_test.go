package runtime

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// adoptiumSemver is an alias for the anonymous struct the Adoptium response
// uses in both its version and version_data fields.
type adoptiumSemver = struct {
	Semver string `json:"semver"`
	Major  int    `json:"major"`
}

// The fixtures below are trimmed copies of what api.adoptium.net returns: the
// feature_releases form carries version_data, the older /assets/version form
// carries version, and both must produce the same recorded version.
const adoptium21JSON = `[
  {"release_name":"jdk-21.0.12.1+1","binaries":[{"package":{"link":"https://example.invalid/jdk21_12.zip","name":"OpenJDK21U-jdk_x64_21.0.12.1_1.zip"}}],"version_data":{"semver":"21.0.12.1+1","major":21}},
  {"release_name":"jdk-21.0.10+7","binaries":[{"package":{"link":"https://example.invalid/jdk21_10.zip","name":"OpenJDK21U-jdk_x64_21.0.10_7.zip"}}],"version_data":{"semver":"21.0.10+7","major":21}},
  {"release_name":"jdk-21.0.5","binaries":[{"package":{"link":"https://example.invalid/jdk21_05.zip","name":"OpenJDK21U-jdk_x64_21.0.5_1.zip"}}],"version_data":{"semver":"21.0.5","major":21}}
]`

const adoptium8JSON = `[
  {"release_name":"jdk8u462-b08","binaries":[{"package":{"link":"https://example.invalid/jdk8.zip","name":"OpenJDK8U-jdk_x64_8u462b08.zip"}}],"version":{"semver":"8.0.462+8","major":8}}
]`

// A release published before its archive was mirrored: no binaries at all.
const adoptium42JSON = `[
  {"release_name":"jdk-42.0.1+2","binaries":[],"version_data":{"semver":"42.0.1+2","major":42}}
]`

// A package without a file name: the plan must synthesise one rather than hand
// the installer an empty name.
const adoptium43JSON = `[
  {"release_name":"jdk-43.0.1+2","binaries":[{"package":{"link":"https://example.invalid/jdk43.zip","name":""}}],"version_data":{"semver":"43.0.1+2","major":43}}
]`

// adoptiumServer serves the paths javaList/javaPlan/javaNewest ask for and points
// the provider at itself. The release_names endpoint is paged, so page=0 returns
// a full page (20 names) and page=1 a short one, which is where the paging loop
// stops.
func adoptiumServer(t *testing.T) string {
	t.Helper()
	routes := map[string]string{
		"/assets/feature_releases/21/ga": adoptium21JSON,
		"/assets/feature_releases/8/ga":  adoptium8JSON,
		"/assets/feature_releases/42/ga": adoptium42JSON,
		"/assets/feature_releases/43/ga": adoptium43JSON,
		"/assets/feature_releases/99/ga": `[]`,
	}
	srv := server(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/info/release_names" {
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			if page == 0 {
				names := make([]string, 0, 20)
				for i := 0; i < 20; i++ {
					names = append(names, fmt.Sprintf("jdk-27.0.%d+1", i))
				}
				fmt.Fprintf(w, `{"release_names":[%s]}`, quoteJoin(names))
				return
			}
			fmt.Fprint(w, `{"releases":["jdk-21.0.12.1+1","jdk-17.0.13+11"]}`)
			return
		}
		body, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(body))
	})
	override(t, &adoptiumAPI, srv.URL)
	return srv.URL
}

func quoteJoin(items []string) string {
	quoted := make([]string, 0, len(items))
	for _, s := range items {
		quoted = append(quoted, strconv.Quote(s))
	}
	return strings.Join(quoted, ",")
}

func TestJavaListFiltersByFeatureLine(t *testing.T) {
	adoptiumServer(t)

	for _, filter := range []string{"21", "v21"} {
		got, err := javaList(filter)
		if err != nil {
			t.Fatalf("javaList(%s): %v", filter, err)
		}
		if len(got) != 3 {
			t.Fatalf("javaList(%s) returned %d releases, want all three 21.x builds", filter, len(got))
		}
		if got[0].Version != "21.0.12.1+1" || got[0].Note != "21.0.12.1+1" {
			t.Fatalf("javaList(%s)[0] = %+v", filter, got[0])
		}
	}

	one, err := javaList("21.0.12")
	if err != nil {
		t.Fatalf("javaList(21.0.12): %v", err)
	}
	if len(one) != 1 || one[0].Version != "21.0.12.1+1" {
		t.Fatalf("javaList(21.0.12) = %+v, want just 21.0.12.1+1", one)
	}

	// Java 8 is named 8u<build>-b<build>, which shares no dotted prefix with the
	// bare feature number (see javaMatches).
	eight, err := javaList("8")
	if err != nil {
		t.Fatalf("javaList(8): %v", err)
	}
	if len(eight) != 1 || eight[0].Version != "8u462-b08" {
		t.Fatalf("javaList(8) = %+v, want 8u462-b08", eight)
	}
}

// BUG-022: the feature lookup strips the jdk-/v prefix, but the match used the raw
// request, so `available java jdk-21` listed nothing while `available java 21`
// listed every 21.x build. The prefixed form must narrow the same way.
func TestJavaListPrefixedRequestsMatchTheFeatureLine(t *testing.T) {
	adoptiumServer(t)

	for _, tc := range []struct {
		request string
		want    []string
	}{
		{"21", []string{"21.0.12.1+1", "21.0.10+7", "21.0.5"}},
		{"v21", []string{"21.0.12.1+1", "21.0.10+7", "21.0.5"}},
		{"jdk-21", []string{"21.0.12.1+1", "21.0.10+7", "21.0.5"}},
		{"jdk-21.0.5", []string{"21.0.5"}},
		{"jdk-8", []string{"8u462-b08"}},
	} {
		got, err := javaList(tc.request)
		if err != nil {
			t.Fatalf("javaList(%s): %v", tc.request, err)
		}
		versions := make([]string, 0, len(got))
		for _, r := range got {
			versions = append(versions, r.Version)
		}
		if !equalStrings(versions, tc.want) {
			t.Errorf("javaList(%s) = %v, want %v", tc.request, versions, tc.want)
		}
	}

	// The channel keywords keep their meaning: they list the newest releases
	// rather than being read as a version request.
	for _, channel := range []string{"latest", "lts", "stable"} {
		got, err := javaList(channel)
		if err != nil {
			t.Fatalf("javaList(%s): %v", channel, err)
		}
		if len(got) == 0 {
			t.Errorf("javaList(%s) returned nothing; a channel must list releases", channel)
		}
	}

	// And a request that cannot name a feature line still matches nothing: the
	// BUG-003 guard must survive the normalisation.
	for _, unknown := range []string{"foo", "jdk8", "abc21", "jdk-foo"} {
		got, err := javaList(unknown)
		if err != nil {
			t.Fatalf("javaList(%s): %v", unknown, err)
		}
		if len(got) != 0 {
			t.Errorf("javaList(%s) = %+v, want nothing", unknown, got)
		}
	}
}

func TestJavaMatchFilter(t *testing.T) {
	cases := map[string]string{
		"jdk-21":     "21",
		"jdk-21.0.5": "21.0.5",
		"jdk-8":      "8",
		"v21":        "21",
		"21":         "21",
		// The bare "jdk8" form is not a feature request at all, so the filter
		// leaves it alone and the (empty) feature lookup rejects it first.
		"jdk8": "jdk8",
		"":     "",
	}
	for in, want := range cases {
		if got := javaMatchFilter(in); got != want {
			t.Errorf("javaMatchFilter(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestJavaListChannelUsesTheNewestReleases(t *testing.T) {
	adoptiumServer(t)

	for _, channel := range []string{"latest", "stable"} {
		got, err := javaList(channel)
		if err != nil {
			t.Fatalf("javaList(%s): %v", channel, err)
		}
		// One full page (20) plus the short second page (2) that ends the loop.
		if len(got) != 22 {
			t.Fatalf("javaList(%s) returned %d releases, want 22", channel, len(got))
		}
		for _, r := range got {
			if strings.HasPrefix(r.Version, "jdk-") {
				t.Fatalf("javaList(%s) kept the jdk- prefix: %+v", channel, r)
			}
		}
	}
}

func TestJavaPlanBuildsMirrorAndOfficialBases(t *testing.T) {
	adoptiumServer(t)

	// Without a mirror the official Adoptium link comes first, then the TUNA
	// fallback, which carries the same layout as the official site.
	plan, err := javaPlan("21.0.12.1+1")
	if err != nil {
		t.Fatalf("javaPlan: %v", err)
	}
	if plan.Exact != "21.0.12.1+1" {
		t.Fatalf("Exact = %q", plan.Exact)
	}
	if plan.File != "OpenJDK21U-jdk_x64_21.0.12.1_1.zip" {
		t.Fatalf("File = %q", plan.File)
	}
	if plan.Layout != LayoutFlatten {
		t.Fatalf("Layout = %v, want LayoutFlatten", plan.Layout)
	}
	wantRel := fmt.Sprintf("21/jdk/%s/%s/%s", adoptiumArch(), adoptiumOS(), plan.File)
	if len(plan.Bases) != 2 || plan.Bases[0] != "https://example.invalid/jdk21_12.zip" {
		t.Fatalf("Bases = %v, want the official link first", plan.Bases)
	}
	if want := "https://mirrors.tuna.tsinghua.edu.cn/Adoptium/" + wantRel; plan.Bases[1] != want {
		t.Fatalf("fallback base = %q, want %q", plan.Bases[1], want)
	}
	if got := plan.URLFor(plan.Bases[0]); got != plan.Bases[0] {
		t.Fatalf("URLFor = %q, want the base itself for Adoptium", got)
	}

	// With a configured mirror the mirror is first and the official link is the
	// fallback.
	writeConfig(t, map[string]string{"java": "https://mirror.invalid/Adoptium/"})
	plan, err = javaPlan("21.0.12.1+1")
	if err != nil {
		t.Fatalf("javaPlan with a mirror: %v", err)
	}
	if want := "https://mirror.invalid/Adoptium/" + wantRel; len(plan.Bases) != 2 || plan.Bases[0] != want {
		t.Fatalf("Bases = %v, want %q first", plan.Bases, want)
	}
	if plan.Bases[1] != "https://example.invalid/jdk21_12.zip" {
		t.Fatalf("Bases = %v, want the official link as the fallback", plan.Bases)
	}
}

func TestJavaPlanFallsBackToTheNewestBuildOfTheFeatureLine(t *testing.T) {
	adoptiumServer(t)

	// 21.0.20 is not published; the newest 21.x build is the closest thing the
	// feature line has to offer, and that is what the installer must get.
	plan, err := javaPlan("21.0.20")
	if err != nil {
		t.Fatalf("javaPlan(21.0.20): %v", err)
	}
	if plan.Exact != "21.0.12.1+1" {
		t.Fatalf("Exact = %q, want the newest 21.x build", plan.Exact)
	}
}

func TestJavaPlanReportsTheVersionsItCannotServe(t *testing.T) {
	adoptiumServer(t)

	if _, err := javaPlan("99.0.1"); err == nil {
		t.Fatal("javaPlan must fail when the feature line has no releases at all")
	}
	if _, err := javaPlan("42.0.1+2"); err == nil {
		t.Fatal("javaPlan must fail when Adoptium publishes no download link")
	}

	// A package without a name still yields a usable file name.
	plan, err := javaPlan("43.0.1+2")
	if err != nil {
		t.Fatalf("javaPlan(43.0.1+2): %v", err)
	}
	if plan.File != "jdk-43.0.1+2.zip" {
		t.Fatalf("File = %q, want a synthesised archive name", plan.File)
	}
}

func TestAdoptiumVersionExtraction(t *testing.T) {
	cases := []struct {
		name    string
		rel     adoptiumRelease
		version string
		semver  string
		feature string
	}{
		{
			name:    "newer line carries version_data",
			rel:     adoptiumRelease{ReleaseName: "jdk-21.0.12.1+1", VersionData: adoptiumSemver{Semver: "21.0.12.1+1", Major: 21}},
			version: "21.0.12.1+1", semver: "21.0.12.1+1", feature: "21",
		},
		{
			name:    "java 8 has no dash after the prefix",
			rel:     adoptiumRelease{ReleaseName: "jdk8u462-b08"},
			version: "8u462-b08", semver: "8u462-b08", feature: "8",
		},
		{
			name:    "assets/version carries version",
			rel:     adoptiumRelease{ReleaseName: "jdk-17.0.13+11", Version: adoptiumSemver{Semver: "17.0.13+11", Major: 17}},
			version: "17.0.13+11", semver: "17.0.13+11", feature: "17",
		},
		{
			name:    "no release name falls back to the semver",
			rel:     adoptiumRelease{VersionData: adoptiumSemver{Semver: "25.0.1+8", Major: 25}},
			version: "25.0.1+8", semver: "25.0.1+8", feature: "25",
		},
		{
			name:    "nothing usable stays empty",
			rel:     adoptiumRelease{},
			version: "", semver: "", feature: "",
		},
	}
	for _, tc := range cases {
		if got := tc.rel.nameVersion(); got != tc.version {
			t.Errorf("%s: nameVersion(%q) = %q, want %q", tc.name, tc.rel.ReleaseName, got, tc.version)
		}
		if got := tc.rel.semver(); got != tc.semver {
			t.Errorf("%s: semver(%q) = %q, want %q", tc.name, tc.rel.ReleaseName, got, tc.semver)
		}
		if got := javaFeatureOf(tc.version); got != tc.feature {
			t.Errorf("%s: javaFeatureOf(%q) = %q, want %q", tc.name, tc.version, got, tc.feature)
		}
	}
}

func TestJavaMatchesAcceptsTheJava8Naming(t *testing.T) {
	cases := []struct {
		request   string
		candidate string
		want      bool
	}{
		{"8", "8u462-b08", true},
		{"21", "21.0.12.1", true},
		{"21.0.12", "21.0.12.1", true},
		{"21.0.12.1", "21.0.12.1", true},
		{"21", "17.0.13+11", false},
		{"8", "9u1-b01", false},
	}
	for _, tc := range cases {
		if got := javaMatches(tc.request, tc.candidate); got != tc.want {
			t.Errorf("javaMatches(%q, %q) = %v, want %v", tc.request, tc.candidate, got, tc.want)
		}
	}
}

func TestJavaFeatureOfMapsEveryNameToItsFeature(t *testing.T) {
	cases := map[string]string{
		"8u462-b08":   "8",
		"8":           "8",
		"21.0.12.1+1": "21",
		"jdk-21.0.2":  "21",
		"v17.0.13":    "17",
		"":            "",
	}
	for in, want := range cases {
		if got := javaFeatureOf(in); got != want {
			t.Errorf("javaFeatureOf(%q) = %q, want %q", in, got, want)
		}
	}
}

// The per-platform Adoptium names are asserted in layout_test.go; the two
// mappings must also agree with the seam for the running host, which that table
// covers as its host row.
