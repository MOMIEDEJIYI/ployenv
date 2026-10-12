package fsutil

import (
	"runtime"
	"testing"
)

// BUG-011: SamePath folded case everywhere, so on Linux "/home/A" and "/home/a"
// were treated as the same directory.
func TestSamePathFollowsPlatformCaseRules(t *testing.T) {
	if !SamePath(`dir/a`, `dir/./a`) {
		t.Error("equivalent paths must compare equal")
	}

	folded := SamePath("/home/A", "/home/a")
	if runtime.GOOS == "windows" {
		if !folded {
			t.Error("Windows path comparison folds case")
		}
		return
	}
	if folded {
		t.Error("the Unix-likes are case-sensitive")
	}
}
