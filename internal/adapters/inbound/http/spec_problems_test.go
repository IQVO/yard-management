package http

// Spec/code parity of the problem types (the slug at the end of an RFC 7807
// `type`). A client is written from apis/openapi.yaml, so a problem the adapter
// can answer and the spec does not name is one the client has no entry for.
//
// The code side is every problem literal in this package's sources: the
// catalogue of errors.go AND the problems written outside it (the idempotency
// middleware, the NUL guard, the 405 and the unknown path, the readiness probe,
// internal-error). It is read from the sources, so a problem added anywhere in
// the package is seen without touching this test.
//
// The spec side is how apis/openapi.yaml declares a problem type today:
//   - an example of a response whose `type` is the problem URI (with its
//     `status` and `title`), or
//   - the slug in backticks in a description (`409 `appointment-required``).
//
// Both directions are held: the adapter answers nothing the spec does not
// declare, and the spec shows no problem the adapter cannot write.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const openAPIPath = "../../../../apis/openapi.yaml"

// problemConstructors are the helpers of errors.go that build a problem of a
// fixed status from a slug and a title.
var problemConstructors = map[string]int{
	"badRequest": http.StatusBadRequest, "notFound": http.StatusNotFound,
	"conflict": http.StatusConflict, "unprocessable": http.StatusUnprocessableEntity,
}

// statusConstants are the net/http constants a problem literal may name.
var statusConstants = map[string]int{
	"StatusBadRequest": http.StatusBadRequest, "StatusNotFound": http.StatusNotFound,
	"StatusMethodNotAllowed": http.StatusMethodNotAllowed, "StatusConflict": http.StatusConflict,
	"StatusPreconditionFailed": http.StatusPreconditionFailed, "StatusUnprocessableEntity": http.StatusUnprocessableEntity,
	"StatusInternalServerError": http.StatusInternalServerError, "StatusServiceUnavailable": http.StatusServiceUnavailable,
}

// mappedButNeverAnswered are the catalogue entries no route can answer today,
// with the reason. They are deliberately NOT in the spec: declaring one would
// promise clients an answer that never comes. Delete an entry the day a route
// can surface its error; the spec must declare it from then on.
var mappedButNeverAnswered = map[string]string{
	"lease-not-expired": "yardmove.ErrLeaseNotExpired is raised only by Move.ExpireLease, called only by the lease sweep (usecases.SweepMoveLeases), which skips the move and answers 200",
}

// answerableProblems reads every problem this package can write from its own
// sources: each `problem{...}` literal and each call of a problemConstructor.
func answerableProblems(t *testing.T) map[string]problem {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	scan := problemScan{t: t, fset: token.NewFileSet(), found: map[string]problem{}}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(scan.fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && problemConstructors[fn.Name.Name] != 0 {
				continue // the constructor itself builds a problem from its arguments
			}
			ast.Inspect(decl, scan.visit)
		}
	}
	return scan.found
}

// problemScan collects the problems of the sources by slug.
type problemScan struct {
	t     *testing.T
	fset  *token.FileSet
	found map[string]problem
}

func (s *problemScan) visit(n ast.Node) bool {
	p, isProblem, ok := problemOf(n)
	if !isProblem {
		return true
	}
	at := s.fset.Position(n.Pos())
	if !ok {
		s.t.Errorf("%s: a problem this test cannot read (slug, title and status must be literals): teach problemOf the new form", at)
		return true
	}
	if prior, twice := s.found[p.slug]; twice && prior != p {
		s.t.Errorf("%s: slug %q is written as %+v and as %+v", at, p.slug, prior, p)
	}
	s.found[p.slug] = p
	return true
}

// problemOf reads n when it is a problem literal or a problemConstructor call;
// ok is false when it is one whose parts are not literals.
func problemOf(n ast.Node) (p problem, isProblem, ok bool) {
	switch e := n.(type) {
	case *ast.CallExpr:
		fn, isIdent := e.Fun.(*ast.Ident)
		if !isIdent || problemConstructors[fn.Name] == 0 {
			return problem{}, false, false
		}
		if len(e.Args) != 2 {
			return problem{}, true, false
		}
		slug, okSlug := stringLit(e.Args[0])
		title, okTitle := stringLit(e.Args[1])
		return problem{problemConstructors[fn.Name], slug, title}, true, okSlug && okTitle
	case *ast.CompositeLit:
		if typ, isIdent := e.Type.(*ast.Ident); !isIdent || typ.Name != "problem" {
			return problem{}, false, false
		}
		parts := literalFields(e)
		if len(parts) != 3 {
			return problem{}, true, false
		}
		sel, isSel := parts[0].(*ast.SelectorExpr)
		slug, okSlug := stringLit(parts[1])
		title, okTitle := stringLit(parts[2])
		if !isSel || statusConstants[sel.Sel.Name] == 0 {
			return problem{}, true, false
		}
		return problem{statusConstants[sel.Sel.Name], slug, title}, true, okSlug && okTitle
	}
	return problem{}, false, false
}

