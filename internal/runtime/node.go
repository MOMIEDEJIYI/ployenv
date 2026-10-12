package runtime

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/MOMIEDEJIYI/ployenv/internal/netx"
	"github.com/MOMIEDEJIYI/ployenv/internal/platform"
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

func nodeToolPath(tool, _ string) string {
	switch tool {
	case "node":
		return exeName("node")
	case "npm":
		return "npm" + scriptExt()
	case "npx":
		return "npx" + scriptExt()
	case "corepack":
		return "corepack" + scriptExt()
	}
	return ""
}

func nodeVerify(dir string) (string, error) {
	out, err := captureOutput(filepath.Join(dir, exeName("node")), "--version")
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

func nodePlan(ver string) (Plan, error) {
	v := strings.TrimPrefix(ver, "v")
	osName := map[string]string{"windows": "win", "darwin": "darwin", "linux": "linux"}[platform.OS()]
	arch := map[string]string{"amd64": "x64", "arm64": "arm64", "386": "x86"}[platform.Arch()]
	ext := "tar.gz"
	if platform.IsWindows() {
		ext = "zip"
	}
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
