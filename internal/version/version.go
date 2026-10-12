// Package version parses and compares dotted version strings.
package version

import (
	"sort"
	"strconv"
	"strings"
)

// channelWords are requests that are resolved against a version index or the
// set of installed versions rather than parsed as a concrete number.
var channelWords = map[string]bool{
	"": true, "all": true, "latest": true, "lts": true, "stable": true,
}

// IsChannel reports whether s is a channel keyword rather than a concrete
// version request. Runtimes must never treat a channel as "no filter".
func IsChannel(s string) bool {
	return channelWords[strings.ToLower(strings.TrimSpace(s))]
}

// ParseNums extracts the leading numeric components of a version, ignoring any
// prefix (v/go/jdk-) and any pre-release/build suffix (-rc1, +20241206).
func ParseNums(s string) []int {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimPrefix(s, "V")
	core := s
	if i := strings.IndexAny(core, "-+"); i >= 0 {
		core = core[:i]
	}
	parts := strings.Split(core, ".")
	nums := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			break
		}
		nums = append(nums, n)
	}
	return nums
}

// Compare returns -1, 0 or 1 for a<b, a==b, a>b.
func Compare(a, b string) int {
	an, bn := ParseNums(a), ParseNums(b)
	n := len(an)
	if len(bn) > n {
		n = len(bn)
	}
	for i := 0; i < n; i++ {
		av, bv := 0, 0
		if i < len(an) {
			av = an[i]
		}
		if i < len(bn) {
			bv = bn[i]
		}
		if av != bv {
			if av < bv {
				return -1
			}
			return 1
		}
	}
	return 0
}

// SortAsc sorts versions ascending in place.
func SortAsc(vs []string) {
	sort.Slice(vs, func(i, j int) bool { return Compare(vs[i], vs[j]) < 0 })
}

// Highest returns the greatest version, or "" for an empty slice.
func Highest(vs []string) string {
	if len(vs) == 0 {
		return ""
	}
	SortAsc(vs)
	return vs[len(vs)-1]
}

// Matches reports whether candidate satisfies a request such as
// "22", "22.11", "v22.11.0" or "1.24".
func Matches(requested, candidate string) bool {
	r := strings.ToLower(strings.TrimPrefix(requested, "v"))
	c := strings.ToLower(strings.TrimPrefix(candidate, "v"))
	if r == "" {
		return false
	}
	if r == c {
		return true
	}
	return strings.HasPrefix(c, r+".")
}

// IsExact reports whether v looks like a full x.y.z version.
func IsExact(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) < 3 {
		return false
	}
	for _, p := range parts {
		// Counting components is not enough: "node.js.x" has three of them.
		if _, err := strconv.Atoi(p); err != nil {
			return false
		}
	}
	return true
}

// FirstComponent returns the leading numeric component ("21" from "21.0.1").
func FirstComponent(v string) string {
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".")
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}
