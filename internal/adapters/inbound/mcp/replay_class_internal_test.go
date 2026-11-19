package mcp

// The replay class of each MCP problem entry (ADR 0019) is the class the REST
// catalogue declares for the same slug. This test reads the REST catalogue's
// source (the two adapters never import each other), so a class changed on one
// side and not the other fails here.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

const restErrorsPath = "../http/errors.go"

// mcpOnly are the entries with no REST catalogue entry: the guard's own errors.
var mcpOnly = map[string]bool{"idempotency-key-invalid": true, "idempotency-key-reused": true}

// restClasses reads slug -> class identifier (replayFinal, ...) off the
// `problemCatalogue` literal of the REST adapter.
func restClasses(t *testing.T) map[string]string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), restErrorsPath, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", restErrorsPath, err)
	}
	classes := map[string]string{}
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok || len(spec.Names) != 1 || spec.Names[0].Name != "problemCatalogue" || len(spec.Values) != 1 {
			return true
		}
		for _, elt := range spec.Values[0].(*ast.CompositeLit).Elts {
			parts := elt.(*ast.CompositeLit).Elts
			if len(parts) != 3 {
				t.Fatalf("a REST catalogue entry has %d parts, want 3 (error, problem, class)", len(parts))
			}
			classes[slugOf(t, parts[1])] = parts[2].(*ast.Ident).Name
		}
		return false
	})
	if len(classes) == 0 {
		t.Fatal("no entry found in the REST problemCatalogue: teach this test the new shape")
	}
	return classes
}

// slugOf is the slug literal of `conflict("slug", "title")` or of
// `problem{status, "slug", "title"}`.
func slugOf(t *testing.T, e ast.Expr) string {
	t.Helper()
	var lit ast.Expr
	switch p := e.(type) {
	case *ast.CallExpr:
		lit = p.Args[0]
	case *ast.CompositeLit:
		lit = p.Elts[1]
	default:
		t.Fatalf("a REST problem this test cannot read: %T", e)
	}
	slug, err := strconv.Unquote(lit.(*ast.BasicLit).Value)
	if err != nil {
		t.Fatal(err)
	}
	return slug
}

func className(c replayClass) string {
	switch c {
	case replayFinal:
		return "replayFinal"
	case replayRetryable:
		return "replayRetryable"
	}
	return "unset"
}

func TestEveryMCPProblemDeclaresTheClassOfItsRESTTwin(t *testing.T) {
	rest := restClasses(t)
	for _, e := range errorCatalogue {
		if e.replay == replayUnset {
			t.Errorf("%s has no replay class: declare replayFinal or replayRetryable on its entry (ADR 0019)", e.p.slug)
			continue
		}
		want, twin := rest[e.p.slug]
		switch {
		case !twin && !mcpOnly[e.p.slug]:
			t.Errorf("%s has no entry in the REST catalogue: add one, or list it in mcpOnly", e.p.slug)
		case twin && className(e.replay) != want:
			t.Errorf("%s is %s here and %s in the REST catalogue: one class per problem type on both surfaces", e.p.slug, className(e.replay), want)
		}
	}
}
