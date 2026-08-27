// The two behaviours every columnar sheet reads, held to living in ONE place.
//
// sc-jde-lift moved the "does this body scroll?" arithmetic and the status
// row's bound out of the purchasing screens and into jde_form.go. Moving them
// is only half the job: the reason they had to be moved is that a copy is easy
// to make and silent when it drifts, so this file is the half that keeps them
// moved.
//
// It has two source sweeps and two behavioural checks, and neither kind is
// sufficient alone:
//
//   - The SWEEPS say no sheet answers either question itself. That is a claim
//     about all ~35 files that render through this layer at once, including the
//     ones no test can construct, and syntax is the only place such a claim can
//     be checked.
//   - The BEHAVIOURAL checks say the answers the layer gives are the RIGHT ones
//     — that the scroll question agrees with the frame it describes, and that
//     the bound is applied to a real screen's real status row at the widths
//     this project checks.
//
// See the READ THIS BEFORE DELETING IT note on
// TestJDEForm_EveryTextRowIsSizedByTheLayer (jde_cells_test.go) for why source
// inspection is a deliberate, stated exception to this project's test-quality
// rule rather than a violation of it. The same argument applies here for the
// same reason: every defect on this branch of work has recurred by a fix going
// exactly as far as the list of screens somebody was handed.
package tui

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/uid0/scantty/internal/omsapi"
)

// jdeLayerFile is the one file the shared columnar layer lives in. Everything
// else in the package is a SHEET as far as these sweeps are concerned.
const jdeLayerFile = "jde_form.go"

// jdeParsePackage parses the package's non-test source, which is what both
// sweeps judge.
func jdeParsePackage(t *testing.T) (*token.FileSet, map[string]*ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ParseComments)
	if err != nil {
		t.Fatalf("parsing the package: %v", err)
	}
	files := map[string]*ast.File{}
	for _, pkg := range pkgs {
		for name, f := range pkg.Files {
			files[name] = f
		}
	}
	if len(files) == 0 {
		t.Fatal("parsed no files; the sweeps below would pass vacuously")
	}
	return fset, files
}

// jdeIsLayer reports whether a parsed file is the shared layer itself.
func jdeIsLayer(path string) bool { return strings.HasSuffix(path, jdeLayerFile) }

// ---------------------------------------------------------------------------
// Sweep 1: no sheet answers the scroll question itself
// ---------------------------------------------------------------------------

// jdeLayerOnlyMarker is how the layer NOMINATES a helper as its own. A
// declaration in jde_form.go whose doc comment carries this line may not be
// named anywhere else in the package's non-test source.
//
// The marker is on the DECLARATION rather than in a roster here because a
// roster kept in a test is exactly the hand-maintained list this project has
// been bitten by: adding a new budget helper to the layer and forgetting to add
// it to a list would fail silently, which is how three dead keys reached an
// operator's terminal. Marking it is a one-line edit in the same field of view
// as the function being written, and an omission there is visible to the next
// reader of the layer rather than invisible to everybody.
const jdeLayerOnlyMarker = "jde:layer-only"

// TestJDEForm_NoSheetAnswersTheScrollQuestionItself fails if any sheet computes
// a row budget, measures a bar, or compares a body's length against one.
//
// This is the door sc-jde-lift closed. Before it, "does this body scroll?" was
// answered by roughly fifty per-sheet copies in two shapes — `s.bodyRows()`
// against `body.Len()` on the sheets with a one-row bar, and a hand-written
// `bodyRowsForBar(actionBarRowsFor(...))` on the three purchasing screens — and
// the copies did not agree with each other at the edges. One of them had
// already been rewritten once because it had been asking ClampScroll instead,
// which reserves the two indicator rows and so said "scrollable" from two lines
// before the window really moves.
//
// A bar that names UP/DN, PgUp/PgDn or Home/End over a body that cannot move
// breaks the standing rule that a key the bar names must act; a bar that stays
// silent over one that CAN move strands the operator on the first paneful. Both
// are one wrong copy away, and neither shows up as a compile error.
//
// Two independent nets, because the first is a rule about NAMES and a
// determined copy can avoid names:
//
//	the marked layer helpers   — bodyRows, bodyRowsForBar, actionBarRowsFor and
//	                             jdeLines.Scrolls carry a jde:layer-only line in
//	                             their doc comment, and no sheet may name them.
//	Len() in a comparison      — the arithmetic itself. Counting a body's lines
//	                             for any other purpose (the add-line tally
//	                             subtracts what it has already added) is
//	                             untouched; comparing that count against
//	                             anything is the scroll question by another
//	                             route.
func TestJDEForm_NoSheetAnswersTheScrollQuestionItself(t *testing.T) {
	fset, files := jdeParsePackage(t)

	// The forbidden set is DERIVED from the layer's own markers.
	layerOnly := map[string]bool{}
	for path, f := range files {
		if !jdeIsLayer(path) {
			continue
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Doc == nil {
				continue
			}
			if strings.Contains(fn.Doc.Text(), jdeLayerOnlyMarker) {
				layerOnly[fn.Name.Name] = true
			}
		}
	}
	if len(layerOnly) == 0 {
		t.Fatalf("no declaration in %s carries a %q marker, so this sweep would "+
			"pass over any sheet at all. The marker is what makes the forbidden set "+
			"derived rather than restated here; if a helper stopped being "+
			"layer-only, say so at the declaration", jdeLayerFile, jdeLayerOnlyMarker)
	}

	pkg := jdeFuncIndex(files)
	for path, f := range files {
		if jdeIsLayer(path) {
			continue
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			bodies := jdeLinesLocals(fn, pkg)
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch v := n.(type) {
				case *ast.CallExpr:
					// The callee only. A parameter that happens to be spelled
					// `bodyRows` is a name, not a call to the layer.
					if id, ok := v.Fun.(*ast.Ident); ok && layerOnly[id.Name] {
						jdeReportLayerOnly(t, fset, path, id)
					}
				case *ast.SelectorExpr:
					if layerOnly[v.Sel.Name] {
						jdeReportLayerOnly(t, fset, path, v.Sel)
					}
				case *ast.BinaryExpr:
					switch v.Op {
					case token.GTR, token.LSS, token.GEQ, token.LEQ, token.EQL, token.NEQ:
						if jdeIsBodyLenCall(v.X, bodies, pkg) || jdeIsBodyLenCall(v.Y, bodies, pkg) {
							t.Errorf("%s: a jdeLines' Len() is compared here. That comparison IS "+
								"the scroll question, whatever it is compared against; ask "+
								"bodyScrolls / bodyScrollsForBar so the answer and the window "+
								"stay one expression", fset.Position(v.Pos()))
						}
					}
				}
				return true
			})
		}
	}
}

