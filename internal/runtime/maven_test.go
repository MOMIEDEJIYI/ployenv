package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MOMIEDEJIYI/ployenv/internal/version"
)

// mavenMetadataXML is trimmed from the real maven-metadata.xml, keeping the one
// property that matters: <latest>/<release> point at a release candidate, so any
// channel that trusted them would install Maven 4.
const mavenMetadataXML = `<?xml version="1.0" encoding="UTF-8"?>
<metadata>
  <groupId>org.apache.maven</groupId>
  <artifactId>apache-maven</artifactId>
  <versioning>
    <latest>4.0.0-rc-7</latest>
    <release>4.0.0-rc-7</release>
    <versions>
      <version>3.0-alpha-1</version>
      <version>3.9.6</version>
      <version>3.9.9</version>
      <version>3.10.0</version>
      <version>4.0.0-rc-7</version>
    </versions>
    <lastUpdated>20261018000000</lastUpdated>
  </versioning>
</metadata>`

// mavenIndexServer points the Maven endpoints at a local server, so no test ever
// reaches the network.
func mavenIndexServer(t *testing.T, routes map[string]string) string {
	t.Helper()
	if routes == nil {
		routes = map[string]string{}
	}
	if _, ok := routes["/maven-metadata.xml"]; !ok {
		routes["/maven-metadata.xml"] = mavenMetadataXML
	}
	srv := staticServer(t, routes)
	override(t, &mavenCentral, srv.URL)
	old := mavenDistMirrors
	mavenDistMirrors = nil
	t.Cleanup(func() { mavenDistMirrors = old })
	return srv.URL
}

func highest(rels []Release) string {
	best := ""
	for _, r := range rels {
		if best == "" || version.Compare(r.Version, best) > 0 {
			best = r.Version
		}
	}
	return best
}

func TestMavenChannelNeverResolvesToAReleaseCandidate(t *testing.T) {
	mavenIndexServer(t, nil)

	for _, filter := range []string{"", "latest", "stable"} {
		rels, err := mavenList(filter)
		if err != nil {
			t.Fatalf("mavenList(%q): %v", filter, err)
		}
		if got := highest(rels); got != "3.10.0" {
			t.Errorf("mavenList(%q) resolves to %q, want 3.10.0 (4.0.0-rc-7 is a release candidate)", filter, got)
		}
	}
}

func TestMavenListMarksPrereleasesAndFiltersPrefixes(t *testing.T) {
	mavenIndexServer(t, nil)

	all, err := mavenList("all")
	if err != nil {
		t.Fatalf("mavenList(all): %v", err)
	}
	notes := map[string]string{}
	for _, r := range all {
		notes[r.Version] = r.Note
	}
	if len(notes) != 5 {
		t.Fatalf("mavenList(all) = %v, want all five versions", notes)
	}
	for _, v := range []string{"3.0-alpha-1", "4.0.0-rc-7"} {
		if notes[v] != "preview" {
			t.Errorf("%s: note = %q, want preview", v, notes[v])
		}
	}
	if notes["3.10.0"] != "" {
		t.Errorf("3.10.0: note = %q, want empty", notes["3.10.0"])
	}

	sub, err := mavenList("3.9")
	if err != nil {
		t.Fatalf("mavenList(3.9): %v", err)
	}
	if len(sub) != 2 || sub[0].Version != "3.9.6" || sub[1].Version != "3.9.9" {
		t.Errorf("mavenList(3.9) = %v, want 3.9.6 and 3.9.9", sub)
	}

	if _, err := mavenList("lts"); err == nil {
		t.Error("maven has no LTS channel, so mavenList(lts) must explain that instead of guessing")
	}
}

func TestMavenReleaseChannelRefusesMaven4(t *testing.T) {
	mavenIndexServer(t, nil)

	_, err := mavenList("release")
	if err == nil {
		t.Fatal("upstream <release> is 4.0.0-rc-7, so @release must refuse instead of installing it")
	}
	if !strings.Contains(err.Error(), "4.0.0-rc-7") {
		t.Errorf("error = %v, want it to name the version it refused", err)
	}
}

func TestMavenPlanURLShapes(t *testing.T) {
	urlFor := mavenURLFor("3.10.0", "apache-maven-3.10.0-bin.zip")

	got := urlFor("https://repo1.maven.org/maven2/org/apache/maven/apache-maven")
	want := "https://repo1.maven.org/maven2/org/apache/maven/apache-maven/3.10.0/apache-maven-3.10.0-bin.zip"
	if got != want {
		t.Errorf("repository base:\n got %s\nwant %s", got, want)
	}

	got = urlFor("https://dlcdn.apache.org/maven/maven-3")
	want = "https://dlcdn.apache.org/maven/maven-3/3.10.0/binaries/apache-maven-3.10.0-bin.zip"
	if got != want {
		t.Errorf("distribution base:\n got %s\nwant %s", got, want)
	}
}

