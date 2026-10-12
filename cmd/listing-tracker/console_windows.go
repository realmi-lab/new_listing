//go:build windows

package main

import "syscall"

// useUTF8Console switches a legacy Windows console (code page 949) to UTF-8
// so the Korean progress lines are readable.
func useUTF8Console() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	_, _, _ = kernel32.NewProc("SetConsoleOutputCP").Call(65001)
	_, _, _ = kernel32.NewProc("SetConsoleCP").Call(65001)
}
