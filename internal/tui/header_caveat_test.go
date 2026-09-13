package tui

// A CAVEAT on a pinned header is drawn WHOLE or NOT AT ALL.
//
// jdeFitHeader used to give ground ROW by row, so a caveat folded across rows
// was left half-present at exactly the heights nobody looks at, saying something
// its author never wrote. The instance that routed this was the line-void
// prompt: a remedy ending in the order's number folded so the bare number sat
// alone on the last row, the trim dropped exactly that row, and the pane said
// "search by number" with no number. That one was fixed by moving the number
// ahead of the phrase, which made one sentence safe by where its words break and
// left every other multi-row caveat depending on where ITS words break. So the
// layer holds it now (jdeHeader.addCaveat), and this file holds the layer to it
// three ways, none of which is sufficient alone:
//
//   - the TRIM, directly: a caveat is one unit, the rows a whole drop gives back
//     are offered to what went before it, and the header still comes to exactly
//     the budget the body was windowed against;
//   - the SOURCE: every folded sentence that reaches a pinned header goes in as
//     a block — a caveat, or a fitted value recorded with its reason — because a
//     fold handed to add or addBlock is independent rows to the trim however it
//     reads, and that is how the class survived;
//   - the PANES: every header site, in every state the header sweep builds it in,
//     at every honest width and every drawable height, draws each of its caveats
//     whole or not at all.
//
// Watched failing before it was trusted: with jdeHeadUnits reverted to one unit
// per row (the trim this replaced) the pane sweep reports partial caveats on the
// picker sites, the void prompts, the asset meter and document confirms and the
// rest of the converted set; and run against the builders as they stood before
// the conversion, the source sweep names every site that handed a fold to add,
// addBlock or an unrecorded addFitted.

import (
	"fmt"
	"go/ast"
	"sort"
	"strings"
	"testing"
)

// TestJDEHeader_TheTrimGivesACaveatUpWhole is jdeFitHeader's contract for a
// caveat, stated on a header small enough to read.
func TestJDEHeader_TheTrimGivesACaveatUpWhole(t *testing.T) {
	header := jdeHeader(nil).
		add(jdeHeadDecorative, "title").
		add(jdeHeadEssential, "essential").
		addCaveat(jdeHeadContext, []string{"caveat 1", "caveat 2", "caveat 3"}).
		add(jdeHeadContext, "after")
	const budget = 20
	for _, c := range []struct {
		keep int
		want []string
	}{
		// Nothing has to give.
		{6, []string{"title", "essential", "caveat 1", "caveat 2", "caveat 3", "after"}},
		// The title goes first, then the last context row; the caveat is whole.
		{5, []string{"essential", "caveat 1", "caveat 2", "caveat 3", "after"}},
		{4, []string{"essential", "caveat 1", "caveat 2", "caveat 3"}},
		// Now the caveat itself would have to give a row, so it goes WHOLE, and
		// the rows that frees are offered back to what went before it — "after"
		// and then the title — before any blank is drawn.
		{3, []string{"title", "essential", "after"}},
		{2, []string{"essential", "after"}},
		{1, []string{"essential"}},
		{0, []string{}},
	} {
		got := jdeFitHeader(header, budget, budget-c.keep)
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("keeping %d: got %q, want %q", c.keep, got, c.want)
		}
		if len(got) != c.keep {
			t.Errorf("keeping %d: the header came to %d rows, so the body windowed "+
				"against the other half of the split is drawn off by the difference",
				c.keep, len(got))
		}
	}

	// Where nothing that went before fits the rows a whole drop gave back, they
	// are drawn blank, at the END — still exactly the budget.
	lone := jdeHeader(nil).
		add(jdeHeadEssential, "essential").
		addCaveat(jdeHeadContext, []string{"a", "b", "c"})
	if got := jdeFitHeader(lone, 10, 8); strings.Join(got, "|") != "essential|" {
		t.Errorf("keeping 2 of a lone caveat: got %q, want the essential row and a blank", got)
	}

	// A caveat carrying an essential row goes no earlier than that row would
	// have — which is also why a builder may not fold one: the smallest budget
	// keeps one row, so the block would be given up whole anyway.
	ranked := jdeHeader(nil).
		add(jdeHeadContext, "context").
		addCaveat(jdeHeadEssential, []string{"x", "y"})
	if got := jdeFitHeader(ranked, 10, 8); strings.Join(got, "|") != "x|y" {
		t.Errorf("keeping 2: got %q, want the essential caveat kept over a context row", got)
	}

	// A one-row caveat is a row, not a block.
	if h := jdeHeader(nil).addCaveat(jdeHeadContext, []string{"one"}); len(h) != 1 || h[0].block != nil {
		t.Errorf("a one-row caveat was added as %+v", h)
	}
	if h := jdeHeader(nil).addCaveatBlock(jdeHeadContext, nil); len(h) != 0 {
		t.Errorf("an empty caveat block left %d row(s) behind", len(h))
	}
}

