package tui

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"gopkg.in/yaml.v3"
)

// WHERE THIS PACKAGE'S TESTS RUN, AND WHY A PLAIN `go test` LEAVES SOME OUT.
//
// internal/tui outgrew `go test`'s 600s per-package default: its derived sweeps
// walk every screen at every drawable width and height, they are real coverage,
// and they grow every time a sweep gains a screen, a state or a size. CI's
// bound was raised to 20 minutes as a stopgap (issue #171), which made a HUNG
// test take twenty minutes to report, and the package then crept to within
// three minutes of that bound too.
//
// So the tests are SCHEDULED, never dropped. testdata/heavy_tests.txt lists the
// heavy ones, and this TestMain turns that one file into the two halves of a
// partition:
//
//   - unset: the ordinary suite. The heavy tests are passed to -test.skip, so
//     `go test ./...` finishes well inside the default bound, locally and in
//     CI's ordinary job.
//   - heavy:N: exactly the tests marked N, via -test.run — one CI shard.
//   - heavy: every heavy test, for a developer running them locally.
//   - all: nothing is skipped; the whole package, the way it ran before.
//
// Both halves are regexps built from the SAME names, so a test is in exactly
// one of them by construction; TestTestSchedule_EveryTestRunsInExactlyOneJob
// checks that over every top-level test in the package rather than trusting
// it. Naming tests yourself with -run or -skip turns the schedule off in the
// ordinary mode — `go test -run TestJDEForm_NoRowRunsPastThePane` runs that
// sweep — because a developer who chose the tests has already said what to
// run; in a heavy mode it is refused, since the two selections cannot be
// combined into one -run pattern and silently preferring either would run
// something other than what was asked.
//
// WHY NOT A BUILD TAG, testing.Short OR A -run PATTERN IN THE WORKFLOW. A tag
// can leave a sweep compiled into no job at all, and it would mean moving the
// sweeps into files of their own. testing.Short needs a line in every heavy
// test and still runs everything on a plain `go test`, which is the run that
// times out. A pattern written into ci.yml is the partition kept in two copies
// that go stale separately. One list, read here and by CI, and checked in both
// directions, has none of those failure modes; the one it does have — a heavy
// test nobody listed — runs in the ordinary job and costs time, not coverage.
const (
	tuiTestsEnv    = "SCANTTY_TUI_TESTS"
	heavyTestsFile = "testdata/heavy_tests.txt"
)

func TestMain(m *testing.M) {
	flag.Parse()
	chosen := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "test.run" || f.Name == "test.skip" {
			chosen = true
		}
	})
	roster, err := readHeavyTests(heavyTestsFile)
	if err == nil {
		err = applyTestSchedule(os.Getenv(tuiTestsEnv), chosen, roster, flag.Set)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "internal/tui test schedule: %v\n", err)
		os.Exit(2)
	}
	os.Exit(m.Run())
}

// heavyTest is one line of testdata/heavy_tests.txt.
type heavyTest struct {
	shard int
	name  string
}

// heavyTestName is what a line may name: a top-level Go test identifier, which
// also guarantees the name carries no regexp syntax and no `/` — so the pattern
// built from it matches whole top-level names only and is one -run element.
var heavyTestName = regexp.MustCompile(`^Test[A-Za-z0-9_]*$`)

// readHeavyTests parses the roster strictly, because CI's shell reads the same
// file with a plain field split and the two readings must not disagree.
func readHeavyTests(path string) ([]heavyTest, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []heavyTest
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("%s:%d: want `<shard> <TestName>`, got %q", path, n, line)
		}
		shard, err := strconv.Atoi(fields[0])
		if err != nil || shard < 1 {
			return nil, fmt.Errorf("%s:%d: shard %q is not a positive integer", path, n, fields[0])
		}
		if !heavyTestName.MatchString(fields[1]) {
			return nil, fmt.Errorf("%s:%d: %q is not a top-level test name", path, n, fields[1])
		}
		if seen[fields[1]] {
			return nil, fmt.Errorf("%s:%d: %s is listed twice", path, n, fields[1])
		}
		seen[fields[1]] = true
		out = append(out, heavyTest{shard, fields[1]})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s lists no tests, so no shard job would run anything", path)
	}
	return out, nil
}

// testNamesPattern matches exactly the named top-level tests.
func testNamesPattern(names []string) string {
	return "^(?:" + strings.Join(names, "|") + ")$"
}

