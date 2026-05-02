//go:build windows

package main

import (
	"os"

	"Ithiltir-node/internal/runner"
)

func main() {
	os.Exit(runner.Run(os.Args[1:]))
}
