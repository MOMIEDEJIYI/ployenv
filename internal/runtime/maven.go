package runtime

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/MOMIEDEJIYI/ployenv/internal/config"
	"github.com/MOMIEDEJIYI/ployenv/internal/fsutil"
	"github.com/MOMIEDEJIYI/ployenv/internal/logx"
	"github.com/MOMIEDEJIYI/ployenv/internal/netx"
	"github.com/MOMIEDEJIYI/ployenv/internal/version"
)

// mavenCentral is the repository root that carries every published version. The
// Apache distribution mirrors only keep the current ones, so listing always comes
// from a repository-shaped base.
var mavenCentral = "https://repo1.maven.org/maven2/org/apache/maven/apache-maven"

// mavenSpec treats Maven as a tool, not a language, and injects nothing but its
// bin/ directory: mvn.cmd derives MAVEN_HOME from its own location, and Maven
// writes no state outside ~/.m2, so there is nothing for ployenv to configure.
var mavenSpec = &Spec{
	Name:     "maven",
	Title:    "Apache Maven",
	Kind:     "tool",
	Aliases:  []string{"mvn"},
	Official: func() string { return mavenCentral },
	CmdTools: []string{"mvn", "mvnDebug"},
	Primary:  "mvn",
	ToolPath: mavenToolPath,
	PathDirs: func(dir string) []string { return []string{filepath.Join(dir, "bin")} },
	Verify:   mavenVerify,
	List:     mavenList,
	Plan:     mavenPlan,
}

// mavenDistMirrors are Apache distribution mirrors: same layout, but they only carry
// the versions Apache still publishes there.
var mavenDistMirrors = []string{
	"https://dlcdn.apache.org/maven/maven-3",
	"https://mirrors.tuna.tsinghua.edu.cn/apache/maven/maven-3",
	"https://mirrors.aliyun.com/apache/maven/maven-3",
	"https://mirrors.ustc.edu.cn/apache/maven/maven-3",
}

type mavenMetadata struct {
	Latest   string   `xml:"versioning>latest"`
	Release  string   `xml:"versioning>release"`
	Versions []string `xml:"versioning>versions>version"`
}

// mavenPreview matches the pre-release markers Apache uses ("4.0.0-rc-7",
// "3.0-alpha-1", "3.9.0-beta-1"). No stable 3.x version contains one.
var mavenPreview = regexp.MustCompile(`(?i)alpha|beta|-rc|milestone|snapshot|-m[0-9]+$`)

var mavenSHA512 = regexp.MustCompile(`^[0-9a-fA-F]{128}$`)

func mavenToolPathFor(osName, tool string) string {
	switch tool {
	case "mvn", "mvnDebug":
		// Unlike node and python, Maven keeps its tools in bin/ on every
		// platform, so toolDirFor (which lifts them to the version root on
		// Windows) must not be used here.
		return filepath.Join("bin", scriptFor(osName, tool))
	}
	return ""
}

func mavenToolPath(tool, _ string) string { return mavenToolPathFor(hostOS(), tool) }

// mavenArchiveExt picks the archive: Apache ships a zip for Windows and a
// tar.gz everywhere else. Both hold a single apache-maven-<ver>/ folder, so the
// installer's LayoutFlatten applies to either.
func mavenArchiveExt() string {
	if hostOS() == "windows" {
		return ".zip"
	}
	return ".tar.gz"
}

// mavenURLFor returns the archive URL for one base. A repository base
// (Maven Central and its mirrors) nests the version directly; an Apache
// distribution base nests it under <ver>/binaries/.
func mavenURLFor(ver, name string) func(base string) string {
	return func(base string) string {
		if strings.Contains(base, "/maven2/") {
			return base + "/" + ver + "/" + name
		}
		return base + "/" + ver + "/binaries/" + name
	}
}

