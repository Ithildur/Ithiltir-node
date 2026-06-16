//go:build linux || darwin

package app

import (
	"os"
	"syscall"

	"Ithiltir-node/internal/selfupdate"
)

func restartForUpdate() error {
	path := selfupdate.NodePath()
	argv := append([]string{path}, os.Args[1:]...)
	return syscall.Exec(path, argv, os.Environ())
}
