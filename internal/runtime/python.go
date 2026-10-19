package runtime

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/MOMIEDEJIYI/ployenv/internal/config"
	"github.com/MOMIEDEJIYI/ployenv/internal/fsutil"
	"github.com/MOMIEDEJIYI/ployenv/internal/netx"
	"github.com/MOMIEDEJIYI/ployenv/internal/version"
)

var pythonSpec = &Spec{
	Name:     "python",
	Title:    "Python",
	Kind:     "language",
	Aliases:  []string{"py", "cpython"},
	Official: pythonOfficial,
	ExeTools: []string{"python", "python3"},
	CmdTools: []string{"pip", "pip3"},
	Primary:  "python",
	ToolPath: pythonToolPath,
	PathDirs: pythonPathDirs,
	Verify:   pythonVerify,
	List:     pythonList,
	Plan:     pythonPlan,
}

// pythonToolPathFor maps a tool to its path inside a version directory on
// osName. Windows installs come from NuGet, which keeps python.exe at the root
// and the pip shims in Scripts/; the standalone builds used elsewhere keep both
// in bin/.
func pythonToolPathFor(osName, tool string) string {
	switch tool {
	case "python", "python3":
		return filepath.Join(toolDirFor(osName), exeFor(osName, "python"))
	case "pip", "pip3":
		if osName == "windows" {
			return filepath.Join("Scripts", exeFor(osName, "pip"))
		}
		return filepath.Join(toolDirFor(osName), exeFor(osName, "pip"))
	}
	return ""
}

func pythonToolPath(tool, _ string) string { return pythonToolPathFor(hostOS(), tool) }

func pythonPathDirsFor(osName, dir string) []string {
	if osName == "windows" {
		// A Windows install keeps python.exe in dir and only sometimes has a
		// Scripts subdirectory; a directory that does not exist must not be put
		// on PATH (I-09).
		dirs := []string{dir}
		if scripts := filepath.Join(dir, "Scripts"); fsutil.DirExists(scripts) {
			dirs = append(dirs, scripts)
		}
		return dirs
	}
	return []string{filepath.Join(dir, "bin")}
}

func pythonPathDirs(dir string) []string { return pythonPathDirsFor(hostOS(), dir) }

