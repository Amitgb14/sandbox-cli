package contract

import (
	_ "embed"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
)

// The Swift mirror of the contract, for the iOS client.
//
// It shares the *walk* with the TypeScript mirror — collect, reach, jsonTag and
// the generator's index are about Go and know nothing about a target language —
// and differs only in rendering. That split is the point: a second traversal
// would be a second opinion about which types are on the wire, and the whole
// reason this package exists is that two opinions had already drifted.
//
// Three renderings differ from TypeScript in ways worth stating, because each is
// a place Swift cannot say what the TS mirror says:
//
//   - **Optional and nullable collapse.** TS distinguishes `x?: T` (the key may
//     be absent) from `x: T | null` (the key is always sent and may be null).
//     Swift has one spelling, `T?`, and Codable's decodeIfPresent treats both
//     alike. So a pointer or an omitempty both become `T?`, and the distinction
//     lives in docs/studio-api/types.ts, which is where a client author reads
//     the contract anyway. What survives is the part that mattered:
//     Worktree.Verified is `Bool?`, and nil still means nothing checked it
//     rather than something checked it and said no.
//
//   - **A timestamp stays a String.** encoding/json writes time.Time as RFC3339
//     and the TS mirror calls it `string`; decoding to Date here would mean this
//     file choosing a date strategy on the app's behalf, and getting it wrong
//     turns one malformed field into a whole response that will not decode.
//
//   - **An enum is exhaustive, and unrecognised values throw** — unless the Go
//     side declared a member literally named "unknown", in which case decoding
//     falls back to it. That is not a hedge: RunState has an `unknown` because
//     the daemon genuinely cannot always tell, so a newer state arriving at an
//     older app lands somewhere honest. LogEventType has none, and must not — it
//     discriminates a protocol, and a client that guessed at a fourth event type
//     would render an incomplete stream as a complete one, which is the one
//     thing the contract comment on it says a log viewer must not do.

// SwiftPreamble is the hand-written head of the Swift mirror — the rules a
// client must keep that are properties of the *server* rather than of any one
// type, so they have nowhere to live in types.go. Everything below it is
// generated.
//
//go:embed preamble.swift
var SwiftPreamble string

// GenerateSwift renders the Swift mirror. Same inputs as Generate, and
// deliberately the same shape of function, so the two cannot be given different
// roots by accident.
func GenerateSwift(rootFile string, deps []Source, preamble string) (string, error) {
	fset := token.NewFileSet()
	rootAST, err := parser.ParseFile(fset, rootFile, nil, parser.ParseComments)
	if err != nil {
		return "", fmt.Errorf("parsing %s: %w", rootFile, err)
	}

	g := &generator{
		types:  map[string]*ast.TypeSpec{},
		docs:   map[string]string{},
		consts: map[string][]constVal{},
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
		out, err := g.renderSwift(name, g.types[name])
		if err != nil {
			return "", err
		}
		b.WriteString(out)
	}
	if len(g.reachedForeign) > 0 {
		b.WriteString("\n// ---------------------------------------------------------------------------\n")
		b.WriteString("// Shapes owned by other packages, reached from the types above.\n")
		b.WriteString("// ---------------------------------------------------------------------------\n")
		names := append([]string(nil), g.reachedForeign...)
		sort.Strings(names)
		for _, name := range names {
			out, err := g.renderSwift(name, g.types[name])
			if err != nil {
				return "", err
			}
			b.WriteString(out)
		}
	}
	return b.String(), nil
}

