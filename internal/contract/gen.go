// Package contract generates the TypeScript mirror of the Studio API's wire
// shapes from the Go types that define them.
//
// It exists because the contract had grown four hand-maintained copies —
// internal/studioapi/types.go, docs/studio-api/types.ts, studio/src/lib/types.ts
// and every client written against them — and the copies had already drifted:
// `AgentInfo` in the docs mirror was three fields while the Go struct was ten,
// and `SessionSummary` was absent entirely, so the one shape whose meaning
// changed was the one a client could not read the contract for. A second client
// (the SDKs) would make it five, across a network, where two clients can
// disagree with each other as well as with the daemon.
//
// The rule this keeps: **the Go types are the contract, and the comments on them
// are its documentation.** Nothing here invents prose. A field with no doc
// comment in Go gets none in TypeScript, which is a visible gap in the right
// place rather than an explanation that only one of the two files carries.
package contract

import (
	_ "embed"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Preamble is the hand-written head of the mirror: the rules a client must keep
// that are properties of the *server*, not of any one type, so they have nowhere
// to live in types.go. Everything below it is generated.
//
//go:embed preamble.ts
var Preamble string

// Source is one package to read types from.
type Source struct {
	Dir string // directory to parse
	Pkg string // the qualifier used for it in the root file ("history", "agentctx"); empty for the root
}

// Generate renders the mirror. root is the file whose types are emitted in full;
// deps supply the foreign types those reference (time, history, agentctx), and
// only the ones actually reached are emitted — a mirror carrying a package's
// whole surface would be describing more than the API answers with.
func Generate(rootFile string, deps []Source, preamble string) (string, error) {
	fset := token.NewFileSet()
	rootAST, err := parser.ParseFile(fset, rootFile, nil, parser.ParseComments)
	if err != nil {
		return "", fmt.Errorf("parsing %s: %w", rootFile, err)
	}

	g := &generator{
		types:  map[string]*ast.TypeSpec{},
		docs:   map[string]string{},
		consts: map[string][]constVal{},
		order:  nil,
	}
	g.collect(rootAST, "")

	for _, d := range deps {
		pkgs, err := parser.ParseDir(fset, d.Dir, nil, parser.ParseComments)
		if err != nil {
			return "", fmt.Errorf("parsing %s: %w", d.Dir, err)
		}
		for _, pkg := range pkgs {
			for name, f := range pkg.Files {
				if strings.HasSuffix(name, "_test.go") {
					continue
				}
				g.collect(f, d.Pkg)
			}
		}
	}

	var b strings.Builder
	b.WriteString(preamble)
	for _, name := range g.order {
		spec := g.types[name]
		out, err := g.render(name, spec)
		if err != nil {
			return "", err
		}
		b.WriteString(out)
	}
	// Foreign types come last and only when reached, under a heading that says
	// where they are defined — a reader who finds `history.Stats` in a response
	// should not have to guess which package owns it.
	if len(g.reachedForeign) > 0 {
		b.WriteString("\n// ---------------------------------------------------------------------------\n")
		b.WriteString("// Shapes owned by other packages, reached from the types above.\n")
		b.WriteString("// ---------------------------------------------------------------------------\n")
		names := append([]string(nil), g.reachedForeign...)
		sort.Strings(names)
		for _, name := range names {
			out, err := g.render(name, g.types[name])
			if err != nil {
				return "", err
			}
			b.WriteString(out)
		}
	}
	return b.String(), nil
}

type constVal struct {
	name string
	val  string
	doc  string
}

type generator struct {
	types  map[string]*ast.TypeSpec
	docs   map[string]string
	consts map[string][]constVal // named string type -> its declared values
	order  []string              // root-file types, in source order

	reachedForeign []string
	seenForeign    map[string]bool
}

// collect indexes every exported type and const in a file. qualifier is "" for
// the root package and the import name for a dependency, which is how a field
// written `history.Stats` finds its declaration.
func (g *generator) collect(f *ast.File, qualifier string) {
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		switch gd.Tok {
		case token.TYPE:
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || !ts.Name.IsExported() {
					continue
				}
				key := ts.Name.Name
				if qualifier != "" {
					key = qualifier + "." + ts.Name.Name
				}
				if _, exists := g.types[key]; exists {
					continue
				}
				g.types[key] = ts
				doc := ts.Doc
				if doc == nil {
					doc = gd.Doc
				}
				g.docs[key] = doc.Text()
				if qualifier == "" {
					g.order = append(g.order, key)
				}
			}
		case token.CONST:
			// Values of a named string type become that type's union members, so
			// a client switching on RunState exhaustively is checked by the
			// compiler rather than by hope.
			var lastType string
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				typeName := lastType
				if vs.Type != nil {
					if id, ok := vs.Type.(*ast.Ident); ok {
						typeName = id.Name
						lastType = typeName
					}
				}
				if typeName == "" || len(vs.Values) == 0 {
					continue
				}
				lit, ok := vs.Values[0].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				val, err := strconv.Unquote(lit.Value)
				if err != nil {
					continue
				}
				key := typeName
				if qualifier != "" {
					key = qualifier + "." + typeName
				}
				doc := vs.Doc.Text()
				if doc == "" {
					doc = vs.Comment.Text()
				}
				g.consts[key] = append(g.consts[key], constVal{name: vs.Names[0].Name, val: val, doc: doc})
			}
		}
	}
}

