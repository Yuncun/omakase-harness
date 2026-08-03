package overlay

// TestVoiceLivesInMessages is the deterministic check behind the one-file
// voice rule (#179 D4): every user-facing sentence this package can print
// lives in messages.go as a named constant, so one file shows the whole
// voice and a constant's name states what its call site must have proven.
// The test parses the package and fails on any fmt.Fprint*/Sprint* call
// outside messages.go whose arguments contain a string literal with letters
// in it. Letter-free literals ("%s\n", " · ") are formatting, not voice,
// and pass. Known blind spot: sentences inside fmt.Errorf error values
// (#179 D3's remaining raw-error work).

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestVoiceLivesInMessages(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, 0)
	if err != nil {
		t.Fatalf("parse package: %v", err)
	}
	var violations []string
	inspected, sawMessages := 0, false
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			base := filepath.Base(name)
			if base == "messages.go" {
				sawMessages = true
				continue
			}
			if strings.HasSuffix(base, "_test.go") {
				continue
			}
			inspected++
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				ident, ok := sel.X.(*ast.Ident)
				if !ok || ident.Name != "fmt" ||
					!(strings.HasPrefix(sel.Sel.Name, "Fprint") || strings.HasPrefix(sel.Sel.Name, "Sprint")) {
					return true
				}
				for _, arg := range call.Args {
					if lit := wordyLiteral(arg); lit != "" {
						pos := fset.Position(arg.Pos())
						violations = append(violations,
							fmt.Sprintf("%s:%d: %s", pos.Filename, pos.Line, lit))
					}
				}
				return true
			})
		}
	}
	// A wrong cwd (or a renamed messages.go) must fail loudly, not pass with
	// nothing inspected.
	if !sawMessages || inspected == 0 {
		t.Fatalf("voice check inspected nothing (messages.go seen: %v, files: %d) — wrong directory?", sawMessages, inspected)
	}
	if len(violations) > 0 {
		t.Errorf("user-facing string literals outside messages.go (%d) — name them as constants there:\n  %s",
			len(violations), strings.Join(violations, "\n  "))
	}
}

// formatNoise matches the letters that belong to formatting, not voice:
// escape sequences (\n, \t) and printf verbs (%s, %-8d, %.1f).
var formatNoise = regexp.MustCompile(`\\[a-zA-Z]|%[^a-zA-Z%]*[a-zA-Z]`)

// wordyLiteral returns a short preview if the expression is (or contains,
// via concatenation) a string literal with any letter in it after format
// verbs and escapes are stripped; "" otherwise.
func wordyLiteral(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING || !strings.ContainsFunc(formatNoise.ReplaceAllString(v.Value, ""), isLetter) {
			return ""
		}
		s := v.Value
		if len(s) > 40 {
			s = s[:40] + "…"
		}
		return s
	case *ast.BinaryExpr:
		if lit := wordyLiteral(v.X); lit != "" {
			return lit
		}
		return wordyLiteral(v.Y)
	}
	return ""
}

func isLetter(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
}
