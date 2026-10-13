package runtime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MOMIEDEJIYI/ployenv/internal/platform"
)

// The product claims three platforms, so every platform mapping is asserted for
// all of them here: a runner only executes one, which is how the Unix layouts
// stayed broken while Windows was green.
var allOSes = []string{"windows", "linux", "darwin"}

func TestExeAndScriptNamesPerPlatform(t *testing.T) {
	// The pure forms must agree with the platform package for the host, so the
	// seam cannot silently drift from what the rest of the program uses.
	if got, want := exeFor(platform.OS(), "go"), platform.ExeName("go"); got != want {
		t.Errorf("exeFor(host, go) = %q, want platform.ExeName = %q", got, want)
	}
	if got, want := scriptFor(platform.OS(), "npm"), "npm"+platform.ScriptExt(); got != want {
		t.Errorf("scriptFor(host, npm) = %q, want npm+platform.ScriptExt = %q", got, want)
	}

	for _, tc := range []struct {
		osName string
		exe    string
		script string
		dir    string
	}{
		{"windows", "go.exe", "npm.cmd", ""},
		{"linux", "go", "npm", "bin"},
		{"darwin", "go", "npm", "bin"},
	} {
		if got := exeFor(tc.osName, "go"); got != tc.exe {
			t.Errorf("exeFor(%s, go) = %q, want %q", tc.osName, got, tc.exe)
		}
		if got := scriptFor(tc.osName, "npm"); got != tc.script {
			t.Errorf("scriptFor(%s, npm) = %q, want %q", tc.osName, got, tc.script)
		}
		if got := toolDirFor(tc.osName); got != tc.dir {
			t.Errorf("toolDirFor(%s) = %q, want %q", tc.osName, got, tc.dir)
		}
	}
}

func TestNodeToolPathsPerPlatform(t *testing.T) {
	for _, osName := range allOSes {
		dir := toolDirFor(osName)
		want := map[string]string{
			"node":     filepath.Join(dir, exeFor(osName, "node")),
			"npm":      filepath.Join(dir, scriptFor(osName, "npm")),
			"npx":      filepath.Join(dir, scriptFor(osName, "npx")),
			"corepack": filepath.Join(dir, scriptFor(osName, "corepack")),
		}
		for tool, w := range want {
			if got := nodeToolPathFor(osName, tool); got != w {
				t.Errorf("nodeToolPathFor(%s, %s) = %q, want %q", osName, tool, got, w)
			}
		}
		if got := nodeToolPathFor(osName, "yarn"); got != "" {
			t.Errorf("nodeToolPathFor(%s, yarn) = %q, want empty", osName, got)
		}
	}
}

func TestPythonToolPathsPerPlatform(t *testing.T) {
	for _, osName := range allOSes {
		dir := toolDirFor(osName)
		wantPython := filepath.Join(dir, exeFor(osName, "python"))
		for _, tool := range []string{"python", "python3"} {
			if got := pythonToolPathFor(osName, tool); got != wantPython {
				t.Errorf("pythonToolPathFor(%s, %s) = %q, want %q", osName, tool, got, wantPython)
			}
		}
		wantPip := filepath.Join(dir, exeFor(osName, "pip"))
		if osName == "windows" {
			// NuGet keeps the pip shims in Scripts/.
			wantPip = filepath.Join("Scripts", exeFor(osName, "pip"))
		}
		for _, tool := range []string{"pip", "pip3"} {
			if got := pythonToolPathFor(osName, tool); got != wantPip {
				t.Errorf("pythonToolPathFor(%s, %s) = %q, want %q", osName, tool, got, wantPip)
			}
		}
		if got := pythonToolPathFor(osName, "node"); got != "" {
			t.Errorf("pythonToolPathFor(%s, node) = %q, want empty", osName, got)
		}
	}
}

// Go and Java are laid out the same way on every platform (the Go zip and the
// Adoptium archives both use bin/), so their tools must not move with the OS.
func TestGoAndJavaToolPathsIgnoreThePlatform(t *testing.T) {
	for _, osName := range allOSes {
		withOS(t, osName)
		if got, want := goToolPath("go", "1.27.2"), filepath.Join("bin", exeName("go")); got != want {
			t.Errorf("on %s goToolPath = %q, want %q", osName, got, want)
		}
		if got, want := javaToolPath("java", "21"), filepath.Join("bin", exeName("java")); got != want {
			t.Errorf("on %s javaToolPath = %q, want %q", osName, got, want)
		}
	}
}

