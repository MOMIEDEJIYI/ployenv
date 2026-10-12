package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestMain gives this binary a second role: with PLOYENV_TEST_MAIN_ARGS set it
// behaves like the installed command (main() ends in os.Exit, so it cannot be
// called inside a test process without ending the test run).
func TestMain(m *testing.M) {
	if args := os.Getenv("PLOYENV_TEST_MAIN_ARGS"); args != "" {
		os.Args = append([]string{"ployenv"}, strings.Fields(args)...)
		main() // never returns
	}
	os.Exit(m.Run())
}

// runMain re-executes this binary as the command does, and returns its streams
// and exit code.
func runMain(t *testing.T, args string) (stdout, stderr string, code int) {
	t.Helper()
	root := t.TempDir() // keep the real data directory out of it
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "PLOYENV_TEST_MAIN_ARGS="+args, "PLOYENV_ROOT="+root)

	var out, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errBuf
	err := cmd.Run()
	code = 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("running the command: %v", err)
	}
	return out.String(), errBuf.String(), code
}

// The point of this test is the wiring in main: it must hand os.Args[1:] to the
// CLI and exit with what it returned. Everything else is covered in internal/cli.
func TestMainEntryPointDelegatesToTheCLI(t *testing.T) {
	stdout, stderr, code := runMain(t, "--version")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "ployenv ") {
		t.Errorf("stdout = %q, want the version line", stdout)
	}
	if !strings.Contains(stdout, "(") || !strings.Contains(stdout, "/") {
		t.Errorf("stdout = %q, want the platform in parentheses", stdout)
	}
}

func TestMainEntryPointPropagatesExitCodes(t *testing.T) {
	stdout, stderr, code := runMain(t, "definitely-not-a-command")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("an unknown command must not write to stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "unknown command") {
		t.Errorf("stderr = %q, want the diagnostic", stderr)
	}
}

// Arguments are passed through untouched, including the ones that look like
// ployenv's own flags.
func TestMainEntryPointPassesArgumentsThrough(t *testing.T) {
	stdout, _, code := runMain(t, "help")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "usage: ployenv <command>") {
		t.Errorf("stdout = %q, want the help text", stdout)
	}
}
