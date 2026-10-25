//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// enableVT turns on ANSI escape processing and UTF-8 output for the console.
// It reports false if stdout is not a console (for example, when piped).
func enableVT() bool {
	h := windows.Handle(os.Stdout.Fd())
	var mode uint32
	if windows.GetConsoleMode(h, &mode) != nil {
		return false
	}
	if windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) != nil {
		return false
	}
	windows.NewLazySystemDLL("kernel32.dll").NewProc("SetConsoleOutputCP").Call(65001)
	return true
}

// consoleWidth returns the console window's column count, or 0 if unknown.
func consoleWidth() int {
	var info windows.ConsoleScreenBufferInfo
	if windows.GetConsoleScreenBufferInfo(windows.Handle(os.Stdout.Fd()), &info) != nil {
		return 0
	}
	return int(info.Window.Right-info.Window.Left) + 1
}