// jdeHeaderFoldRoots are the two PROSE folders every other fold in the package
// is built on: the sentence-to-rows primitives AGENTS.md names ("fold it"), the
// columnar layer's and the one outside it. Everything else that folds is DERIVED
// from these by call — jdeCaveatLines, jdeNoteLines, jdeFitRow, a screen's
// noteLines — so a new folding helper is swept the moment it calls one of them.
// jdeWrapTokens is deliberately not a root: it lays grid TOKENS into rows, which
// is a table, not a sentence.
var jdeHeaderFoldRoots = []string{"jdeWrapNote", "pickerWrap"}

// jdeHeaderFittedFolds are the header sites that hand a fold to addFitted rather
// than addCaveat, each with the reason its value is one whose HEAD stands on its
// own. A fitted block is re-drawn at fewer rows with its cut marked, which is
// right for an error body or an answer and wrong for a caveat, so the choice is
// recorded here where it can be read and fails stale.
//
// THE ENTRY IS PER SITE, NOT PER FOLD, and that is its limit: a builder recorded
// here for one fitted value could hand a CAVEAT to addFitted beside it and this
// sweep would not tell the two apart. The pane sweep below cannot catch that
// either, since it reads caveats off the blocks the builder declared — so a new
// fold in one of these four builders is a reason to read the builder.
var jdeHeaderFittedFolds = map[string]string{
	"PurchaseOrderAddLineScreen/headerLines": "the note is the screen's ANSWER to the last " +
		"keypress, and its first row is the header's one essential row — an answer cut after " +
		"its first clause still answered, while a dropped one did not; the failure DETAIL is " +
		"an OMS body whose head is the start of the error",
	"PurchaseOrderCreateScreen/headerLines": "the failure detail is an OMS body, and the answer " +
		"and standing fact each lead with the one essential row the phase may keep",
	"InventoryItemFormScreen/chainHeader": "a validation message says the sheet will not save, " +
		"and the last one's first row is the header's one essential row; a block that could " +
		"only be dropped whole would lose it at the smallest drawable budget",
	"PurchaseOrderDetailScreen/orderPadHeader": "the omitted-lines warning leads with the COUNT, " +
		"which is the essential row and stands on its own; the names after it are a list, and " +
		"a list cut with its cut marked still says how many went",
}

// jdeHeaderRowFolds are the header sites that hand a fold to add or addBlock —
// INDEPENDENT rows — each with the reason no row of it can be left misstating
// the rest. Empty is the expected state: a fold is one value.
var jdeHeaderRowFolds = map[string]string{
	"LocationReconcileScreen/headerLines": "the screen divides its own header rows (headerSplit) " +
		"before building the header, and the note and failure detail are each fitted into their " +
		"share by a renderer that marks its own cut — the note's fit and failDetailLines — so " +
		"what reaches the header is already a marked fit, not a fold for the layer to trim",
	"ReceiveFormScreen/headerLines": "the screen divides its own header rows (headerSplit) " +
		"before building the header, and the note and failure detail are each fitted into their " +
		"share by a renderer that marks its own cut — the note's fit and failDetailLines — so " +
		"what reaches the header is already a marked fit, not a fold for the layer to trim",
}

