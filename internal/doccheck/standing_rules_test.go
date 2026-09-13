package doccheck

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// WHY A CITATION OF A NUMBERED RULE IS CHECKED AT ALL.
//
// This module's prose cites numbered rules — "standing rule", "house rule" or
// bare "rule", each followed by a number — close to a hundred times, and for
// most of the project's life no file in the repository said what any of those numbers
// meant: the roster lived only in the task instructions for this work. A
// citation that resolves to nothing reads as though a rule had been checked
// while telling the reader nothing, and a numbered rule with no text is the
// easiest kind of claim to invent a meaning for. docs/standing-rules.md is the
// roster now, and this sweep is what keeps every citation pointing into it.
//
// PROVEN:
//
//   - TestDocs_EveryStandingRuleCitationIsInTheRoster — every "rule N" /
//     "rules N and M" in any text file under the module root (Go source,
//     comments and string literals alike, and every Markdown file) names a
//     number the roster defines; a citation by ORDINAL (an English ordinal
//     word in front of "standing rule") fails outright, because it cites the
//     roster in a spelling the numeric check cannot read; the roster's numbers run 1..N with no gap and
//     no repeat; and AGENTS.md points at the roster, so a reader who meets a
//     citation there can find it.
//
// NOT PROVEN, said plainly:
//
//   - That a citation names the RIGHT rule for the sentence it sits in. That is
//     a judgement about the prose, and no parser can make it.
//   - Anything about an UNNUMBERED "the standing rule" — the sentence either
//     states the substance beside it or it does not, which is again a reading.
//   - That the roster's wording matches what the rules were when they were
//     first written; docs/standing-rules.md records where its text came from.

// standingRulesRoster is the one file that defines what a numbered rule says.
const standingRulesRoster = "docs/standing-rules.md"

// rosterEntry is one rule's heading in the roster: "## 3. Never conflate …".
var rosterEntry = regexp.MustCompile(`(?m)^## (\d+)\. \S`)

// citeSep is what may sit between the words of a citation: whitespace, and the
// comment marker that starts the next line when a Go comment wraps between
// "rule" and its number.
const citeSep = `(?:\s|//)+`

// ruleCitation matches "rule N" in any letter case, "rules N and M", and a
// citation whose Go comment wraps between the word and its number. The
// character before "rule" may not be a letter, digit, underscore or hyphen, so "pre-rule 201" (a fixture's name) and an
// identifier ending in "rule" are not citations.
var ruleCitation = regexp.MustCompile(`(?i)(?:^|[^\w-])rules?` + citeSep +
	`(\d+)((?:` + citeSep + `?(?:,|and|or|&)` + citeSep + `?\d+)*)`)

// ordinalCitation matches a numbered rule cited by its ordinal instead.
var ordinalCitation = regexp.MustCompile(`(?i)\b(?:first|second|third|fourth|fifth|sixth|` +
	`seventh|eighth|ninth|tenth|eleventh|twelfth)` + citeSep + `(?:standing|house)` +
	citeSep + `rules?\b`)

var citedNumber = regexp.MustCompile(`\d+`)

func TestDocs_EveryStandingRuleCitationIsInTheRoster(t *testing.T) {
	mod := moduleRoot(t)
	defined := standingRuleNumbers(t, mod)

	agents, err := os.ReadFile(filepath.Join(mod, "AGENTS.md"))
	if err != nil {
		t.Fatalf("reading AGENTS.md: %v", err)
	}
	if !bytes.Contains(agents, []byte(standingRulesRoster)) {
		t.Errorf("AGENTS.md does not name %s. AGENTS.md cites numbered standing rules, and "+
			"a citation whose roster the reader cannot find is the defect this sweep exists "+
			"for; point at the roster from AGENTS.md (do not inline it there)", standingRulesRoster)
	}

	var cited int
	forEachTextFile(t, mod, func(path string, text string) {
		for _, m := range ruleCitation.FindAllStringSubmatchIndex(text, -1) {
			numbers := text[m[2]:m[3]] + text[m[4]:m[5]]
			for _, n := range citedNumber.FindAllString(numbers, -1) {
				cited++
				if defined[n] {
					continue
				}
				t.Errorf("%s:%d: this text cites rule %s, and %s defines no rule %s. Cite the "+
					"rule the sentence means, or state the substance instead of a number — "+
					"never a number the roster does not carry",
					relPath(mod, path), lineOf(text, m[2]), n, standingRulesRoster, n)
			}
		}
		for _, m := range ordinalCitation.FindAllStringIndex(text, -1) {
			t.Errorf("%s:%d: %q cites a standing rule by ordinal, which no check can resolve "+
				"against %s; cite it by its number instead",
				relPath(mod, path), lineOf(text, m[0]), text[m[0]:m[1]], standingRulesRoster)
		}
	})

	// A sweep that found no citation passes for the wrong reason.
	if cited == 0 {
		t.Fatal("no text under the module root cites a numbered rule; the sweep examined " +
			"nothing and its PASS means nothing")
	}
	t.Logf("%d numbered rule citations checked against %d rules in %s",
		cited, len(defined), standingRulesRoster)
}

// standingRuleNumbers reads the roster and answers which rule numbers it
// defines, failing on a roster that is missing, empty, or numbered with a gap
// or a repeat — each of which would let a citation resolve to a rule nobody
// wrote down, or to two.
func standingRuleNumbers(t *testing.T, mod string) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(mod, standingRulesRoster))
	if err != nil {
		t.Fatalf("reading the standing-rules roster: %v. Every numbered \"standing rule N\" "+
			"in this module is a citation of it, so without it each one resolves to nothing", err)
	}
	var numbers []int
	for _, m := range rosterEntry.FindAllStringSubmatch(string(raw), -1) {
		n, _ := strconv.Atoi(m[1])
		numbers = append(numbers, n)
	}
	if len(numbers) == 0 {
		t.Fatalf("%s defines no rule (no \"## N. …\" heading); every citation would fail "+
			"for a reason unrelated to the citation", standingRulesRoster)
	}
	defined := map[string]bool{}
	for i, n := range numbers {
		if n != i+1 {
			t.Fatalf("%s numbers its rules %v; they must run 1..N in order with no gap and "+
				"no repeat, or a citation can resolve to a rule nobody wrote or to two",
				standingRulesRoster, numbers)
		}
		defined[strconv.Itoa(n)] = true
	}
	return defined
}

// forEachTextFile hands fn every text file under the module root outside
// dot-directories. Which files may carry a citation is derived from what a
// file IS (valid UTF-8 with no NUL byte) rather than from a list of extensions,
// so a citation in a README, a testdata note or a script is swept too.
func forEachTextFile(t *testing.T, mod string, fn func(path, text string)) {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(mod, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && p != mod {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", mod, err)
	}
	sort.Strings(paths)
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("reading %s: %v", p, err)
		}
		if bytes.IndexByte(raw, 0) >= 0 || !utf8.Valid(raw) {
			continue
		}
		fn(p, string(raw))
	}
}

func lineOf(text string, offset int) int {
	return strings.Count(text[:offset], "\n") + 1
}

func relPath(mod, path string) string {
	if r, err := filepath.Rel(mod, path); err == nil {
		return r
	}
	return path
}
