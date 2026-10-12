package runtime

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/MOMIEDEJIYI/ployenv/internal/config"
	"github.com/MOMIEDEJIYI/ployenv/internal/netx"
	"github.com/MOMIEDEJIYI/ployenv/internal/platform"
	"github.com/MOMIEDEJIYI/ployenv/internal/version"
)

var javaSpec = &Spec{
	Name:     "java",
	Title:    "Java (Eclipse Temurin JDK)",
	Kind:     "language",
	Aliases:  []string{"jdk", "openjdk", "temurin"},
	Official: func() string { return adoptiumAPI },
	ExeTools: []string{"java", "javac", "jar", "jshell", "keytool"},
	Primary:  "java",
	EnvVar:   "JAVA_HOME",
	ToolPath: javaToolPath,
	PathDirs: func(dir string) []string { return []string{filepath.Join(dir, "bin")} },
	Verify:   javaVerify,
	List:     javaList,
	Plan:     javaPlan,
}

// adoptiumAPI serves the version index. No public mirror carries this API, so
// the list always comes from the official endpoint (requirements FR-6: the
// index and the download source are independent).
const adoptiumAPI = "https://api.adoptium.net/v3"

func adoptiumOS() string {
	switch platform.OS() {
	case "windows":
		return "windows"
	case "darwin":
		return "mac"
	default:
		return "linux"
	}
}

func adoptiumArch() string {
	switch platform.Arch() {
	case "amd64":
		return "x64"
	case "arm64":
		return "aarch64"
	default:
		return platform.Arch()
	}
}

func javaToolPath(tool, _ string) string {
	switch tool {
	case "java", "javac", "jar", "jshell", "keytool":
		return filepath.Join("bin", exeName(tool))
	}
	return ""
}

func javaVerify(dir string) (string, error) {
	out, err := captureOutput(filepath.Join(dir, "bin", exeName("java")), "-version")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "openjdk version") || strings.HasPrefix(line, "java version") {
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				return strings.Trim(fields[2], "\""), nil
			}
		}
	}
	return "", nil
}

// adoptiumRelease models both /assets/feature_releases (release_name +
// version_data) and /assets/version (release_name + version) responses.
type adoptiumRelease struct {
	ReleaseName string `json:"release_name"`
	Binaries    []struct {
		Package struct {
			Link string `json:"link"`
			Name string `json:"name"`
		} `json:"package"`
	} `json:"binaries"`
	Version struct {
		Semver string `json:"semver"`
		Major  int    `json:"major"`
	} `json:"version"`
	VersionData struct {
		Semver string `json:"semver"`
		Major  int    `json:"major"`
	} `json:"version_data"`
}

// nameVersion is the version ployenv records: the release name without its
// "jdk" prefix. Java 8 uses "jdk8u504-b01" (no dash) while newer lines use
// "jdk-21.0.12.1+1", so both forms are stripped.
func (r adoptiumRelease) nameVersion() string {
	v := strings.TrimPrefix(r.ReleaseName, "jdk-")
	v = strings.TrimPrefix(v, "jdk")
	if v != "" {
		return v
	}
	return r.semver()
}

func (r adoptiumRelease) semver() string {
	if r.Version.Semver != "" {
		return r.Version.Semver
	}
	if r.VersionData.Semver != "" {
		return r.VersionData.Semver
	}
	v := strings.TrimPrefix(r.ReleaseName, "jdk-")
	return strings.TrimPrefix(v, "jdk")
}

// javaFeatureReleases lists GA releases for one feature version (21, 17, 8…).
// The plain release_names endpoint only ever returns its newest page, so an
// older feature line must be requested explicitly (see javaList).
func javaFeatureReleases(feature string) ([]adoptiumRelease, error) {
	u := fmt.Sprintf("%s/assets/feature_releases/%s/ga?architecture=%s&image_type=jdk&os=%s&page_size=50&project=jdk&sort_order=DESC&vendor=eclipse",
		adoptiumAPI, feature, adoptiumArch(), adoptiumOS())
	var rels []adoptiumRelease
	if err := netx.GetJSONRetry(u, &rels); err != nil {
		return nil, err
	}
	return rels, nil
}

func javaList(filter string) ([]Release, error) {
	f := strings.ToLower(strings.TrimSpace(filter))

	if version.IsChannel(f) {
		return javaNewest()
	}
	feature := javaFeatureFilter(f)
	if feature == "" {
		// Neither a feature version nor a channel keyword: nothing can match.
		// Falling through to the newest-releases list would hand back an
		// unrelated version, which is how `install java@foo` used to download
		// jdk-27 (BUG-003).
		return nil, nil
	}
	rels, err := javaFeatureReleases(feature)
	if err != nil {
		return nil, err
	}
	var out []Release
	for _, r := range rels {
		ver := r.nameVersion()
		if ver == "" || !javaMatches(f, ver) {
			continue
		}
		out = append(out, Release{Version: ver, Note: r.semver()})
	}
	return out, nil
}