func (g *generator) renderSwift(key string, ts *ast.TypeSpec) (string, error) {
	if ts == nil {
		return "", fmt.Errorf("no declaration for %s", key)
	}
	name := ts.Name.Name
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(swiftDoc(g.docs[key], ""))

	switch t := ts.Type.(type) {
	case *ast.StructType:
		return g.renderSwiftStruct(name, b.String(), t)
	case *ast.Ident:
		if vals := g.consts[key]; len(vals) > 0 && t.Name == "string" {
			return g.renderSwiftEnum(name, b.String(), vals), nil
		}
		if p := swiftPrimitive(t.Name); p != "" {
			fmt.Fprintf(&b, "public typealias %s = %s\n", name, p)
			return b.String(), nil
		}
		if _, known := g.types[t.Name]; known {
			g.reachLocal(t.Name)
			fmt.Fprintf(&b, "public typealias %s = %s\n", name, t.Name)
			return b.String(), nil
		}
		return "", fmt.Errorf("%s: cannot render a named type over %q — add a mapping or a Deps entry", name, t.Name)
	default:
		return "", fmt.Errorf("%s: unsupported type declaration %T", name, ts.Type)
	}
}

// swiftField is one rendered property, held rather than written straight out
// because a struct needs its fields three times: as properties, as CodingKeys,
// and as memberwise-init parameters.
type swiftField struct {
	jsonName  string // the key on the wire
	swiftName string // the property name, which differs when the key is not an identifier
	typ       string
	optional  bool
	doc       string
	trailing  string
}

func (g *generator) renderSwiftStruct(name, head string, t *ast.StructType) (string, error) {
	var fields []swiftField
	for _, field := range t.Fields.List {
		f, ok, err := g.swiftField(field)
		if err != nil {
			return "", fmt.Errorf("%s: %w", name, err)
		}
		if ok {
			fields = append(fields, f)
		}
	}

	var b strings.Builder
	b.WriteString(head)
	// Sendable because these cross actor boundaries constantly — StudioClient is
	// an actor and every screen is on the main one. Hashable because SwiftUI
	// lists want an identity and a value type is the cheapest one to give.
	fmt.Fprintf(&b, "public struct %s: Codable, Hashable, Sendable {\n", name)

	for _, f := range fields {
		if f.doc != "" {
			b.WriteString(swiftDoc(f.doc, "    "))
		}
		opt := ""
		if f.optional {
			opt = "?"
		}
		fmt.Fprintf(&b, "    public var %s: %s%s", f.swiftName, f.typ, opt)
		if f.trailing != "" {
			fmt.Fprintf(&b, "  // %s", f.trailing)
		}
		b.WriteString("\n")
	}

	if needsCodingKeys(fields) {
		b.WriteString("\n    enum CodingKeys: String, CodingKey {\n")
		for _, f := range fields {
			fmt.Fprintf(&b, "        case %s = %q\n", f.swiftName, f.jsonName)
		}
		b.WriteString("    }\n")
	}

	// An explicit public memberwise init: Swift's synthesised one is internal,
	// so without this the app target could read every response and construct no
	// request. Optionals default to nil, which is what makes RunCreateRequest —
	// twenty-odd fields, three of them usually set — bearable to call.
	if len(fields) > 0 {
		b.WriteString("\n    public init(\n")
		for i, f := range fields {
			opt, def := "", ""
			if f.optional {
				opt, def = "?", " = nil"
			}
			comma := ","
			if i == len(fields)-1 {
				comma = ""
			}
			fmt.Fprintf(&b, "        %s: %s%s%s%s\n", f.swiftName, f.typ, opt, def, comma)
		}
		b.WriteString("    ) {\n")
		for _, f := range fields {
			fmt.Fprintf(&b, "        self.%s = %s\n", f.swiftName, f.swiftName)
		}
		b.WriteString("    }\n")
	} else {
		b.WriteString("\n    public init() {}\n")
	}

	b.WriteString("}\n")
	return b.String(), nil
}