// The archive names are chosen per platform too, so the whole table is asserted
// here rather than only the runner's own row.
func TestNodeArchiveNamingPerPlatform(t *testing.T) {
	cases := []struct {
		osName, arch string
		want         string
	}{
		{"windows", "amd64", "node-v22.11.0-win-x64.zip"},
		{"windows", "arm64", "node-v22.11.0-win-arm64.zip"},
		{"windows", "386", "node-v22.11.0-win-x86.zip"},
		{"linux", "amd64", "node-v22.11.0-linux-x64.tar.gz"},
		{"linux", "arm64", "node-v22.11.0-linux-arm64.tar.gz"},
		{"darwin", "amd64", "node-v22.11.0-darwin-x64.tar.gz"},
		{"darwin", "arm64", "node-v22.11.0-darwin-arm64.tar.gz"},
	}
	for _, tc := range cases {
		withOS(t, tc.osName)
		withArch(t, tc.arch)
		plan, err := nodePlan("v22.11.0")
		if err != nil {
			t.Fatalf("nodePlan on %s/%s: %v", tc.osName, tc.arch, err)
		}
		if plan.File != tc.want {
			t.Errorf("nodePlan on %s/%s picked %q, want %q", tc.osName, tc.arch, plan.File, tc.want)
		}
	}
}

func TestPythonAssetSuffixPerPlatform(t *testing.T) {
	cases := []struct {
		osName, arch string
		want         string
	}{
		{"windows", "amd64", "x86_64-pc-windows-msvc-install_only.tar.gz"},
		{"windows", "arm64", "aarch64-pc-windows-msvc-install_only.tar.gz"},
		{"darwin", "arm64", "aarch64-apple-darwin-install_only.tar.gz"},
		{"darwin", "amd64", "x86_64-apple-darwin-install_only.tar.gz"},
		{"linux", "arm64", "aarch64-unknown-linux-gnu-install_only.tar.gz"},
		{"linux", "amd64", "x86_64-unknown-linux-gnu-install_only.tar.gz"},
		// Anything else falls back to the generic Linux x86_64 build.
		{"linux", "386", "x86_64-unknown-linux-gnu-install_only.tar.gz"},
		{"freebsd", "amd64", "x86_64-unknown-linux-gnu-install_only.tar.gz"},
	}
	for _, tc := range cases {
		withOS(t, tc.osName)
		withArch(t, tc.arch)
		if got := pythonAssetSuffix(); got != tc.want {
			t.Errorf("pythonAssetSuffix on %s/%s = %q, want %q", tc.osName, tc.arch, got, tc.want)
		}
	}
}

func TestAdoptiumNamesPerPlatform(t *testing.T) {
	cases := []struct {
		osName, arch string
		wantOS       string
		wantArch     string
	}{
		{"windows", "amd64", "windows", "x64"},
		{"windows", "arm64", "windows", "aarch64"},
		{"darwin", "amd64", "mac", "x64"},
		{"darwin", "arm64", "mac", "aarch64"},
		{"linux", "amd64", "linux", "x64"},
		{"linux", "arm64", "linux", "aarch64"},
		// An architecture Adoptium does not rename keeps its own name.
		{"linux", "386", "linux", "386"},
		// An OS the provider does not know is treated as Linux.
		{"freebsd", "amd64", "linux", "x64"},
	}
	for _, tc := range cases {
		withOS(t, tc.osName)
		withArch(t, tc.arch)
		if got := adoptiumOS(); got != tc.wantOS {
			t.Errorf("adoptiumOS on %s = %q, want %q", tc.osName, got, tc.wantOS)
		}
		if got := adoptiumArch(); got != tc.wantArch {
			t.Errorf("adoptiumArch on %s/%s = %q, want %q", tc.osName, tc.arch, got, tc.wantArch)
		}
	}
}

// A1 / REQ-008: Adoptium's darwin archives are bundles (Contents/Home), every
// other platform ships a flat tree, so the layout rule must select per OS. The
// pure mapping is asserted for all three so the darwin branch is covered from
// windows-latest and ubuntu-latest too, and the host accessor is checked to
// follow the seam.
func TestJavaLayoutPerPlatform(t *testing.T) {
	for _, tc := range []struct {
		osName string
		want   Layout
	}{
		{"darwin", LayoutMacJDK},
		{"windows", LayoutFlatten},
		{"linux", LayoutFlatten},
	} {
		if got := javaLayoutFor(tc.osName); got != tc.want {
			t.Errorf("javaLayoutFor(%s) = %v, want %v", tc.osName, got, tc.want)
		}
	}
	for _, osName := range allOSes {
		withOS(t, osName)
		if got, want := javaLayout(), javaLayoutFor(osName); got != want {
			t.Errorf("javaLayout on %s = %v, want %v", osName, got, want)
		}
	}
}

// python's upstream index differs per platform (NuGet on Windows, the
// python-build-standalone releases elsewhere), so both are asserted.
func TestPythonOfficialPerPlatform(t *testing.T) {
	for _, tc := range []struct {
		osName string
		want   string
	}{
		{"windows", pythonNugetBase},
		{"linux", pythonReleasesURL},
		{"darwin", pythonReleasesURL},
	} {
		withOS(t, tc.osName)
		if got := pythonOfficial(); got != tc.want {
			t.Errorf("pythonOfficial on %s = %q, want %q", tc.osName, got, tc.want)
		}
	}
}

