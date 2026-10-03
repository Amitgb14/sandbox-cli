// Command sandbox-cli runs AI coding agents and arbitrary commands inside a
// disposable, isolated sandbox.
package main

import (
	"os"

	"github.com/Amitgb14/sandbox-cli/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