// javaNewest pages the plain release_names endpoint for the newest releases. It
// caps page_size at 20, so a few pages are fetched to give a useful list.
func javaNewest() ([]Release, error) {
	var names []string
	const maxPages = 5
	for page := 0; page < maxPages; page++ {
		u := fmt.Sprintf("%s/info/release_names?architecture=%s&heap_size=normal&image_type=jdk&os=%s&page=%d&page_size=20&project=jdk&release_type=ga&sort_order=DESC&vendor=eclipse",
			adoptiumAPI, adoptiumArch(), adoptiumOS(), page)
		var res struct {
			ReleaseNames []string `json:"release_names"`
			Releases     []string `json:"releases"`
		}
		if err := netx.GetJSONRetry(u, &res); err != nil {
			if page == 0 {
				return nil, err
			}
			break
		}
		batch := append(append([]string{}, res.ReleaseNames...), res.Releases...)
		if len(batch) == 0 {
			break
		}
		names = append(names, batch...)
		if len(batch) < 20 {
			break
		}
	}
	var out []Release
	for _, name := range names {
		out = append(out, Release{Version: strings.TrimPrefix(name, "jdk-"), Note: name})
	}
	return out, nil
}

// javaFeatureFilter extracts the leading numeric feature version from a filter
// ("21" -> "21", "21.0.2" -> "21"); it returns "" when the filter is not a
// feature version at all.
func javaFeatureFilter(f string) string {
	f = strings.TrimPrefix(f, "jdk-")
	f = strings.TrimPrefix(f, "v")
	first := strings.Split(f, ".")[0]
	if first == "" {
		return ""
	}
	for _, r := range first {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return first
}

// javaMatches is version.Matches extended for the Java 8 "8u<build>-b<build>"
// naming, which shares no dotted prefix with the bare feature number.
func javaMatches(request, candidate string) bool {
	if version.Matches(request, candidate) {
		return true
	}
	return strings.HasPrefix(candidate, request+"u")
}

// javaFeatureOf maps a Java version to its feature number
// ("8u462-b08" -> "8", "21.0.12.1+1" -> "21").
func javaFeatureOf(ver string) string {
	v := strings.TrimPrefix(strings.TrimSpace(ver), "jdk-")
	v = strings.TrimPrefix(v, "v")
	if v == "8" || strings.HasPrefix(v, "8u") {
		return "8"
	}
	return version.FirstComponent(v)
}

func javaPlan(ver string) (Plan, error) {
	feature := javaFeatureOf(ver)
	rels, err := javaFeatureReleases(feature)
	if err != nil {
		return Plan{}, err
	}
	best := -1
	for i, r := range rels {
		name := r.nameVersion()
		if name == ver || r.semver() == ver || version.Matches(ver, name) {
			if best == -1 || version.Compare(name, rels[best].nameVersion()) > 0 {
				best = i
			}
		}
	}
	if best == -1 {
		if len(rels) == 0 {
			return Plan{}, fmt.Errorf("java %s not found on Adoptium", ver)
		}
		best = 0
	}
	r := rels[best]
	if len(r.Binaries) == 0 || r.Binaries[0].Package.Link == "" {
		return Plan{}, fmt.Errorf("adoptium returned no download link for java %s", ver)
	}
	exact := r.nameVersion()
	if exact == "" {
		exact = ver
	}
	name := r.Binaries[0].Package.Name
	if name == "" {
		name = "jdk-" + exact + ".zip"
	}
	link := r.Binaries[0].Package.Link
	major := javaFeatureOf(exact)
	rel := fmt.Sprintf("%s/jdk/%s/%s/%s", major, adoptiumArch(), adoptiumOS(), name)

	// A configured mirror is tried first, using the Adoptium / TUNA layout
	// (<mirror>/<major>/jdk/<arch>/<os>/<file>), then the official GitHub link.
	// Without a mirror, the official link is first and TUNA is the fallback.
	mirror := config.Mirror("java")
	var bases []string
	if mirror != "" && major != "" {
		bases = append(bases, strings.TrimRight(mirror, "/")+"/"+rel)
	}
	bases = append(bases, link)
	if mirror == "" && major != "" {
		bases = append(bases, "https://mirrors.tuna.tsinghua.edu.cn/Adoptium/"+rel)
	}
	return Plan{
		Exact:  exact,
		File:   name,
		Bases:  bases,
		URLFor: func(base string) string { return base },
		Layout: LayoutFlatten,
	}, nil
}