// The host accessors must follow the seam, otherwise the pure functions above
// would be tested while the code path the product actually runs is not.
func TestHostToolPathsFollowTheSeam(t *testing.T) {
	for _, osName := range allOSes {
		withOS(t, osName)
		for _, tool := range append(append([]string{}, nodeSpec.ExeTools...), nodeSpec.CmdTools...) {
			if got, want := nodeToolPath(tool, "22.11.0"), nodeToolPathFor(osName, tool); got != want {
				t.Errorf("nodeToolPath(%s) = %q, want %q", tool, got, want)
			}
		}
		for _, tool := range append(append([]string{}, pythonSpec.ExeTools...), pythonSpec.CmdTools...) {
			if got, want := pythonToolPath(tool, "3.12.10"), pythonToolPathFor(osName, tool); got != want {
				t.Errorf("pythonToolPath(%s) = %q, want %q", tool, got, want)
			}
		}
		if got, want := pythonSpec.PathEntries("/v"), pythonPathDirsFor(osName, "/v"); !equalStrings(got, want) {
			t.Errorf("python PathEntries on %s = %v, want %v", osName, got, want)
		}
		if got, want := nodeSpec.PathEntries("/v"), nodePathDirsFor(osName, "/v"); !equalStrings(got, want) {
			t.Errorf("node PathEntries on %s = %v, want %v", osName, got, want)
		}
	}
}

func TestPathDirsPerPlatform(t *testing.T) {
	dir := filepath.Join("root", "versions", "node", "22.11.0")
	for _, tc := range []struct {
		osName string
		want   string
	}{
		{"windows", dir},
		{"linux", filepath.Join(dir, "bin")},
		{"darwin", filepath.Join(dir, "bin")},
	} {
		got := nodePathDirsFor(tc.osName, dir)
		if len(got) != 1 || got[0] != tc.want {
			t.Errorf("nodePathDirsFor(%s) = %v, want [%s]", tc.osName, got, tc.want)
		}
	}

	// python's Windows install adds Scripts/ only when it exists (I-09).
	pdir := t.TempDir()
	for _, osName := range []string{"linux", "darwin"} {
		want := filepath.Join(pdir, "bin")
		if got := pythonPathDirsFor(osName, pdir); len(got) != 1 || got[0] != want {
			t.Errorf("pythonPathDirsFor(%s) = %v, want [%s]", osName, got, want)
		}
	}
	if got := pythonPathDirsFor("windows", pdir); len(got) != 1 || got[0] != pdir {
		t.Errorf("pythonPathDirsFor(windows) without Scripts = %v, want [%s]", got, pdir)
	}
	scripts := filepath.Join(pdir, "Scripts")
	if err := os.MkdirAll(scripts, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if got := pythonPathDirsFor("windows", pdir); len(got) != 2 || got[0] != pdir || got[1] != scripts {
		t.Errorf("pythonPathDirsFor(windows) = %v, want [%s %s]", got, pdir, scripts)
	}
}

// The consequence that made this a blocker: the Unix archives keep the tools in
// bin/, so a version directory that only has bin/<tool> is installed. Looking at
// the version root reported every such install as broken and install rolled it
// back.
func TestHasBinaryFollowsThePlatformLayout(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	for _, tool := range []string{"python", "node"} {
		if err := os.WriteFile(filepath.Join(dir, "bin", tool), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	withOS(t, "linux")
	if !pythonSpec.HasBinary(dir, "3.12.10") {
		t.Error("bin/python is the Unix layout: HasBinary must find it")
	}
	if !nodeSpec.HasBinary(dir, "22.11.0") {
		t.Error("bin/node is the Unix layout: HasBinary must find it")
	}

	withOS(t, "windows")
	if pythonSpec.HasBinary(dir, "3.12.10") {
		t.Error("the Windows layout keeps python.exe at the version root, not in bin/")
	}
	if nodeSpec.HasBinary(dir, "22.11.0") {
		t.Error("the Windows layout keeps node.exe at the version root, not in bin/")
	}
}

// pythonVerify/nodeVerify must resolve the tool the same way the layout does,
// otherwise the post-install self-check fails on the Unix-likes even though the
// install is fine. Only the host's own layout can be executed, so that is what
// this runs; the other one is covered by the path assertions above and by the
// ubuntu runner in CI.
func TestVerifyFindsTheToolWhereTheLayoutPutsIt(t *testing.T) {
	host := platform.OS()
	cases := []struct {
		name string
		rel  string
		out  string
		want string
		run  func(string) (string, error)
	}{
		{"python", pythonToolPathFor(host, "python"), "3.12.10\n", "3.12.10", pythonVerify},
		{"node", nodeToolPathFor(host, "node"), "v22.11.0\n", "22.11.0", nodeVerify},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			fakeTool(t, filepath.Join(dir, tc.rel))
			helperOutput(t, tc.out)
			got, err := tc.run(dir)
			if err != nil {
				t.Fatalf("%sVerify: %v (looked for %s)", tc.name, err, tc.rel)
			}
			if got != tc.want {
				t.Fatalf("%sVerify = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}