func jdeReportLayerOnly(t *testing.T, fset *token.FileSet, path string, id *ast.Ident) {
	t.Helper()
	t.Errorf("%s: %s calls %s, which is the shared layer's own. A sheet asks "+
		"bodyScrolls / bodyScrollsForBar whether its body moves, and "+
		"bodyAvail / bodyAvailForBar / scrollRows for the rows it has — those "+
		"are the same expression the frame windows with, so they cannot part "+
		"company with it. A copy here can, and has",
		fset.Position(id.Pos()), path, id.Name)
}

// jdeIsBodyLenCall reports a `<a jdeLines>.Len()` call.
//
// The receiver has to be narrowed to a jdeLines or the check is useless:
// strings.Builder has a Len() too, and two sheets legitimately compare one
// against zero while assembling a styled run. There is no type checker here, so
// the narrowing is syntactic — a local or parameter DECLARED as *jdeLines, or a
// call to a function of this package that RETURNS one — which is every spelling
// the sheets actually use (`body.Len()`, `l.Len()`, `s.chooseBody().Len()`).
func jdeIsBodyLenCall(e ast.Expr, locals map[string]bool, pkg map[string][]*ast.FuncDecl) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Len" {
		return false
	}
	switch recv := sel.X.(type) {
	case *ast.Ident:
		return locals[recv.Name]
	case *ast.CallExpr:
		name := ""
		switch fun := recv.Fun.(type) {
		case *ast.Ident:
			name = fun.Name
		case *ast.SelectorExpr:
			name = fun.Sel.Name
		}
		for _, fn := range pkg[name] {
			if jdeReturnsLines(fn) {
				return true
			}
		}
	}
	return false
}

// jdeLinesLocals names every identifier in fn that holds a *jdeLines: its
// parameters, its receivers' locals declared from a composite literal, and
// anything assigned from a function that returns one.
func jdeLinesLocals(fn *ast.FuncDecl, pkg map[string][]*ast.FuncDecl) map[string]bool {
	out := map[string]bool{}
	if fn.Type.Params != nil {
		for _, field := range fn.Type.Params.List {
			if !jdeIsLinesPtr(field.Type) {
				continue
			}
			for _, name := range field.Names {
				out[name.Name] = true
			}
		}
	}
	if fn.Body == nil {
		return out
	}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, lhs := range as.Lhs {
			id, ok := lhs.(*ast.Ident)
			if !ok || i >= len(as.Rhs) {
				continue
			}
			if jdeYieldsLines(as.Rhs[i], pkg) {
				out[id.Name] = true
			}
		}
		return true
	})
	return out
}

// jdeYieldsLines reports an expression whose value is a *jdeLines.
func jdeYieldsLines(e ast.Expr, pkg map[string][]*ast.FuncDecl) bool {
	switch v := e.(type) {
	case *ast.UnaryExpr: // &jdeLines{}
		if v.Op == token.AND {
			if lit, ok := v.X.(*ast.CompositeLit); ok {
				id, ok := lit.Type.(*ast.Ident)
				return ok && id.Name == "jdeLines"
			}
		}
	case *ast.CallExpr:
		name := ""
		switch fun := v.Fun.(type) {
		case *ast.Ident:
			name = fun.Name
		case *ast.SelectorExpr:
			name = fun.Sel.Name
		}
		for _, fn := range pkg[name] {
			if jdeReturnsLines(fn) {
				return true
			}
		}
	}
	return false
}

// jdeReturnsLines reports whether fn's first result is a *jdeLines.
func jdeReturnsLines(fn *ast.FuncDecl) bool {
	if fn.Type.Results == nil || len(fn.Type.Results.List) == 0 {
		return false
	}
	return jdeIsLinesPtr(fn.Type.Results.List[0].Type)
}

func jdeIsLinesPtr(e ast.Expr) bool {
	star, ok := e.(*ast.StarExpr)
	if !ok {
		return false
	}
	id, ok := star.X.(*ast.Ident)
	return ok && id.Name == "jdeLines"
}

// ---------------------------------------------------------------------------
// Sweep 2: no sheet draws its own status row
// ---------------------------------------------------------------------------

