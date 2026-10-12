//go:build !windows

package platform

// UseUTF8Console is a no-op off Windows: terminals are UTF-8 already.
func UseUTF8Console() func() { return func() {} }
