//go:build windows

package platform

import "syscall"

var (
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleOutputCP = kernel32.NewProc("GetConsoleOutputCP")
	procSetConsoleOutputCP = kernel32.NewProc("SetConsoleOutputCP")
)

// UseUTF8Console switches the attached Windows console to the UTF-8 output code
// page so ployenv's UTF-8 text renders correctly even when the console default
// is a legacy code page (e.g. 936/GBK). It returns a restore function that puts
// the previous code page back; the restore is a no-op when there is no console
// or the code page is already UTF-8. Only the console display is affected —
// redirected output is untouched.
func UseUTF8Console() func() {
	old, _, _ := procGetConsoleOutputCP.Call()
	if old == 0 || old == 65001 {
		return func() {}
	}
	if ok, _, _ := procSetConsoleOutputCP.Call(65001); ok == 0 {
		return func() {}
	}
	return func() { _, _, _ = procSetConsoleOutputCP.Call(old) }
}