func TestMavenPlanPicksTheStableAndTheRightArchive(t *testing.T) {
	mavenIndexServer(t, nil)

	withOS(t, "windows")
	plan, err := mavenPlan("latest")
	if err != nil {
		t.Fatalf("mavenPlan(latest): %v", err)
	}
	if plan.Exact != "3.10.0" {
		t.Errorf("Exact = %q, want 3.10.0", plan.Exact)
	}
	if plan.File != "apache-maven-3.10.0-bin.zip" {
		t.Errorf("File = %q, want the zip on Windows", plan.File)
	}
	if plan.Layout != LayoutFlatten {
		t.Errorf("Layout = %v, want LayoutFlatten (the archive has one top-level folder)", plan.Layout)
	}

	withOS(t, "linux")
	plan, err = mavenPlan("3.10.0")
	if err != nil {
		t.Fatalf("mavenPlan(3.10.0): %v", err)
	}
	if plan.Exact != "3.10.0" {
		t.Errorf("Exact = %q, want the exact 3.10.0", plan.Exact)
	}
	if plan.File != "apache-maven-3.10.0-bin.tar.gz" {
		t.Errorf("File = %q, want the tar.gz off Windows", plan.File)
	}
}

func TestMavenPlanReadsTheSHA512Sidecar(t *testing.T) {
	sum := strings.Repeat("a1", 64)
	mavenIndexServer(t, map[string]string{
		"/3.10.0/binaries/apache-maven-3.10.0-bin.tar.gz.sha512": sum + "  apache-maven-3.10.0-bin.tar.gz\n",
	})
	withOS(t, "linux")

	plan, err := mavenPlan("3.10.0")
	if err != nil {
		t.Fatalf("mavenPlan: %v", err)
	}
	if plan.SHA512 != sum {
		t.Errorf("SHA512 = %q, want the sidecar digest", plan.SHA512)
	}
}

func TestMavenPlanWithoutASidecarIsStillInstallable(t *testing.T) {
	mavenIndexServer(t, nil)
	withOS(t, "linux")

	plan, err := mavenPlan("3.10.0")
	if err != nil {
		t.Fatalf("mavenPlan: %v", err)
	}
	if plan.SHA512 != "" {
		t.Errorf("SHA512 = %q, want empty when no sidecar is published", plan.SHA512)
	}
}

func TestMavenToolPathIsTheWrapperScript(t *testing.T) {
	for _, tc := range []struct{ osName, want string }{
		{"windows", filepath.Join("bin", "mvn.cmd")},
		{"linux", filepath.Join("bin", "mvn")},
		{"darwin", filepath.Join("bin", "mvn")},
	} {
		if got := mavenToolPathFor(tc.osName, "mvn"); got != tc.want {
			t.Errorf("mavenToolPathFor(%q) = %q, want %q", tc.osName, got, tc.want)
		}
		if got := mavenToolPathFor(tc.osName, "mvnDebug"); got != filepath.Join("bin", scriptFor(tc.osName, "mvnDebug")) {
			t.Errorf("mvnDebug path on %s = %q", tc.osName, got)
		}
	}
	if got := mavenToolPathFor("linux", "mvnX"); got != "" {
		t.Errorf("unknown tool = %q, want empty", got)
	}
}

func TestMavenSpecInjectsOnlyItsBinDirectory(t *testing.T) {
	spec := Lookup("mvn")
	if spec == nil || spec.Name != "maven" {
		t.Fatalf("Lookup(mvn) = %v, want the maven spec", spec)
	}
	dir := filepath.Join(t.TempDir(), "3.10.0")
	if got := spec.EnvEntries(dir); len(got) != 0 {
		t.Errorf("EnvEntries = %v: mvn.cmd derives MAVEN_HOME itself, so nothing should be injected", got)
	}
	if got := spec.PathEntries(dir); len(got) != 1 || got[0] != filepath.Join(dir, "bin") {
		t.Errorf("PathEntries = %v, want only <dir>/bin", got)
	}
}

func TestJavaProblem(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(binDir, exeName("java")), []byte("jdk\n"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	emptyDir := t.TempDir()

	for _, tc := range []struct {
		name    string
		env     []string
		want    string // "" means no problem
		mustSay string
	}{
		{name: "a valid JAVA_HOME", env: []string{"JAVA_HOME=" + dir}},
		{name: "no JAVA_HOME but java on PATH", env: []string{"PATH=" + binDir}},
		{
			name:    "a JAVA_HOME that points at nothing",
			env:     []string{"JAVA_HOME=" + emptyDir},
			mustSay: "does not contain bin/java",
		},
		{
			// mvn.cmd does not fall back to PATH once JAVA_HOME is set, so a
			// stale value breaks Maven even though a java is installed.
			name:    "a stale JAVA_HOME with java on PATH",
			env:     []string{"JAVA_HOME=" + emptyDir, "PATH=" + binDir},
			mustSay: "does not contain bin/java",
		},
		{name: "nothing at all", env: []string{"PATH=" + emptyDir}, mustSay: "no JDK found"},
		{name: "an empty environment", env: nil, mustSay: "no JDK found"},
	} {
		got := JavaProblem(tc.env)
		if tc.mustSay == "" {
			if got != "" {
				t.Errorf("%s: JavaProblem = %q, want no complaint", tc.name, got)
			}
			continue
		}
		if !strings.Contains(got, tc.mustSay) {
			t.Errorf("%s: JavaProblem = %q, want it to mention %q", tc.name, got, tc.mustSay)
		}
	}
}

func TestWithEnvReplacesInsteadOfDuplicating(t *testing.T) {
	got := withEnv([]string{"PATH=/bin", "JAVA_HOME=/old"}, "JAVA_HOME", "/new")
	want := []string{"PATH=/bin", "JAVA_HOME=/new"}
	if !equalStrings(got, want) {
		t.Errorf("withEnv = %v, want %v", got, want)
	}
}
