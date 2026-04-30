package app

import (
	"os"
	"syscall"
)

func exitCodeForSignal(sig os.Signal) int {
	if sig == os.Interrupt {
		return 130
	}
	if s, ok := sig.(syscall.Signal); ok && s == syscall.SIGTERM {
		return 143
	}
	return 1
}
