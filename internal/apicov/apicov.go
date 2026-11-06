// Package apicov derives the API coverage table (README "API coverage"):
// documented endpoint -> client method -> CLI command -> MCP tool.
//
// Nothing here is a hand-written list. The endpoint set comes from
// openapi.yaml; the client column from the Go AST of the root package (the
// path argument of every doGET/doPOST/... call); the CLI and MCP columns
// from the AST of cmd/hooppy and cmd/hooppy-mcp (cobra RegisterSubcommand
// calls and mcpserver.AddTool calls), linked to client methods by following
// calls transitively through package-local helpers and the ops layer. The
// cmd packages' own tests cross-check the derived command and tool names
// against the real cobra tree and the real MCP ListTools result, so a
// registration pattern the AST walk cannot see fails a test instead of
// silently dropping a row.
package apicov

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Op is one HTTP operation with its path normalised: every %d verb and every
// {param} becomes "{}" so a documented /posts/{id} and the client's
// "/posts/%d" compare equal.
type Op struct{ Method, Path string }

func (o Op) String() string { return o.Method + " " + o.Path }

// SpecOp is one documented operation.
type SpecOp struct {
	Op
	RawPath string // path as written in the spec, e.g. /posts/{id}
	Summary string
}

// LoadSpec reads every path+method of an OpenAPI file.
func LoadSpec(file string) ([]SpecOp, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Paths map[string]map[string]struct {
			Summary string `yaml:"summary"`
		} `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("apicov: parse %s: %w", file, err)
	}
	var out []SpecOp
	for p, methods := range doc.Paths {
		for m, op := range methods {
			switch strings.ToUpper(m) {
			case "GET", "POST", "PUT", "PATCH", "DELETE":
				out = append(out, SpecOp{Op: Op{strings.ToUpper(m), normalizeSpecPath(p)}, RawPath: p, Summary: op.Summary})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].RawPath != out[j].RawPath {
			return out[i].RawPath < out[j].RawPath
		}
		return out[i].Method < out[j].Method
	})
	return out, nil
}

func normalizeSpecPath(p string) string {
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		if p[i] == '{' {
			j := strings.IndexByte(p[i:], '}')
			if j < 0 {
				break
			}
			b.WriteString("{}")
			i += j
			continue
		}
		b.WriteByte(p[i])
	}
	return b.String()
}

// normalizeGoPath turns a Go format path ("/posts/%d/edit") into the
// normalised form. Only %d is an id placeholder: "%s" is a free-form segment
// (e.g. the /posts/{mode} dispatcher) and must not be credited as /posts/{id}.
func normalizeGoPath(p string) string {
	p = strings.ReplaceAll(p, "%d", "{}")
	return strings.ReplaceAll(p, "%s", "{s}")
}

// Surfaces is everything derived from the Go sources.
type Surfaces struct {
	// ClientDirect maps an exported Client method to the operations its own
	// body issues (path argument of a do* call).
	ClientDirect map[string][]Op
	// ClientReach is ClientDirect closed over calls to other Client methods.
	ClientReach map[string][]Op
	// CLI maps a command path ("schedules update") to the Client methods it
	// reaches. MCP maps a tool name likewise.
	CLI map[string][]string
	MCP map[string][]string
}

// Derive walks the module at root.
func Derive(root string) (*Surfaces, error) {
	fset := token.NewFileSet()
	rootPkg, err := parseDir(fset, root)
	if err != nil {
		return nil, err
	}
	opsPkg, err := parseDir(fset, filepath.Join(root, "ops"))
	if err != nil {
		return nil, err
	}
	cliPkg, err := parseDir(fset, filepath.Join(root, "cmd", "hooppy"))
	if err != nil {
		return nil, err
	}
	mcpPkg, err := parseDir(fset, filepath.Join(root, "cmd", "hooppy-mcp"))
	if err != nil {
		return nil, err
	}

	consts := stringConsts(rootPkg)
	s := &Surfaces{ClientDirect: map[string][]Op{}, ClientReach: map[string][]Op{}, CLI: map[string][]string{}, MCP: map[string][]string{}}

	// --- client: direct operations + intra-client call edges
	clientCalls := map[string]map[string]bool{}
	clientMethods := map[string]bool{}
	for _, f := range rootPkg {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || !isClientMethod(fd) {
				continue
			}
			clientMethods[fd.Name.Name] = true
		}
	}
	for _, f := range rootPkg {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || !isClientMethod(fd) || fd.Body == nil {
				continue
			}
			name := fd.Name.Name
			calls := map[string]bool{}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if verb := doVerb(sel.Sel.Name); verb != "" && len(call.Args) >= 2 {
					if p, ok := resolvePath(call.Args[1], consts); ok {
						s.ClientDirect[name] = appendOp(s.ClientDirect[name], Op{verb, normalizeGoPath(p)})
					}
					return true
				}
				if clientMethods[sel.Sel.Name] {
					calls[sel.Sel.Name] = true
				}
				return true
			})
			clientCalls[name] = calls
		}
	}
	for m := range clientMethods {
		seen := map[string]bool{}
		var walk func(string)
		walk = func(x string) {
			if seen[x] {
				return
			}
			seen[x] = true
			for _, op := range s.ClientDirect[x] {
				s.ClientReach[m] = appendOp(s.ClientReach[m], op)
			}
			for c := range clientCalls[x] {
				walk(c)
			}
		}
		walk(m)
	}

	// --- ops layer: function name -> client methods it reaches
	opsFuncs := funcDecls(opsPkg)
	opsEntries := registryEntries(opsPkg)

	// --- MCP tools
	mcpFuncs := funcDecls(mcpPkg)
	for _, f := range mcpPkg {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			entryVars := entryVarNames(fd.Body)
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || !isSelector(call.Fun, "mcpserver", "AddTool") || len(call.Args) < 3 {
					return true
				}
				name := toolName(call.Args[1], entryVars, opsEntries)
				if name == "" {
					return true
				}
				refs := reach(call.Args[len(call.Args)-1], clientMethods, mcpFuncs, opsFuncs)
				s.MCP[name] = refs
				return true
			})
		}
	}

	// --- CLI commands
	cliFuncs := funcDecls(cliPkg)
	for _, f := range cliPkg {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			deriveCLI(fd, clientMethods, cliFuncs, opsFuncs, opsEntries, s.CLI)
		}
	}
	return s, nil
}

// cliNode is one RegisterSubcommand result inside a function body.
type cliNode struct {
	parent string // variable name of the parent command ("" or "root" = top level)
	name   string
}

func deriveCLI(fd *ast.FuncDecl, clientMethods map[string]bool, cliFuncs, opsFuncs map[string]*ast.FuncDecl, entries map[string]regEntry, out map[string][]string) {
	entryVars := entryVarNames(fd.Body)
	nodes := map[string]cliNode{}
	runs := map[string][]ast.Expr{}
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		if len(as.Lhs) == 1 && len(as.Rhs) == 1 {
			if call, ok := as.Rhs[0].(*ast.CallExpr); ok && isSelector(call.Fun, "cli", "RegisterSubcommand") && len(call.Args) == 2 {
				lhs, ok := as.Lhs[0].(*ast.Ident)
				if !ok {
					return true
				}
				parent := ""
				if p, ok := call.Args[0].(*ast.Ident); ok {
					parent = p.Name
				}
				name := subcommandName(call.Args[1], entryVars, entries)
				if name != "" {
					nodes[lhs.Name] = cliNode{parent: parent, name: name}
				}
				return true
			}
			if sel, ok := as.Lhs[0].(*ast.SelectorExpr); ok && (sel.Sel.Name == "Run" || sel.Sel.Name == "RunE") {
				if x, ok := sel.X.(*ast.Ident); ok {
					runs[x.Name] = append(runs[x.Name], as.Rhs[0])
				}
			}
		}
		return true
	})
	var pathOf func(v string, depth int) string
	pathOf = func(v string, depth int) string {
		n, ok := nodes[v]
		if !ok || depth > 8 {
			return ""
		}
		if n.parent == "" || n.parent == "root" {
			return n.name
		}
		if pp := pathOf(n.parent, depth+1); pp != "" {
			return pp + " " + n.name
		}
		return n.parent + " " + n.name // parent registered in another function (group command)
	}
	for v := range nodes {
		path := pathOf(v, 0)
		var refs []string
		for _, r := range runs[v] {
			refs = append(refs, reach(r, clientMethods, cliFuncs, opsFuncs)...)
		}
		if _, dup := out[path]; !dup || len(refs) > 0 {
			out[path] = dedupe(append(out[path], refs...))
		}
	}
}

// reach returns the Client methods reachable from expr: direct selector
// references, calls into package-local functions (transitively) and calls
// into the ops package (ops.Fn resolved against opsFuncs).
func reach(expr ast.Node, clientMethods map[string]bool, local, opsFuncs map[string]*ast.FuncDecl) []string {
	found := map[string]bool{}
	seenLocal := map[string]bool{}
	seenOps := map[string]bool{}
	var visit func(n ast.Node)
	visit = func(n ast.Node) {
		ast.Inspect(n, func(x ast.Node) bool {
			switch v := x.(type) {
			case *ast.SelectorExpr:
				if id, ok := v.X.(*ast.Ident); ok && id.Name == "ops" {
					if fd := opsFuncs[v.Sel.Name]; fd != nil && !seenOps[v.Sel.Name] {
						seenOps[v.Sel.Name] = true
						// ops functions live in another package: resolve
						// their own calls against the ops decls.
						visitOps(fd, clientMethods, opsFuncs, found, map[string]bool{})
					}
					return true
				}
				if clientMethods[v.Sel.Name] {
					found[v.Sel.Name] = true
				}
			case *ast.Ident:
				if fd := local[v.Name]; fd != nil && fd.Body != nil && !seenLocal[v.Name] {
					seenLocal[v.Name] = true
					visit(fd.Body)
				}
			}
			return true
		})
	}
	visit(expr)
	return sortedKeys(found)
}

func visitOps(fd *ast.FuncDecl, clientMethods map[string]bool, opsFuncs map[string]*ast.FuncDecl, found, seen map[string]bool) {
	if fd.Body == nil || seen[fd.Name.Name] {
		return
	}
	seen[fd.Name.Name] = true
	ast.Inspect(fd.Body, func(x ast.Node) bool {
		switch v := x.(type) {
		case *ast.SelectorExpr:
			if clientMethods[v.Sel.Name] {
				found[v.Sel.Name] = true
			}
		case *ast.Ident:
			if f2 := opsFuncs[v.Name]; f2 != nil && f2 != fd {
				visitOps(f2, clientMethods, opsFuncs, found, seen)
			}
		}
		return true
	})
}

// regEntry is one ops.Registry row read from the AST.
type regEntry struct{ cliPath, mcpTool, mcpSingle string }

func registryEntries(pkg map[string]*ast.File) map[string]regEntry {
	out := map[string]regEntry{}
	for _, f := range pkg {
		ast.Inspect(f, func(n ast.Node) bool {
			cl, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			kv := map[string]string{}
			for _, el := range cl.Elts {
				if e, ok := el.(*ast.KeyValueExpr); ok {
					if k, ok := e.Key.(*ast.Ident); ok {
						if lit, ok := e.Value.(*ast.BasicLit); ok && lit.Kind == token.STRING {
							kv[k.Name], _ = strconv.Unquote(lit.Value)
						}
					}
				}
			}
			if kv["Name"] != "" && kv["CLIPath"] != "" && kv["MCPTool"] != "" {
				out[kv["Name"]] = regEntry{cliPath: kv["CLIPath"], mcpTool: kv["MCPTool"], mcpSingle: kv["MCPSingle"]}
			}
			return true
		})
	}
	return out
}

// entryVarNames maps local variables assigned from ops.MustEntry("X") to X.
func entryVarNames(body *ast.BlockStmt) map[string]string {
	out := map[string]string{}
	ast.Inspect(body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			return true
		}
		call, ok := as.Rhs[0].(*ast.CallExpr)
		if !ok || !isSelector(call.Fun, "ops", "MustEntry") || len(call.Args) != 1 {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		id, ok2 := as.Lhs[0].(*ast.Ident)
		if ok && ok2 && lit.Kind == token.STRING {
			out[id.Name], _ = strconv.Unquote(lit.Value)
		}
		return true
	})
	return out
}

// toolName reads Name from an &mcp.Tool{...} literal: a string literal, or
// <entryVar>.MCPTool (or .MCPSingle, the single-id tool) resolved through the
// ops registry.
func toolName(arg ast.Expr, entryVars map[string]string, entries map[string]regEntry) string {
	u, ok := arg.(*ast.UnaryExpr)
	if !ok {
		return ""
	}
	cl, ok := u.X.(*ast.CompositeLit)
	if !ok {
		return ""
	}
	for _, el := range cl.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if k, ok := kv.Key.(*ast.Ident); !ok || k.Name != "Name" {
			continue
		}
		switch v := kv.Value.(type) {
		case *ast.BasicLit:
			s, _ := strconv.Unquote(v.Value)
			return s
		case *ast.SelectorExpr:
			if id, ok := v.X.(*ast.Ident); ok {
				switch v.Sel.Name {
				case "MCPTool":
					return entries[entryVars[id.Name]].mcpTool
				case "MCPSingle":
					return entries[entryVars[id.Name]].mcpSingle
				}
			}
		}
	}
	return ""
}

// subcommandName reads Name from a cli.SubcommandConfig{...} literal: a
// string literal, or <entryVar>.CLILeaf() resolved through the registry
// (the leaf; the parent chain supplies the rest, matching the real tree).
func subcommandName(arg ast.Expr, entryVars map[string]string, entries map[string]regEntry) string {
	cl, ok := arg.(*ast.CompositeLit)
	if !ok {
		return ""
	}
	for _, el := range cl.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if k, ok := kv.Key.(*ast.Ident); !ok || k.Name != "Name" {
			continue
		}
		val := kv.Value
		// `entry.CLILeaf() + " <post-id>"`: the name is the leftmost operand.
		for {
			be, ok := val.(*ast.BinaryExpr)
			if !ok || be.Op != token.ADD {
				break
			}
			val = be.X
		}
		switch v := val.(type) {
		case *ast.BasicLit:
			// cobra takes the first word of Use as the name; the rest is
			// the argument synopsis ("show <schedule-id>").
			s, _ := strconv.Unquote(v.Value)
			if f := strings.Fields(s); len(f) > 0 {
				return f[0]
			}
			return ""
		case *ast.CallExpr:
			if sel, ok := v.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "CLILeaf" {
				if id, ok := sel.X.(*ast.Ident); ok {
					parts := strings.Fields(entries[entryVars[id.Name]].cliPath)
					if len(parts) > 0 {
						return parts[len(parts)-1]
					}
				}
			}
		}
	}
	return ""
}

func isSelector(e ast.Expr, pkg, name string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg
}

func isClientMethod(fd *ast.FuncDecl) bool {
	if fd.Recv == nil || len(fd.Recv.List) != 1 {
		return false
	}
	star, ok := fd.Recv.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	id, ok := star.X.(*ast.Ident)
	return ok && id.Name == "Client"
}

// doVerb maps the client's request helpers to HTTP verbs.
func doVerb(name string) string {
	switch name {
	case "doGET", "doGETRaw":
		return "GET"
	case "doPOST", "doMultipart", "doMultipartStream":
		return "POST"
	case "doPUT", "doPUTRaw":
		return "PUT"
	case "doDELETE":
		return "DELETE"
	}
	return ""
}

// resolvePath resolves a request path argument: a string literal, a const
// ident, or fmt.Sprintf(<one of those>, ...).
func resolvePath(e ast.Expr, consts map[string]string) (string, bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind == token.STRING {
			s, err := strconv.Unquote(v.Value)
			return s, err == nil
		}
	case *ast.Ident:
		s, ok := consts[v.Name]
		return s, ok
	case *ast.CallExpr:
		if isSelector(v.Fun, "fmt", "Sprintf") && len(v.Args) >= 1 {
			return resolvePath(v.Args[0], consts)
		}
	case *ast.ParenExpr:
		return resolvePath(v.X, consts)
	}
	return "", false
}

func stringConsts(pkg map[string]*ast.File) map[string]string {
	out := map[string]string{}
	for _, f := range pkg {
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, sp := range gd.Specs {
				vs := sp.(*ast.ValueSpec)
				for i, n := range vs.Names {
					if i < len(vs.Values) {
						if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
							out[n.Name], _ = strconv.Unquote(lit.Value)
						}
					}
				}
			}
		}
	}
	return out
}

// parseDir parses the non-test .go files of one directory.
func parseDir(fset *token.FileSet, dir string) (map[string]*ast.File, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := map[string]*ast.File{}
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, n), nil, 0)
		if err != nil {
			return nil, err
		}
		out[n] = f
	}
	return out, nil
}

func funcDecls(pkg map[string]*ast.File) map[string]*ast.FuncDecl {
	out := map[string]*ast.FuncDecl{}
	for _, f := range pkg {
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil {
				out[fd.Name.Name] = fd
			}
		}
	}
	return out
}

func appendOp(ops []Op, o Op) []Op {
	for _, x := range ops {
		if x == o {
			return ops
		}
	}
	return append(ops, o)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func dedupe(in []string) []string {
	m := map[string]bool{}
	for _, s := range in {
		m[s] = true
	}
	return sortedKeys(m)
}

// Row is one coverage-table row.
type Row struct {
	Spec   SpecOp
	Client []string // exported Client methods whose own body issues the operation
	CLI    []string // commands that reach one of those methods
	MCP    []string // tools that reach one of those methods
}

// Rows joins the spec with the derived surfaces.
func Rows(spec []SpecOp, s *Surfaces) []Row {
	rows := make([]Row, 0, len(spec))
	for _, so := range spec {
		r := Row{Spec: so}
		for m, ops := range s.ClientDirect {
			if !ast.IsExported(m) {
				continue
			}
			for _, o := range ops {
				if o == so.Op {
					r.Client = append(r.Client, m)
				}
			}
		}
		sort.Strings(r.Client)
		r.CLI = surfacesReaching(s.CLI, s.ClientReach, so.Op)
		r.MCP = surfacesReaching(s.MCP, s.ClientReach, so.Op)
		rows = append(rows, r)
	}
	return rows
}

// surfacesReaching lists the commands/tools whose referenced Client methods
// reach op (transitively, through ClientReach).
func surfacesReaching(surf map[string][]string, reachOps map[string][]Op, op Op) []string {
	var out []string
	for name, refs := range surf {
	next:
		for _, r := range refs {
			for _, o := range reachOps[r] {
				if o == op {
					out = append(out, name)
					break next
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

// Markers delimit the generated block in README.md.
const (
	BeginMarker = "<!-- apicov:begin (generated by `go test -run TestCoverageTable -update-readme .`; do not edit) -->"
	EndMarker   = "<!-- apicov:end -->"
)

// Render builds the markdown table.
func Render(rows []Row) string {
	var b strings.Builder
	covered := 0
	for _, r := range rows {
		if len(r.Client) > 0 {
			covered++
		}
	}
	fmt.Fprintf(&b, "%d documented operations, %d with a client method.\n\n", len(rows), covered)
	b.WriteString("| Endpoint | Client method | CLI command | MCP tool |\n|---|---|---|---|\n")
	cell := func(xs []string, f func(string) string) string {
		if len(xs) == 0 {
			return "—"
		}
		parts := make([]string, len(xs))
		for i, x := range xs {
			parts[i] = f(x)
		}
		return strings.Join(parts, ", ")
	}
	code := func(s string) string { return "`" + s + "`" }
	for _, r := range rows {
		fmt.Fprintf(&b, "| `%s %s` | %s | %s | %s |\n", r.Spec.Method, r.Spec.RawPath,
			cell(r.Client, code),
			cell(r.CLI, func(s string) string { return code("hooppy " + s) }),
			cell(r.MCP, code))
	}
	return b.String()
}

// ReplaceBlock swaps the text between the markers in readme with table.
func ReplaceBlock(readme, table string) (string, error) {
	i := strings.Index(readme, BeginMarker)
	j := strings.Index(readme, EndMarker)
	if i < 0 || j < 0 || j < i {
		return "", fmt.Errorf("apicov: README markers missing")
	}
	return readme[:i+len(BeginMarker)] + "\n\n" + table + "\n" + readme[j:], nil
}