// jdeHeaderFolds is the package's set of folding functions, by name: the roots
// and every function that calls one (transitively), with the positions of each
// one's []string results — a fold taints only those, so `fitted, notes :=
// jdeFitRow(...)` taints notes and not the field.
func jdeHeaderFolds(t *testing.T, files map[string]*ast.File) map[string][]int {
	t.Helper()
	decls := map[string][]*ast.FuncDecl{}
	for _, f := range files {
		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Body != nil {
				decls[fn.Name.Name] = append(decls[fn.Name.Name], fn)
			}
		}
	}
	stringsAt := func(fn *ast.FuncDecl) []int {
		var at []int
		if fn.Type.Results == nil {
			return nil
		}
		i := 0
		for _, r := range fn.Type.Results.List {
			n := len(r.Names)
			if n == 0 {
				n = 1
			}
			arr, isArr := r.Type.(*ast.ArrayType)
			el, isIdent := ast.Expr(nil), false
			if isArr && arr.Len == nil {
				el = arr.Elt
				_, isIdent = el.(*ast.Ident)
			}
			for k := 0; k < n; k++ {
				if isIdent && el.(*ast.Ident).Name == "string" {
					at = append(at, i)
				}
				i++
			}
		}
		return at
	}
	folds := map[string][]int{}
	for _, root := range jdeHeaderFoldRoots {
		fns := decls[root]
		if len(fns) == 0 {
			t.Fatalf("%s is a fold root and is not declared in this package; the header "+
				"fold sweep would derive nothing from it", root)
		}
		folds[root] = stringsAt(fns[0])
	}
	for changed := true; changed; {
		changed = false
		for name, fns := range decls {
			if _, ok := folds[name]; ok {
				continue
			}
			for _, fn := range fns {
				at := stringsAt(fn)
				if len(at) == 0 {
					continue
				}
				calls := false
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					if call, ok := n.(*ast.CallExpr); ok {
						if _, ok := folds[jdeCalleeName(call)]; ok {
							calls = true
						}
					}
					return !calls
				})
				if calls {
					folds[name] = at
					changed = true
					break
				}
			}
		}
	}
	return folds
}

func jdeCalleeName(call *ast.CallExpr) string {
	switch f := call.Fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	}
	return ""
}

// jdeHeaderFoldCalls walks one function and returns every header-builder call
// whose arguments carry a fold, by builder name — through a local assigned from
// a fold, a range over one, or a closure that calls one.
func jdeHeaderFoldCalls(body *ast.BlockStmt, folds map[string][]int) map[string]int {
	tainted := map[string]bool{}
	var carries func(e ast.Node) bool
	carries = func(e ast.Node) bool {
		found := false
		ast.Inspect(e, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.CallExpr:
				if _, ok := folds[jdeCalleeName(v)]; ok {
					found = true
				}
			case *ast.Ident:
				if tainted[v.Name] {
					found = true
				}
			}
			return !found
		})
		return found
	}
	for changed := true; changed; {
		changed = false
		taint := func(id ast.Expr) {
			if ident, ok := id.(*ast.Ident); ok && ident.Name != "_" && !tainted[ident.Name] {
				tainted[ident.Name] = true
				changed = true
			}
		}
		ast.Inspect(body, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.AssignStmt:
				if len(v.Rhs) == 1 && len(v.Lhs) > 1 {
					if call, ok := v.Rhs[0].(*ast.CallExpr); ok {
						for _, i := range folds[jdeCalleeName(call)] {
							if i < len(v.Lhs) {
								taint(v.Lhs[i])
							}
						}
					}
					return true
				}
				for i, rhs := range v.Rhs {
					if i < len(v.Lhs) && !jdeIsHeaderBuild(rhs) && carries(rhs) {
						taint(v.Lhs[i])
					}
				}
			case *ast.RangeStmt:
				if v.Value != nil && carries(v.X) {
					taint(v.Value)
				}
			}
			return true
		})
	}
	out := map[string]int{}
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !jdeHeaderBuilders[sel.Sel.Name] {
			return true
		}
		for _, arg := range call.Args {
			if carries(arg) {
				out[sel.Sel.Name]++
				break
			}
		}
		return true
	})
	return out
}

// jdeHeaderBuilders are jdeHeader's own row builders, which is what a fold is
// judged by reaching.
var jdeHeaderBuilders = map[string]bool{
	"add": true, "addBlock": true, "addFitted": true, "addFittedBlock": true,
	"addCaveat": true, "addCaveatBlock": true,
}

// jdeIsHeaderBuild reports whether an expression is a chain of header builds —
// `h.add(...).addCaveat(...)` — whose result is a HEADER, not a fold, whatever
// its arguments carry.
func jdeIsHeaderBuild(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && jdeHeaderBuilders[sel.Sel.Name]
}

// jdeHeaderFoldSites is every function that hands a fold to a header builder,
// keyed `<receiver type>/<method>` (or the bare function name), split by which
// builder: a caveat, a fitted block, or independent rows.
func jdeHeaderFoldSites(t *testing.T) (caveats, fitted, rows map[string]bool) {
	t.Helper()
	_, files := jdeParsePackage(t)
	folds := jdeHeaderFolds(t, files)
	caveats, fitted, rows = map[string]bool{}, map[string]bool{}, map[string]bool{}
	for path, f := range files {
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			if jdeIsLayer(path) && fn.Recv != nil && jdeRecvName(fn) == "jdeHeader" {
				continue // the builders themselves
			}
			site := fn.Name.Name
			if fn.Recv != nil {
				site = jdeRecvName(fn) + "/" + site
			}
			for builder := range jdeHeaderFoldCalls(fn.Body, folds) {
				switch builder {
				case "addCaveat", "addCaveatBlock":
					caveats[site] = true
				case "addFitted", "addFittedBlock":
					fitted[site] = true
				default:
					rows[site] = true
				}
			}
		}
	}
	if len(caveats) == 0 {
		t.Fatal("no header site hands a fold to addCaveat, so this derivation found nothing " +
			"to judge — either the folds or the builders moved, and it needs rewriting")
	}
	return caveats, fitted, rows
}