// TestJDEForm_EveryStatusRowComesFromTheLayer fails if any sheet hands a frame
// a status row the layer did not bound.
//
// The status row is ONE row of the frame and it cannot fold, so an unbounded
// message is cut by clampToBox — which drops runes off the END of a styled
// string and takes the closing SGR reset with them, leaving the terminal
// coloured for everything drawn afterwards. That bound used to be a purchasing
// helper (poStatusError), applied on three screens and on none of the other
// thirty-odd that render through this layer.
//
// The check is written from the FRAME's side rather than as a list of screens,
// and both the frame set and the position of the status argument are read out
// of jde_form.go: any frame variant added later is swept the moment it declares
// a `status string` parameter, without anyone remembering to add it here.
//
// What is allowed in that position is the empty string (a frame that draws no
// status row at all) and any expression that passes through statusRow or
// fitStatus — including one that goes through a sheet's own helper, which is
// followed rather than trusted. StyleStatusError.Render is not by itself
// forbidden anywhere: it is how a dozen screens mark a BODY line, and a body
// line folds.
func TestJDEForm_EveryStatusRowComesFromTheLayer(t *testing.T) {
	fset, files := jdeParsePackage(t)

	// frames[name] is the index of the `status string` parameter, derived from
	// the layer's own declarations.
	frames := map[string]int{}
	for path, f := range files {
		if !jdeIsLayer(path) {
			continue
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Type.Params == nil {
				continue
			}
			if i, ok := jdeStatusParam(fn); ok {
				frames[fn.Name.Name] = i
			}
		}
	}
	if len(frames) == 0 {
		t.Fatalf("no method in %s takes a `status string` parameter, so this sweep "+
			"has nothing to check. The frame set is derived from those declarations; "+
			"if the status row stopped being a frame argument, this test needs "+
			"rewriting rather than deleting", jdeLayerFile)
	}

	// A sheet may declare a method of its own with a frame's NAME (po_edit wraps
	// `frame` to supply the verb). Those wrappers are checked through their own
	// body, where they call the layer; their call sites are not the layer's.
	shadow := map[string]bool{} // "ReceiverType.method"
	for path, f := range files {
		if jdeIsLayer(path) {
			continue
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv == nil {
				continue
			}
			if _, isFrame := frames[fn.Name.Name]; isFrame {
				shadow[jdeRecvType(fn)+"."+fn.Name.Name] = true
			}
		}
	}

	pkg := jdeFuncIndex(files)
	for path, f := range files {
		if jdeIsLayer(path) {
			continue
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			recv := jdeRecvType(fn)
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				idx, isFrame := frames[sel.Sel.Name]
				if !isFrame || idx >= len(call.Args) {
					return true
				}
				// `s.frame(…)` inside a type that declares its own `frame` is the
				// wrapper, not the layer. `s.jdeScreen.frame(…)` always is.
				if !jdeNamesEmbeddedScreen(sel.X) && shadow[recv+"."+sel.Sel.Name] {
					return true
				}
				if !jdeStatusFromLayer(call.Args[idx], fn, pkg, map[string]bool{}) {
					t.Errorf("%s: %s is handed a status row the layer did not bound. That row "+
						"is one unwrapped line of the frame, so an over-long message is cut by "+
						"clampToBox — taking the closing SGR reset with it and leaving the "+
						"terminal coloured. Build it with jdeScreen.statusRow (or bound the "+
						"text with fitStatus) so the pane is measured once, in one place",
						fset.Position(call.Args[idx].Pos()), sel.Sel.Name)
				}
				return true
			})
		}
	}
}

// jdeStatusParam returns the flattened index of a `status string` parameter.
func jdeStatusParam(fn *ast.FuncDecl) (int, bool) {
	i := 0
	for _, field := range fn.Type.Params.List {
		id, isString := field.Type.(*ast.Ident)
		for _, name := range field.Names {
			if name.Name == "status" && isString && id.Name == "string" {
				return i, true
			}
			i++
		}
		if len(field.Names) == 0 {
			i++
		}
	}
	return 0, false
}

// jdeRecvType names a method's receiver type, pointer or not.
func jdeRecvType(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}
	t := fn.Recv.List[0].Type
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	if id, ok := t.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// jdeNamesEmbeddedScreen reports `x.jdeScreen`, the spelling a sheet uses when
// it reaches past its own shadowing method to the layer's.
func jdeNamesEmbeddedScreen(e ast.Expr) bool {
	sel, ok := e.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "jdeScreen"
}

// jdeFuncIndex maps every non-test function and method name to its declaration,
// so the status-argument check can follow a sheet's own helper instead of
// trusting it. A name declared on two types is indexed once; both would have to
// bottom out in the layer for the check to pass, which is the safe direction.
func jdeFuncIndex(files map[string]*ast.File) map[string][]*ast.FuncDecl {
	out := map[string][]*ast.FuncDecl{}
	for _, f := range files {
		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Body != nil {
				out[fn.Name.Name] = append(out[fn.Name.Name], fn)
			}
		}
	}
	return out
}