// mavenSupported reports whether a version is on the 3.x line. Maven 4 is still
// a release candidate, so it is listed (marked preview) but never chosen.
func mavenSupported(v string) bool { return version.FirstComponent(v) == "3" }

type mavenIndexCandidate struct {
	url   string
	retry bool
}

// mavenIndexBases lists where maven-metadata.xml may live, in priority order.
// A configured mirror comes first, then Central: Apache distribution mirrors keep
// the archives but not the index, so the mirror attempt is a single try and the
// Central one retries.
func mavenIndexBases() []mavenIndexCandidate {
	var out []mavenIndexCandidate
	if m := strings.TrimRight(config.MirrorFor("maven", mavenCentral), "/"); m != "" && m != mavenCentral {
		out = append(out, mavenIndexCandidate{m + "/maven-metadata.xml", false})
	}
	return append(out, mavenIndexCandidate{mavenCentral + "/maven-metadata.xml", true})
}

func mavenIndex() (mavenMetadata, error) {
	var lastErr error
	for _, cand := range mavenIndexBases() {
		var (
			txt string
			err error
		)
		if cand.retry {
			txt, err = netx.GetTextRetry(cand.url)
		} else {
			txt, err = netx.GetText(cand.url)
		}
		if err != nil {
			lastErr = err
			continue
		}
		var md mavenMetadata
		if err := xml.Unmarshal([]byte(txt), &md); err != nil {
			lastErr = err
			continue
		}
		if len(md.Versions) == 0 {
			lastErr = fmt.Errorf("%s listed no versions", netx.ShortURL(cand.url))
			continue
		}
		return md, nil
	}
	return mavenMetadata{}, lastErr
}

func mavenList(filter string) ([]Release, error) {
	f := strings.ToLower(strings.TrimSpace(filter))
	if f == "lts" {
		return nil, fmt.Errorf("maven has no LTS channel; use latest or an explicit 3.x version")
	}
	md, err := mavenIndex()
	if err != nil {
		return nil, err
	}
	if f == "release" {
		// <release> currently points at 4.0.0-rc-7, so naming it is only safe
		// when upstream has moved to a 3.x release.
		if md.Release == "" {
			return nil, fmt.Errorf("the upstream index does not advertise a <release> version")
		}
		if !mavenSupported(md.Release) || mavenPreview.MatchString(md.Release) {
			return nil, fmt.Errorf("upstream <release> is %s, which this version does not support; pick a 3.x version or latest", md.Release)
		}
		return []Release{{Version: md.Release}}, nil
	}

	all := f == "all"
	// A channel (or no filter at all) must never resolve to a release candidate:
	// upstream's own <latest>/<release> both point at 4.0.0-rc-7.
	stableOnly := f == "" || f == "latest" || f == "stable"
	explicitPreview := mavenPreview.MatchString(f)
	var out []Release
	for _, v := range md.Versions {
		preview := mavenPreview.MatchString(v)
		if stableOnly {
			if preview || !mavenSupported(v) {
				continue
			}
		} else if !all {
			// A partial request must not pull a release candidate in: `maven@3.10`
			// resolved to 3.10.0-rc-1 because the two compare equal and the
			// metadata lists the candidate first. Only a request that names a
			// pre-release gets one.
			if preview && !explicitPreview {
				continue
			}
			if !version.Matches(f, v) {
				continue
			}
		}
		note := ""
		if preview {
			note = "preview"
		}
		out = append(out, Release{Version: v, Note: note})
	}
	return out, nil
}