// TestJDEHeader_EveryFoldReachesTheHeaderAsOneBlock: a sentence folded across
// rows is handed to a pinned header as ONE block — addCaveat, or addFitted where
// jdeHeaderFittedFolds says why — and never as independent rows.
//
// READ THIS BEFORE DELETING IT. This is a LINT, not a behavioural test, and is
// a deliberate exception to the test-quality rule. It reaches a property
// behaviour cannot: a fold handed to add or addBlock is invisible to the pane
// sweep, which reads only declared blocks, and only states somebody built are
// rendered. Its behavioural counterparts are
// TestJDEHeader_ACaveatIsDrawnWholeOrNotAtAll and
// TestJDEHeader_TheTrimGivesACaveatUpWhole.
//
// Derived from source because the set is the thing that kept being wrong: the
// fold was right at every site and the builder under it decided whether the
// trim saw one value or several, which no pane sweep over hand-built states can
// see for a state nobody built. The SITES come from the functions that call a
// header builder, the FOLDS from jdeHeaderFoldRoots by call, and the report is
// the caveat set this change converted.
func TestJDEHeader_EveryFoldReachesTheHeaderAsOneBlock(t *testing.T) {
	caveats, fitted, rows := jdeHeaderFoldSites(t)
	for site := range rows {
		if _, ok := jdeHeaderRowFolds[site]; !ok {
			t.Errorf("%s hands a folded sentence to a pinned header as INDEPENDENT rows "+
				"(add / addBlock), so a short pane keeps its head and drops its tail and the "+
				"operator reads a sentence nobody wrote. Use addCaveat — or addFitted, recorded "+
				"in jdeHeaderFittedFolds, where its head stands on its own", site)
		}
	}
	for site := range fitted {
		if _, ok := jdeHeaderFittedFolds[site]; !ok {
			t.Errorf("%s hands a folded sentence to a pinned header as a FITTED block, which "+
				"a short pane re-draws in part with its cut marked. A caveat is kept whole or "+
				"dropped whole (addCaveat); if this value's head really does stand on its own, "+
				"record why in jdeHeaderFittedFolds", site)
		}
	}
	for site, reason := range jdeHeaderFittedFolds {
		if !fitted[site] {
			t.Errorf("jdeHeaderFittedFolds records %s, which no longer hands a fold to addFitted. "+
				"A stale entry is an exemption waiting for the next one", site)
		}
		if reason == "" {
			t.Errorf("%s is recorded as a fitted fold with no reason", site)
		}
	}
	for site, reason := range jdeHeaderRowFolds {
		if !rows[site] {
			t.Errorf("jdeHeaderRowFolds records %s, which no longer hands a fold to add or "+
				"addBlock. A stale entry is an exemption waiting for the next one", site)
		}
		if reason == "" {
			t.Errorf("%s is recorded as a row fold with no reason", site)
		}
	}
	names := make([]string, 0, len(caveats))
	for site := range caveats {
		names = append(names, site)
	}
	sort.Strings(names)
	t.Logf("header sites carrying a caveat: %s", strings.Join(names, ", "))
}

// jdeHeaderCaveats is each caveat block in a built header, as the trimmed plain
// text of its rows.
func jdeHeaderCaveats(h jdeHeader) [][]string {
	var out [][]string
	for i := 0; i < len(h); {
		b := h[i].block
		if !b.whole() {
			i++
			continue
		}
		var rows []string
		for ; i < len(h) && h[i].block == b; i++ {
			rows = append(rows, strings.TrimSpace(stripANSI(h[i].Text)))
		}
		out = append(out, rows)
	}
	return out
}

// jdeCaveatOnPane reports whether a caveat's rows are on the pane whole, and
// whether any row of it is on the pane at all.
func jdeCaveatOnPane(lines, caveat []string) (whole, some bool) {
	for i := range lines {
		for _, row := range caveat {
			if lines[i] == row {
				some = true
			}
		}
		if lines[i] != caveat[0] || i+len(caveat) > len(lines) {
			continue
		}
		match := true
		for k, row := range caveat {
			if lines[i+k] != row {
				match = false
				break
			}
		}
		if match {
			return true, true
		}
	}
	return false, some
}

