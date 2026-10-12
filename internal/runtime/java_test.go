package runtime

import "testing"

// BUG-003: an unknown filter used to fall through to the unfiltered list, so
// `available java foo` printed the newest JDK releases and `install java@foo`
// downloaded jdk-27. A filter that cannot match must match nothing - and must
// do so without touching the network.
func TestJavaListUnknownFilterMatchesNothing(t *testing.T) {
	for _, filter := range []string{"foo", "not-a-version", "abc21", "x"} {
		rels, err := javaList(filter)
		if err != nil {
			t.Fatalf("javaList(%q) returned an error: %v", filter, err)
		}
		if len(rels) != 0 {
			t.Fatalf("javaList(%q) returned %d releases, want none", filter, len(rels))
		}
	}
}

func TestJavaFeatureFilter(t *testing.T) {
	tests := map[string]string{
		"21":      "21",
		"21.0.2":  "21",
		"jdk-21":  "21",
		"v17":     "17",
		"8":       "8",
		"foo":     "",
		"abc":     "",
		"abc21":   "",
		"21abc":   "",
		"":        "",
		"latest":  "",
		"all":     "",
		"lts":     "",
		"stable":  "",
		"21-beta": "",
	}
	for in, want := range tests {
		if got := javaFeatureFilter(in); got != want {
			t.Errorf("javaFeatureFilter(%q) = %q, want %q", in, got, want)
		}
	}
}
