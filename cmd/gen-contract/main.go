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
	// Both copies come from the same render. The SDK could have imported the
	// docs one, but a published package cannot reach outside its own directory —
	// and a second *hand-written* copy is exactly what this generator exists to
	// prevent, so the answer is a second write rather than a second author.
	for _, dest := range []string{
		filepath.Join(repo, "docs", "studio-api", "types.ts"),
		filepath.Join(repo, "sdk", "typescript", "src", "contract.ts"),
	} {
		if err := os.WriteFile(dest, []byte(out), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "gen-contract:", err)
			os.Exit(1)
		}
		fmt.Println("wrote", dest)
	}

	// The Swift mirror, for the iOS client. Written here rather than in that
	// repository's own toolchain because the contract is defined here: a
	// generator living beside the consumer would be a second reader of these Go
	// types, which is the drift this package exists to stop.
	//
	// The copy under docs/ is the canonical one and is what the drift test
	// checks, exactly as it is for TypeScript. The app repository gets a second
	// *write* — never a second author — and only when it is checked out, since
	// most people running `make contract` have no reason to have it.
	sw, err := contract.GenerateSwift(contract.RootFile(repo), contract.Deps(repo), contract.SwiftPreamble)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gen-contract:", err)
		os.Exit(1)
	}
	swiftDests := []string{filepath.Join(repo, "docs", "studio-api", "Contract.swift")}
	if app := os.Getenv("IOSAPP"); app != "" {
		dir := filepath.Join(app, "Sources", "SandboxStudioKit", "Contract")
		if _, err := os.Stat(dir); err != nil {
			// Named but not there is a typo, not a preference — and silently
			// skipping it is how somebody ships an app built against last
			// month's contract.
			fmt.Fprintf(os.Stderr, "gen-contract: IOSAPP=%s has no %s\n", app, dir)
			os.Exit(1)
		}
		swiftDests = append(swiftDests, filepath.Join(dir, "Generated.swift"))
	}
	for _, dest := range swiftDests {
		if err := os.WriteFile(dest, []byte(sw), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "gen-contract:", err)
			os.Exit(1)
		}
		fmt.Println("wrote", dest)
	}
}
