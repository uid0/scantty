package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// list_nav_surfaces_test.go — WHICH surfaces the movement rule is PROVEN on, and
// which are excused, derived rather than listed.
//
// The rule is one sentence: on every list surface, the bar names exactly the
// keys that will act on that surface in its current state. Two behavioural
// sweeps hold it, and each can only read the kind of bar its half of the app
// draws:
//
//   - TestJDEForm_EveryMovementTokenIsNamedExactlyWhereItMoves walks every type
//     embedding jdeScreen, at every width and every drawable height, and reads
//     the columnar []actionBarItem off the rendered pane.
//   - TestList_FooterNamesExactlyTheKeysThatWork walks every *ListScreen the nav
//     tree reaches, at every row count in listRowCases, and parses the footer
//     STRING through a transcription table.
//
// A third kind of surface is outside both, and this file is where that is said
// out loud rather than left to be discovered: the receivers listed in
// listNavUnsweptReceivers below — the roster the classifier checks, and so the
// authority on how many there are — write their bar as a muted literal straight
// into a strings.Builder inside View. There is
// no record to read, so there is nothing for a sweep to press keys against —
// adding them to a roster would not help, because the roster is not what is
// missing.
//
// WHAT THIS FILE GUARANTEES INSTEAD, which is the honest narrowing: the SET of
// such surfaces cannot grow in silence. Every method in the package that binds a
// navigation keystroke is classified by its receiver — swept as columnar, swept
// as a ListScreen, or recorded here WITH A REASON — and a new one that is none
// of those fails the build. So the exclusion is a derived, reviewed list rather
// than an unexamined remainder, and the next reader meets it where the code is
// rather than in a pull request nobody re-reads.
//
// WHAT IT DOES NOT GUARANTEE, stated because a claim no check delivers is worse
// than no claim: it does not say those bars are honest. They are not. A
// typical one reads "j/k move · n new · E/enter edit · x delete · r refresh ·
// esc back" while binding the arrows, g/G, home/end and pgup/pgdn as well — and
// at 80 columns the pane gives a list body 51 cells, which that footer is
// already past before a single key is added to it. Naming the rest would make
// them longer and therefore LESS readable, and "a bar the operator cannot read
// is not honest, it is absent" (AGENTS.md). Closing it properly means giving
// each of those screens the folded footer and the row budget ListScreen already
// has (footerRows), which is a conversion of the same shape as sc-jde-lift and
// is why it is written down here rather than half-done in passing.
//
// THE HALF THAT IS CLOSED FOR THEM is the vocabulary: they no longer bind
// anything no bar in the program spells (list_nav.go). That half is held by
// PRESSING the retired chords on the fixture sets
// TestListNav_NoSurfaceBindsARetiredChord builds — that test's subtests are the
// authority on which and how many, and a count restated here is a number that
// has drifted every time it has been written down.
//
// A PROSE-BAR SURFACE IS REACHED BY A PRESS WHEN A TEST CAN BUILD ONE, which is
// a fact about the fixture and not about the bar: TextScroller and the cursor
// pickers are VALUES the sweep constructs and drives directly, where the rest
// are screens with no record to read and no cheap way to stand one up. Whichever
// of them a press reaches, an entry here says so in the same words.
//
// A SURFACE NO PRESS REACHES is not left silent either. What reaches it is this
// file — not a claim about its keys, but the guarantee that a new one cannot
// join the app unexamined.

