package runtime

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/MOMIEDEJIYI/ployenv/internal/netx"
	"github.com/MOMIEDEJIYI/ployenv/internal/version"
)

var goSpec = &Spec{
	Name:     "go",
	Title:    "Go",
	Kind:     "language",
	Aliases:  []string{"golang"},
	Official: func() string { return "https://go.dev/dl" },
	ExeTools: []string{"go", "gofmt"},
	Primary:  "go",
	EnvVar:   "GOROOT",
	ToolPath: goToolPath,
	PathDirs: func(dir string) []string { return []string{filepath.Join(dir, "bin")} },
	Verify:   goVerify,
	List:     goList,
	Plan:     goPlan,
}

var goMirrors = []string{
	"https://golang.google.cn/dl",
	"https://mirrors.aliyun.com/golang",
	"https://mirrors.ustc.edu.cn/golang",
}

type goRelease struct {
	Version string `json:"version"`
	Stable  bool   `json:"stable"`
	Files   []struct {
		Filename string `json:"filename"`
		OS       string `json:"os"`
		Arch     string `json:"arch"`
		Kind     string `json:"kind"`
		SHA256   string `json:"sha256"`
	} `json:"files"`
}

func goToolPath(tool, _ string) string {
	switch tool {
	case "go", "gofmt":
		return filepath.Join("bin", exeName(tool))
	}
	return ""
}

func goVerify(dir string) (string, error) {
	out, err := captureOutput(filepath.Join(dir, goToolPath("go", "")), "version")
	if err != nil {
		return "", err
	}
	// `go version` prints "go version go1.27.2 windows/amd64", so the version is
	// the field that starts with "go" and continues with a digit; the first field
	// is always the literal word "go" (BUG-023). A devel toolchain prints
	// "devel go1.28-abc123" first, which the same rule picks up.
	for _, field := range strings.Fields(out) {
		if !strings.HasPrefix(field, "go") || len(field) < 3 {
			continue
		}
		if field[2] < '0' || field[2] > '9' {
			continue
		}
		return strings.TrimPrefix(field, "go"), nil
	}
	return "", nil
}

func goIndex() ([]goRelease, error) {
	var rels []goRelease
	var lastErr error
	for _, base := range downloadBases("go", "https://go.dev/dl", goMirrors...) {
		if err := netx.GetJSONRetry(base+"/?mode=json&include=all", &rels); err != nil {
			lastErr = err
			continue
		}
		return rels, nil
	}
	return nil, lastErr
}

func goList(filter string) ([]Release, error) {
	rels, err := goIndex()
	if err != nil {
		return nil, err
	}
	f := strings.ToLower(strings.TrimSpace(filter))
	var out []Release
	for _, r := range rels {
		ver := strings.TrimPrefix(r.Version, "go")
		if f == "stable" && !r.Stable {
			continue
		}
		if f != "" && f != "all" && f != "latest" && f != "stable" && !version.Matches(f, ver) {
			continue
		}
		note := ""
		if !r.Stable {
			note = "preview"
		}
		out = append(out, Release{Version: ver, Note: note})
	}
	return out, nil
}

func goPlan(ver string) (Plan, error) {
	want := strings.TrimPrefix(strings.TrimPrefix(ver, "v"), "go")
	rels, err := goIndex()
	if err != nil {
		return Plan{}, err
	}
	for _, r := range rels {
		if strings.TrimPrefix(r.Version, "go") != want {
			continue
		}
		for _, f := range r.Files {
			if f.Kind == "archive" && f.OS == hostOS() && f.Arch == hostArch() {
				name := f.Filename
				return Plan{
					Exact:  want,
					File:   name,
					Bases:  downloadBases("go", "https://go.dev/dl", goMirrors...),
					URLFor: func(base string) string { return base + "/" + name },
					SHA256: f.SHA256,
					Layout: LayoutFlatten,
				}, nil
			}
		}
	}
	return Plan{}, fmt.Errorf("no Go %s archive for this platform (%s/%s)", want, hostOS(), hostArch())
}
