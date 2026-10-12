package jsonx

import (
	"encoding/json"
	"strings"
	"testing"
)

// BUG-005: the old escaper emitted \U%08x for astral runes, which is not a JSON
// escape, so a path containing an emoji or a CJK extension B character produced
// a document no parser could read.
func TestEncodeIsValidPureASCIIJSON(t *testing.T) {
	const sample = "C:\\\u4e2d\u6587\\\U0001F600\\\U00020000"
	out, err := Encode(map[string]any{"path": []string{sample}})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !json.Valid([]byte(out)) {
		t.Fatalf("output is not valid JSON:\n%s", out)
	}
	for i := 0; i < len(out); i++ {
		if out[i] > 0x7F {
			t.Fatalf("byte %#x at offset %d is not ASCII:\n%s", out[i], i, out)
		}
	}
	if strings.Contains(out, `\U`) {
		t.Fatalf("output uses \\U, which JSON does not define:\n%s", out)
	}

	var back map[string][]string
	if err := json.Unmarshal([]byte(out), &back); err != nil {
		t.Fatalf("round trip failed: %v", err)
	}
	if got := back["path"][0]; got != sample {
		t.Fatalf("round trip changed the value:\n got %q\nwant %q", got, sample)
	}
}

func TestEscapeNonASCII(t *testing.T) {
	tests := map[string]string{
		"plain":            "plain",
		"\u4e2d":           `\u4e2d`,
		"\U0001F600":       `\ud83d\ude00`,
		"a\u00e9b":         `a\u00e9b`,
		"line\nbreak":      "line\nbreak",
		`already \u4e2d`:   `already \u4e2d`,
		"quote\"and<amp>&": "quote\"and<amp>&",
	}
	for in, want := range tests {
		if got := EscapeNonASCII(in); got != want {
			t.Errorf("EscapeNonASCII(%q) = %q, want %q", in, got, want)
		}
	}
}