// listNavUnsweptReceivers are the receivers that bind a keystroke the navigation
// vocabulary spells and that neither behavioural sweep can read a bar for.
//
// MOSTLY, that is because the bar is prose written straight into View. Several
// entries are not, and they are here rather than filtered out because the
// derivation is a set of KEY NAMES and cannot tell a movement `g` from a `g`
// that means generate, nor a list cursor from a FIELD-FORM focus pair — a filter
// clever enough to drop one would eventually drop a real one, and this map is
// where that limit is visible instead of invisible.
//
// THE FIELD-FORM ENTRIES ARRIVED WITH THE SECOND ALPHABET. Until listNavCaseKey
// learned to read bubbletea's tea.Key* constants, a receiver that bound movement
// as `case tea.KeyTab, tea.KeyDown:` was invisible here, so five appeared in no
// class at all — the same silence the TextScroller delegation hole was, one
// spelling further along. Those that are field forms whose focus wraps carry
// slotCardPrompt's exemption; the cursor lists among them are pressed rather
// than merely excused, and each entry says which it is.
//
// One entry per receiver, each saying what the surface is — not "excluded",
// which is the fact the map already carries, but what a reader would need to
// know to convert it. A stale entry fails as loudly as a missing one, so a
// screen that joins a swept class must be taken OUT of here.
//
// SCREENS HAVE ALREADY BEEN TAKEN OUT, AND THAT IS THE ONE SHAPE OF CHANGE THIS
// MAP IS MEANT TO RECORD — no count of them is written here, because it moves
// every time one converts and a number beside a derivation is the part of it
// that goes stale. The ones that held a TextScroller and spelled no key of their
// own went first: they were the clearest instance of the prose-bar gap — one
// shared handler, and each sheet deciding for itself which of its keys to name,
// so four of them named "j/k scroll" alone while the arrows, pgup/pgdn, g/G and
// home/end all worked. The WINDOWED cursor lists went next, as one recipe rather
// than one screen at a time (prose_bar_windowed_lists_test.go), the SIMPLE
// FLAT lists after them (prose_bar_flat_lists_test.go), and then the lists that
// draw a SECOND SURFACE — a picker, a detail, a prompt — in their own place
// (prose_bar_second_surface_test.go), and then the lists binding more of the
// movement vocabulary than those recipes' helpers named
// (prose_bar_wider_vocabulary_test.go), and then the simple FIELD FORMS, whose
// up/down move a focus rather than a cursor (prose_bar_field_forms_test.go), and
// then the lists whose rows are several lines and whose window already packed
// them by line cost (prose_bar_row_packed_lists_test.go), and then the flat
// lists sharing their pane with a second section (prose_bar_sections_test.go),
// and then the flat lists whose prompt, form or confirm is drawn under or in place
// of their rows (prose_bar_foot_prompts_test.go), and then the last two scroller
// sheets, whose modes draw in place of their footer
// (prose_bar_scroller_sheets_test.go), and then the storage slot list, whose
// window budget had counted its bar and its print overlay as one constant
// (prose_bar_storage_slots_test.go), and then the shared report table every
// tabbed report rides (prose_bar_report_table_test.go), and then the storage
// overview, a rack grid whose cursor moves in two dimensions and whose bar
// names each direction on its own (prose_bar_storage_overview_test.go), and
// then the parts list on an asset, whose multi-line parts made a row-counted
// page stop being a page (prose_bar_asset_parts_test.go). A
// field-form entry here was an exemption from THIS classifier, never from the
// bar rule, and a form's record names its focus keys like any other. Their bars
// are RECORDS now (prose_bar.go) and prose_bar_honesty_test.go presses the whole key
// space at them, so the classifier counts them as a THIRD swept class and an
// entry left here for one of them fails. What is still a literal is named in
// proseBarUnconverted with the shape of the work its conversion needs.
var listNavUnsweptReceivers = map[string]string{
	"TextScroller": "not a list at all: a read-only text body with a scroll offset and " +
		"no cursor, shared by every detail sheet that holds one (listNavDelegatingReceivers " +
		"derives that set every run, so no count is restated here to drift). It is here " +
		"because Handle binds the same movement keys the vocabulary spells. Its callers' " +
		"footers used to disagree about which of them to name — the prose-footer gap in its " +
		"purest form — and every one of those callers now draws a proseBar record, so the " +
		"keys are pressed through each sheet's bar; the component itself has no footer of " +
		"its own to read",
	"LocationDetailScreen": "NOT a navigation binding: its `g` generates the location's QR " +
		"code. It is here because the vocabulary is a set of KEY NAMES and cannot tell a " +
		"movement `g` from a `g` that means generate — which is a limit of the derivation " +
		"and is recorded rather than silently filtered, since filtering it would need a " +
		"rule that also hid a real one",
	"ForgeKeyDeviceDetailScreen": "NOT a list: up/down move between the indicator-edit fields beside tab/shift+tab, and setIndicatorFocus wraps modulo the field count — the field-form exemption",
	"slotCardPrompt": "a two-row modal prompt inside the storage-slot list, not a list of " +
		"rows: up/down move between a text field and a toggle and its cursor WRAPS, so the " +
		"field-form exemption applies (AGENTS.md)",
	"Root": "not a screen: app.go's root, which moves the NAV TREE cursor. The sidebar is its own surface with its own legend and is not a list of rows",
}