// applyTestSchedule sets -test.run or -test.skip for mode. chosen reports that
// the command line already set one of them.
func applyTestSchedule(mode string, chosen bool, roster []heavyTest, set func(name, value string) error) error {
	var all []string
	for _, h := range roster {
		all = append(all, h.name)
	}
	switch {
	case mode == "":
		if chosen {
			return nil
		}
		return set("test.skip", testNamesPattern(all))
	case mode == "all":
		return nil
	}
	if chosen {
		return fmt.Errorf("%s=%s selects the tests itself; unset it to choose tests with -run or -skip", tuiTestsEnv, mode)
	}
	if mode == "heavy" {
		return set("test.run", testNamesPattern(all))
	}
	if n, ok := strings.CutPrefix(mode, "heavy:"); ok {
		shard, err := strconv.Atoi(n)
		var names []string
		for _, h := range roster {
			if err == nil && h.shard == shard {
				names = append(names, h.name)
			}
		}
		if len(names) == 0 {
			return fmt.Errorf("%s=%s: %s marks no test for shard %q", tuiTestsEnv, mode, heavyTestsFile, n)
		}
		return set("test.run", testNamesPattern(names))
	}
	return fmt.Errorf("%s=%q: want unset, \"all\", \"heavy\" or \"heavy:<shard>\"", tuiTestsEnv, mode)
}

