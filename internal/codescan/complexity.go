package codescan

import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strings"

	"github.com/ctford/tokenamun/internal/model"
)

// complexity measures per-function complexity.
//
// Go is parsed, so the result is a real count of decision points in the
// control-flow graph and is labelled derived. Every other language is
// approximated by counting branch keywords, which is labelled derived-approx:
// it correlates with cyclomatic complexity but is not a computation of it, and
// presenting it as one would be exactly the false precision this tool exists
// to avoid.
func complexity(path, lang, src string) ([]FunctionMetrics, model.Provenance) {
	if lang == "go" {
		if funcs, ok := goComplexity(path, src); ok {
			return funcs, model.Derived
		}
	}
	return keywordComplexity(lang, src), model.DerivedApprox
}

// goComplexity walks the AST. A file that does not parse -- mid-edit, or a
// syntax we do not handle -- falls back to the approximation rather than
// reporting nothing.
func goComplexity(path, src string) ([]FunctionMetrics, bool) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, false
	}

	var out []FunctionMetrics
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		name := fn.Name.Name
		if fn.Recv != nil && len(fn.Recv.List) > 0 {
			name = receiverName(fn.Recv.List[0].Type) + "." + name
		}
		out = append(out, FunctionMetrics{
			Name:       name,
			Line:       fset.Position(fn.Pos()).Line,
			Complexity: 1 + decisionPoints(fn.Body),
		})
	}
	return out, true
}

// decisionPoints counts the branches in a function body. One per branching
// construct, one per case or comm clause, and one per boolean operator, which
// is the standard cyclomatic formulation.
func decisionPoints(body *ast.BlockStmt) int {
	n := 0
	ast.Inspect(body, func(node ast.Node) bool {
		switch stmt := node.(type) {
		case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt:
			n++
		case *ast.CaseClause:
			// A default clause adds no branch: it is the fall-through.
			if len(stmt.List) > 0 {
				n++
			}
		case *ast.CommClause:
			if stmt.Comm != nil {
				n++
			}
		case *ast.BinaryExpr:
			if stmt.Op == token.LAND || stmt.Op == token.LOR {
				n++
			}
		}
		return true
	})
	return n
}

func receiverName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return receiverName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return receiverName(t.X)
	default:
		return "?"
	}
}

// branchKeywords are counted for languages we do not parse.
var branchKeywords = regexp.MustCompile(
	`\b(if|elif|else\s+if|for|while|case|when|catch|rescue|except|unless|&&|\|\||\?\?)\b`)

// functionStart matches something that looks like a function definition across
// the C-family and the scripting languages, well enough to attribute a run of
// lines to a name.
var functionStart = regexp.MustCompile(
	`^\s*(?:export\s+|public\s+|private\s+|protected\s+|static\s+|async\s+)*` +
		`(?:func|function|def|fn|sub|method)\s+([A-Za-z_][A-Za-z0-9_]*)`)

// keywordComplexity approximates by counting branch keywords between
// apparent function boundaries. Where no function can be identified the whole
// file is reported as one unit named "(file)", which is honest about the
// resolution available.
func keywordComplexity(lang string, src string) []FunctionMetrics {
	lines := strings.Split(src, "\n")
	var out []FunctionMetrics
	cur := FunctionMetrics{Name: "(file)", Line: 1, Complexity: 1}
	found := false

	for i, line := range lines {
		if !isCode(line, lang) {
			continue
		}
		if m := functionStart.FindStringSubmatch(line); m != nil {
			if found {
				out = append(out, cur)
			}
			cur = FunctionMetrics{Name: m[1], Line: i + 1, Complexity: 1}
			found = true
			continue
		}
		cur.Complexity += len(branchKeywords.FindAllString(line, -1))
	}
	out = append(out, cur)
	return out
}