// listNavBindingSurfaces parses the package and returns, for every method that
// binds a navigation keystroke in a `case` clause, the receiver type it belongs
// to and where.
//
// AST rather than a grep, because the answer has to be per RECEIVER and a file
// routinely holds several, in DIFFERENT classes: category_form.go declares
// CategoryFormScreen, which is columnar, beside CategoryListScreen, whose bar
// is a proseBar record. A file-level classification would answer for the second
// on the strength of the first, and it would go on doing so when one of them
// converted and the other did not.
func listNavBindingSurfaces(t *testing.T) map[string][]string {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing the package: %v", err)
	}
	pkg, ok := pkgs["tui"]
	if !ok {
		t.Fatal("the tui package did not parse — the derivation is broken, not the app")
	}

	out := map[string][]string{}
	for name, file := range pkg.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || len(fn.Recv.List) == 0 {
				return true
			}
			recv := listNavReceiverName(fn.Recv.List[0].Type)
			if recv == "" {
				return true
			}
			ast.Inspect(fn.Body, func(inner ast.Node) bool {
				// A VALUE switch only. A TYPE switch's `case tea.KeyMsg:` names a
				// message type rather than a keystroke, and reading the two as one
				// alphabet would hand tea.KeyMsg to the resolver below and fail on
				// a clause that binds nothing. Nested switches are reached as their
				// own SwitchStmt nodes, so taking each one's immediate clauses is
				// exact rather than approximate.
				sw, ok := inner.(*ast.SwitchStmt)
				if !ok {
					return true
				}
				for _, stmt := range sw.Body.List {
					cl, ok := stmt.(*ast.CaseClause)
					if !ok {
						continue
					}
					for _, expr := range cl.List {
						key, spelled := listNavCaseKey(t, expr)
						if !spelled || !listNavBinds(key) {
							continue
						}
						pos := fset.Position(expr.Pos())
						out[recv] = append(out[recv],
							name+":"+strconv.Itoa(pos.Line)+" "+strconv.Quote(key))
					}
				}
				return true
			})
			return false
		})
	}
	if len(out) < 20 {
		t.Fatalf("the AST scan found only %d receivers binding a navigation key, which "+
			"is far fewer than this package has — the derivation is broken", len(out))
	}
	return out
}

// listNavCaseKey resolves ONE case expression to the keystroke it binds.
//
// TWO ALPHABETS, because a screen may spell a key either way and reading only
// one is how this derivation went blind. Most of the package writes
// `case "up", "k":` — a string literal, matched by Update's KeyMsg.String().
// bubbletea's own constants are the other spelling: `case tea.KeyUp:` in a
// switch over m.Type binds exactly the same keystroke and was invisible here,
// so five receivers appeared in NO class and three surfaces went on moving a
// cursor on chords list_nav.go records as retired, with both records saying
// otherwise. A derivation is only as complete as its alphabet.
//
// THE CONSTANT'S SPELLING IS ASKED OF BUBBLETEA (listNavKeyTypeSpelling), never
// transcribed here: a hand-kept table would be a third place for the same drift
// to hide, and this file already carries the lesson about rosters that must be
// edited in step with the code.
func listNavCaseKey(t *testing.T, expr ast.Expr) (string, bool) {
	t.Helper()
	switch v := expr.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false
		}
		key, err := strconv.Unquote(v.Value)
		if err != nil {
			return "", false
		}
		return key, true
	case *ast.SelectorExpr:
		pkg, ok := v.X.(*ast.Ident)
		if !ok || pkg.Name != "tea" || !strings.HasPrefix(v.Sel.Name, "Key") {
			return "", false
		}
		return listNavKeyTypeSpelling(t, v.Sel.Name)
	}
	return "", false
}

// listNavSpellingIndex maps a bubbletea KeyType CONSTANT NAME to the keystroke
// that constant spells, derived by walking the KeyType space and asking each one
// what it is called.
//
// The identifier and the spelling differ only in case and punctuation —
// KeyCtrlN spells "ctrl+n", KeyShiftTab spells "shift+tab" — so normalising both
// sides to lowercase alphanumerics matches them without anyone writing the pairs
// down. The range is wide enough to cover bubbletea's negative control codes and
// its positive named keys; a constant outside it simply does not resolve, and an
// unresolved constant is a FATAL at the call site rather than a silent skip.
func listNavSpellingIndex() map[string]string {
	out := map[string]string{}
	for i := -64; i <= 256; i++ {
		spelling := tea.KeyType(i).String()
		if norm := listNavNormaliseKey(spelling); norm != "" {
			out[norm] = spelling
		}
	}
	return out
}

