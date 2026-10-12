// Command ployenv is a portable, non-invasive multi-runtime version manager.
package main

import (
	"os"

	"github.com/MOMIEDEJIYI/ployenv/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:]))
}