// jdeStatusFromLayer reports whether `e` is a status row the layer produced.
//
// Empty string is a frame that draws no status row. Otherwise the expression
// has to pass through statusRow or fitStatus somewhere: directly, through a
// local variable, or through a helper of this package whose returns do.
func jdeStatusFromLayer(e ast.Expr, in *ast.FuncDecl, pkg map[string][]*ast.FuncDecl, seen map[string]bool) bool {
	if lit, ok := e.(*ast.BasicLit); ok && lit.Kind == token.STRING && lit.Value == `""` {
		return true
	}
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.SelectorExpr:
			if v.Sel.Name == "statusRow" || v.Sel.Name == "fitStatus" {
				found = true
			}
		case *ast.Ident:
			switch v.Name {
			case "statusRow", "fitStatus":
				found = true
			}
		}
		return !found
	})
	if found {
		return true
	}
	// A local: every assignment to it in this function has to qualify.
	if id, ok := e.(*ast.Ident); ok && in != nil && in.Body != nil {
		assigned, all := false, true
		ast.Inspect(in.Body, func(n ast.Node) bool {
			as, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for i, lhs := range as.Lhs {
				lid, ok := lhs.(*ast.Ident)
				if !ok || lid.Name != id.Name || i >= len(as.Rhs) {
					continue
				}
				assigned = true
				if !jdeStatusFromLayer(as.Rhs[i], in, pkg, seen) {
					all = false
				}
			}
			return true
		})
		if assigned {
			return all
		}
	}
	// A helper of this package: follow what it returns.
	if call, ok := e.(*ast.CallExpr); ok {
		name := ""
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			name = fun.Name
		case *ast.SelectorExpr:
			name = fun.Sel.Name
		}
		if name == "" || seen[name] {
			// Recursion, not an answer. A helper that only ever returns itself
			// never reaches the layer, so this is the safe direction.
			return false
		}
		decls := pkg[name]
		if len(decls) == 0 {
			return false
		}
		// The guard is a STACK, not a memo: `s.statusLine()` is declared on four
		// storage screens and each of them returns storageSlotStatusLine, so a
		// guard that stayed set after the first one would fail the other three
		// for having already been visited.
		seen[name] = true
		defer delete(seen, name)
		for _, fn := range decls {
			// EVERY return has to reach the layer. Any one of them is not enough:
			// a helper with a bounded happy path and one raw early return is
			// exactly the shape this sweep exists to catch.
			everyReturn := true
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				ret, isRet := n.(*ast.ReturnStmt)
				if !isRet || len(ret.Results) == 0 {
					return true
				}
				if !jdeStatusFromLayer(ret.Results[0], fn, pkg, seen) {
					everyReturn = false
				}
				return true
			})
			if !everyReturn {
				return false
			}
		}
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// The answers themselves: the scroll question against the frame it describes
// ---------------------------------------------------------------------------

// jdeLiftSizes are the terminal sizes this project checks, plus the 24-row
// floor at each width. 80 columns is the width that must HOLD.
var jdeLiftSizes = []tea.WindowSizeMsg{
	{Width: 80, Height: 24}, {Width: 80, Height: 30}, {Width: 80, Height: 40},
	{Width: 100, Height: 24}, {Width: 100, Height: 30},
	{Width: 120, Height: 24}, {Width: 120, Height: 40},
}

// TestJDEScroll_TheAnswerMatchesTheFrameThatDrawsIt is the behavioural half of
// the lift: bodyScrolls and bodyScrollsForBar are checked against what the
// frame REALLY does with the same body, rather than against a restatement of
// the arithmetic.
//
// "The body scrolls" means the operator cannot see all of it at once, and the
// frame says so itself: Window and WindowFrom spend their first and last rows
// on the "↑ n more above" / "↓ n more below" markers exactly when they are
// holding lines back, and hand back every line untouched when they are not. So
// the property is checkable without repeating any of the sums — render the
// frame, look for a line the body has that the frame does not, and require the
// answer to agree.
//
// It walks body lengths either side of every budget at every size, with and
// without a pinned header and with a bar short enough for one row and long
// enough to fold. That is where the old copies disagreed with each other: at
// exactly `avail` and `avail-1` lines, and on a folded bar.
func TestJDEScroll_TheAnswerMatchesTheFrameThatDrawsIt(t *testing.T) {
	shortBar := []actionBarItem{{"Esc", "Back"}}
	longBar := []actionBarItem{
		{"Enter", "Open"}, {"E", "Edit"}, {"A", "Attachments"}, {"x", "Void"},
		{"UP/DN", "Move"}, {"PgUp/PgDn", "Page"}, {"Home/End", "Top/bottom"},
		{"r", "Reload"}, {"Esc", "Back"},
	}
	for _, size := range jdeLiftSizes {
		for _, headerRows := range []int{0, 1, 3} {
			for _, bar := range [][]actionBarItem{shortBar, longBar} {
				g := jdeScreen{}
				g.setSize(size)
				var header jdeHeader
				for i := 0; i < headerRows; i++ {
					header = header.add(jdeHeadContext, fmt.Sprintf("pinned %d", i))
				}
				// Straddle both budgets so the boundary cases are always hit.
				for _, n := range jdeLiftBodyLengths(g, headerRows, bar) {
					body := jdeLiftBody(n)

					wrapped := g.frameWrapped(header, body, 0, "", bar)
					if got, want := g.bodyScrollsForBar(body, headerRows, bar), jdeFrameHoldsBack(wrapped, body); got != want {
						t.Errorf("%dx%d, %d header row(s), %d bar row(s), %d body line(s): "+
							"bodyScrollsForBar says %v but frameWrapped %s\n%s",
							size.Width, size.Height, headerRows,
							actionBarRowsFor(g.barWidth(), bar), n, got,
							jdeHoldsBackWord(want), wrapped)
					}

					// frameWithHeader IS frameWrapped now (the non-wrapping bar was
					// what let eleven form screens run their legend off a
					// 51-column pane), so this half checks they have not drifted
					// apart again rather than checking a second budget.
					if plain := g.frameWithHeader(header, body, 0, "", bar); plain != wrapped {
						t.Errorf("%dx%d, %d header row(s), %d body line(s): frameWithHeader and "+
							"frameWrapped drew different frames\n--- frameWithHeader\n%s\n"+
							"--- frameWrapped\n%s", size.Width, size.Height, headerRows, n, plain, wrapped)
					}
				}
			}
		}
	}
}