// render emits one type.
func (g *generator) render(key string, ts *ast.TypeSpec) (string, error) {
	if ts == nil {
		return "", fmt.Errorf("no declaration for %s", key)
	}
	name := ts.Name.Name
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(comment(g.docs[key], ""))

	switch t := ts.Type.(type) {
	case *ast.StructType:
		fmt.Fprintf(&b, "export interface %s {\n", name)
		for _, field := range t.Fields.List {
			line, ok := g.renderField(field)
			if !ok {
				continue
			}
			b.WriteString(line)
		}
		b.WriteString("}\n")
	case *ast.Ident:
		// A named primitive. With declared values it is a union; without, an
		// alias — and an alias is the honest rendering, since the Go side really
		// does accept any string there.
		if vals := g.consts[key]; len(vals) > 0 && t.Name == "string" {
			fmt.Fprintf(&b, "export type %s =\n", name)
			for i, v := range vals {
				if v.doc != "" {
					b.WriteString(comment(v.doc, "  "))
				}
				sep := " |"
				if i == len(vals)-1 {
					sep = ";"
				}
				fmt.Fprintf(&b, "  %q%s\n", v.val, sep)
			}
		} else {
			fmt.Fprintf(&b, "export type %s = %s;\n", name, tsPrimitive(t.Name))
		}
	default:
		return "", fmt.Errorf("%s: unsupported type declaration %T", name, ts.Type)
	}
	return b.String(), nil
}

func (g *generator) renderField(field *ast.Field) (string, bool) {
	if len(field.Names) == 0 || !field.Names[0].IsExported() {
		return "", false // embedded or unexported: not on the wire
	}
	tag := ""
	if field.Tag != nil {
		tag, _ = strconv.Unquote(field.Tag.Value)
	}
	jsonName, omitempty, skip := jsonTag(tag, field.Names[0].Name)
	if skip {
		return "", false
	}
	tsType, optional := g.tsType(field.Type)
	if tsType == "" {
		return "", false
	}
	var b strings.Builder
	if doc := field.Doc.Text(); doc != "" {
		b.WriteString(comment(doc, "  "))
	}
	q := ""
	if omitempty || optional {
		q = "?"
	}
	fmt.Fprintf(&b, "  %s%s: %s;\n", jsonName, q, tsType)
	if c := field.Comment.Text(); c != "" {
		// A trailing comment carries the enumeration often enough to be worth
		// keeping: `// "docker" | "podman"` is the field's real domain.
		return strings.TrimSuffix(b.String(), "\n") + "  // " + strings.TrimSpace(c) + "\n", true
	}
	return b.String(), true
}

