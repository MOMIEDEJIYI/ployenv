package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureStderr runs f with os.Stderr redirected and returns everything written.
func captureStderr(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()

	f()

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stderr = old
	return <-done
}

// noJava clears the two places mvn looks for a JVM: JAVA_HOME and PATH.
func noJava(t *testing.T) {
	t.Helper()
	t.Setenv("JAVA_HOME", "")
	t.Setenv("PATH", filepath.Join(t.TempDir(), "empty"))
}

// REQ-009: maven is the only runtime whose tools need something ployenv does not
// install, and mvn's own failure message never says what to do about it.
func TestDoctorHintsWhenMavenHasNoJDK(t *testing.T) {
	resetSandbox(t)
	noJava(t)
	makeVersion(t, "maven", "3.10.0")
	writeSandboxConfig(t, `{"defaults":{"maven":"3.10.0"}}`)

	var out, errOut string
	code := 0
	errOut = captureStderr(t, func() {
		out = captureStdout(t, func() { code = Run([]string{"doctor"}) })
	})
	if code != 0 {
		t.Fatalf("doctor exited with %d", code)
	}
	if !strings.Contains(errOut, "maven needs a JDK") {
		t.Errorf("stderr = %q, want a JDK hint", errOut)
	}
	if strings.Contains(out, "maven needs a JDK") {
		t.Errorf("the hint must not pollute stdout:\n%s", out)
	}
}

func TestDoctorStaysQuietWhenJavaIsConfigured(t *testing.T) {
	resetSandbox(t)
	noJava(t)
	javaDir := makeVersion(t, "java", "21.0.0")
	makeVersion(t, "maven", "3.10.0")
	writeSandboxConfig(t, `{"defaults":{"java":"21.0.0","maven":"3.10.0"}}`)

	errOut := captureStderr(t, func() {
		captureStdout(t, func() { Run([]string{"doctor"}) })
	})
	if strings.Contains(errOut, "maven needs a JDK") {
		t.Errorf("stderr = %q: a managed java satisfies maven, so there is nothing to hint at", errOut)
	}
	// The version directory is what matters, not the version string.
	if _, err := os.Stat(filepath.Join(javaDir, "bin")); err != nil {
		t.Fatalf("fixture: %v", err)
	}
}