// jdeLiftBodyLengths straddles both frames' budgets, so the ±1 boundary — where
// the copies this lift removed disagreed with each other — is always walked.
func jdeLiftBodyLengths(g jdeScreen, headerRows int, bar []actionBarItem) []int {
	seen := map[int]bool{}
	var out []int
	for _, mid := range []int{g.bodyAvailForBar(headerRows, nil), g.bodyAvailForBar(headerRows, bar)} {
		for _, n := range []int{mid - 2, mid - 1, mid, mid + 1, mid + 2} {
			if n >= 1 && !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	return out
}

// jdeLiftBody builds n DISTINCT lines. Distinct because a body of one repeated
// string looks the same however much of it is being held back, so a frame that
// dropped half of it would still contain every line it was given.
func jdeLiftBody(n int) *jdeLines {
	l := &jdeLines{}
	for i := 0; i < n; i++ {
		l.AddRow(i, fmt.Sprintf("line-%03d", i))
	}
	return l
}

// jdeFrameHoldsBack reports whether the rendered frame is missing any of the
// body's lines — the operator's own definition of "this scrolls".
func jdeFrameHoldsBack(frame string, body *jdeLines) bool {
	for _, line := range body.text {
		if !strings.Contains(frame, line) {
			return true
		}
	}
	return false
}

func jdeHoldsBackWord(holds bool) string {
	if holds {
		return "draws only part of the body"
	}
	return "draws all of it"
}

// ---------------------------------------------------------------------------
// The answers themselves: the status row on a real screen
// ---------------------------------------------------------------------------

// TestJDEStatus_ALongErrorStaysOnThePaneOnAConvertedSheet drives a sheet that
// is NOT one of the purchasing screens, because that is what the lift changed:
// the bound used to be a purchasing helper, so an OMS failure on any of the
// other thirty-odd converted sheets reached a row that cannot fold with nothing
// bounding it at all.
//
// It asserts through Root.View() at every width the project checks, clipped the
// way Root clips, and with the colour profile forced — lipgloss strips every
// escape when stdout is not a TTY, so a lost SGR reset and a present one are
// byte-identical in a test binary and the whole point of the bound would be
// invisible. jdeCells decodes what the terminal would actually paint.
//
// Two things have to hold, and the second is the one the bound exists for:
//
//	the row fits          — no line of the frame is wider than the pane, so
//	                        clampToBox has nothing to cut.
//	the colour closes     — the last cell of the pane's LAST row is not still
//	                        wearing the error colour. A cut styled string loses
//	                        its closing reset, and everything drawn after it on
//	                        that terminal comes out red.
func TestJDEStatus_ALongErrorStaysOnThePaneOnAConvertedSheet(t *testing.T) {
	withColorProfile(t, termenv.TrueColor)

	// An OMS failure with no code in the envelope: omsapi.parseError puts the
	// entire raw response body into the message, and this is the shape it
	// arrives in.
	const oms = "oms: http 502: cannot reach the gateway at " +
		"https://oms.internal.example.com/api/purchasing/suppliers/: dial tcp " +
		"10.42.7.19:443: connect: connection refused (after 3 attempts)"

	for _, width := range []int{80, 100, 120} {
		for _, height := range []int{24, 30} {
			s := NewSupplierFormScreen(Deps{}, "")
			r := newTestRoot(s)
			r, _ = jdeLiftUpdate(r, tea.WindowSizeMsg{Width: width, Height: height})
			s.errMsg = oms

			pane := clampToBox(r.View(), width, height)
			row := jdeLiftErrorRow(t, pane, width, height)

			// The whole terminal row, nav column and all — this is what the
			// operator's terminal draws, and anything past it is gone.
			if w := lipgloss.Width(row); w > width {
				t.Errorf("at %dx%d the status row is %d cells and the terminal is %d: %q",
					width, height, w, width, row)
			}
			// And the message inside the pane is inside the PANE's budget, so
			// clampToBox had nothing to take.
			if msg := jdeLiftPaneText(row); lipgloss.Width(msg) > screenBodyWidth(width) {
				t.Errorf("at %dx%d the status message is %d cells against a %d-cell pane: %q",
					width, height, lipgloss.Width(msg), screenBodyWidth(width), msg)
			}
			if !strings.Contains(row, "…") {
				t.Errorf("at %dx%d a %d-cell message was drawn on the status row with no "+
					"mark to say it was shortened: %q", width, height, lipgloss.Width(oms), row)
			}
			jdeLiftAssertColourCloses(t, pane, width, height)
		}
	}
}

// TestJDEStatus_TheWorkingLineIsBoundedToo is the other branch of the same row.
// A verb is usually a fixed word, but the add-line screen's names the work AND
// the subject, so it carries a supplier's name and a catalogue name — and it is
// rendered with StyleMuted, which loses its closing reset to a cut exactly as
// the error does.
func TestJDEStatus_TheWorkingLineIsBoundedToo(t *testing.T) {
	long := strings.Repeat("Consolidated Fastener & Industrial Supply ", 6)
	for _, size := range jdeLiftSizes {
		g := jdeScreen{}
		g.setSize(size)
		row := g.statusRow(true, long, "")
		if w := lipgloss.Width(row); w > screenBodyWidth(size.Width) {
			t.Errorf("at %dx%d the working line is %d cells against a %d-cell pane: %q",
				size.Width, size.Height, w, screenBodyWidth(size.Width), row)
		}
	}
	// And an unsized screen does NOT truncate: zero width means "not known
	// yet", and throwing away columns the terminal may well have is the defect
	// on the other side of this bound.
	if got := (jdeScreen{}).statusRow(true, long, ""); !strings.Contains(got, long) {
		t.Errorf("an unsized screen shortened the working line to %q; with no "+
			"WindowSizeMsg yet there is no pane to measure against, and Root's own "+
			"clamp is what decides", got)
	}
}

// jdeLiftUpdate sends one message through Root and returns the Root back.
func jdeLiftUpdate(r Root, msg tea.Msg) (Root, tea.Cmd) {
	m, cmd := r.Update(msg)
	return m.(Root), cmd
}

// jdeLiftErrorRow finds the status row in a clipped pane by its mark.
func jdeLiftErrorRow(t *testing.T, pane string, width, height int) string {
	t.Helper()
	for _, line := range strings.Split(pane, "\n") {
		if strings.Contains(line, "✗") {
			return line
		}
	}
	t.Fatalf("no status row is on the pane at %dx%d:\n%s", width, height, pane)
	return ""
}

// jdeLiftPaneText is the part of a rendered Root row that belongs to the
// screen, with the nav column and its rule taken off — what the pane's own
// budget is measured against.
func jdeLiftPaneText(row string) string {
	if i := strings.Index(row, "│"); i >= 0 {
		row = row[i+len("│"):]
	}
	// The escapes have to come off BEFORE the trim: the styled run opens with a
	// reset, so the pane's own two-column indent sits inside the string and
	// TrimSpace would not see it.
	return strings.TrimSpace(jdeLiftStripSGR(row))
}

// jdeLiftStripSGR removes escape sequences, leaving what the terminal paints.
func jdeLiftStripSGR(s string) string {
	var b strings.Builder
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		if runes[i] == '\x1b' && i+1 < len(runes) && runes[i+1] == '[' {
			j := i + 2
			for j < len(runes) && !(runes[j] >= '@' && runes[j] <= '~') {
				j++
			}
			i = j
			continue
		}
		b.WriteRune(runes[i])
	}
	return b.String()
}

// jdeLiftAssertColourCloses fails if any row of the pane ENDS with a style
// still open — which is exactly what a cut styled string leaves behind, and
// what then bleeds onto everything the terminal draws after it.
//
// It decodes the escapes rather than looking for a trailing reset, because the
// reset does not have to be last: a styled run in the middle of a row is closed
// and followed by plain padding, and a test that demanded a trailing "\x1b[0m"
// would report every such row.
func jdeLiftAssertColourCloses(t *testing.T, pane string, width, height int) {
	t.Helper()
	for i, line := range strings.Split(pane, "\n") {
		if jdeLiftStyleLeftOpen(line) {
			t.Errorf("at %dx%d row %d ends with a style still open, so the colour bleeds "+
				"into everything drawn after it: %q", width, height, i+1, line)
		}
	}
}

// jdeLiftStyleLeftOpen reports whether the line finishes inside an SGR run.
func jdeLiftStyleLeftOpen(line string) bool {
	open := false
	runes := []rune(line)
	for i := 0; i < len(runes); i++ {
		if runes[i] != '\x1b' || i+1 >= len(runes) || runes[i+1] != '[' {
			continue
		}
		j := i + 2
		for j < len(runes) && !(runes[j] >= '@' && runes[j] <= '~') {
			j++
		}
		if j >= len(runes) {
			// The line was cut THROUGH an escape sequence, which is the same
			// defect with the terminal left in an unknown state.
			return true
		}
		if runes[j] == 'm' {
			open = true
			for _, p := range strings.Split(string(runes[i+2:j]), ";") {
				if p == "" || p == "0" {
					open = false
				}
			}
		}
		i = j
	}
	return open
}

// TestJDEStatus_AMultiLineErrorKeepsTheActionBarOnThePane is the ROW half of
// the same bound, and it is a different failure from the width one.
//
// A message carrying newlines does not overflow the width at all:
// lipgloss.Width reports the widest LINE, and nginx's stock 502 page is seven
// lines of at most 42 columns — inside the 49 an error has at 80 columns — so
// the bound let it through untouched and the frame appended a SEVEN-row block
// where its budget had reserved one. clampToBox drops from the bottom, so what
// went off the pane was the whole action bar: every key on the screen unnamed
// at once, the operator staring at HTML with nothing telling them how to leave.
// omsapi.parseError puts the entire raw body into APIError.Message whenever the
// envelope carries no code, so this is the ordinary shape of a gateway failure
// on any of the thirty-odd converted sheets.
//
// It asserts what the operator's terminal shows: one status row, the bar still
// on it, and nothing wider than the pane.
func TestJDEStatus_AMultiLineErrorKeepsTheActionBarOnThePane(t *testing.T) {
	withColorProfile(t, termenv.TrueColor)

	const gateway = "oms: http 502: <html>\r\n<head><title>502 Bad Gateway</title></head>\n" +
		"<body>\n<center><h1>502 Bad Gateway</h1></center>\n<hr><center>nginx</center>\n" +
		"</body>\n</html>\n"

	for _, width := range []int{80, 100, 120} {
		for _, height := range []int{24, 30} {
			s := NewSupplierFormScreen(Deps{}, "")
			r := newTestRoot(s)
			r, _ = jdeLiftUpdate(r, tea.WindowSizeMsg{Width: width, Height: height})
			s.errMsg = gateway

			pane := clampToBox(r.View(), width, height)
			rows := strings.Split(pane, "\n")

			marked := 0
			for _, row := range rows {
				if strings.Contains(row, "✗") {
					marked++
				}
			}
			if marked != 1 {
				t.Errorf("at %dx%d the status row is %d rows of the frame; it is ONE row and "+
					"the budget above it is sized for one:\n%s", width, height, marked, pane)
			}
			// The bar the supplier sheet always draws. Losing it is losing every
			// key on the screen at once, which is what this test is about.
			for _, key := range []string{"Enter", "Esc"} {
				if !strings.Contains(pane, key) {
					t.Errorf("at %dx%d the action bar is off the pane — no %q anywhere on it:\n%s",
						width, height, key, pane)
				}
			}
			for i, row := range rows {
				if w := lipgloss.Width(row); w > width {
					t.Errorf("at %dx%d row %d is %d cells and the terminal is %d: %q",
						width, height, i+1, w, width, row)
				}
			}
			if msg := jdeLiftPaneText(jdeLiftErrorRow(t, pane, width, height)); lipgloss.Width(msg) > screenBodyWidth(width) {
				t.Errorf("at %dx%d the flattened message is %d cells against a %d-cell pane: %q",
					width, height, lipgloss.Width(msg), screenBodyWidth(width), msg)
			}
			jdeLiftAssertColourCloses(t, pane, width, height)
		}
	}
}

// TestJDEStatus_AnUnmarkedRowKeepsWhatNoMarkTakes is the other direction of the
// same bound, and the rule it holds is the one this project states about every
// width: never discard data the terminal had room to show.
//
// The row's budget used to subtract a flat two columns for the "✗ " on EVERY
// branch, including the two that draw no mark at all — the muted working line
// and the storage sheets' standing note. Those rows have the whole pane, 51
// cells at the 80-column floor, and were cut at 49.
//
// Both surfaces here are real screens rendered through Root, because a bound
// measured on a frame in isolation cannot see the pane the terminal really
// gives. The marked branch is checked in the same breath: it must still reserve
// its two, or the mark pushes the message a cell past the pane.
func TestJDEStatus_AnUnmarkedRowKeepsWhatNoMarkTakes(t *testing.T) {
	withColorProfile(t, termenv.TrueColor)

	// Exactly the pane at 80 columns. It is what the rack generator really
	// says, and it fits only because no mark is drawn in front of it.
	const note = "the cards for these slots print from the slots list"

	for _, width := range []int{80, 100, 120} {
		for _, height := range []int{24, 30} {
			pane := width - 80 + 51 // screenBodyWidth is width less the nav column
			if pane != screenBodyWidth(width) {
				t.Fatalf("the pane at %d columns is %d, not %d", width, screenBodyWidth(width), pane)
			}

			// The NOTE branch: a fixed sentence the width of the pane.
			g := NewStorageSlotGenerateScreen(Deps{}, 7)
			g.phase = genPhaseResult
			g.result = &omsapi.GenerateRackResult{Rack: 7, CreatedCount: 2, Created: []string{"7A1", "7A2"}}
			gr := newTestRoot(g)
			gr, _ = jdeLiftUpdate(gr, tea.WindowSizeMsg{Width: width, Height: height})
			gpane := clampToBox(gr.View(), width, height)
			if !strings.Contains(jdeLiftStripSGR(gpane), note) {
				t.Errorf("at %dx%d the %d-cell note was shortened on a %d-cell pane that had room "+
					"for all of it:\n%s", width, height, lipgloss.Width(note), pane, gpane)
			}

			// The WORKING LINE branch: data-driven, so it grows to whatever the
			// pane gives and shows how much that is.
			a := NewPurchaseOrderAddLineScreen(Deps{}, &omsapi.PurchaseOrder{
				ID: "po-1", Number: "PO-2026-0042", Status: "draft",
				SupplierDetails: "Consolidated Fastener & Industrial Supply Company of Ohio",
			})
			ar := newTestRoot(a)
			ar, _ = jdeLiftUpdate(ar, tea.WindowSizeMsg{Width: width, Height: height})
			a.idIn.SetValue("MFR-PART-NUMBER-000000000000000000000000000000")
			a.phase = poAddPhaseLooking
			apane := clampToBox(ar.View(), width, height)
			work := ""
			for _, row := range strings.Split(apane, "\n") {
				if strings.Contains(jdeLiftStripSGR(row), "Looking up ") {
					work = jdeLiftPaneText(row)
				}
			}
			if work == "" {
				t.Fatalf("no working line on the pane at %dx%d:\n%s", width, height, apane)
			}
			if w := lipgloss.Width(work); w != pane {
				t.Errorf("at %dx%d the working line is %d cells on a %d-cell pane; with no mark "+
					"in front of it every one of them is its to spend: %q",
					width, height, w, pane, work)
			}

			// And the MARKED branch still reserves what the mark spends.
			s := NewSupplierFormScreen(Deps{}, "")
			sr := newTestRoot(s)
			sr, _ = jdeLiftUpdate(sr, tea.WindowSizeMsg{Width: width, Height: height})
			s.errMsg = strings.Repeat("x", pane)
			spane := clampToBox(sr.View(), width, height)
			row := jdeLiftPaneText(jdeLiftErrorRow(t, spane, width, height))
			if w := lipgloss.Width(row); w > pane {
				t.Errorf("at %dx%d the marked status row is %d cells against a %d-cell pane: %q",
					width, height, w, pane, row)
			}
			if !strings.Contains(row, "…") {
				t.Errorf("at %dx%d a %d-cell message plus a 2-cell mark was drawn on a %d-cell "+
					"pane with no sign it was shortened: %q", width, height, pane, pane, row)
			}
			jdeLiftAssertColourCloses(t, spane, width, height)
		}
	}
}

// TestJDEScroll_APaneTooShortToDrawTheBodyKeepsTheOperatorsPlace.
//
// frameScrolled hands the clamped offset back and both callers store it, so
// clamping against a body that gets NO rows — a pinned header that fills the
// pane — answered zero and overwrote where the operator had scrolled to. The
// frame is identical either way (nothing of the body is drawn at that height),
// which is exactly why it went unnoticed: the cost is only paid when the
// terminal grows back, and what it costs is the operator's place in a long
// order pad.
//
// The state that reaches it has now moved TWICE, and both times the guard below
// is what said so rather than the test quietly exercising something else. It
// was a written-down 80x12; then the budget stopped being floored and the
// header started giving way before the body's last row; and now the body's
// floor holds on a REFUSED pane too (jdeBodyAvail), so "the body gets no rows"
// is no longer a state a sized terminal can be in at all. What is left is the
// REFUSAL — the one path in frameScrolled that still skips the clamp — so that
// is what this searches for, asked of the same predicate the frame asks.
func TestJDEScroll_APaneTooShortToDrawTheBodyKeepsTheOperatorsPlace(t *testing.T) {
	build := func(t *testing.T) (*PurchaseOrderDetailScreen, Root) {
		t.Helper()
		s := NewPurchaseOrderDetailScreen(Deps{}, "po-1")
		s.loading = false
		s.po = poViewPO()
		r := poViewRootSized(t, s, 80, 40)
		var rows []string
		for i := 0; i < 40; i++ {
			rows = append(rows, fmt.Sprintf("PART-%04d\t%d", i, i+1))
		}
		s.orderPad = true
		s.orderPadExport = &omsapi.OrderPadExport{
			Text: strings.Join(rows, "\n"), Supplier: "Acme Fasteners & Industrial Supply",
			Filename: "PO-2026-0042-order.csv", LineCount: len(rows),
			// A header tall enough that a short pane leaves the body nothing,
			// which is the state the offset used to be thrown away in.
			MissingSku: []string{"Widget clamp", "Gear housing", "Bearing race"},
		}
		return s, r
	}

	s, r := build(t)
	for i := 0; i < 3; i++ {
		s.Update(poKeyMsg("pgdown"))
	}
	scrolled := r.View()
	place := s.padScroll
	if place == 0 {
		t.Fatalf("the pad did not scroll at 80x40, so there is no place to lose:\n%s", scrolled)
	}

	// The height the frame is refused at is DERIVED, not written down. A
	// written-down height would go on passing while exercising a different
	// state entirely, which is what it did the last two times the geometry
	// moved under it.
	padRefused := func(g *PurchaseOrderDetailScreen) bool {
		return g.tooShort(actionBarRowsFor(g.barWidth(), g.orderPadBar()), len(g.orderPadHeader()))
	}
	shortH := 0
	probe, pr := build(t)
	for h := 20; h >= 7; h-- {
		pr, _ = jdeLiftUpdate(pr, tea.WindowSizeMsg{Width: 80, Height: h})
		pr.View()
		if padRefused(probe) {
			shortH = h
			break
		}
	}
	if shortH == 0 {
		t.Fatal("no supported height refuses this frame, so the state this test is " +
			"about is unreachable and it needs rewriting rather than deleting")
	}

	// The terminal is dragged short — the frame is refused outright — and back.
	r, _ = jdeLiftUpdate(r, tea.WindowSizeMsg{Width: 80, Height: shortH})
	short := r.View()
	if !padRefused(s) {
		t.Fatalf("at 80x%d the layer still draws this frame, so this is not the state "+
			"under test:\n%s", shortH, short)
	}
	if !strings.Contains(s.View(), "Too short") {
		t.Fatalf("at 80x%d the frame is refused and the pane does not say so, so the "+
			"state under test is not the one on screen:\n%s", shortH, short)
	}
	if s.padScroll != place {
		t.Errorf("a pane too short to draw the body reset the scroll offset from %d to %d; "+
			"there is nothing to clamp against, and the operator's place is not the frame's "+
			"to throw away", place, s.padScroll)
	}

	r, _ = jdeLiftUpdate(r, tea.WindowSizeMsg{Width: 80, Height: 40})
	if got := r.View(); got != scrolled {
		t.Errorf("growing the terminal back did not return the operator to where they were.\n"+
			"before:\n%s\nafter:\n%s", scrolled, got)
	}
}
