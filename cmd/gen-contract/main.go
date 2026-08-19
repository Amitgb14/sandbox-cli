// Command gen-contract writes the TypeScript mirror of the Studio API's wire
// shapes from the Go types that define them.
//
//	make contract
//
// The mirror is checked in because it is documentation as much as it is a type
// declaration: a client author reads it, and a generated file nobody can see
// without a toolchain is not documentation. TestContractMirrorIsInSync is what
// keeps the checked-in copy honest.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Amitgb14/sandbox-cli/internal/contract"
)

func main() {
	repo := "."
	if len(os.Args) > 1 {
		repo = os.Args[1]
	}
	out, err := contract.Generate(contract.RootFile(repo), contract.Deps(repo), contract.Preamble)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gen-contract:", err)
		os.Exit(1)
	}
	dest := filepath.Join(repo, "docs", "studio-api", "types.ts")
	if err := os.WriteFile(dest, []byte(out), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "gen-contract:", err)
		os.Exit(1)
	}
	fmt.Println("wrote", dest)
}
