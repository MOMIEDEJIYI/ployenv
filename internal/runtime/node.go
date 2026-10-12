package runtime

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/MOMIEDEJIYI/ployenv/internal/netx"
	"github.com/MOMIEDEJIYI/ployenv/internal/version"
)

var nodeSpec = &Spec{
	Name:     "node",
	Title:    "Node.js",
	Kind:     "language",
	Aliases:  []string{"nodejs", "node.js"},
	Official: func() string { return "https://nodejs.org/dist" },
	ExeTools: []string{"node"},
	CmdTools: []string{"npm", "npx", "corepack"},
	Primary:  "node",
	ToolPath: nodeToolPath,
	// PathDirs is what a version adds to PATH: the layout differs per platform,
	// so without this the Unix-likes were handed the version root (BUG-021).
	PathDirs: nodePathDirs,
	Verify:   nodeVerify,
	List:     nodeList,
	Plan:     nodePlan,
}

// nodeMirrors are well-known mirrors used when the official source fails.
var nodeMirrors = []string{
	"https://registry.npmmirror.com/-/binary/node",
	"https://mirrors.ustc.edu.cn/node",
	"https://mirrors.huaweicloud.com/nodejs",
	"https://mirrors.cloud.tencent.com/nodejs-release",
}

type nodeIndexEntry struct {
	Version string          `json:"version"`
	LTS     json.RawMessage `json:"lts"`
}

// nodeToolPathFor maps a tool to its path inside a version directory on osName.
// Node's Windows archive keeps node.exe and the npm/npx shims at the root; the
// Unix tarballs keep them in bin/.
func nodeToolPathFor(osName, tool string) string {
	dir := toolDirFor(osName)
	switch tool {
	case "node":
		return filepath.Join(dir, exeFor(osName, "node"))
	case "npm":
		return filepath.Join(dir, scriptFor(osName, "npm"))
	case "npx":
		return filepath.Join(dir, scriptFor(osName, "npx"))
	case "corepack":
		return filepath.Join(dir, scriptFor(osName, "corepack"))
	}
	return ""
}

func nodeToolPath(tool, _ string) string { return nodeToolPathFor(hostOS(), tool) }

// nodePathDirsFor is what gets prepended to PATH for an active version: the
// version directory on Windows, its bin/ on the Unix-likes.
func nodePathDirsFor(osName, dir string) []string {
	return []string{filepath.Join(dir, toolDirFor(osName))}
}

func nodePathDirs(dir string) []string { return nodePathDirsFor(hostOS(), dir) }

func nodeVerify(dir string) (string, error) {
	out, err := captureOutput(filepath.Join(dir, nodeToolPath("node", "")), "--version")
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(strings.TrimSpace(out), "v"), nil
}

func nodeList(filter string) ([]Release, error) {
	var items []nodeIndexEntry
	// The index lives alongside the archives, so a configured mirror that
	// carries index.json is used first (FR-6), falling back to the official one.
	var lastErr error
	for _, base := range downloadBases("node", "https://nodejs.org/dist", nodeMirrors...) {
		if err := netx.GetJSON(base+"/index.json", &items); err != nil {
			lastErr = err
			continue
		}
		lastErr = nil
		break
	}
	if lastErr != nil {
		return nil, lastErr
	}
	f := strings.ToLower(strings.TrimSpace(filter))
	var out []Release
	for _, it := range items {
		ver := strings.TrimPrefix(it.Version, "v")
		lts := ""
		if len(it.LTS) > 0 && string(it.LTS) != "false" {
			var name string
			if json.Unmarshal(it.LTS, &name) == nil {
				lts = name
			}
		}
		switch f {
		case "lts":
			if lts == "" {
				continue
			}
		case "", "latest", "all":
		default:
			if !version.Matches(f, ver) {
				continue
			}
		}
		note := ""
		if lts != "" {
			note = "LTS " + lts
		}
		out = append(out, Release{Version: ver, LTS: lts, Note: note})
	}
	return out, nil
}

// nodeArchiveOS, nodeArchiveArch and nodeArchiveExt are the pure forms of the
// archive naming, so each mapping can be asserted for every platform.
func nodeArchiveOS(osName string) string {
	return map[string]string{"windows": "win", "darwin": "darwin", "linux": "linux"}[osName]
}

func nodeArchiveArch(arch string) string {
	return map[string]string{"amd64": "x64", "arm64": "arm64", "386": "x86"}[arch]
}

func nodeArchiveExt(osName string) string {
	if osName == "windows" {
		return "zip"
	}
	return "tar.gz"
}

func nodePlan(ver string) (Plan, error) {
	v := strings.TrimPrefix(ver, "v")
	osName := nodeArchiveOS(hostOS())
	arch := nodeArchiveArch(hostArch())
	ext := nodeArchiveExt(hostOS())
	// Node archives keep the "v" prefix: node-v20.20.2-win-x64.zip.
	archive := fmt.Sprintf("node-v%s-%s-%s.%s", v, osName, arch, ext)
	return Plan{
		Exact:  v,
		File:   archive,
		Bases:  downloadBases("node", "https://nodejs.org/dist", nodeMirrors...),
		URLFor: func(base string) string { return fmt.Sprintf("%s/v%s/%s", base, v, archive) },
		Layout: LayoutFlatten,
	}, nil
}
