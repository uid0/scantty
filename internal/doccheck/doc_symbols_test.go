// Package doccheck holds the module-wide sweeps that keep prose about the code
// honest about the code — its names here, and README.md's environment table in
// readme_env_test.go.
//
// It exists because a comment naming a symbol that does not exist misleads the
// person reading the code RIGHT NOW: they grep the name, find nothing, and have
// to reconstruct from scratch what the comment was for. This project has paid
// for that shape repeatedly — a doc comment that had kept its function's old
// name through a rename, an error message telling a caller to use a helper the
// lift had deleted — and every instance was invisible to the compiler, to
// `go vet`, and to a reader who trusted the prose.
//
// It lives in its own directory rather than in internal/tui because the claim
// is about the whole module and the sweep walks the whole module. It has no
// non-test file on purpose: there is nothing here to import.
package doccheck

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// WHAT THESE TWO SWEEPS PROVE, AND WHAT THEY DELIBERATELY DO NOT.
//
// Both ask one question — does this name exist anywhere in this module? — of
// two narrow populations, and neither carries an allowlist. That is the whole
// design: an exception roster for prose would be a hand-maintained inventory of
// every sentence somebody once wrote, which is the very defect these sweeps are
// here to report, one level up.
//
// PROVEN:
//
//   - TestDocs_EveryDeclarationDocLeadsWithANameThatExists — a doc comment that
//     OPENS by naming an identifier ("fooBar is the …") names one this module
//     has. Go's own convention is that the name is the declared one; this
//     codebase writes essay-style comments that legitimately open with the
//     SUBJECT of the paragraph instead, so the sweep asks only the weaker and
//     undeniable half: whatever it opens with must at least exist.
//   - TestDocs_EveryBacktickedHelperNameExists — a lower-camel name in
//     backticks, anywhere in any comment, exists. Backticks are this codebase's
//     convention for "this word is a symbol", and the lower-camel shape is one
//     no proper noun, prose word, or Django/DRF class name can take — which is
//     what lets the sweep run with no roster of foreign symbols at all.
//
// NOT PROVEN, said plainly because a claim no check delivers is worse than no
// claim:
//
//   - A BARE (unbackticked) name in the middle of a comment. Many in the
//     module resolve nowhere, and most of those are legitimate:
//     deliberate history ("the local poInputWidth that used to hang off it"),
//     symbols in OMS's Python or in bubbles, and ordinary prose that happens to
//     be camel-shaped (mTLS, macOS, ePaper). Separating those needs a judgement
//     no parser can make, and an exception list for them would be the
//     inventory this file refuses to keep. Backtick a name and it is checked.
//   - That the name resolves to the RIGHT symbol, or is in the right package. A
//     comment may point across packages, and does.
//   - Anything about a name inside a string literal — including the ones test
//     failures print, which is where a caller is told which helper to call.

// identLead matches the first word of a doc comment when it is identifier-shaped.
var identLead = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\b`)

// backticked matches one `…` span within a comment line.
var backticked = regexp.MustCompile("`([^`\n]+)`")

// separatorLine is a comment line carrying no words — the rule of dashes several
// files use between sections, which rides on the following declaration's doc
// group and would otherwise hide its opening name from the sweep.
var separatorLine = regexp.MustCompile(`^[^\p{L}\p{N}]*$`)

func TestDocs_EveryDeclarationDocLeadsWithANameThatExists(t *testing.T) {
	mod := moduleRoot(t)
	names := declaredNames(t, mod)

	var leads int
	forEachGoFile(t, mod, func(path string, fset *token.FileSet, f *ast.File) {
		check := func(doc *ast.CommentGroup, pos token.Pos) {
			if doc == nil {
				return
			}
			lead, ok := docLead(doc)
			if !ok {
				return
			}
			leads++
			if names[lead] {
				return
			}
			t.Errorf("%s: this doc comment opens by naming %q, which no declaration in "+
				"this module has. Either the symbol was renamed and the prose kept the old "+
				"name, or the sentence is about something that is gone: name what is there, "+
				"or say plainly that it is history and what replaced it. A comment whose "+
				"subject is a symbol in OMS or in a dependency should not open with its "+
				"name either — nothing here can tell that name from a stale one",
				rel(mod, fset.Position(pos)), lead)
		}
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				check(d.Doc, d.Pos())
			case *ast.GenDecl:
				check(d.Doc, d.Pos())
				for _, sp := range d.Specs {
					switch s := sp.(type) {
					case *ast.TypeSpec:
						check(s.Doc, s.Pos())
					case *ast.ValueSpec:
						check(s.Doc, s.Pos())
					}
				}
			}
		}
	})

	// A sweep that examined nothing passes for the wrong reason.
	if leads == 0 {
		t.Fatal("no declaration doc comment opened with an identifier-shaped word; " +
			"the sweep examined nothing and its PASS means nothing")
	}
	t.Logf("%d declaration doc comments open by naming a symbol", leads)
}

