package doccheck

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
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
const citeGap = `(?:\s|//)*`

// ruleCitation matches "rule N" in any letter case, "rules N and M", and a
// citation whose Go comment wraps between the word and its number. The
// character before "rule" may not be a letter, digit, underscore or hyphen, so "pre-rule 201" (a fixture's name) and an
// identifier ending in "rule" are not citations.
var ruleCitation = regexp.MustCompile(`(?i)(?:^|[^\w-])rules?` + citeSep +
	`(\d+(?:` + citeGap + `(?:,` + citeGap + `(?:and|or|&)?|and|or|&|-|–|to)` + citeGap + `\d+)*)`)

// ordinalCitation matches a numbered rule cited by its ordinal instead.
var ordinalCitation = regexp.MustCompile(`(?i)\b(?:first|second|third|fourth|fifth|sixth|` +
	`seventh|eighth|ninth|tenth|eleventh|twelfth)` + citeSep + `(?:standing|house)` +
	citeSep + `rules?\b`)

var citedNumber = regexp.MustCompile(`\d+`)
var citedRange = regexp.MustCompile(`(?i)^(?:\s|//)*(?:-|–|to)(?:\s|//)*$`)

type ruleReference struct {
	first   int
	last    int
	literal string
	offset  int
	invalid bool
}

type ordinalReference struct {
	text   string
	offset int
}

func citedRules(text string) ([]ruleReference, []ordinalReference) {
	var rules []ruleReference
	for _, match := range ruleCitation.FindAllStringSubmatchIndex(text, -1) {
		chainStart, chainEnd := match[2], match[3]
		locations := citedNumber.FindAllStringIndex(text[chainStart:chainEnd], -1)
		for i := 0; i < len(locations); i++ {
			firstLocation := locations[i]
			lastLocation := firstLocation
			if i+1 < len(locations) {
				next := locations[i+1]
				delimiter := text[chainStart+firstLocation[1] : chainStart+next[0]]
				if citedRange.MatchString(delimiter) {
					lastLocation = next
					i++
				}
			}
			firstText := text[chainStart+firstLocation[0] : chainStart+firstLocation[1]]
			lastText := text[chainStart+lastLocation[0] : chainStart+lastLocation[1]]
			first, firstErr := strconv.Atoi(firstText)
			last, lastErr := strconv.Atoi(lastText)
			rules = append(rules, ruleReference{
				first: first, last: last,
				literal: text[chainStart+firstLocation[0] : chainStart+lastLocation[1]],
				offset:  chainStart + firstLocation[0], invalid: firstErr != nil || lastErr != nil,
			})
		}
	}
	var ordinals []ordinalReference
	for _, match := range ordinalCitation.FindAllStringIndex(text, -1) {
		ordinals = append(ordinals, ordinalReference{text: text[match[0]:match[1]], offset: match[0]})
	}
	return rules, ordinals
}

func ruleReferenceError(rule ruleReference, highest int) string {
	if rule.invalid {
		return fmt.Sprintf("rule citation %q has an invalid or overflowing endpoint", rule.literal)
	}
	if rule.first > rule.last {
		return fmt.Sprintf("rule citation %q is reversed", rule.literal)
	}
	if rule.first < 1 || rule.first > highest {
		return fmt.Sprintf("rule citation %q names undefined endpoint %d", rule.literal, rule.first)
	}
	if rule.last > highest {
		return fmt.Sprintf("rule citation %q names undefined endpoint %d", rule.literal, rule.last)
	}
	return ""
}