// literalFields returns the status, slug and title expressions of a problem
// literal, positional or keyed; nil when a field is missing.
func literalFields(lit *ast.CompositeLit) []ast.Expr {
	order := map[string]int{"status": 0, "slug": 1, "title": 2}
	out := make([]ast.Expr, 3)
	for i, elt := range lit.Elts {
		kv, keyed := elt.(*ast.KeyValueExpr)
		if !keyed {
			if i < 3 {
				out[i] = elt
			}
			continue
		}
		if key, isIdent := kv.Key.(*ast.Ident); isIdent {
			if at, known := order[key.Name]; known {
				out[at] = kv.Value
			}
		}
	}
	for _, e := range out {
		if e == nil {
			return nil
		}
	}
	return out
}

func stringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}

// specExample is one problem document the spec shows as an example.
type specExample struct {
	where  string // e.g. components.responses.Conflict.examples.locationOccupied
	slug   string
	status int
	title  string
}

// openAPI is what this test reads of apis/openapi.yaml.
type openAPI struct {
	raw      string
	examples []specExample
	// answeredAs is, per shared response, the status codes operations refer to it under.
	answeredAs map[string]map[string]bool
	// sharedExamples is, per shared response, its problem examples.
	sharedExamples map[string][]specExample
}

func loadOpenAPI(t *testing.T) openAPI {
	t.Helper()
	raw, err := os.ReadFile(openAPIPath)
	if err != nil {
		t.Fatalf("read the spec: %v", err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		t.Fatalf("parse the spec: %v", err)
	}
	spec := openAPI{raw: string(raw), answeredAs: map[string]map[string]bool{}, sharedExamples: map[string][]specExample{}}
	spec.collect(&root, nil)
	return spec
}

// collect walks the document: every mapping with a problem URI in `type` is an
// example, and every `$ref` to a shared response under a status code is noted.
func (s *openAPI) collect(n *yaml.Node, path []string) {
	if n.Kind == yaml.DocumentNode || n.Kind == yaml.SequenceNode {
		for _, child := range n.Content {
			s.collect(child, path)
		}
		return
	}
	if n.Kind != yaml.MappingNode {
		return
	}
	fields := map[string]string{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, value := n.Content[i].Value, n.Content[i+1]
		if value.Kind == yaml.ScalarNode {
			fields[key] = value.Value
		}
		s.collect(value, append(path[:len(path):len(path)], key))
	}
	s.noteSharedResponse(fields["$ref"], path)
	s.noteExample(fields, path)
}

// noteSharedResponse records the status code an operation declares a shared
// response under: path ends with it (paths./x.get.responses.409).
func (s *openAPI) noteSharedResponse(ref string, path []string) {
	const prefix = "#/components/responses/"
	if !strings.HasPrefix(ref, prefix) || len(path) == 0 {
		return
	}
	name := strings.TrimPrefix(ref, prefix)
	if s.answeredAs[name] == nil {
		s.answeredAs[name] = map[string]bool{}
	}
	s.answeredAs[name][path[len(path)-1]] = true
}

// noteExample records a mapping whose `type` is a problem URI of this service.
func (s *openAPI) noteExample(fields map[string]string, path []string) {
	if !strings.HasPrefix(fields["type"], problemBaseURI) {
		return
	}
	ex := specExample{where: strings.Join(path, "."), slug: strings.TrimPrefix(fields["type"], problemBaseURI), title: fields["title"]}
	ex.status, _ = strconv.Atoi(fields["status"])
	s.examples = append(s.examples, ex)
	if len(path) > 2 && path[0] == "components" && path[1] == "responses" {
		s.sharedExamples[path[2]] = append(s.sharedExamples[path[2]], ex)
	}
}

// declares reports whether the spec names the slug: as the `type` of an
// example or in backticks in a description.
func (s *openAPI) declares(slug string) bool {
	for _, ex := range s.examples {
		if ex.slug == slug {
			return true
		}
	}
	return strings.Contains(s.raw, "`"+slug+"`")
}

func sortedSlugs(problems map[string]problem) []string {
	slugs := make([]string, 0, len(problems))
	for slug := range problems {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	return slugs
}

// TestTheSpecDeclaresEveryProblemTypeTheAdapterAnswers is the gate: a problem
// the adapter can write and apis/openapi.yaml does not name fails here.
func TestTheSpecDeclaresEveryProblemTypeTheAdapterAnswers(t *testing.T) {
	spec := loadOpenAPI(t)
	answerable := answerableProblems(t)
	for _, slug := range sortedSlugs(answerable) {
		if _, never := mappedButNeverAnswered[slug]; never || spec.declares(slug) {
			continue
		}
		t.Errorf("the adapter can answer %d `%s` and apis/openapi.yaml does not declare it: a client written from the spec has no entry for it. "+
			"FIX: name it in the spec on the operations that can answer it (an example of the shared response of its status, and the slug in backticks "+
			"in the description of the operation or parameter), then regenerate docs/docs/api-reference/rest", answerable[slug].status, slug)
	}
}

// TestTheSpecShowsNoProblemTheAdapterCannotWrite is the other direction: every
// problem example of the spec is one the adapter writes, with the adapter's
// status and title, under a shared response used for that status only.
func TestTheSpecShowsNoProblemTheAdapterCannotWrite(t *testing.T) {
	spec := loadOpenAPI(t)
	answerable := answerableProblems(t)
	if len(spec.examples) == 0 {
		t.Fatal("no problem example found in apis/openapi.yaml: the spec changed how it declares problem types, teach this test the new way")
	}
	for _, ex := range spec.examples {
		p, ok := answerable[ex.slug]
		switch {
		case !ok:
			t.Errorf("%s: the spec shows `%s`, a problem the adapter never writes", ex.where, ex.slug)
		case ex.status != p.status:
			t.Errorf("%s: `%s` is shown with status %d, the adapter answers %d", ex.where, ex.slug, ex.status, p.status)
		case ex.title != p.title:
			t.Errorf("%s: `%s` is shown with title %q, the adapter writes %q", ex.where, ex.slug, ex.title, p.title)
		}
	}
	for name, examples := range spec.sharedExamples {
		for code := range spec.answeredAs[name] {
			for _, ex := range examples {
				if strconv.Itoa(ex.status) != code {
					t.Errorf("%s: a %d problem in a response that operations declare as their %s", ex.where, ex.status, code)
				}
			}
		}
	}
}

// statusThenSlug matches the prose form of the spec, "409 `location-occupied`".
var statusThenSlug = regexp.MustCompile("\\b([45][0-9]{2}) `([a-z0-9]+(?:-[a-z0-9]+)*)`")

// TestTheProseOfTheSpecGivesEachProblemItsStatus holds the descriptions to the
// code: a "<status> `slug`" names a problem the adapter writes, with that status.
func TestTheProseOfTheSpecGivesEachProblemItsStatus(t *testing.T) {
	spec := loadOpenAPI(t)
	answerable := answerableProblems(t)
	mentions := statusThenSlug.FindAllStringSubmatch(spec.raw, -1)
	if len(mentions) == 0 {
		t.Fatal("no \"<status> `slug`\" in apis/openapi.yaml: the spec changed its prose, teach this test the new form")
	}
	for _, m := range mentions {
		p, ok := answerable[m[2]]
		switch {
		case !ok:
			t.Errorf("the spec says %s `%s`: the adapter writes no such problem", m[1], m[2])
		case strconv.Itoa(p.status) != m[1]:
			t.Errorf("the spec says %s `%s`: the adapter answers %d", m[1], m[2], p.status)
		}
	}
}

// TestProblemsMappedButNeverAnsweredStayOutOfTheSpec keeps the exemption list
// honest: an entry is a slug the adapter still maps and the spec does not name.
func TestProblemsMappedButNeverAnsweredStayOutOfTheSpec(t *testing.T) {
	spec := loadOpenAPI(t)
	answerable := answerableProblems(t)
	for slug, reason := range mappedButNeverAnswered {
		if _, mapped := answerable[slug]; !mapped {
			t.Errorf("`%s` is no longer a problem of the adapter: delete it from mappedButNeverAnswered", slug)
		}
		if spec.declares(slug) {
			t.Errorf("`%s` is declared in the spec and listed as never answered (%s): keep one of the two", slug, reason)
		}
	}
}

// TestTheProblemScanSeesWhatTheAdapterWrites guards the scan itself: it must
// find, with the same status and title, the catalogue and every problem the
// adapter writes outside it, so a refactor cannot blind the tests above.
func TestTheProblemScanSeesWhatTheAdapterWrites(t *testing.T) {
	answerable := answerableProblems(t)
	written := []problem{
		internalProblem, methodNotAllowed, notFoundResource, dependencyUnavailable,
		idempotencyKeyRequired, idempotencyKeyInvalid, idempotencyKeyReused,
	}
	for _, e := range problemCatalogue {
		written = append(written, e.p)
	}
	for _, p := range written {
		if got, ok := answerable[p.slug]; !ok || got != p {
			t.Errorf("the scan read %+v (found %v) for %+v", got, ok, p)
		}
	}
	if len(answerable) != len(written) {
		t.Errorf("the scan found %d problems, the adapter's variables hold %d: %v", len(answerable), len(written), sortedSlugs(answerable))
	}
}
