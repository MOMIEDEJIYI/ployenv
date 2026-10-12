//go:build windows

package platform

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unicode/utf16"
)

// Win32 constants for mount-point reparse points (junctions). A junction needs
// neither administrator rights nor developer mode, unlike a symbolic link.
const (
	fsctlSetReparsePoint     = 0x000900A4
	ioReparseTagMountPoint   = 0xA0000003
	genericWrite             = 0x40000000
	openExisting             = 3
	fileFlagBackupSemantics  = 0x02000000
	fileFlagOpenReparsePoint = 0x00200000

	// Where the name buffer starts inside a mount-point reparse buffer: the
	// 8-byte generic header plus the four uint16 offset/length fields.
	reparseNamesOffset = 16
)

// LinkDir makes link a junction pointing at target.
//
// The reparse point is attached in-process through DeviceIoControl instead of
// running `cmd /c mklink /J`. Passing a pre-built command string to cmd meant
// Go re-quoted the argument list, which mangled the paths and made every
// junction fail; doing it here also means no child process (and no timeout to
// police).
func LinkDir(link, target string) error {
	absLink, err := filepath.Abs(link)
	if err != nil {
		return err
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	absTarget = filepath.Clean(absTarget)

	st, err := os.Stat(absTarget)
	if err != nil {
		return fmt.Errorf("the junction target does not exist: %s", absTarget)
	}
	if !st.IsDir() {
		return fmt.Errorf("the junction target is not a directory: %s", absTarget)
	}

	if err := prepareLinkPath(absLink); err != nil {
		return err
	}
	return setMountPoint(absLink, absTarget)
}

// prepareLinkPath makes sure link is an empty directory, ready to carry a
// reparse point. Anything we did not create ourselves is left alone.
func prepareLinkPath(link string) error {
	st, err := os.Lstat(link)
	switch {
	case os.IsNotExist(err):
		return os.MkdirAll(link, 0o755)
	case err != nil:
		return err
	case st.Mode()&os.ModeSymlink != 0:
		// A link from an earlier registration: replace it.
		if err := os.Remove(link); err != nil {
			return fmt.Errorf("cannot replace the existing link %s: %w", link, err)
		}
		return os.MkdirAll(link, 0o755)
	case !st.IsDir():
		return fmt.Errorf("%s already exists and is not a directory", link)
	}
	// A plain directory can carry a reparse point only while it is empty.
	entries, err := os.ReadDir(link)
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return fmt.Errorf("%s already exists and is not empty", link)
	}
	return nil
}

func setMountPoint(link, target string) error {
	// The on-disk layout is the one fsutil reports for a junction created by
	// mklink. Offsets are relative to the path buffer, the length fields count
	// characters only (no NUL), and each name is NUL-terminated in the buffer:
	//
	//   8   header (tag, ReparseDataLength, reserved)
	//   8   SubstituteNameOffset/Length, PrintNameOffset/Length
	//   n   substitute name, then NUL
	//   m   print name, then NUL
	//   ReparseDataLength = 8 + n + 2 + m + 2
	//
	// SubstituteName is the internal form the filesystem resolves, PrintName
	// the human-readable one.
	sub := utf16.Encode([]rune(`\??\` + target))
	printName := utf16.Encode([]rune(target))
	subLen := len(sub) * 2
	printLen := len(printName) * 2
	pathLen := subLen + 2 + printLen + 2
	dataLen := 8 + pathLen

	buf := make([]byte, 8+dataLen)
	binary.LittleEndian.PutUint32(buf[0:], ioReparseTagMountPoint)
	binary.LittleEndian.PutUint16(buf[4:], uint16(dataLen))   // ReparseDataLength
	binary.LittleEndian.PutUint16(buf[6:], 0)                 // Reserved
	binary.LittleEndian.PutUint16(buf[8:], 0)                 // SubstituteNameOffset
	binary.LittleEndian.PutUint16(buf[10:], uint16(subLen))   // SubstituteNameLength
	binary.LittleEndian.PutUint16(buf[12:], uint16(subLen+2)) // PrintNameOffset
	binary.LittleEndian.PutUint16(buf[14:], uint16(printLen)) // PrintNameLength
	putUTF16(buf[reparseNamesOffset:], sub)
	putUTF16(buf[reparseNamesOffset+subLen+2:], printName)

	linkPtr, err := syscall.UTF16PtrFromString(link)
	if err != nil {
		return err
	}
	h, err := syscall.CreateFile(
		linkPtr,
		genericWrite,
		0,
		nil,
		openExisting,
		fileFlagBackupSemantics|fileFlagOpenReparsePoint,
		0,
	)
	if err != nil {
		return fmt.Errorf("cannot open %s: %w", link, err)
	}
	defer syscall.CloseHandle(h)

	var returned uint32
	if err := syscall.DeviceIoControl(h, fsctlSetReparsePoint, &buf[0], uint32(len(buf)), nil, 0, &returned, nil); err != nil {
		return fmt.Errorf("cannot attach the junction at %s: %w", link, err)
	}
	return nil
}

func putUTF16(dst []byte, s []uint16) {
	for i, c := range s {
		binary.LittleEndian.PutUint16(dst[i*2:], c)
	}
}