// packageTopLevelTests reads every top-level test this package compiles in the
// current build context, from source, so the check below cannot agree with
// itself by asking the schedule what exists.
func packageTopLevelTests(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var out []string
	for _, p := range paths {
		if ok, err := build.Default.MatchFile(".", p); err != nil || !ok {
			continue
		}
		file, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !isTestName(fn.Name.Name) ||
				fn.Type.Params == nil || len(fn.Type.Params.List) != 1 {
				continue
			}
			if star, ok := fn.Type.Params.List[0].Type.(*ast.StarExpr); ok {
				if sel, ok := star.X.(*ast.SelectorExpr); ok && sel.Sel.Name == "T" {
					out = append(out, fn.Name.Name)
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

// isTestName is `go test`'s rule: "Test", then nothing or a rune that is not
// lower case.
func isTestName(name string) bool {
	rest, ok := strings.CutPrefix(name, "Test")
	return ok && (rest == "" || !unicode.IsLower([]rune(rest)[0]))
}

// TestTestSchedule_EveryHeavyTestIsATopLevelTestHere fails on a roster line
// naming a test that does not exist — a rename or a deletion. The renamed test
// has not been lost (the skip no longer matches it, so it runs in the ordinary
// job), but its shard job would fail looking for a PASS it cannot find and the
// ordinary job would carry its cost, so the stale line is reported here, in the
// job a developer runs.
func TestTestSchedule_EveryHeavyTestIsATopLevelTestHere(t *testing.T) {
	roster, err := readHeavyTests(heavyTestsFile)
	if err != nil {
		t.Fatal(err)
	}
	exists := map[string]bool{}
	for _, name := range packageTopLevelTests(t) {
		exists[name] = true
	}
	for _, h := range roster {
		if !exists[h.name] {
			t.Errorf("%s lists %s (shard %d), and this package declares no such top-level test "+
				"in the default build: rename or remove the line", heavyTestsFile, h.name, h.shard)
		}
	}
}

// TestTestSchedule_EveryTestRunsInExactlyOneJob asks the schedule's own
// patterns, as the testing package will receive them, about every top-level
// test in the package: the ordinary job must run it or exactly one shard must,
// and never both. It is the partition proven over the real set rather than
// argued from how the patterns are built.
//
// NOT PROVEN here: that CI runs every shard. The workflow derives its matrix
// from the roster's shard numbers and each shard job checks that every name
// marked for it reported a PASS (.github/scripts/tui-heavy-shard.sh);
// TestTestSchedule_TheWorkflowRunsBothHalves checks the workflow still says so.
func TestTestSchedule_EveryTestRunsInExactlyOneJob(t *testing.T) {
	roster, err := readHeavyTests(heavyTestsFile)
	if err != nil {
		t.Fatal(err)
	}
	patternFor := func(mode, flagName string) *regexp.Regexp {
		t.Helper()
		var got string
		err := applyTestSchedule(mode, false, roster, func(name, value string) error {
			if name != flagName {
				t.Fatalf("mode %q set %s, want %s", mode, name, flagName)
			}
			got = value
			return nil
		})
		if err != nil {
			t.Fatalf("mode %q: %v", mode, err)
		}
		return regexp.MustCompile(got)
	}

	ordinarySkip := patternFor("", "test.skip")
	shards := map[int]*regexp.Regexp{}
	for _, h := range roster {
		if shards[h.shard] == nil {
			shards[h.shard] = patternFor(fmt.Sprintf("heavy:%d", h.shard), "test.run")
		}
	}
	allHeavy := patternFor("heavy", "test.run")

	tests := packageTopLevelTests(t)
	ordinary, heavy := 0, 0
	for _, name := range tests {
		var ranIn []string
		if !ordinarySkip.MatchString(name) {
			ranIn = append(ranIn, "the ordinary job")
			ordinary++
		}
		for shard, run := range shards {
			if run.MatchString(name) {
				ranIn = append(ranIn, fmt.Sprintf("shard %d", shard))
				heavy++
			}
		}
		if len(ranIn) != 1 {
			t.Errorf("%s runs in %d places %v, want exactly one", name, len(ranIn), ranIn)
		}
		if allHeavy.MatchString(name) == !ordinarySkip.MatchString(name) {
			t.Errorf("%s: %s=heavy and the ordinary suite disagree about whether it is heavy", name, tuiTestsEnv)
		}
	}
	if ordinary == 0 || heavy == 0 {
		t.Fatalf("of %d top-level tests the ordinary job runs %d and the shards %d: "+
			"a partition with an empty side proves nothing", len(tests), ordinary, heavy)
	}
}

// TestTestSchedule_ASelectionOnTheCommandLineIsNeverOverridden holds the two
// rules for -run/-skip given by hand: the ordinary mode steps aside, and a
// heavy mode refuses rather than guessing which selection was meant.
func TestTestSchedule_ASelectionOnTheCommandLineIsNeverOverridden(t *testing.T) {
	roster := []heavyTest{{1, "TestA"}, {2, "TestB"}}
	set := func(name, value string) error {
		t.Errorf("a hand-given selection was overridden: %s=%s", name, value)
		return nil
	}
	if err := applyTestSchedule("", true, roster, set); err != nil {
		t.Errorf("ordinary mode with -run: %v", err)
	}
	if err := applyTestSchedule("all", true, roster, set); err != nil {
		t.Errorf("all with -run: %v", err)
	}
	for _, mode := range []string{"heavy", "heavy:1"} {
		if err := applyTestSchedule(mode, true, roster, set); err == nil {
			t.Errorf("%s with -run was accepted; want a refusal", mode)
		}
	}
	for _, mode := range []string{"heavy:3", "heavy:x", "sweeps"} {
		if err := applyTestSchedule(mode, false, roster, set); err == nil {
			t.Errorf("%s=%s was accepted; want a refusal naming what is valid", tuiTestsEnv, mode)
		}
	}
}

type workflowFile struct {
	Env  map[string]any         `yaml:"env"`
	Jobs map[string]workflowJob `yaml:"jobs"`
}

type workflowJob struct {
	Needs    workflowNeeds     `yaml:"needs"`
	Env      map[string]any    `yaml:"env"`
	Outputs  map[string]string `yaml:"outputs"`
	Strategy workflowStrategy  `yaml:"strategy"`
	Steps    []workflowStep    `yaml:"steps"`
}

type workflowNeeds []string

func (n *workflowNeeds) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		*n = []string{value.Value}
		return nil
	}
	return value.Decode((*[]string)(n))
}

type workflowStrategy struct {
	FailFast *bool          `yaml:"fail-fast"`
	Matrix   workflowMatrix `yaml:"matrix"`
}

type workflowMatrix struct {
	Shard string `yaml:"shard"`
}

type workflowStep struct {
	ID  string         `yaml:"id"`
	Env map[string]any `yaml:"env"`
	Run string         `yaml:"run"`
}

func hasNeed(job workflowJob, need string) bool {
	for _, got := range job.Needs {
		if got == need {
			return true
		}
	}
	return false
}

func commandRunsGoTestAll(run string) bool {
	for _, line := range strings.Split(run, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == "go" && fields[1] == "test" {
			for _, field := range fields[2:] {
				if field == "./..." {
					return true
				}
			}
		}
	}
	return false
}

// TestTestSchedule_TheWorkflowRunsBothHalves validates the workflow's parsed
// job graph and commands. It also executes the plan step locally to prove that
// the real roster emits the matrix shards; GitHub's execution remains outside
// this test, and the shard script reports a shard that ran nothing.
func TestTestSchedule_TheWorkflowRunsBothHalves(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow workflowFile
	if err := yaml.Unmarshal(raw, &workflow); err != nil {
		t.Fatalf("parse ci.yml: %v", err)
	}

	ordinary, ok := workflow.Jobs["build-vet-test"]
	if !ok {
		t.Fatal("ci.yml has no build-vet-test job")
	}
	if _, set := workflow.Env[tuiTestsEnv]; set {
		t.Errorf("workflow sets %s: the ordinary job must use the default schedule", tuiTestsEnv)
	}
	if _, set := ordinary.Env[tuiTestsEnv]; set {
		t.Errorf("ordinary job sets %s: it must use the default schedule", tuiTestsEnv)
	}
	foundOrdinary := false
	for _, step := range ordinary.Steps {
		if commandRunsGoTestAll(step.Run) {
			foundOrdinary = true
			if _, set := step.Env[tuiTestsEnv]; set {
				t.Errorf("ordinary go test step sets %s: it must use the default schedule", tuiTestsEnv)
			}
		}
	}
	if !foundOrdinary {
		t.Error("ordinary job has no step invoking go test over ./...")
	}

	plan, ok := workflow.Jobs["tui-heavy-plan"]
	if !ok {
		t.Fatal("ci.yml has no tui-heavy-plan job")
	}
	if plan.Outputs["shards"] != "${{ steps.plan.outputs.shards }}" {
		t.Errorf("plan shards output = %q, want plan step's shards output", plan.Outputs["shards"])
	}
	var planRun string
	for _, step := range plan.Steps {
		if step.ID == "plan" {
			planRun = step.Run
		}
	}
	if planRun == "" {
		t.Fatal("plan job has no executable plan step")
	}

	heavy, ok := workflow.Jobs["tui-heavy"]
	if !ok {
		t.Fatal("ci.yml has no tui-heavy job")
	}
	if !hasNeed(heavy, "tui-heavy-plan") {
		t.Error("tui-heavy job does not need tui-heavy-plan")
	}
	if heavy.Strategy.FailFast == nil || *heavy.Strategy.FailFast {
		t.Error("tui-heavy strategy must explicitly disable fail-fast")
	}
	if heavy.Strategy.Matrix.Shard != "${{ fromJSON(needs.tui-heavy-plan.outputs.shards) }}" {
		t.Errorf("heavy shard matrix = %q, want plan output decoded with fromJSON", heavy.Strategy.Matrix.Shard)
	}
	foundShard := false
	for _, step := range heavy.Steps {
		fields := strings.Fields(step.Run)
		if len(fields) >= 4 && fields[0] == ".github/scripts/tui-heavy-shard.sh" && strings.Join(fields[1:4], " ") == `"${{ matrix.shard }}"` {
			foundShard = true
		}
	}
	if !foundShard {
		t.Error("tui-heavy has no step invoking the shard script with matrix.shard")
	}
	if !hasNeed(workflow.Jobs["release"], "tui-heavy") {
		t.Error("release job does not need tui-heavy")
	}

	t.Run("plan emits roster shards", func(t *testing.T) {
		for _, tool := range []string{"bash", "jq"} {
			if _, err := exec.LookPath(tool); err != nil {
				t.Skipf("%s unavailable: %v", tool, err)
			}
		}
		output := filepath.Join(t.TempDir(), "github-output")
		cmd := exec.Command("bash", "-c", planRun)
		cmd.Dir = filepath.Join("..", "..")
		cmd.Env = append(os.Environ(), "GITHUB_OUTPUT="+output)
		if got, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("run plan step: %v\n%s", err, got)
		}
		emitted, err := os.ReadFile(output)
		if err != nil {
			t.Fatal(err)
		}
		const prefix = "shards="
		line := strings.TrimSpace(string(emitted))
		if !strings.HasPrefix(line, prefix) {
			t.Fatalf("plan output %q has no shards output", line)
		}
		var got []int
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, prefix)), &got); err != nil {
			t.Fatalf("parse emitted shards: %v", err)
		}
		roster, err := readHeavyTests(filepath.Join("testdata", "heavy_tests.txt"))
		if err != nil {
			t.Fatal(err)
		}
		set := map[int]bool{}
		for _, test := range roster {
			set[test.shard] = true
		}
		var want []int
		for shard := range set {
			want = append(want, shard)
		}
		sort.Ints(want)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("emitted shards %v, want roster shards %v", got, want)
		}
	})
}
