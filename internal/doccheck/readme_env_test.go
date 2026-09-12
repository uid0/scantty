package doccheck

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// scanttyEnv matches one SCANTTY_-prefixed environment variable name.
var scanttyEnv = regexp.MustCompile(`SCANTTY_[A-Z0-9_]+`)

// TestDocs_TheReadmeEnvTableIsTheOneTheLoaderReads holds the one inventory in
// README.md that is not replaced by a pointer, because it cannot be: an
// operator setting up a host has to be able to READ the variables somewhere,
// and `go doc` will not print a table of them.
//
// So the table stays and is CHECKED instead. It drifted the ordinary way —
// SCANTTY_THEME was added to the loader and the table was not touched, so the
// documented set was a strict subset of the real one and the omission was
// silent in both directions: nothing failed, and the variable simply did not
// exist as far as a reader was concerned.
//
// It compares NAMES and nothing else. What a variable is FOR, and what it
// defaults to, are prose no test can judge — this only says the two lists name
// the same variables, which is the half that goes wrong by forgetting.
func TestDocs_TheReadmeEnvTableIsTheOneTheLoaderReads(t *testing.T) {
	mod := moduleRoot(t)

	readme := readFile(t, filepath.Join(mod, "README.md"))
	loader := readFile(t, filepath.Join(mod, "internal", "config", "config.go"))

	documented := envNames(readme)
	read := envNames(loader)

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

func envNames(src string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range scanttyEnv.FindAllString(src, -1) {
		if seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
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
