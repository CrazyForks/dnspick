//go:build !windows

package console

// PauseOnExit is a no-op on non-Windows platforms, where binaries are run from a
// shell that stays open after the process exits.
func PauseOnExit() {}
