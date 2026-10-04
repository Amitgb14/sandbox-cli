package githard

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Every git the host runs goes through this package: one exec.Command("git")
// outside it is a hook, filter or merge driver the agent named, running as
// the user. The rule was a hostile-repository test in beta.15; here it is a
// scan of the source, so a new call site fails before it is ever run.
//
// gitCallSites are the places allowed to start git, each with why.
var gitCallSites = map[string]string{
	"internal/githard/githard.go": "githard itself: `git config --list` and `hash-object` read, and run nothing the config names",
}

func TestEveryHostGitCallIsHardened(t *testing.T) {
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	found := map[string]bool{}
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || (sel.Sel.Name != "Command" && sel.Sel.Name != "CommandContext") {
					return true
				}
				if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "exec" {
					return true
				}
				prog := 0
				if sel.Sel.Name == "CommandContext" {
					prog = 1
				}
				if len(call.Args) <= prog || !namesGit(call.Args[prog]) {
					return true
				}
				found[rel] = true
				if _, ok := gitCallSites[rel]; !ok {
					t.Errorf("%s: runs git directly; host-side git goes through githard.Args and Env", fset.Position(call.Pos()))
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// A listed site that no longer runs git is an exemption nobody needs.
	for site := range gitCallSites {
		if !found[site] {
			t.Errorf("%s is listed as a git call site but runs none; remove it", site)
		}
	}
}

// namesGit reports whether a program argument is git: the literal, or the
// name this package keeps it under.
func namesGit(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.BasicLit:
		s, err := strconv.Unquote(v.Value)
		return err == nil && (s == "git" || strings.HasSuffix(s, "/git"))
	case *ast.Ident:
		return v.Name == "gitBin"
	}
	return false
}