func mavenPlan(ver string) (Plan, error) {
	exact := strings.TrimPrefix(strings.TrimSpace(ver), "v")
	if version.IsChannel(exact) || exact == "release" {
		rels, err := mavenList(exact)
		if err != nil {
			return Plan{}, err
		}
		if len(rels) == 0 {
			return Plan{}, fmt.Errorf("no Maven version matches %q", ver)
		}
		exact = rels[0].Version
		for _, r := range rels[1:] {
			if version.Compare(r.Version, exact) > 0 {
				exact = r.Version
			}
		}
	}

	name := "apache-maven-" + exact + "-bin" + mavenArchiveExt()
	urlFor := mavenURLFor(exact, name)
	bases := downloadBases("maven", mavenCentral, mavenDistMirrors...)
	plan := Plan{
		Exact:  exact,
		File:   name,
		Bases:  bases,
		URLFor: urlFor,
		Layout: LayoutFlatten,
	}
	plan.SHA512 = mavenChecksum(bases, urlFor)
	if plan.SHA512 == "" {
		// Central publishes .sha256 as 404 and only .sha512 exists; a mirror
		// without sidecars is not a reason to block the install.
		logx.Warnf("no SHA-512 published for Apache Maven %s; installing without an integrity check", exact)
	}
	return plan, nil
}

// mavenChecksum reads the .sha512 sidecar from the first base that has one.
func mavenChecksum(bases []string, urlFor func(string) string) string {
	for _, base := range bases {
		txt, err := netx.GetText(urlFor(base) + ".sha512")
		if err != nil {
			logx.Verbosef("no SHA-512 from %s: %v", netx.ShortURL(base), err)
			continue
		}
		for _, field := range strings.Fields(txt) {
			if mavenSHA512.MatchString(field) {
				return field
			}
		}
	}
	return ""
}

func mavenVerify(dir string) (string, error) {
	env := os.Environ()
	problem := JavaProblem(env)
	if problem != "" {
		// Maven itself is fine; only its runtime dependency is missing. Report
		// it, but let the install stand (the files are in place).
		logx.Warnf("Apache Maven %s needs a JDK: %s", filepath.Base(dir), problem)
		return "", nil
	}
	if home := javaHomeFromConfig(); home != "" && os.Getenv("JAVA_HOME") == "" {
		env = withEnv(env, "JAVA_HOME", home)
	}
	out, err := captureOutputEnv(env, filepath.Join(dir, mavenToolPath("mvn", "")), "-v")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "Apache Maven "); ok {
			return strings.Fields(rest)[0], nil
		}
	}
	return "", nil
}

// JavaProblem explains why mvn would not find a JVM in env, mirroring
// bin/mvn.cmd: a non-empty JAVA_HOME wins and is never retried on PATH, so a
// stale JAVA_HOME breaks Maven even when a java is installed elsewhere.
func JavaProblem(env []string) string {
	home, path := "", ""
	for _, kv := range env {
		i := strings.Index(kv, "=")
		if i < 0 {
			continue
		}
		switch strings.ToUpper(kv[:i]) {
		case "JAVA_HOME":
			home = strings.TrimSpace(kv[i+1:])
		case "PATH":
			path = kv[i+1:]
		}
	}
	if home != "" {
		if _, err := os.Stat(filepath.Join(home, "bin", exeName("java"))); err != nil {
			return "JAVA_HOME=" + home + " does not contain bin/java"
		}
		return ""
	}
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, exeName("java"))); err == nil {
			return ""
		}
	}
	return "no JDK found (JAVA_HOME is unset and there is no java on PATH)"
}

// javaHomeFromConfig returns the JAVA_HOME of the configured java version, so a
// self-check can run before any shell is activated.
func javaHomeFromConfig() string {
	ver := config.Load().Defaults["java"]
	if ver == "" {
		return ""
	}
	dir := config.VersionDir("java", ver)
	if fsutil.FileExists(filepath.Join(dir, "bin", exeName("java"))) {
		return dir
	}
	return ""
}

// withEnv sets name in env, replacing an existing entry rather than adding a
// second one: duplicate keys resolve differently per platform.
func withEnv(env []string, name, value string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if i := strings.Index(kv, "="); i > 0 && strings.EqualFold(kv[:i], name) {
			continue
		}
		out = append(out, kv)
	}
	return append(out, name+"="+value)
}
