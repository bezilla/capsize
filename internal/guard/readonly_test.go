package guard

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// bannedCalls maps a method name to the reason it may never be called.
// Matching is on the selector name alone, so it catches the call regardless of
// which client, package or interface it is reached through.
//
// Deliberately absent: "Replace" (strings.Replace is everywhere, and the REST
// client spells its write verbs Post/Put/Patch/Delete anyway).
var bannedCalls = map[string]string{
	"Create":                    "creates an object",
	"CreateOrUpdate":            "creates or mutates an object",
	"Update":                    "mutates an object",
	"UpdateStatus":              "mutates an object's status",
	"UpdateScale":               "mutates a scale subresource",
	"UpdateEphemeralContainers": "mutates a running pod",
	"Patch":                     "mutates an object",
	"PatchStatus":               "mutates an object's status",
	"Apply":                     "server-side applies an object",
	"ApplyStatus":               "server-side applies a status",
	"ApplyScale":                "server-side applies a scale",
	"Delete":                    "deletes an object",
	"DeleteCollection":          "deletes many objects",
	"Evict":                     "evicts a pod",
	"EvictV1":                   "evicts a pod",
	"EvictV1beta1":              "evicts a pod",
	"Post":                      "issues a write request",
	"Put":                       "issues a write request",
	"Bind":                      "binds a pod to a node",
}

// allowedExceptions holds fully-qualified call expressions that share a name
// with a banned method but are provably local and harmless. Add to this only
// with a comment explaining why the call cannot reach the API server.
var allowedExceptions = map[string]bool{
	// (empty: capsize currently needs none)
}

// readVerbs are the only values permitted as an argument to rest.Request.Verb.
var readVerbs = map[string]bool{"GET": true, "HEAD": true, "OPTIONS": true, "WATCH": true}

// clientGoPrefix may only be imported by the one package that owns the API
// connection. Everything else works with plain k8s.io/api types.
const clientGoPrefix = "k8s.io/client-go"

var clientGoOwners = map[string]bool{
	"internal/k8s":   true,
	"internal/guard": true, // this test names the prefix in a string, not an import
}

func TestNoWritePathExistsAnywhere(t *testing.T) {
	root := moduleRoot(t)
	fset := token.NewFileSet()

	var violations []string

	for _, path := range goFiles(t, root) {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = path
		}
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", rel, err)
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}

			if reason, banned := bannedCalls[sel.Sel.Name]; banned {
				full := render(fset, sel)
				if !allowedExceptions[full] {
					violations = append(violations, location(fset, rel, sel.Pos())+": "+full+"() "+reason)
				}
			}

			// rest.Request.Verb("POST") would sidestep the name check.
			if sel.Sel.Name == "Verb" && len(call.Args) == 1 {
				if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if v, err := strconv.Unquote(lit.Value); err == nil && !readVerbs[strings.ToUpper(v)] {
						violations = append(violations, location(fset, rel, sel.Pos())+": Verb("+lit.Value+") is not a read verb")
					}
				}
			}
			return true
		})
	}

	if len(violations) > 0 {
		t.Fatalf("capsize must contain no write path, but found %d:\n  %s\n\n"+
			"If one of these is a false positive (a local method that shares a name with an\n"+
			"API write verb), add it to allowedExceptions with a comment explaining why it\n"+
			"cannot reach the API server. Do not delete this test.",
			len(violations), strings.Join(violations, "\n  "))
	}
}

func TestOnlyTheK8sPackageTouchesClientGo(t *testing.T) {
	root := moduleRoot(t)
	fset := token.NewFileSet()

	var violations []string

	for _, path := range goFiles(t, root) {
		rel, _ := filepath.Rel(root, path)
		pkgDir := filepath.ToSlash(filepath.Dir(rel))
		if clientGoOwners[pkgDir] {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly|parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", rel, err)
		}
		for _, imp := range file.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				continue
			}
			if strings.HasPrefix(p, clientGoPrefix) {
				violations = append(violations, location(fset, rel, imp.Pos())+": imports "+p)
			}
		}
	}

	if len(violations) > 0 {
		t.Fatalf("only internal/k8s may import %s, but found %d other importer(s):\n  %s\n\n"+
			"Routing every cluster read through one package is what makes the read-only\n"+
			"guarantee checkable. Add a read accessor to internal/k8s instead.",
			clientGoPrefix, len(violations), strings.Join(violations, "\n  "))
	}
}

// TestGuardActuallyDetectsAWrite proves the walker is not silently passing.
func TestGuardActuallyDetectsAWrite(t *testing.T) {
	const src = `package p
type c struct{}
func (c) Deployments(string) c { return c{} }
func (c) Update(any) error     { return nil }
func f(cl c) { _ = cl.Deployments("ns").Update(nil) }
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "synthetic.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing synthetic source: %v", err)
	}
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			if _, banned := bannedCalls[sel.Sel.Name]; banned {
				found = true
			}
		}
		return true
	})
	if !found {
		t.Fatal("the guard failed to flag an obvious write call; the detector is broken")
	}
}

// --- helpers --------------------------------------------------------------

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate go.mod above the test's working directory")
		}
		dir = parent
	}
}

var skipDirs = map[string]bool{
	".git":     true,
	"vendor":   true,
	"testdata": true,
	"dist":     true,
}

func goFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	if len(out) == 0 {
		t.Fatalf("found no .go files under %s; the guard would pass vacuously", root)
	}
	return out
}

func render(fset *token.FileSet, n ast.Node) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, n); err != nil {
		return "<unprintable>"
	}
	return buf.String()
}

func location(fset *token.FileSet, rel string, pos token.Pos) string {
	p := fset.Position(pos)
	return rel + ":" + strconv.Itoa(p.Line)
}