func parseStandingRuleRoster(text string) (map[int]bool, error) {
	var numbers []int
	for _, match := range rosterEntry.FindAllStringSubmatch(text, -1) {
		n, _ := strconv.Atoi(match[1])
		numbers = append(numbers, n)
	}
	if len(numbers) == 0 {
		return nil, fmt.Errorf("defines no rule (no \"## N. …\" heading)")
	}
	defined := map[int]bool{}
	for i, n := range numbers {
		if n != i+1 {
			return nil, fmt.Errorf("numbers its rules %v; they must run 1..N in order with no gap and no repeat", numbers)
		}
		defined[n] = true
	}
	return defined, nil
}

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
		rules, ordinals := citedRules(text)
		for _, rule := range rules {
			cited++
			if problem := ruleReferenceError(rule, len(defined)); problem == "" {
				continue
			} else {
				t.Errorf("%s:%d: %s; %s defines rules 1-%d. Cite the rule the sentence means, "+
					"or state the substance instead of a number — never a number the roster does not carry",
					relPath(mod, path), lineOf(text, rule.offset), problem,
					standingRulesRoster, len(defined))
			}
		}
		for _, ordinal := range ordinals {
			t.Errorf("%s:%d: %q cites a standing rule by ordinal, which no check can resolve "+
				"against %s; cite it by its number instead",
				relPath(mod, path), lineOf(text, ordinal.offset), ordinal.text, standingRulesRoster)
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
func standingRuleNumbers(t *testing.T, mod string) map[int]bool {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(mod, standingRulesRoster))
	if err != nil {
		t.Fatalf("reading the standing-rules roster: %v. Every numbered \"standing rule N\" "+
			"in this module is a citation of it, so without it each one resolves to nothing", err)
	}
	defined, err := parseStandingRuleRoster(string(raw))
	if err != nil {
		t.Fatalf("%s %v; every citation would fail for a reason unrelated to the citation",
			standingRulesRoster, err)
	}
	return defined
}

func TestCitedRules(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		spans   [][2]int
		ordinal bool
	}{
		{"plain", "standing rule 4", [][2]int{{4, 4}}, false},
		{"capitalised", "Standing Rule 3", [][2]int{{3, 3}}, false},
		{"conjunction", "rules 5 and 6", [][2]int{{5, 5}, {6, 6}}, false},
		{"comma list", "rules 1, 3, and 5", [][2]int{{1, 1}, {3, 3}, {5, 5}}, false},
		{"hyphen range", "rules 1-4", [][2]int{{1, 4}}, false},
		{"en dash range", "rules 5–8", [][2]int{{5, 8}}, false},
		{"to range", "rules 9 to 11", [][2]int{{9, 11}}, false},
		{"wrapped comment", "standing\n//\trule 12", [][2]int{{12, 12}}, false},
		{"pre-rule", "pre-rule 201", nil, false},
		{"identifier", "house_rule 202", nil, false},
		{"ordinal", "the fifth " + "standing rule", nil, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rules, ordinals := citedRules(test.text)
			var spans [][2]int
			for _, rule := range rules {
				spans = append(spans, [2]int{rule.first, rule.last})
				if rule.offset < 0 || rule.offset >= len(test.text) {
					t.Fatalf("offset %d is outside input", rule.offset)
				}
			}
			if !reflect.DeepEqual(spans, test.spans) {
				t.Errorf("spans = %v, want %v", spans, test.spans)
			}
			if got := len(ordinals) > 0; got != test.ordinal {
				t.Errorf("ordinal detected = %v, want %v", got, test.ordinal)
			}
		})
	}
}

func TestCitedRules_Offsets(t *testing.T) {
	rules, _ := citedRules("rules 2-4")
	want := []ruleReference{{first: 2, last: 4, literal: "2-4", offset: 6}}
	if !reflect.DeepEqual(rules, want) {
		t.Errorf("citedRules() = %v, want %v", rules, want)
	}
}

func TestRuleReferenceError(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{"valid", "rules 1-8", ""},
		{"overflow", "rules 1-" + "999999999999999999999", "invalid or overflowing endpoint"},
		{"reversed", "rules 8-" + "5", "is reversed"},
		{"high endpoint", "rules 9-" + "12", "undefined endpoint 12"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rules, _ := citedRules(test.text)
			if len(rules) != 1 {
				t.Fatalf("citedRules() returned %d references, want 1", len(rules))
			}
			got := ruleReferenceError(rules[0], 11)
			if !strings.Contains(got, test.want) {
				t.Errorf("ruleReferenceError() = %q, want substring %q", got, test.want)
			}
		})
	}
}

func TestParseStandingRuleRoster(t *testing.T) {
	tests := []struct {
		name string
		text string
		want map[int]bool
	}{
		{"valid", "## 1. One\n## 2. Two\n", map[int]bool{1: true, 2: true}},
		{"gap", "## 1. One\n## 3. Three\n", nil},
		{"repeat", "## 1. One\n## 1. Again\n", nil},
		{"empty", "# Standing rules\n", nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseStandingRuleRoster(test.text)
			if test.want == nil {
				if err == nil {
					t.Fatalf("parseStandingRuleRoster() = %v, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseStandingRuleRoster(): %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("parseStandingRuleRoster() = %v, want %v", got, test.want)
			}
		})
	}
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
