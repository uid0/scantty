package tui

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// list_nav_test.go — the rule that no BEHAVIOURAL sweep can hold for the whole
// app.
//
// The two behavioural sweeps each own a slice of the program and read a bar the
// way that slice draws it: jde_pane_fit_test.go's
// TestJDEForm_EveryMovementTokenIsNamedExactlyWhereItMoves walks every type
// embedding jdeScreen and reads a []actionBarItem, and
// list_bar_honesty_test.go's TestList_FooterNamesExactlyTheKeysThatWork walks
// every *ListScreen the nav tree reaches and parses a footer STRING. Between
// them they cover the surfaces whose bar is a machine-readable RECORD.
//
// Some thirty screens are outside both, and it is not an oversight that can be
// closed by adding them to a roster: their bar is a muted literal written
// straight into a strings.Builder inside View, so there is nothing to read
// structurally and nothing to press it against. list_nav.go's package comment
// records that as the standing exclusion.
//
// What CAN be held over all of them is the VOCABULARY — which keystrokes are
// navigation at all — and that is what this file does, by reading the package's
// own source. A key nothing anywhere spells cannot become bound on screen
// thirty-one without failing the build here, whatever kind of bar that screen
// draws.

// listNavSourceFiles is every non-test Go file of this package, read once.
//
// Derived from the directory rather than listed, for the reason
// TestJDEForm_NoSheetAnswersTheScrollQuestionItself derives its own set: a
// roster of files to check is a file that can be added without being checked,
// which is the omission this whole area keeps paying for.
func listNavSourceFiles(t *testing.T) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}
	out := map[string]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		out[name] = string(b)
	}
	if len(out) < 50 {
		t.Fatalf("the package source scan found %d files, which is far fewer than this "+
			"package has — the derivation is broken, not the app", len(out))
	}
	return out
}

// listNavCaseKeys pulls the keystroke literals out of every `case "…":` clause
// in a file, ignoring comments.
//
// A `case` over key STRINGS is how every screen in this package binds a key —
// the handlers switch on tea.KeyMsg.String() — so it is where a binding is
// visible in source. Comments are stripped first because list_nav.go's own
// documentation quotes the retired arm it exists to explain, and a check that
// failed on its own explanation would teach the next reader to delete the
// explanation.
func listNavCaseKeys(src string) map[string][]int {
	out := map[string][]int{}
	caseLine := regexp.MustCompile(`^\s*case\s+("(?:[^"\\]|\\.)*"\s*(?:,\s*"(?:[^"\\]|\\.)*"\s*)*):`)
	lit := regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`)
	inBlock := false
	for i, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if inBlock {
			if strings.Contains(trimmed, "*/") {
				inBlock = false
			}
			continue
		}
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		if strings.HasPrefix(trimmed, "/*") {
			inBlock = !strings.Contains(trimmed, "*/")
			continue
		}
		m := caseLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		for _, k := range lit.FindAllStringSubmatch(m[1], -1) {
			out[k[1]] = append(out[k[1]], i+1)
		}
	}
	return out
}

// TestListNav_NoSurfaceBindsARetiredChord: no screen in the package binds a
// keystroke that list_nav.go records as retired.
//
// THIS IS HALF ONE OF THE RULE, and it is the half a bar cannot report on
// itself: "the footer names a key that does nothing" is visible to anyone who
// presses it, while "a key acts and no bar in the program spells it" is
// discoverable only by pressing keys nothing told you about. It shipped for
// exactly that reason — twenty-four `case "ctrl+d", "pgdown":` arms across
// seventeen files, none of them named by any bar, after the four purchasing
// surfaces had already been brought into line.
//
// DERIVED on both axes: the files come from the directory and the keys come from
// listNavRetiredChords, so a screen added tomorrow is checked today and a chord
// retired tomorrow is enforced by adding one line to the map. The failure names
// the reason the map carries, because "ctrl+d is not allowed" without the reason
// is how a rule gets worked around instead of understood.
func TestListNav_NoSurfaceBindsARetiredChord(t *testing.T) {
	if len(listNavRetiredChords) == 0 {
		t.Fatal("listNavRetiredChords is empty, so this check asserted nothing")
	}
	for file, src := range listNavSourceFiles(t) {
		keys := listNavCaseKeys(src)
		for chord, why := range listNavRetiredChords {
			for _, line := range keys[chord] {
				t.Errorf("%s:%d binds %q. That chord is retired: %s\n"+
					"Every list surface in the app must bind the same navigation set, or "+
					"the same key pages one list and does nothing on the next — and no bar "+
					"in this program spells a chord, so a binding here is a key the "+
					"operator can only find by guessing.", file, line, chord, why)
			}
		}
	}
}

// TestListNav_TheVocabularyAndTheRetiredSetAreDisjoint: a keystroke cannot be
// both the navigation set's and retired.
//
// Without it the two halves of list_nav.go could disagree — listNavHint would
// promise a key the sweep above forbids anyone to bind — and the failure would
// show up as a footer naming a key nothing answers, which is the defect rather
// than the report of it.
func TestListNav_TheVocabularyAndTheRetiredSetAreDisjoint(t *testing.T) {
	for _, m := range listNavSet() {
		for _, k := range m.Keys {
			if why, retired := listNavRetiredChords[k]; retired {
				t.Errorf("%q is in the navigation set (it is spelled by %q) and also "+
					"recorded as retired (%s) — one of the two is wrong", k, m.Hint, why)
			}
		}
	}
}

// TestListNav_EveryHintSpellsExactlyTheKeysItClaims is the transcription rule
// over list_nav.go's own table, held against the sweep's table rather than by
// eye.
//
// listBarKeyNames (list_bar_honesty_test.go) is the independent transcription of
// what a footer TOKEN spells, and the honesty sweep parses real footers through
// it. If listNavSet's hints and that table disagree, the footer this vocabulary
// builds is pressed against a claim it did not make — the sweep would either
// skip a key or credit one, and both directions hide a defect. So each hint is
// split the way listNamedKeys splits a footer segment and required to resolve to
// exactly the keys the vocabulary says it spells.
func TestListNav_EveryHintSpellsExactlyTheKeysItClaims(t *testing.T) {
	for _, m := range listNavSet() {
		got := map[string]bool{}
		fields := strings.Fields(m.Hint)
		if len(fields) == 0 {
			t.Errorf("the navigation hint %q is empty", m.Hint)
			continue
		}
		head, ok := listBarKeyNames[fields[0]]
		if !ok {
			t.Errorf("the navigation hint %q leads with the token %q, which "+
				"listBarKeyNames does not know — the honesty sweep would fail on the "+
				"footer this hint builds", m.Hint, fields[0])
			continue
		}
		for _, k := range head {
			got[k] = true
		}
		for _, f := range fields[1:] {
			for _, k := range listBarAliasKeys[f] {
				got[k] = true
			}
		}
		want := map[string]bool{}
		for _, k := range m.Keys {
			want[k] = true
		}
		if !sameKeySet(got, want) {
			t.Errorf("the navigation hint %q spells %v, but listNavSet says it is the "+
				"affordance for %v — a hint credited with a key it does not spell is "+
				"this vocabulary making a claim on the bar's behalf",
				m.Hint, sortedKeys(got), m.Keys)
		}
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sameKeySet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}
