package runtime

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/MOMIEDEJIYI/ployenv/internal/logx"
	"github.com/MOMIEDEJIYI/ployenv/internal/netx"
	"github.com/MOMIEDEJIYI/ployenv/internal/version"
)

// androidRepository is the SDK repository root. platform-tools is published
// there as one archive per host OS, with a SHA-1 (and no stronger digest) in the
// index; there are no sidecar checksum files.
var androidRepository = "https://dl.google.com/android/repository"

// androidMirrors carry the same repository layout and the same bytes.
var androidMirrors = []string{
	"https://mirrors.cloud.tencent.com/AndroidSDK",
}

// platformToolsPackage is the repository path of the component ployenv manages.
const platformToolsPackage = "platform-tools"

// adbSpec manages Android platform-tools, the home of adb. Like Maven it is a
// tool rather than a language: an active version adds nothing but its own
// directory to PATH. ANDROID_HOME and ANDROID_SDK_ROOT stay untouched, because
// platform-tools is a standalone SDK component, not an SDK root.
var adbSpec = &Spec{
	Name:     "adb",
	Title:    "Android Platform-Tools (adb)",
	Kind:     "tool",
	Aliases:  []string{"platform-tools"},
	Official: func() string { return androidRepository },
	ExeTools: []string{"adb", "fastboot"},
	Primary:  "adb",
	ToolPath: adbToolPath,
	Verify:   adbVerify,
	List:     adbList,
	Plan:     adbPlan,
}

// adbHostOS and adbArchiveOS are the repository's two names for one platform:
// <host-os> in the index ("macosx") and the archive suffix ("darwin"). Both are
// pure functions so every mapping is assertable from any runner. platform-tools
// ships one archive per OS and none per architecture.
func adbHostOS(osName string) string {
	switch osName {
	case "windows":
		return "windows"
	case "darwin":
		return "macosx"
	default:
		return "linux"
	}
}

func adbArchiveOS(osName string) string {
	switch osName {
	case "windows":
		return "win"
	case "darwin":
		return "darwin"
	default:
		return "linux"
	}
}

// adbToolPath: the archive keeps adb, fastboot and their DLLs in one flat
// folder, so both tools sit at the version root once the layout is flattened.
func adbToolPathFor(osName, tool string) string {
	switch tool {
	case "adb", "fastboot":
		return exeFor(osName, tool)
	}
	return ""
}

func adbToolPath(tool, _ string) string { return adbToolPathFor(hostOS(), tool) }

// adbArchiveName is the published file name for one version on one platform.
func adbArchiveName(osName, ver string) string {
	return "platform-tools_r" + ver + "-" + adbArchiveOS(osName) + ".zip"
}

// The repository index, reduced to the parts ployenv reads.
type androidChecksum struct {
	Type  string `xml:"type,attr"`
	Value string `xml:",chardata"`
}

type androidArchive struct {
	HostOS   string          `xml:"host-os"`
	Size     int64           `xml:"complete>size"`
	Checksum androidChecksum `xml:"complete>checksum"`
	URL      string          `xml:"complete>url"`
}

type androidRevision struct {
	Major int `xml:"major"`
	Minor int `xml:"minor"`
	Micro int `xml:"micro"`
}

type androidPackage struct {
	Path     string           `xml:"path,attr"`
	Revision androidRevision  `xml:"revision"`
	Archives []androidArchive `xml:"archives>archive"`
}

type androidRepositoryIndex struct {
	RemotePackages []androidPackage `xml:"remotePackage"`
}

func (p androidPackage) version() string {
	return fmt.Sprintf("%d.%d.%d", p.Revision.Major, p.Revision.Minor, p.Revision.Micro)
}

func (idx androidRepositoryIndex) platformTools() (androidPackage, bool) {
	for _, p := range idx.RemotePackages {
		if p.Path == platformToolsPackage {
			return p, true
		}
	}
	return androidPackage{}, false
}

// adbIndexBases lists the indexes in priority order: a configured mirror first,
// then the official repository.
func adbIndexBases() []string {
	return downloadBases("adb", androidRepository, androidMirrors...)
}

// adbIndexCache keeps the parsed index for one process. An install asks for the
// index twice (resolve, then the checksum), which used to mean two downloads of
// the same 423 KB document. The key is the base list, so a test that points the
// repository somewhere else never reads a stale answer, and an error is never
// cached (a transient failure must not poison the rest of the process).
var (
	adbIndexMu    sync.Mutex
	adbIndexKey   string
	adbIndexValue androidRepositoryIndex
)