func listNavNormaliseKey(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// listNavUnspellableKeyTypes are the KeyType constants whose own String() is not
// a NAME, so the derivation above cannot reach them, each with the reason.
//
// Recorded rather than filtered, for the reason AGENTS.md gives about the two
// exclusion entries in listNavUnsweptReceivers: a rule clever enough to drop
// these silently would eventually drop a real one, so the limit of the
// derivation is visible here instead of invisible. TestListNav_EveryKeyConstant
// TheDerivationMeetsResolves fails in both directions — an unresolvable constant
// that is not listed, and a listed one that has become resolvable.
//
// None of them can spell a navigation key: every keystroke in listNavSet() has
// an alphanumeric name, and an entry is here precisely because its constant has
// none.
var listNavUnspellableKeyTypes = map[string]string{
	"KeySpace": "its String() is the space character itself rather than the word " +
		"\"space\", so there is nothing alphanumeric to match the identifier against",
}

// listNavKeyTypeSpelling is the keystroke a bubbletea KeyType constant name
// spells, or a FATAL naming the constant it could not resolve.
//
// Fatal rather than skip: a constant this cannot read is a binding the
// classifier would miss, which is the exact silence the whole file exists to
// prevent — the same shape as poPickerKeyMsg falling through to KeyRunes for a
// name it had not been taught.
func listNavKeyTypeSpelling(t *testing.T, ident string) (string, bool) {
	t.Helper()
	if _, unspellable := listNavUnspellableKeyTypes[ident]; unspellable {
		return "", false
	}
	spelling, ok := listNavSpellingIndex()[listNavNormaliseKey(strings.TrimPrefix(ident, "Key"))]
	if !ok {
		t.Fatalf("a case clause binds tea.%s and this derivation cannot say which "+
			"keystroke that spells, so a binding through it would be invisible to the "+
			"classifier. Either bubbletea renamed the constant or it is one whose "+
			"String() is not a name — if the latter, record it in "+
			"listNavUnspellableKeyTypes with the reason", ident)
	}
	return spelling, ok
}

// TestListNav_EveryKeyConstantTheDerivationMeetsResolves: every tea.Key*
// constant the package binds in a value switch resolves to a keystroke, or is
// recorded as unspellable WITH A REASON.
//
// BOTH DIRECTIONS FAIL, for the reason every roster in this file does: an
// unresolvable constant that is not listed is a hole, and a listed one that has
// become resolvable is a stale excuse. The set of constants is DERIVED from the
// package rather than written down, so a screen binding a new one is checked the
// day it lands.
func TestListNav_EveryKeyConstantTheDerivationMeetsResolves(t *testing.T) {
	index := listNavSpellingIndex()
	met := map[string]bool{}
	for _, ident := range listNavKeyConstantsBound(t) {
		met[ident] = true
		_, unspellable := listNavUnspellableKeyTypes[ident]
		_, resolves := index[listNavNormaliseKey(strings.TrimPrefix(ident, "Key"))]
		switch {
		case !resolves && !unspellable:
			t.Errorf("tea.%s is bound in a case clause and neither resolves to a "+
				"keystroke nor is recorded in listNavUnspellableKeyTypes — a binding "+
				"through it is invisible to the classifier", ident)
		case resolves && unspellable:
			t.Errorf("tea.%s is recorded as unspellable but the derivation resolves it "+
				"now; the excuse has outlived the constant it was written about", ident)
		}
	}
	if len(met) == 0 {
		t.Fatal("the package binds no tea.Key* constant at all by this scan — the " +
			"derivation is broken, not the app")
	}
	for ident := range listNavUnspellableKeyTypes {
		if !met[ident] {
			t.Errorf("listNavUnspellableKeyTypes records tea.%s, which no case clause "+
				"binds any more", ident)
		}
	}
}

// listNavKeyConstantsBound is every tea.Key* constant name the package uses as a
// VALUE case expression, derived from the source.
func listNavKeyConstantsBound(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing the package: %v", err)
	}
	seen := map[string]bool{}
	for _, file := range pkgs["tui"].Files {
		ast.Inspect(file, func(n ast.Node) bool {
			sw, ok := n.(*ast.SwitchStmt)
			if !ok {
				return true
			}
			for _, stmt := range sw.Body.List {
				cl, ok := stmt.(*ast.CaseClause)
				if !ok {
					continue
				}
				for _, expr := range cl.List {
					sel, ok := expr.(*ast.SelectorExpr)
					if !ok {
						continue
					}
					if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "tea" &&
						strings.HasPrefix(sel.Sel.Name, "Key") {
						seen[sel.Sel.Name] = true
					}
				}
			}
			return true
		})
	}
	out := make([]string, 0, len(seen))
	for ident := range seen {
		out = append(out, ident)
	}
	sort.Strings(out)
	return out
}

