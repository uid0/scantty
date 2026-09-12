package doccheck

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestDocs_TheReadmeTableNamesEverySCANTTYVariableTheLoaderReads holds the
// SCANTTY_-prefixed part of the one inventory in README.md that is not replaced
// by a pointer, because it cannot be: an operator setting up a host has to be
// able to READ the variables somewhere, and `go doc` will not print a table of
// them.
//
// So the table stays and is CHECKED instead. It drifted the ordinary way —
// SCANTTY_THEME was added to the loader and the table was not touched, so the
// documented set was a strict subset of the real one and the omission was
// silent in both directions: nothing failed, and the variable simply did not
// exist as far as a reader was concerned.
//
// It compares only the SCANTTY_-prefixed table rows with the env constants in
// internal/config/config.go. What a variable is FOR, and what it defaults to,
// are prose no test can judge — this only says those two lists name the same
// variables, which is the half that goes wrong by forgetting.
//
// NOT PROVEN: the SENTRY_* rows. Their loader lives in internal/observability,
// so deleting or misspelling one there would not fail this test.
func TestDocs_TheReadmeTableNamesEverySCANTTYVariableTheLoaderReads(t *testing.T) {
	mod := moduleRoot(t)

	readme := readFile(t, filepath.Join(mod, "README.md"))
	documented := readmeEnvNames(t, readme)
	read := loaderEnvNames(t, filepath.Join(mod, "internal", "config", "config.go"))

	if len(documented) == 0 {
		t.Fatal("README.md's environment table names no SCANTTY_ variable; the " +
			"comparison would pass against an empty set and prove nothing")
	}
	if len(read) == 0 {
		t.Fatal("internal/config/config.go names no SCANTTY_ variable; the comparison " +
			"would pass against an empty set and prove nothing")
	}

	for _, name := range read {
		if !contains(documented, name) {
			t.Errorf("internal/config reads %s and README.md's environment table does not "+
				"name it: a variable a host has to set is documented nowhere", name)
		}
	}
	for _, name := range documented {
		if !contains(read, name) {
			t.Errorf("README.md documents %s and internal/config/config.go does not read "+
				"it: an operator setting it would get no effect and no error", name)
		}
	}
}

func readmeEnvNames(t *testing.T, src string) []string {
	t.Helper()
	const heading = "## Configuration (environment variables)"
	lines := strings.Split(src, "\n")
	inSection := false
	seen := map[string]bool{}
	var out []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == heading {
			inSection = true
			continue
		}
		if inSection && strings.HasPrefix(trimmed, "## ") {
			break
		}
		if !inSection || !strings.HasPrefix(trimmed, "|") {
			continue
		}
		cells := strings.Split(trimmed, "|")
		if len(cells) < 3 {
			continue
		}
		name := strings.Trim(strings.TrimSpace(cells[1]), "`")
		if !strings.HasPrefix(name, "SCANTTY_") || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func loaderEnvNames(t *testing.T, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}

	seen := map[string]bool{}
	var out []string
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			values := spec.(*ast.ValueSpec)
			for i, name := range values.Names {
				if !strings.HasPrefix(name.Name, "env") || i >= len(values.Values) {
					continue
				}
				literal, ok := values.Values[i].(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					t.Fatalf("%s: const %s must have a string literal value", path, name.Name)
				}
				value, err := strconv.Unquote(literal.Value)
				if err != nil {
					t.Fatalf("%s: decoding const %s: %v", path, name.Name, err)
				}
				if seen[value] {
					continue
				}
				seen[value] = true
				out = append(out, value)
			}
		}
	}
	sort.Strings(out)
	return out
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}
