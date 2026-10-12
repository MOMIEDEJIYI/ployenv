// Package jsonx is ployenv's single JSON emitter. Every --json output goes
// through it, so the machine interface is one thing to reason about and stays
// valid, pure-ASCII JSON on any console code page (FR-18).
package jsonx

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf16"
)

// Encode renders v as indented JSON with every non-ASCII rune escaped.
func Encode(v any) (string, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return EscapeNonASCII(string(data)), nil
}

// EscapeNonASCII rewrites every non-ASCII rune as a \uXXXX escape.
//
// Runes outside the BMP become a UTF-16 surrogate pair (\uD83D\uDE00).
// JSON has no \U escape, so emitting one would produce a document no parser
// can read.
func EscapeNonASCII(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 16)
	for _, r := range s {
		switch {
		case r < 0x80, r == '\n', r == '\t', r == '\r':
			b.WriteRune(r)
		case r > 0xFFFF:
			hi, lo := utf16.EncodeRune(r)
			fmt.Fprintf(&b, "\\u%04x\\u%04x", hi, lo)
		default:
			fmt.Fprintf(&b, "\\u%04x", r)
		}
	}
	return b.String()
}
