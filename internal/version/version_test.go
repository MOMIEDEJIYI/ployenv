package version

import "testing"

func TestParseNums(t *testing.T) {
	cases := map[string]int{
		"v22.11.0":      22,
		"go1.24.1":      0, // "go1" is not numeric-prefixed, so nothing parses
		"1.24.1":        1,
		"21.0.2+13":     21,
		"3.12.8":        3,
		"22.11.0-rc.1":  22,
		"jdk-21.0.12.1": 0,
	}
	for in, want := range cases {
		got := ParseNums(in)
		if want == 0 {
			if len(got) != 0 {
				t.Errorf("ParseNums(%q) = %v, want empty", in, got)
			}
			continue
		}
		if len(got) == 0 || got[0] != want {
			t.Errorf("ParseNums(%q) = %v, want first %d", in, got, want)
		}
	}
}

func TestCompare(t *testing.T) {
	if Compare("22.11.0", "22.9.0") <= 0 {
		t.Error("22.11.0 should be greater than 22.9.0 (numeric, not lexical)")
	}
	if Compare("21.0.2+13", "21.0.2") != 0 {
		t.Error("build metadata should not affect comparison")
	}
	if Compare("1.0", "1.0.0") != 0 {
		t.Error("missing components should be treated as zero")
	}
}

func TestMatches(t *testing.T) {
	yes := [][2]string{
		{"22", "22.11.0"},
		{"22.11", "22.11.0"},
		{"v22.11.0", "22.11.0"},
		{"3.12", "3.12.8"},
		{"21", "21.0.2+13"},
	}
	for _, c := range yes {
		if !Matches(c[0], c[1]) {
			t.Errorf("Matches(%q,%q) = false, want true", c[0], c[1])
		}
	}
	if Matches("22", "21.0.0") {
		t.Error(`Matches("22","21.0.0") should be false`)
	}
	if Matches("2", "22.0.0") {
		t.Error(`Matches("2","22.0.0") should be false (must be a component prefix)`)
	}
}

func TestHighest(t *testing.T) {
	got := Highest([]string{"1.2.0", "1.10.0", "1.9.0"})
	if got != "1.10.0" {
		t.Errorf("Highest = %q, want 1.10.0", got)
	}
}

func TestIsExact(t *testing.T) {
	if !IsExact("22.11.0") {
		t.Error("22.11.0 should be exact")
	}
	if IsExact("22") || IsExact("22.11") {
		t.Error("partial versions should not be exact")
	}
}