func pythonVerify(dir string) (string, error) {
	out, err := captureOutput(filepath.Join(dir, pythonToolPath("python", "")),
		"-c", "import sys;print('.'.join(map(str,sys.version_info[:3])))")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// ------------------------------------------------------------ Windows (NuGet)

func pythonNugetVersions() ([]string, error) {
	var idx struct {
		Versions []string `json:"versions"`
	}
	if err := netx.GetJSONRetry(pythonNugetBase+"/python/index.json", &idx); err != nil {
		return nil, err
	}
	var out []string
	for _, v := range idx.Versions {
		if strings.ContainsAny(v, "-+") {
			continue // prereleases
		}
		out = append(out, v)
	}
	return out, nil
}

func pythonNugetPlan(ver string) Plan {
	archive := "python." + ver + ".nupkg"
	return Plan{
		Exact:  ver,
		File:   archive,
		Bases:  downloadBases("python", pythonNugetBase, "https://nuget.cdn.azure.cn/v3-flatcontainer"),
		URLFor: func(base string) string { return base + "/python/" + ver + "/" + archive },
		Layout: LayoutNuGet,
	}
}

// ------------------------------------------------- Non-Windows (standalone)

type githubRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// pythonReleasesURL and pythonNugetBase are the two index endpoints that a
// configured mirror cannot carry (the mirrors only serve archives). They are
// variables only so tests can point them at a local server; production never
// reassigns them.
var (
	pythonReleasesURL = "https://api.github.com/repos/astral-sh/python-build-standalone/releases"
	pythonNugetBase   = "https://api.nuget.org/v3-flatcontainer"
)

// pythonOfficial is the upstream source shown by `ployenv config` / `runtimes`.
func pythonOfficial() string {
	if hostOS() == "windows" {
		return pythonNugetBase
	}
	return pythonReleasesURL
}

func pythonAssetSuffix() string {
	switch hostOS() + "/" + hostArch() {
	case "windows/amd64":
		return "x86_64-pc-windows-msvc-install_only.tar.gz"
	case "windows/arm64":
		return "aarch64-pc-windows-msvc-install_only.tar.gz"
	case "darwin/arm64":
		return "aarch64-apple-darwin-install_only.tar.gz"
	case "darwin/amd64":
		return "x86_64-apple-darwin-install_only.tar.gz"
	case "linux/arm64":
		return "aarch64-unknown-linux-gnu-install_only.tar.gz"
	default:
		return "x86_64-unknown-linux-gnu-install_only.tar.gz"
	}
}

func pythonVersionFromAsset(name string) string {
	if !strings.HasPrefix(name, "cpython-") {
		return ""
	}
	rest := strings.TrimPrefix(name, "cpython-")
	if i := strings.Index(rest, "+"); i > 0 {
		return rest[:i]
	}
	return ""
}

type pythonAsset struct {
	Version string
	Name    string
	URL     string
	Tag     string
}

const (
	// A single per_page=40 response is large enough that api.github.com has
	// answered 504 for it; per_page=10 consistently succeeds. Four pages keep the
	// same 40-release window in small, retryable chunks.
	pythonReleasesPerPage = 10
	pythonReleasesPages   = 4
)

// pythonReleases fetches the release index in small pages. Later pages are
// best effort: a transient failure must not hide the versions already seen.
func pythonReleases() ([]githubRelease, error) {
	var all []githubRelease
	for page := 1; page <= pythonReleasesPages; page++ {
		var rels []githubRelease
		url := fmt.Sprintf("%s?per_page=%d&page=%d", pythonReleasesURL, pythonReleasesPerPage, page)
		if err := netx.GetJSONRetry(url, &rels); err != nil {
			if page == 1 {
				return nil, err
			}
			break
		}
		all = append(all, rels...)
		if len(rels) < pythonReleasesPerPage {
			break
		}
	}
	return all, nil
}

func pythonAssetList() ([]pythonAsset, error) {
	rels, err := pythonReleases()
	if err != nil {
		return nil, err
	}
	suffix := pythonAssetSuffix()
	seen := map[string]bool{}
	var out []pythonAsset
	for _, rel := range rels {
		for _, a := range rel.Assets {
			if !strings.HasSuffix(a.Name, suffix) {
				continue
			}
			ver := pythonVersionFromAsset(a.Name)
			if ver == "" || seen[ver] {
				continue
			}
			seen[ver] = true
			out = append(out, pythonAsset{ver, a.Name, a.URL, rel.TagName})
		}
	}
	return out, nil
}

// ------------------------------------------------------------------ shared

func pythonList(filter string) ([]Release, error) {
	f := strings.ToLower(strings.TrimSpace(filter))
	var out []Release
	keep := func(v string) bool {
		return f == "" || f == "all" || f == "latest" || version.Matches(f, v)
	}
	if hostOS() == "windows" {
		versions, err := pythonNugetVersions()
		if err != nil {
			return nil, err
		}
		for _, v := range versions {
			if keep(v) {
				out = append(out, Release{Version: v, Note: "nuget python (includes pip)"})
			}
		}
		return out, nil
	}
	assets, err := pythonAssetList()
	if err != nil {
		return nil, err
	}
	for _, a := range assets {
		if keep(a.Version) {
			out = append(out, Release{Version: a.Version, Note: a.Name})
		}
	}
	return out, nil
}

func pythonPlan(ver string) (Plan, error) {
	if hostOS() == "windows" {
		return pythonNugetPlan(ver), nil
	}
	assets, err := pythonAssetList()
	if err != nil {
		return Plan{}, err
	}
	picked := ""
	for _, a := range assets {
		if version.Matches(ver, a.Version) || a.Version == ver {
			if picked == "" || version.Compare(a.Version, picked) > 0 {
				picked = a.Version
			}
		}
	}
	if picked == "" {
		return Plan{}, fmt.Errorf("no Python version matching %s", ver)
	}
	for _, a := range assets {
		if a.Version != picked {
			continue
		}
		// A configured mirror is tried first (GitHub-release mirror layout:
		// <mirror>/<tag>/<asset>), then the official GitHub asset URL.
		bases := []string{}
		if m := config.Mirror("python"); m != "" && a.Tag != "" {
			bases = append(bases, strings.TrimRight(m, "/")+"/"+a.Tag+"/"+a.Name)
		}
		bases = append(bases, a.URL)
		return Plan{
			Exact:  picked,
			File:   a.Name,
			Bases:  bases,
			URLFor: func(base string) string { return base },
			Layout: LayoutFlatten,
		}, nil
	}
	return Plan{}, fmt.Errorf("no Python %s package found", picked)
}
