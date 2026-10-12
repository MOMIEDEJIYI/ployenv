package install

import "testing"

// BUG-009: adopt used to fall back to the whole directory name, registering
// "nodejs" or "jdk8u504-b01" as a version.
func TestInferVersion(t *testing.T) {
	tests := map[string]string{
		"node-v20.20.2-win-x64":       "20.20.2",
		"static-go-1.27.1":            "1.27.1",
		"jdk-21.0.2+13":               "21.0.2",
		"20.20.2":                     "20.20.2",
		"v1.2.3":                      "1.2.3",
		"go1.27.2":                    "1.27.2",
		"nodejs":                      "",
		"jdk8u504-b01":                "",
		"python":                      "",
		"openjdk-21":                  "",
		"":                            "",
		"some-random-directory-name":  "",
		"cpython-3.12.9+20250101-x64": "3.12.9",
	}
	for base, want := range tests {
		if got := inferVersion(base); got != want {
			t.Errorf("inferVersion(%q) = %q, want %q", base, got, want)
		}
	}
}

func TestLooksLikeVersion(t *testing.T) {
	for _, s := range []string{"1.27", "20.20.2", "0.1"} {
		if !looksLikeVersion(s) {
			t.Errorf("looksLikeVersion(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "1", "node", "node.js.x", "1.2.x", "v1.2.3", "1..2"} {
		if looksLikeVersion(s) {
			t.Errorf("looksLikeVersion(%q) = true, want false", s)
		}
	}
}