func (g *generator) renderSwiftEnum(name, head string, vals []constVal) string {
	var b strings.Builder
	b.WriteString(head)
	fmt.Fprintf(&b, "public enum %s: String, Codable, Hashable, Sendable, CaseIterable {\n", name)

	fallback := ""
	for _, v := range vals {
		if v.doc != "" {
			b.WriteString(swiftDoc(v.doc, "    "))
		}
		caseName := swiftIdent(v.val)
		if v.val == "unknown" {
			fallback = caseName
		}
		fmt.Fprintf(&b, "    case %s = %q\n", caseName, v.val)
	}

	// See the file comment: a value this build has never heard of is a decode
	// error unless the Go side declared somewhere honest to put it.
	if fallback != "" {
		fmt.Fprintf(&b, `
    /// A value this build does not know decodes as `+"`%s`"+` rather than throwing.
    ///
    /// Sound only because the Go side declares that member: it means the daemon
    /// could not tell either, so a state added after this app shipped lands
    /// somewhere the contract already says means "no answer".
    public init(from decoder: any Decoder) throws {
        let raw = try decoder.singleValueContainer().decode(String.self)
        self = %s(rawValue: raw) ?? .%s
    }
`, fallback, name, fallback)
	}

	b.WriteString("}\n")
	return b.String()
}

func (g *generator) swiftField(field *ast.Field) (swiftField, bool, error) {
	if len(field.Names) == 0 {
		return swiftField{}, false, fmt.Errorf("embedded field %s: promoted fields are on the wire and are not rendered — declare it explicitly or teach the generator", exprName(field.Type))
	}
	if len(field.Names) > 1 {
		return swiftField{}, false, fmt.Errorf("field %s: several names share one json tag, which the emitter would render once", field.Names[0].Name)
	}
	if !field.Names[0].IsExported() {
		return swiftField{}, false, nil
	}
	tag := ""
	if field.Tag != nil {
		tag, _ = strconv.Unquote(field.Tag.Value)
	}
	jsonName, omitempty, skip := jsonTag(tag, field.Names[0].Name)
	if skip {
		return swiftField{}, false, nil
	}
	typ, nullable, err := g.swiftType(field.Type)
	if err != nil {
		return swiftField{}, false, fmt.Errorf("field %s: %w", field.Names[0].Name, err)
	}
	return swiftField{
		jsonName:  jsonName,
		swiftName: swiftIdent(jsonName),
		typ:       typ,
		// Both spellings of "may not be there" collapse to one in Swift; see the
		// file comment for why that is a loss the mirror can afford.
		optional: omitempty || nullable,
		doc:      field.Doc.Text(),
		trailing: strings.TrimSpace(field.Comment.Text()),
	}, true, nil
}

func (g *generator) swiftType(expr ast.Expr) (string, bool, error) {
	switch t := expr.(type) {
	case *ast.Ident:
		if p := swiftPrimitive(t.Name); p != "" {
			return p, false, nil
		}
		if _, known := g.types[t.Name]; !known {
			return "", false, fmt.Errorf("unresolved type %q — it is not declared in the root file or any Deps package", t.Name)
		}
		g.reachLocal(t.Name)
		return t.Name, false, nil
	case *ast.StarExpr:
		inner, _, err := g.swiftType(t.X)
		return inner, true, err
	case *ast.ArrayType:
		inner, _, err := g.swiftType(t.Elt)
		if err != nil {
			return "", false, err
		}
		return "[" + inner + "]", false, nil
	case *ast.MapType:
		k, _, err := g.swiftType(t.Key)
		if err != nil {
			return "", false, err
		}
		v, _, err := g.swiftType(t.Value)
		if err != nil {
			return "", false, err
		}
		return fmt.Sprintf("[%s: %s]", k, v), false, nil
	case *ast.SelectorExpr:
		pkg, ok := t.X.(*ast.Ident)
		if !ok {
			return "", false, fmt.Errorf("unsupported qualified type %s", exprName(expr))
		}
		qualified := pkg.Name + "." + t.Sel.Name
		switch qualified {
		case "time.Time":
			// RFC3339, as a String. See the file comment.
			return "String", false, nil
		case "time.Duration":
			return "Int", false, nil
		}
		if _, known := g.types[qualified]; !known {
			return "", false, fmt.Errorf("unresolved type %q — add its package to Deps, or map it like time.Time", qualified)
		}
		g.reach(qualified)
		return t.Sel.Name, false, nil
	case *ast.InterfaceType:
		if t.Methods != nil && len(t.Methods.List) > 0 {
			return "", false, fmt.Errorf("a non-empty interface has no wire shape")
		}
		// Deliberately unmapped rather than rendered as a type-erased box. Swift
		// has no `unknown`, and inventing an AnyCodable here would put a
		// hand-written type in a generated file. If the API grows one of these,
		// that is the moment to decide what it should be.
		return "", false, fmt.Errorf("an empty interface has no Swift spelling — give the field a concrete type or teach the generator")
	}
	return "", false, fmt.Errorf("unsupported type %s", exprName(expr))
}

