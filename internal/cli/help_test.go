package cli

import (
	"strings"
	"testing"

	"github.com/MOMIEDEJIYI/ployenv/internal/platform"
)

// A7 / REQ-008: the help must lead with the shells this platform actually has,
// and must not offer a recipe for a shell that is not there. helpTextFor takes
// the platform as an argument so both spellings are asserted from any runner.
func TestHelpTextPerPlatform(t *testing.T) {
	windows := helpTextFor(true)
	if !strings.Contains(windows, "pwsh|cmd|bash|json") {
		t.Errorf("the Windows help must list the Windows shells:\n%s", windows)
	}
	if strings.Contains(windows, "{{shells}}") {
		t.Error("the shell placeholder must be substituted")
	}

	unix := helpTextFor(false)
	if !strings.Contains(unix, "bash|zsh|sh|json") {
		t.Errorf("the POSIX help must list the POSIX shells:\n%s", unix)
	}
	if strings.Contains(unix, "pwsh") {
		t.Errorf("the POSIX help must not lead with PowerShell:\n%s", unix)
	}
	if strings.Contains(unix, "{{shells}}") {
		t.Error("the shell placeholder must be substituted")
	}

	// The host accessor must agree with the branch the product actually takes.
	if got, want := helpText(), helpTextFor(platform.IsWindows()); got != want {
		t.Errorf("helpText() must equal helpTextFor(%v)", platform.IsWindows())
	}
	if platform.IsWindows() != strings.Contains(helpText(), "pwsh|cmd|bash|json") {
		t.Errorf("on %s the help lists the wrong shells", platform.OS())
	}
}