func listNavReceiverName(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.StarExpr:
		return listNavReceiverName(v.X)
	case *ast.Ident:
		return v.Name
	}
	return ""
}

// listNavDelegatingReceivers is every type that HOLDS a TextScroller, and so
// gets the whole movement vocabulary without spelling a single key of it.
//
// THE HOLE THIS CLOSES was in the derivation above, not in the app.
// listNavBindingSurfaces finds a `case "j", "down":` and keys it by the receiver
// whose method it sits in — which is exactly right for the fifty-odd screens
// that switch on keys themselves, and blind to a screen that owns a
// TextScroller and hands it the key. TextScroller.Handle binds j/k, the arrows,
// pgup/pgdn and g/G/home/end, so those screens have every one of them; they were
// classified only TRANSITIVELY, through the TextScroller entry, and a NEW one
// could have joined the app without appearing in the classification at all.
// That is the silence the whole file exists to prevent, one level of
// indirection out.
//
// SEVEN were in that state — AnalyticsPulseScreen, NotificationsScreen,
// SIGDetailScreen, ProjectStorageDetailScreen, StorageSlotDetailScreen,
// ElectricalPanelDetailScreen and SupplierDetailScreen, all seven since
// converted to a proseBar record. The other TextScroller
// holders (asset, inventory and work-order detail among them) bind keys of their
// own as well and so were already found. No count of the holders is written down
// anywhere in this file: the check derives the set every run, and a number
// restated beside a derivation is the one thing in it that can go stale — which
// it did, saying fifteen where the package holds fourteen, in three places at
// once.
//
// A FIELD TYPE and not a call graph, because that is what go/parser can answer
// without go/types: a screen with a TextScroller in it is a screen that scrolls,
// and there is no way to hold one and not hand it the keyboard.
func listNavDelegatingReceivers(t *testing.T) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing the package: %v", err)
	}
	out := map[string]bool{}
	for _, file := range pkgs["tui"].Files {
		ast.Inspect(file, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				return true
			}
			for _, fld := range st.Fields.List {
				if listNavReceiverName(fld.Type) == "TextScroller" {
					out[ts.Name.Name] = true
				}
			}
			return true
		})
	}
	if len(out) < 5 {
		t.Fatalf("only %d types hold a TextScroller by this scan, which is far fewer than "+
			"the detail sheets that build one — the derivation is broken, not the app", len(out))
	}
	return out
}

// listNavColumnarReceivers is every type in the package that embeds jdeScreen,
// read out of the source — the same derivation jdeEmbedders makes for the pane
// sweeps, repeated here because this file must not depend on a test helper's
// fixture map to answer a question about the SOURCE.
func listNavColumnarReceivers(t *testing.T) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing the package: %v", err)
	}
	out := map[string]bool{}
	for _, file := range pkgs["tui"].Files {
		ast.Inspect(file, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				return true
			}
			for _, f := range st.Fields.List {
				if len(f.Names) != 0 {
					continue // named field, not an embed
				}
				if id, ok := f.Type.(*ast.Ident); ok && id.Name == "jdeScreen" {
					out[ts.Name.Name] = true
				}
			}
			return true
		})
	}
	if len(out) < 20 {
		t.Fatalf("only %d types embed jdeScreen by this scan, which contradicts the "+
			"pane sweeps — the derivation is broken", len(out))
	}
	return out
}

