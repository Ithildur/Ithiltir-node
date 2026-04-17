package main

import (
	"flag"
	"fmt"
	"os"

	"Ithiltir-node/internal/buildversion"
)

func main() {
	version := flag.String("version", "", "version string to validate")
	flag.Parse()

	if *version == "" || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, buildversion.FormatHint)
		os.Exit(1)
	}

	if err := buildversion.Validate(*version); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