// tsType maps a Go type to TypeScript, and reports whether a pointer made the
// field optional.
func (g *generator) tsType(expr ast.Expr) (string, bool) {
	switch t := expr.(type) {
	case *ast.Ident:
		if p := tsPrimitive(t.Name); p != "" {
			return p, false
		}
		g.reachLocal(t.Name)
		return t.Name, false
	case *ast.StarExpr:
		inner, _ := g.tsType(t.X)
		return inner, true
	case *ast.ArrayType:
		inner, _ := g.tsType(t.Elt)
		if inner == "" {
			return "", false
		}
		return inner + "[]", false
	case *ast.MapType:
		k, _ := g.tsType(t.Key)
		v, _ := g.tsType(t.Value)
		if k == "" || v == "" {
			return "", false
		}
		return fmt.Sprintf("Record<%s, %s>", k, v), false
	case *ast.SelectorExpr:
		pkg, ok := t.X.(*ast.Ident)
		if !ok {
			return "", false
		}
		qualified := pkg.Name + "." + t.Sel.Name
		// A timestamp crosses as an RFC3339 string, because that is what
		// encoding/json does with a time.Time and what a client actually parses.
		if qualified == "time.Time" {
			return "string", false
		}
		if qualified == "time.Duration" {
			return "number", false
		}
		g.reach(qualified)
		return t.Sel.Name, false
	case *ast.InterfaceType:
		return "unknown", false
	}
	return "", false
}

func (g *generator) reachLocal(name string) {
	if _, ok := g.types[name]; !ok {
		return
	}
	for _, n := range g.order {
		if n == name {
			return // already emitted in the root pass
		}
	}
	g.reach(name)
}

func (g *generator) reach(key string) {
	if g.seenForeign == nil {
		g.seenForeign = map[string]bool{}
	}
	if g.seenForeign[key] || g.types[key] == nil {
		return
	}
	g.seenForeign[key] = true
	g.reachedForeign = append(g.reachedForeign, key)
	// Reaching a type reaches whatever it holds.
	if st, ok := g.types[key].Type.(*ast.StructType); ok {
		for _, f := range st.Fields.List {
			g.tsType(f.Type)
		}
	}
}

func tsPrimitive(name string) string {
	switch name {
	case "string":
		return "string"
	case "bool":
		return "boolean"
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64":
		return "number"
	case "any":
		return "unknown"
	}
	return ""
}

// jsonTag reads the name, whether the field is omitted when empty, and whether
// it is off the wire entirely.
func jsonTag(tag, fieldName string) (name string, omitempty, skip bool) {
	name = fieldName
	idx := strings.Index(tag, `json:"`)
	if idx < 0 {
		return name, false, false
	}
	rest := tag[idx+len(`json:"`):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return name, false, false
	}
	parts := strings.Split(rest[:end], ",")
	if parts[0] == "-" {
		return "", false, true
	}
	if parts[0] != "" {
		name = parts[0]
	}
	for _, p := range parts[1:] {
		if p == "omitempty" {
			omitempty = true
		}
	}
	return name, omitempty, false
}

// comment renders a Go doc comment as a TS block comment, preserving the
// paragraphs. The prose is the Go author's, unchanged: this file is a
// translation of the contract, not a second account of it.
func comment(doc, indent string) string {
	doc = strings.TrimSpace(doc)
	if doc == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString(indent + "/**\n")
	for _, line := range strings.Split(doc, "\n") {
		line = strings.TrimRight(line, " ")
		if line == "" {
			b.WriteString(indent + " *\n")
			continue
		}
		b.WriteString(indent + " * " + line + "\n")
	}
	b.WriteString(indent + " */\n")
	return b.String()
}

// RootFile and Deps name what the mirror is generated from, so the generator
// binary and the drift test cannot disagree about the inputs.
func RootFile(repo string) string { return filepath.Join(repo, "internal", "studioapi", "types.go") }

func Deps(repo string) []Source {
	return []Source{
		{Dir: filepath.Join(repo, "internal", "history"), Pkg: "history"},
		{Dir: filepath.Join(repo, "internal", "agentctx"), Pkg: "agentctx"},
	}
}