// TestListNav_EverySurfaceThatBindsNavigationIsSweptOrExcused: every receiver in
// the package that binds a movement key is either covered by one of the two
// behavioural sweeps or recorded, with a reason, in
// listNavUnsweptReceivers.
//
// IT IS A COVERAGE CLASSIFICATION AND NOT A BEHAVIOUR CLAIM, and the difference
// matters enough to say before anything else: it reads the package's source to
// answer "which surfaces can a behavioural sweep reach, and which cannot", and
// it asserts NOTHING about what any key does. The behaviour claims are pressed
// on real screens — TestListNav_NoSurfaceBindsARetiredChord for the retired
// chords, TestJDEForm_EveryMovementTokenIsNamedExactlyWhereItMoves and
// TestList_FooterNamesExactlyTheKeysThatWork for the bar's honesty. A source
// parse is legitimate HERE precisely because the question is about the shape of
// the package rather than about behaviour: there is no fixture to press for the
// prose-bar receivers, and the thing being guarded is that their set cannot grow
// without somebody writing down what the new one is.
//
// THIS IS THE COMPLETENESS HALF, and it is deliberately weaker than the rule it
// serves: it does not say the excused bars are honest, it says nothing can be
// excused by nobody having looked. A screen added tomorrow that draws a prose
// footer and binds j/k fails here until somebody writes down what it is — which
// is the difference between a known gap and an unexamined remainder, and the
// difference this area's three previous defects all turned on.
//
// ALL THREE DIRECTIONS FAIL, for the reason jdeUnsizedDeclineCases gives: a
// roster in a test is only worth keeping if being wrong about it is loud. A
// receiver that binds navigation and is neither swept nor listed fails; a listed
// receiver that IS swept fails, because it is excusing something that no longer
// needs excusing; and a listed receiver that binds no navigation key at all
// fails, because it has outlived the screen it was written about.
func TestListNav_EverySurfaceThatBindsNavigationIsSweptOrExcused(t *testing.T) {
	binding := listNavBindingSurfaces(t)
	columnar := listNavColumnarReceivers(t)
	// The THIRD swept class, and the newest: a screen whose footer is a
	// proseBar RECORD rather than a literal (prose_bar.go). Its bar can be read
	// and its keys pressed, so it belongs with the other two rather than in the
	// excused roster — and taking a screen out of that roster as it converts is
	// what makes the remainder shrink VISIBLY, instead of a map that only ever
	// grows.
	prose := proseBarReceivers(t)
	// A screen that HOLDS a TextScroller has the movement vocabulary without
	// spelling any of it, so it is a navigation surface for this rule's purposes
	// even though no `case` in it names a key.
	for recv := range listNavDelegatingReceivers(t) {
		if _, already := binding[recv]; !already {
			binding[recv] = []string{"a TextScroller field (its Handle binds the whole vocabulary)"}
		}
	}

	var unclassified []string
	for recv, sites := range binding {
		switch {
		case columnar[recv]:
			if why, listed := listNavUnsweptReceivers[recv]; listed {
				t.Errorf("%s embeds jdeScreen, so the columnar movement sweep already reads "+
					"its bar — but it is recorded as a prose-footer exclusion (%q). A stale "+
					"exception excuses a surface from the sweep it passes", recv, why)
			}
		case recv == "ListScreen":
			if why, listed := listNavUnsweptReceivers[recv]; listed {
				t.Errorf("ListScreen's footer IS read structurally by "+
					"TestList_FooterNamesExactlyTheKeysThatWork, so the exclusion %q is "+
					"stale", why)
			}
		case prose[recv]:
			if why, listed := listNavUnsweptReceivers[recv]; listed {
				t.Errorf("%s declares proseBar, so "+
					"TestProseBar_TheFooterNamesExactlyTheKeysThatWork presses the whole "+
					"key space at its bar — but it is recorded as a prose-footer exclusion "+
					"(%q). A stale exception excuses a surface from the sweep it passes",
					recv, why)
			}
		default:
			if _, listed := listNavUnsweptReceivers[recv]; !listed {
				sort.Strings(sites)
				unclassified = append(unclassified,
					recv+" — binds navigation at "+strings.Join(sites, ", "))
			}
		}
	}
	sort.Strings(unclassified)
	for _, u := range unclassified {
		t.Errorf("%s\n"+
			"This receiver binds a movement key and no behavioural sweep can read its "+
			"bar: it does not embed jdeScreen (so the columnar sweep skips it), it is "+
			"not a *ListScreen (so the footer sweep does not reach it), and it declares "+
			"no proseBar (so the prose-bar sweep cannot read it either). Either put it "+
			"on one of those three surfaces — which is what makes the rule PROVABLE for "+
			"it — or record it in listNavUnsweptReceivers saying what it is.", u)
	}

	for recv, why := range listNavUnsweptReceivers {
		if _, binds := binding[recv]; !binds {
			t.Errorf("listNavUnsweptReceivers excuses %s (%q) but nothing on it binds a "+
				"navigation key any more — the entry has outlived the screen it was "+
				"written about", recv, why)
		}
	}
}
