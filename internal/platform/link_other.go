//go:build !windows

package platform

import "os"

// LinkDir creates a symbolic link at link pointing to target.
func LinkDir(link, target string) error {
	_ = os.Remove(link)
	return os.Symlink(target, link)
}