func TestDocs_EveryBacktickedHelperNameExists(t *testing.T) {
	mod := moduleRoot(t)
	names := declaredNames(t, mod)

	var checked int
	forEachGoFile(t, mod, func(path string, fset *token.FileSet, f *ast.File) {
		for _, group := range f.Comments {
			for _, c := range group.List {
				for _, m := range backticked.FindAllStringSubmatch(c.Text, -1) {
					name := strings.TrimSuffix(strings.TrimSpace(m[1]), "()")
					if !unexportedGoName(name) {
						continue
					}
					checked++
					if names[name] {
						continue
					}
					t.Errorf("%s: this comment spells `%s` as a symbol of this codebase, "+
						"and no declaration in this module has that name. Name the live one, "+
						"or drop the backticks if the sentence is about history",
						rel(mod, fset.Position(c.Pos())), name)
				}
			}
		}
	})

	if checked == 0 {
		t.Fatal("no comment spelled an unexported Go name in backticks; the sweep " +
			"examined nothing and its PASS means nothing")
	}
	t.Logf("%d backticked unexported names checked", checked)
}

// docLead is the identifier a doc comment opens with, once any leading
// separator rule is skipped. It answers false for a comment that opens with
// prose, punctuation, or an ALL-CAPS word — the shapes this codebase uses for
// emphasis rather than for a symbol.
func docLead(doc *ast.CommentGroup) (string, bool) {
	text := doc.Text()
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" || separatorLine.MatchString(line) {
			continue
		}
		m := identLead.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			return "", false
		}
		lead := m[1]
		if !symbolShaped(lead) {
			return "", false
		}
		return lead, true
	}
	return "", false
}

// symbolShaped is the shape a NAME has in this codebase and an English word,
// an emphasised ALL-CAPS lead, or a keystroke token (PgDn, HandlesKey) does
// not: lowerCamelCase — the shape of every unexported helper here — or a
// capitalised word carrying an underscore, which in Go source is a test name
// and nothing else. A capitalised word WITHOUT one is left alone, because that
// is equally the shape of a keystroke (PgDn) and of an English sentence's first
// word; and a lower-case word without an interior capital is a JSON key
// (last_error, is_occupied), which belongs to OMS's wire and not to this
// module. All four are derivations from how the language and this codebase name
// things, so no roster of excused prose openers is kept.
func symbolShaped(s string) bool {
	if s[0] >= 'A' && s[0] <= 'Z' {
		return strings.Contains(s, "_")
	}
	return s[0] >= 'a' && s[0] <= 'z' && interiorCapital(s)
}

// unexportedGoName is the shape a helper in this codebase has and nothing else
// does: lowerCamelCase with an interior capital. A Django model, a DRF field, a
// proper noun and an English word are all excluded by that shape rather than by
// a list of them.
func unexportedGoName(s string) bool {
	if len(s) < 4 || strings.ContainsAny(s, " \t.\\/-*+,;:'\"()[]{}<>=!?%$#@|~^&_") {
		return false
	}
	if s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return interiorCapital(s)
}

func interiorCapital(s string) bool {
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			return true
		}
	}
	return false
}

// declaredNames is every identifier this module's source declares or reads —
// package-level and local, fields and methods alike. It is deliberately the
// widest possible answer to "does this name exist": the sweeps are looking for
// names that resolve NOWHERE, and a narrower set would report a comment
// pointing at a struct field or a method as though the name were invented.
func declaredNames(t *testing.T, mod string) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	forEachGoFile(t, mod, func(path string, fset *token.FileSet, f *ast.File) {
		ast.Inspect(f, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.Ident:
				names[v.Name] = true
			case *ast.SelectorExpr:
				names[v.Sel.Name] = true
			}
			return true
		})
	})
	if len(names) == 0 {
		t.Fatal("no identifiers found under the module root; the sweep would pass vacuously")
	}
	return names
}

func forEachGoFile(t *testing.T, mod string, fn func(path string, fset *token.FileSet, f *ast.File)) {
	t.Helper()
	fset := token.NewFileSet()
	var paths []string
	err := filepath.WalkDir(mod, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && d.Name() != "." {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, ".go") {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", mod, err)
	}
	sort.Strings(paths)
	for _, p := range paths {
		f, err := parser.ParseFile(fset, p, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parsing %s: %v", p, err)
		}
		fn(p, fset, f)
	}
}

// moduleRoot walks up from the test's own directory to the go.mod, so the sweep
// is not pinned to how deep in the tree this package happens to sit.
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
			t.Fatal("no go.mod above the test's working directory")
		}
		dir = parent
	}
}

func rel(mod string, pos token.Position) string {
	if r, err := filepath.Rel(mod, pos.Filename); err == nil {
		pos.Filename = r
	}
	return pos.String()
}
