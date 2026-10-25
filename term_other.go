//go:build !windows

package main

import "os"

// enableVT reports whether stdout is a terminal that understands ANSI codes.
func enableVT() bool {
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0 && os.Getenv("TERM") != "dumb"
}

// consoleWidth is unknown here; termWidth falls back to $COLUMNS.
func consoleWidth() int { return 0 }
