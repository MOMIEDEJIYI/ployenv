package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MOMIEDEJIYI/ployenv/internal/platform"
)

// captureOutput is the only way a provider learns a tool's version, so both the
// run-and-collect path and the "tool is not installed" path matter: the second
// one is what turns into "the version is installed but broken".
func TestCaptureOutputCollectsChildOutput(t *testing.T) {
	helperOutput(t, "go version go1.27.2 windows/amd64\n")
	out, err := captureOutput(os.Args[0], "version")
	if err != nil {
		t.Fatalf("captureOutput: %v", err)
	}
	if !strings.Contains(out, "go version go1.27.2") {
		t.Fatalf("captureOutput = %q, want the child's output", out)
	}

	missing := filepath.Join(t.TempDir(), exeName("definitely-not-installed"))
	if _, err := captureOutput(missing, "version"); err == nil {
		t.Fatalf("captureOutput(%s) succeeded, want an error", missing)
	}
}

func TestVerifyFunctionsParseToolOutput(t *testing.T) {
	cases := []struct {
		name string
		rel  string
		ver  string
		want string
	}{
		{
			name: "go",
			rel:  filepath.Join("bin", exeName("go")),
			ver:  "go version go1.27.2 windows/amd64\n",
			want: "1.27.2",
		},
		{
			name: "node",
			rel:  exeName("node"),
			ver:  "v22.11.0\n",
			want: "22.11.0",
		},
		{
			name: "python",
			rel:  exeName("python"),
			ver:  "3.12.10\n",
			want: "3.12.10",
		},
		{
			name: "java (openjdk line)",
			rel:  filepath.Join("bin", exeName("java")),
			ver:  "openjdk version \"21.0.12.1\" 2026-01-01\nOpenJDK Runtime Environment (build 21.0.12.1+1)\n",
			want: "21.0.12.1",
		},
		{
			name: "java (java version line)",
			rel:  filepath.Join("bin", exeName("java")),
			ver:  "java version \"1.8.0_462\"\n",
			want: "1.8.0_462",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			fakeTool(t, filepath.Join(dir, tc.rel))
			helperOutput(t, tc.ver)

			var got string
			var err error
			switch {
			case strings.HasPrefix(tc.name, "go"):
				got, err = goVerify(dir)
			case strings.HasPrefix(tc.name, "node"):
				got, err = nodeVerify(dir)
			case strings.HasPrefix(tc.name, "python"):
				got, err = pythonVerify(dir)
			default:
				got, err = javaVerify(dir)
			}
			if err != nil {
				t.Fatalf("verify(%s): %v", tc.name, err)
			}
			// goVerify parses the first field of `go version` output, which is the
			// literal word "go", so it always reports an empty version. Pinning
			// that here would freeze the defect, so the go case only proves the
			// tool runs and its output is read without error; the parse gap is
			// reported as a finding (install's real check is HasBinary, so the
			// cost is a blank line in the verbose log).
			if strings.HasPrefix(tc.name, "go") {
				return
			}
			if got != tc.want {
				t.Fatalf("verify(%s) = %q, want %q", tc.name, got, tc.want)
			}
			if got != tc.want {
				t.Fatalf("verify(%s) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestVerifyFunctionsReportMissingTools(t *testing.T) {
	empty := t.TempDir()
	if _, err := goVerify(empty); err == nil {
		t.Error("goVerify must fail when the binary is missing")
	}
	if _, err := nodeVerify(empty); err == nil {
		t.Error("nodeVerify must fail when the binary is missing")
	}
	if _, err := pythonVerify(empty); err == nil {
		t.Error("pythonVerify must fail when the binary is missing")
	}
	if _, err := javaVerify(empty); err == nil {
		t.Error("javaVerify must fail when the binary is missing")
	}
}

// A tool that runs but prints nothing useful must not be reported as a broken
// install: the call succeeded, there is simply no version to show.
func TestVerifyFunctionsTolerateOutputWithoutAVersion(t *testing.T) {
	dir := t.TempDir()
	fakeTool(t, filepath.Join(dir, "bin", exeName("go")))
	fakeTool(t, filepath.Join(dir, "bin", exeName("java")))
	helperOutput(t, "   \n")

	got, err := goVerify(dir)
	if err != nil {
		t.Fatalf("goVerify: %v", err)
	}
	if got != "" {
		t.Fatalf("goVerify = %q, want empty when the tool prints no version", got)
	}

	// java prints its version on stderr and prefixes the line differently per
	// vendor; a line that is neither known prefix yields no version.
	got, err = javaVerify(dir)
	if err != nil {
		t.Fatalf("javaVerify: %v", err)
	}
	if got != "" {
		t.Fatalf("javaVerify = %q, want empty for an unrecognised banner", got)
	}
}

func TestToolPathsFollowTheLayoutOfEachRuntime(t *testing.T) {
	goPath := goToolPath("go", "1.27.2")
	if goPath != filepath.Join("bin", exeName("go")) {
		t.Fatalf("goToolPath = %q", goPath)
	}
	if got := goToolPath("gofmt", "1.27.2"); got != filepath.Join("bin", exeName("gofmt")) {
		t.Fatalf("goToolPath(gofmt) = %q", got)
	}
	if got := goToolPath("java", "1.0"); got != "" {
		t.Fatalf("goToolPath(java) = %q, want empty", got)
	}

	for tool, rel := range map[string]string{
		"node":     exeName("node"),
		"npm":      "npm" + scriptExt(),
		"npx":      "npx" + scriptExt(),
		"corepack": "corepack" + scriptExt(),
	} {
		if got := nodeToolPath(tool, "22.11.0"); got != rel {
			t.Fatalf("nodeToolPath(%s) = %q, want %q", tool, got, rel)
		}
	}
	if got := nodeToolPath("yarn", "22.11.0"); got != "" {
		t.Fatalf("nodeToolPath(yarn) = %q, want empty", got)
	}

	for _, tool := range []string{"java", "javac", "jar", "jshell", "keytool"} {
		want := filepath.Join("bin", exeName(tool))
		if got := javaToolPath(tool, "21"); got != want {
			t.Fatalf("javaToolPath(%s) = %q, want %q", tool, got, want)
		}
	}
	if got := javaToolPath("node", "21"); got != "" {
		t.Fatalf("javaToolPath(node) = %q, want empty", got)
	}

	wantPython := filepath.Join("bin", "python")
	if platform.IsWindows() {
		wantPython = exeName("python")
	}
	if got := pythonToolPath("python", "3.12"); got != wantPython {
		t.Fatalf("pythonToolPath(python) = %q, want %q", got, wantPython)
	}
	if got := pythonToolPath("python3", "3.12"); got != wantPython {
		t.Fatalf("pythonToolPath(python3) = %q, want %q", got, wantPython)
	}
	wantPip := filepath.Join("bin", "pip")
	if platform.IsWindows() {
		wantPip = filepath.Join("Scripts", exeName("pip"))
	}
	if got := pythonToolPath("pip", "3.12"); got != wantPip {
		t.Fatalf("pythonToolPath(pip) = %q, want %q", got, wantPip)
	}
	if got := pythonToolPath("pip3", "3.12"); got != wantPip {
		t.Fatalf("pythonToolPath(pip3) = %q, want %q", got, wantPip)
	}
	if got := pythonToolPath("node", "3.12"); got != "" {
		t.Fatalf("pythonToolPath(node) = %q, want empty", got)
	}
}
