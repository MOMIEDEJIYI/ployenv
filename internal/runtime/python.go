package runtime

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/MOMIEDEJIYI/ployenv/internal/config"
	"github.com/MOMIEDEJIYI/ployenv/internal/fsutil"
	"github.com/MOMIEDEJIYI/ployenv/internal/netx"
	"github.com/MOMIEDEJIYI/ployenv/internal/platform"
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

func pythonPathDirs(dir string) []string {
	if platform.IsWindows() {
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

func pythonToolPath(tool, _ string) string {
	switch tool {
	case "python", "python3":
		return exeName("python")
	case "pip", "pip3":
		if platform.IsWindows() {
			return filepath.Join("Scripts", exeName("pip"))
		}
		return filepath.Join("bin", "pip")
	}
	return ""
}

func pythonVerify(dir string) (string, error) {
	out, err := captureOutput(filepath.Join(dir, exeName("python")),
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
	if platform.IsWindows() {
		return pythonNugetBase
	}
	return pythonReleasesURL
}

func pythonAssetSuffix() string {
	switch platform.OS() + "/" + platform.Arch() {
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

func pythonAssetList() ([]pythonAsset, error) {
	var rels []githubRelease
	if err := netx.GetJSONRetry(pythonReleasesURL+"?per_page=40", &rels); err != nil {
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
	if platform.IsWindows() {
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
	if platform.IsWindows() {
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
