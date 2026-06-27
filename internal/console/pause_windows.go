package console

import (
	"bufio"
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"github.com/palemoky/dnspick/internal/i18n"
)

var (
	kernel32                  = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleProcessList = kernel32.NewProc("GetConsoleProcessList")
)

// PauseOnExit keeps the console window open when dnspick was launched by a
// double-click (or a launcher such as Listary) rather than from an existing
// shell. In that situation Windows creates a console solely for this process and
// destroys it the instant the process exits, so the user never sees the results.
//
// The heuristic is GetConsoleProcessList: a console owned only by us reports a
// single attached process, whereas a console inherited from cmd.exe/PowerShell
// reports at least two (the shell and us). In the latter case the window
// survives on its own and pausing would just be annoying, so we do nothing.
func PauseOnExit() {
	if !ownsConsole() {
		return
	}
	fmt.Print(i18n.L().PressEnterToExit)
	bufio.NewReader(os.Stdin).ReadString('\n')
}

// ownsConsole reports whether this process is the only one attached to its
// console, which indicates it was started from outside an existing terminal.
func ownsConsole() bool {
	// Pass a one-element buffer; the call returns the real number of attached
	// processes, which is all we need. A zero return means no console.
	var pids [1]uint32
	r, _, _ := procGetConsoleProcessList.Call(
		uintptr(unsafe.Pointer(&pids[0])),
		uintptr(len(pids)),
	)
	return r == 1
}
