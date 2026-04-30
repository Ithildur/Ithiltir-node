package main

import (
	"os"

	"Ithiltir-node/internal/app"
)

func main() {
	os.Exit(app.Run(os.Args[1:]))
}
