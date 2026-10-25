//go:build !windows

package main

// minimizeOwnConsole is only meaningful on Windows, where double-clicking a
// console program opens its own window.
func minimizeOwnConsole() {}