func swiftPrimitive(name string) string {
	switch name {
	case "string":
		return "String"
	case "bool":
		return "Bool"
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64":
		return "Int"
	case "float32", "float64":
		return "Double"
	}
	return ""
}

// needsCodingKeys reports whether any property name differs from its wire key.
//
// All or nothing: Swift's CodingKeys is a complete list or none at all, and a
// partial one silently drops every field missing from it.
func needsCodingKeys(fields []swiftField) bool {
	for _, f := range fields {
		if f.swiftName != f.jsonName {
			return true
		}
	}
	return false
}

// swiftKeywords are the reserved words a JSON key in this contract could
// plausibly collide with. Escaping is by backtick, which keeps the identifier
// spelled the same as the key — so a collision costs two characters rather than
// a CodingKeys entry and a name a reader has to map back by hand.
var swiftKeywords = map[string]bool{
	"associatedtype": true, "class": true, "deinit": true, "enum": true,
	"extension": true, "fileprivate": true, "func": true, "import": true,
	"init": true, "inout": true, "internal": true, "let": true, "open": true,
	"operator": true, "private": true, "protocol": true, "public": true,
	"rethrows": true, "static": true, "struct": true, "subscript": true,
	"typealias": true, "var": true, "break": true, "case": true,
	"continue": true, "default": true, "defer": true, "do": true, "else": true,
	"fallthrough": true, "for": true, "guard": true, "if": true, "in": true,
	"repeat": true, "return": true, "switch": true, "where": true,
	"while": true, "as": true, "catch": true, "false": true, "is": true,
	"nil": true, "super": true, "self": true, "throw": true, "throws": true,
	"true": true, "try": true,
}

// swiftIdent turns a wire key or enum value into a Swift identifier, changing it
// as little as it can.
func swiftIdent(s string) string {
	if s == "" {
		return "_empty"
	}
	var b strings.Builder
	upperNext := false
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '_':
			if upperNext {
				b.WriteString(strings.ToUpper(string(r)))
				upperNext = false
			} else {
				b.WriteRune(r)
			}
		case r >= '0' && r <= '9':
			if i == 0 {
				// A leading digit is not an identifier; prefixing beats dropping,
				// which would collide two different keys onto one name.
				b.WriteString("_")
			}
			b.WriteRune(r)
			upperNext = false
		default:
			// `-` and `.` become camelCase rather than `_`, which is what a Swift
			// reader expects and what the rest of this contract already looks like.
			upperNext = true
		}
	}
	out := b.String()
	if out == "" {
		return "_empty"
	}
	if swiftKeywords[out] {
		return "`" + out + "`"
	}
	return out
}

// swiftDoc renders a Go doc comment as Swift `///` lines.
//
// The prose is the Go author's, unchanged, for the reason the TypeScript mirror
// gives: this file is a translation of the contract, not a second account of it.
func swiftDoc(doc, indent string) string {
	doc = strings.TrimSpace(doc)
	if doc == "" {
		return ""
	}
	var b strings.Builder
	for _, line := range strings.Split(doc, "\n") {
		line = strings.TrimRight(line, " ")
		if line == "" {
			b.WriteString(indent + "///\n")
			continue
		}
		b.WriteString(indent + "/// " + line + "\n")
	}
	return b.String()
}