func adbIndex() (androidRepositoryIndex, error) {
	bases := adbIndexBases()
	key := strings.Join(bases, "|")
	adbIndexMu.Lock()
	defer adbIndexMu.Unlock()
	if adbIndexKey == key && adbIndexValue.RemotePackages != nil {
		return adbIndexValue, nil
	}
	idx, err := fetchADBIndex(bases)
	if err != nil {
		return idx, err
	}
	adbIndexKey, adbIndexValue = key, idx
	return idx, nil
}

func fetchADBIndex(bases []string) (androidRepositoryIndex, error) {
	var lastErr error
	for _, base := range bases {
		txt, err := netx.GetTextRetry(strings.TrimRight(base, "/") + "/repository2-3.xml")
		if err != nil {
			lastErr = err
			continue
		}
		var idx androidRepositoryIndex
		if err := xml.Unmarshal([]byte(txt), &idx); err != nil {
			lastErr = err
			continue
		}
		if _, ok := idx.platformTools(); !ok {
			lastErr = fmt.Errorf("%s does not list %s", netx.ShortURL(base), platformToolsPackage)
			continue
		}
		return idx, nil
	}
	return androidRepositoryIndex{}, lastErr
}

func adbList(filter string) ([]Release, error) {
	f := strings.ToLower(strings.TrimSpace(filter))
	if f == "lts" {
		return nil, fmt.Errorf("adb has no LTS channel; use latest or an explicit version")
	}
	idx, err := adbIndex()
	if err != nil {
		return nil, err
	}
	pkg, ok := idx.platformTools()
	if !ok {
		return nil, fmt.Errorf("the Android repository no longer lists %s", platformToolsPackage)
	}
	ver := pkg.version()
	switch f {
	case "", "latest", "stable", "release", "all":
		return []Release{{Version: ver}}, nil
	}
	if !version.Matches(f, ver) {
		// The repository carries one revision at a time; older platform-tools
		// archives are mostly gone upstream (r34.0.5 answers 404).
		return nil, nil
	}
	return []Release{{Version: ver}}, nil
}

// adbChecksum reads the SHA-1 the repository advertises for one version on this
// host. The index is the only place it appears, so a version the index no longer
// carries installs unverified - reported, not blocked.
func adbChecksum(exact string) string {
	idx, err := adbIndex()
	if err != nil {
		logx.Verbosef("no repository index for the checksum of %s: %v", exact, err)
		return ""
	}
	pkg, ok := idx.platformTools()
	if !ok || pkg.version() != exact {
		return ""
	}
	want := adbHostOS(hostOS())
	for _, a := range pkg.Archives {
		if a.HostOS != want {
			continue
		}
		sum := strings.ToLower(strings.TrimSpace(a.Checksum.Value))
		// repository2-1.xml omits type=""; a 40-hex digest there is still SHA-1.
		if (a.Checksum.Type == "" || strings.EqualFold(a.Checksum.Type, "sha1")) && len(sum) == 40 {
			return sum
		}
	}
	return ""
}

func adbPlan(ver string) (Plan, error) {
	exact := strings.TrimPrefix(strings.TrimSpace(ver), "v")
	if exact == "" || version.IsChannel(exact) {
		rels, err := adbList(exact)
		if err != nil {
			return Plan{}, err
		}
		if len(rels) == 0 {
			return Plan{}, fmt.Errorf("no adb version matches %q", ver)
		}
		exact = rels[0].Version
	}
	name := adbArchiveName(hostOS(), exact)
	plan := Plan{
		Exact: exact,
		File:  name,
		// The name is built from the convention rather than read from the index
		// <url>: that entry is a bare file name with no host, and every mirror
		// serves the same convention.
		Bases:  adbIndexBases(),
		URLFor: func(base string) string { return strings.TrimRight(base, "/") + "/" + name },
		Layout: LayoutFlatten,
	}
	if sum := adbChecksum(exact); sum != "" {
		plan.SHA1 = sum
	} else {
		logx.Warnf("no SHA-1 published for Android platform-tools %s; installing without an integrity check", exact)
	}
	return plan, nil
}

// adbVerify reads the version stamp the archive carries. It deliberately does
// not run adb: the first adb invocation creates %USERPROFILE%\.android, which
// would write outside the data directory in the middle of an install.
func adbVerify(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "source.properties"))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || strings.TrimSpace(key) != "Pkg.Revision" {
			continue
		}
		stamp := strings.TrimSpace(value)
		if want := filepath.Base(dir); stamp != want {
			return stamp, fmt.Errorf("source.properties reports %s, but the version directory is %s", stamp, want)
		}
		return stamp, nil
	}
	return "", fmt.Errorf("source.properties carries no Pkg.Revision")
}