// TestJDEHeader_ACaveatIsDrawnWholeOrNotAtAll drives every pinned header, in
// every state jdeHeaderCases builds it in, at every honest width and every
// drawable height, and fails any pane that draws part of a caveat.
//
// The caveats are read off the header the screen's OWN builder produces at that
// pane, so a reworded or refolded caveat is measured as it is drawn; the panes
// are the clipped frame, because that is what the operator reads. It must also
// reach both outcomes a trim can have — a caveat drawn whole and one given up
// whole — or it passes by never trimming anything.
func TestJDEHeader_ACaveatIsDrawnWholeOrNotAtAll(t *testing.T) {
	whole, dropped := map[string]int{}, map[string]int{}
	widths, heights := receiveHonestWidths(), jdePaneHeights()
	for site, c := range jdeHeaderCases() {
		for name, mk := range c.states(site) {
			if mk == nil {
				t.Errorf("the %s case has no builder", name)
				continue
			}
			var bad []string
			for _, w := range widths {
				s := jdeAtPane(mk(), w, heights[len(heights)-1])
				if c.after != nil {
					c.after(s)
				}
				if len(jdeHeaderCaveats(c.header(s))) == 0 {
					continue // nothing folds into a caveat at this width
				}
				for _, h := range heights {
					s = jdeAtPane(s, w, h)
					if c.after != nil {
						c.after(s)
					}
					view := s.View()
					if jdeBarOfStripped(view) == nil {
						continue // refused: no header is drawn at all
					}
					raw := strings.Split(stripANSI(clampToBox(view, screenBodyCells(w), screenBodyRows(h))), "\n")
					lines := make([]string, len(raw))
					for i, line := range raw {
						lines[i] = strings.TrimSpace(line)
					}
					for _, caveat := range jdeHeaderCaveats(c.header(s)) {
						on, some := jdeCaveatOnPane(lines, caveat)
						switch {
						case on:
							whole[site]++
						case some:
							bad = append(bad, fmt.Sprintf("%dx%d draws part of %q", w, h,
								strings.Join(caveat, " ")))
						default:
							dropped[site]++
						}
					}
				}
			}
			if len(bad) > 0 {
				t.Errorf("%s: %d pane(s) draw a caveat in part, which reads as a sentence its "+
					"author never wrote, e.g. %s", name, len(bad), bad[0])
			}
		}
	}
	total := func(m map[string]int) (n int) {
		for _, v := range m {
			n += v
		}
		return n
	}
	// Every screen type whose builder hands a caveat to its header must have been
	// swept through both outcomes, or a new caveat site passes by never being
	// built in a state that draws it. Asked per TYPE, not per builder: a header
	// case names the frame, and the builder behind it is a closure this sweep
	// cannot read.
	caveatSites, _, _ := jdeHeaderFoldSites(t)
	reached := map[string]bool{}
	pickerReached := false
	for site := range whole {
		if dropped[site] > 0 {
			reached[strings.SplitN(site, "/", 2)[0]] = true
			if strings.Contains(site, "Pick") {
				pickerReached = true
			}
		}
	}
	for site := range caveatSites {
		recv := strings.SplitN(site, "/", 2)[0]
		if recv == "jdePickList" {
			// The shared picker is not a screen: it is reached through every
			// picker case, and one of them exercising both outcomes is the claim.
			if !pickerReached {
				t.Errorf("no picker header case drew the picker's caveat both whole and given " +
					"up whole at any pane")
			}
			continue
		}
		if !reached[recv] {
			t.Errorf("%s hands a caveat to its header and no case of %s drew one both whole "+
				"and given up whole at any pane, so the trim was never exercised there. Build "+
				"the state that draws it in jdeHeaderCases", site, recv)
		}
	}
	if total(whole) == 0 || total(dropped) == 0 {
		t.Fatalf("the sweep drew %d caveat(s) whole and gave %d up whole; it has to reach both "+
			"or it proved nothing about the trim", total(whole), total(dropped))
	}
	sites := make([]string, 0, len(whole)+len(dropped))
	for site := range whole {
		sites = append(sites, site)
	}
	for site := range dropped {
		if whole[site] == 0 {
			sites = append(sites, site)
		}
	}
	sort.Strings(sites)
	for _, site := range sites {
		t.Logf("%s: %d caveat(s) drawn whole, %d given up whole", site, whole[site], dropped[site])
	}
}
