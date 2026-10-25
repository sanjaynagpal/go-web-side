//go:build windows

package main

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32                  = windows.NewLazySystemDLL("kernel32.dll")
	user32                    = windows.NewLazySystemDLL("user32.dll")
	procGetConsoleWindow      = kernel32.NewProc("GetConsoleWindow")
	procGetConsoleProcessList = kernel32.NewProc("GetConsoleProcessList")
	procShowWindow            = user32.NewProc("ShowWindow")
	procGetAncestor           = user32.NewProc("GetAncestor")
	procIsIconic              = user32.NewProc("IsIconic")
)

const (
	gaRootOwner       = 3 // GetAncestor: the owner chain's root
	swShowMinNoActive = 7 // ShowWindow: minimize without activating another window
)

// minimizeOwnConsole minimizes this program's console window, but only when the
// program was started by double-clicking, which gives it a console of its own.
// Run from an existing terminal the console is shared with a shell, so it is
// left alone. The web UI should have the focus, not the log window.
//
// A single ShowWindow call at startup is not enough: Windows Terminal is still
// showing its window then and undoes it. So this keeps minimizing, in the
// background, until the window has really stayed minimized (or gives up after a
// few seconds). It never delays the caller.
func minimizeOwnConsole() {
	hwnd, _, _ := procGetConsoleWindow.Call()
	if hwnd == 0 {
		return
	}
	var pids [2]uint32
	if n, _, _ := procGetConsoleProcessList.Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids))); n != 1 {
		return // another process (a shell) shares this console
	}
	// Under Windows Terminal the console window is a hidden helper; the window
	// you can see is its owner. For the classic console host it is the same window.
	target := hwnd
	if owner, _, _ := procGetAncestor.Call(hwnd, gaRootOwner); owner != 0 {
		target = owner
	}

	go func() {
		const settled = 3 // consecutive checks that must find it minimized
		for stable, deadline := 0, time.Now().Add(5*time.Second); stable < settled && time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
			if iconic, _, _ := procIsIconic.Call(target); iconic != 0 {
				stable++
				continue
			}
			stable = 0
			procShowWindow.Call(target, swShowMinNoActive)
		}
	}()
}
