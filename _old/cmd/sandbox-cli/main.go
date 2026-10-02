// Command sandbox runs AI coding agents and arbitrary commands inside a
// disposable, isolated Docker container.
package main

import (
	"os"

	"github.com/Amitgb14/sandbox-cli/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
