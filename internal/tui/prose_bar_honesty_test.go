package tui

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"sort"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// prose_bar_honesty_test.go — the bar-honesty rule for the screens whose footer
// is neither a columnar []actionBarItem nor ListScreen's footerHint.
//
// THE RULE IS THE ONE THIS PROJECT ALREADY HOLDS ITSELF TO: a key the bar NAMES
// must do something, and a key it does NOT name must do nothing. Two sweeps
// already hold it — TestPOView_BarNamesExactlyTheKeysThatWork over the columnar
// screens and TestList_FooterNamesExactlyTheKeysThatWork over every *ListScreen
// — and both of them read a MACHINE-READABLE bar. A third class of screen had
// no such bar at all: its footer was a muted literal written straight into a
// strings.Builder inside View, so there was nothing for a sweep to press keys
// against, and list_nav_surfaces_test.go had to record the whole class as
// EXCLUDED (listNavUnsweptReceivers) rather than sweep it.
//
// WHAT THAT COST, measured on the screens this file now covers. All but one of
// them holds a TextScroller, whose Handle binds the whole navigation vocabulary
// — j/k, the arrows, pgup/pgdn and g/G/home/end — and FOUR of those named two of
// it ("j/k scroll" alone) while the other eight keystrokes scrolled the sheet
// unannounced. Not one named the whole vocabulary. AnalyticsPulseScreen also
// bound `backspace` as a second way back to the Reports hub and said so nowhere.
// And the two longest literals were past the 51 cells an 80-column pane gives
// before the missing keys were even considered, so clampToBox was taking their
// tails: the storage-slot sheet's footer lost `r refresh · esc back` on an
// occupied slot.
//
// THE EXCEPTION IS THE REORDER QUEUE, and it is here on purpose: it is a cursor
// LIST rather than a scrolled sheet, so it shows the record is not a scroller's
// private arrangement. Its bar changes shape with the row's STATUS, with whether
// a write is out, and with whether the list scrolls at all — the conditional
// shapes the detail sheets cannot exercise.
//
// SO THE FIX WAS A CONVERSION, NOT AN EDIT — the footer becomes a record
// (prose_bar.go), the record is folded against the live pane, and the scroller's
// row budget is derived from the folded result. This file is what that
// conversion BUYS: with a record to read, the rule is mechanical.
//
// WHAT IS AND IS NOT COVERED is derived rather than asserted. proseBarReceivers
// reads the package source for the types that declare proseBar;
// TestProseBar_EveryConvertedScreenIsSwept holds that set and the fixtures below
// in agreement, in both directions; and
// TestProseBar_EveryProseFooterScreenIsConvertedOrNamed holds the REMAINDER —
// every screen still writing a literal is named in proseBarUnconverted with the
// shape of the work, so a partial conversion is a stated one rather than a
// silent one.

// ---------------------------------------------------------------------------
// The roster, derived
// ---------------------------------------------------------------------------

// proseBarReceivers is every type in the package's non-test source that declares
// a `proseBar()` method — that is, every screen whose bar is a record.
//
// AST rather than an interface assertion, for the reason
// listNavBindingSurfaces gives: an interface reports the types somebody
// remembered to assert against it, and a roster that has to be edited in step
// with the code is the omission this package keeps being bitten by. A screen
// converted tomorrow joins this sweep by declaring the method.
func proseBarReceivers(t *testing.T) map[string]bool {
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
	out := map[string]bool{}
	for _, file := range pkg.Files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != "proseBar" || fn.Recv == nil || len(fn.Recv.List) == 0 {
				continue
			}
			if recv := listNavReceiverName(fn.Recv.List[0].Type); recv != "" {
				out[recv] = true
			}
		}
	}
	return out
}

// proseBarUnconverted is every receiver that still writes its footer as a
// literal inside View, with the SHAPE of the work left rather than the bare fact
// that it is left.
//
// THIS IS A NAMED PARTIAL AND THAT IS THE POINT. The conversion this file holds
// the rule for is the sc-jde-lift shape — a record, a fold, and a row budget
// derived from the fold — applied one screen at a time, and sixty-odd screens is
// more than one reviewable change. What must not happen is the remainder going
// quiet: TestProseBar_EveryProseFooterScreenIsConvertedOrNamed fails in both
// directions, so a screen cannot be left out by nobody having looked, and a
// screen that HAS been converted cannot be left listed here.
//
// The set is derived from listNavUnsweptReceivers (list_nav_surfaces_test.go),
// which is itself derived from the source every run — so this map's job is only
// to say what each remaining one NEEDS, and the question of which ones remain is
// answered by the parser.
//
// THE TWO ENTRIES THAT ARE NOT PROSE-FOOTER SCREENS AT ALL stay recorded here
// for the reason listNavUnsweptReceivers records them: the derivation is a set
// of KEY NAMES and cannot tell a movement `g` from a `g` that means generate,
// nor a list cursor from a field form's focus pair. A filter clever enough to
// drop LocationDetailScreen's QR key or slotCardPrompt's wrapping two-row modal
// would eventually drop a real one, and this conversion does not make either of
// them EXPRESSIBLE — a proseBar records which keystrokes a segment spells, which
// says nothing about whether a keystroke is navigation. They are still
// exceptions, and they are still visible.
var proseBarUnconverted = map[string]string{
	// THE SCROLLER SHEETS ARE ALL CONVERTED. They were the group the first
	// conversions came out of — each holds a TextScroller and so had the whole
	// movement vocabulary without spelling a key of it — and the last two were the
	// sheets whose MODES draw in place of the footer, the item sheet and the
	// work-order sheet: prose_bar_scroller_sheets_test.go carries them, and the
	// per-mode answer to "does its prompt name every key it acts on" is written
	// where each bar is built. What is left below is not a scroller sheet.

	// THE CURSOR LISTS, which are the bulk. Each draws rows with a cursor and a
	// prose footer, so a record is only part of it: the movement segments have to
	// be gated on there being a SECOND row (listNavMoves — an empty list and a
	// one-row list both promise three affordances that clamp), and the BODY's row
	// budget has to move with the folded footer. Several of them are a *ListScreen
	// away from needing no record at all.
	//
	// EIGHT RECIPES HAVE BEEN TAKEN OUT OF THIS GROUP, and what is left is split
	// into the two shapes that remain rather than left as one heap — because
	// "what shape is the work" is the only thing this map is for. The first was
	// the WINDOWED list budgeted by a chrome constant of four, whose last row is
	// one unfolded footer line: prose_bar_windowed_lists_test.go carries what
	// those screens had in common and proseListWindow what replaced the constant.
	// The second was the SIMPLE FLAT list — one surface, the step pair and a few
	// row actions bound, every row drawn with no window:
	// prose_bar_flat_lists_test.go carries it, and proseFlatListFrame gave those
	// screens the line-packed window they never had. The third was either of
	// those with a SECOND SURFACE drawn IN PLACE of the list — a picker, a
	// read-only detail, a prompt with a text box — where the record answers for
	// whichever surface is up: prose_bar_second_surface_test.go carries it. The
	// fourth was the lists binding MORE of the movement vocabulary than their
	// recipe's helper names and less than all of it — the two workspace menus with
	// a letter per row, the panel list with no pager, the certificate table that
	// scrolls an offset — where the bar names exactly the keystrokes the switch
	// binds: prose_bar_wider_vocabulary_test.go carries it. The fifth was the
	// simple FIELD FORMS, whose up/down move a focus rather than a cursor:
	// prose_bar_field_forms_test.go carries it. The sixth was the lists whose rows
	// are SEVERAL LINES and whose window already packed them by line cost — the
	// two forecasts, each with a scrolled detail drawn in place of the list, and
	// the supplier links on an item — where the budget was taught the folded bar:
	// prose_bar_row_packed_lists_test.go carries it. The seventh was the flat lists
	// sharing their pane with a second SECTION — the checklist browser's runs above
	// its checklists, with a key moving the focus between them, and the firmware
	// rollouts above two read-only sections — where one window serves the sections
	// a key moves through and the read-only ones give first:
	// prose_bar_sections_test.go carries it. The eighth was the flat lists whose
	// answer to a key is drawn UNDER the rows or IN PLACE of them — the checklist
	// run's notes box, the work-order attachments' delete confirm and upload form,
	// the maker-box directory's service notices, forms and confirms — where the
	// window is budgeted around a FOOT that is more than the bar:
	// prose_bar_foot_prompts_test.go carries it. (The ninth recipe was not a
	// cursor list at all — the two scroller sheets above.) The storage slot list
	// went on its own after those, because its work was its own shape: a windowed
	// list whose chrome constant counted the bar and slotCardPrompt's print
	// overlay as one number, so neither cost could be derived until they were
	// separated — the bar's is its ceiling and an overlay's is what it draws, and
	// prose_bar_storage_slots_test.go carries it. slotCardPrompt stays below. The
	// tenth was one screen rather than a group, because nothing else has its
	// shape: the universal search palette, a flat list drawn under a LIVE QUERY
	// BOX whose results stay on the pane while a search is out — so the box leads,
	// the bar closes and the rows are budgeted between them:
	// prose_bar_search_palette_test.go carries it. The storage overview went on
	// its own after those, because it is not a list of rows at all: a rack GRID
	// whose cursor moves in two dimensions, so each direction is gated on its own
	// rather than as a pair, and whose header, cursor line and legend had to fold
	// or clip before the window could be budgeted from them —
	// prose_bar_storage_overview_test.go carries it. And the windowed recipe's
	// one screen whose rows are SEVERAL LINES BY CONSTRUCTION went on its own —
	// the parts list on an asset, where packing the window by lines turned the
	// row-counted pager into a question of what a page is, answered by
	// proseLinePage: prose_bar_asset_parts_test.go carries it.
	// What remains divides:
	//
	//   - STILL ON THAT RECIPE, but not mechanically. The last two had a window
	//     and a budget, and something about them that the shared helpers do not
	//     answer — rows that are not one line, and a chrome constant counting
	//     something else — and both are converted now (see above), so none is
	//     left here.
	//   - FLAT, with no window at all: they draw EVERY row and then the footer, so
	//     a list longer than the pane pushes the bar off the bottom whatever it
	//     says, and folding alone would not put it back. The ones that were
	//     nothing more than that are converted, and so are the ones whose only
	//     complication was a surface drawn in their place or under their rows;
	//     the last two had something none of those answers — an overlay with a
	//     live query box, and a layout that is not a list of rows — and both
	//     are converted now (see above), so none is left here.

	// Still on the windowed recipe.

	// Flat: no window at all, so the bar and the body budget are one piece of work.

	// THE ONES THAT ARE NEITHER A SCROLLED SHEET NOR A PLAIN CURSOR LIST, each
	// saying what it is instead — a detail sheet with no window, or not a screen
	// at all.
	//
	// THE SHARED REPORT TABLE HAS BEEN TAKEN OUT OF THIS GROUP — the one screen
	// here whose vertical give-order was already written down and enforced
	// (report_table.go's layoutRows: the legend and the bar never give, the body
	// floors at one row), so its conversion turned the bar it never gives up into
	// a record rather than teaching it to budget. It is one type under every
	// tabbed report, so prose_bar_report_table_test.go sweeps it on more than one.
	//
	// THE SIMPLE FIELD FORMS HAVE BEEN TAKEN OUT OF THIS GROUP — the sign-in
	// form, the reorder form and both steps of the batch scan, whose up/down move
	// a FOCUS rather than a cursor. Theirs was an exemption from the navigation
	// CLASSIFIER, which cannot tell a focus pair from a list cursor, and never an
	// exemption from the bar rule: a record says which keystrokes a segment
	// spells, and "tab/shift+tab ↑↓ field" is as answerable as "j/k move".
	// prose_bar_field_forms_test.go carries them.
	"ForgeKeyDeviceDetailScreen": "a device DETAIL SHEET that also holds an indicator-test field form " +
		"and a delete confirm. It was grouped with the field forms because the form's up/down " +
		"move its focus (setIndicatorFocus wraps modulo the field count), and the form is the " +
		"easy part. What stopped it is the detail view: it writes every section — the spec " +
		"rows, the relay and LED state, the temperature sparkline, the recent commands — with " +
		"no window and no scroller, under a bar naming up to fifteen keys that folds onto four " +
		"rows at 80 columns, so a device with a command history is taller than an 80x24 pane " +
		"and clampToBox takes the bar whatever the record says. Keeping the bar on the pane " +
		"needs a TextScroller or a window under it, and either one BINDS keys the sheet does " +
		"not bind today (j/k, the arrows, the pager) — a change to what the screen does, " +
		"which is a decision and not part of making its bar honest",
	"LocationDetailScreen": "NOT a prose-footer screen in the sense the rest of this map is: it is here because the navigation derivation is a set of KEY NAMES and its `g` GENERATES the location's QR code. A proseBar records which keystrokes a segment spells, which says nothing about whether a keystroke is navigation — so converting it would not make this exception expressible, and it stays an exception. Recorded rather than filtered, since a filter clever enough to drop it would eventually drop a real one",
	"Root":                 "NOT a screen: app.go's root, whose movement keys walk the NAV TREE. The sidebar is its own surface with its own legend and is not a list of rows, so there is no footer here to make a record of",
	"TextScroller":         "NOT a screen and so has no footer to convert: the shared read-only body the scroller sheets hold. EVERY sheet holding one now declares a record, so each keystroke its Handle binds is pressed through that sheet's bar — the condition this entry used to wait on. It stays because the navigation derivation reads key names off RECEIVERS, and a shared component whose Handle binds the vocabulary is one; a filter clever enough to drop a component would be clever enough to drop a screen, so it is recorded rather than filtered",
	"slotCardPrompt":       "NOT a list and NOT a prose footer: a two-row modal inside the storage-slot list (and the slot detail) whose up/down move between a text field and a toggle, and whose cursor WRAPS. The field-form exemption, and the second of the two entries a filter would have to be clever enough to drop — so it stays an exception too. Its HOSTS are converted and give it room: StorageSlotsScreen budgets its window around the rows it draws (foot), and both hand it the live pane so it folds rather than running past the edge — but its own words about its keys are still a literal, and a record for them would first need the field-form question answered for a two-row modal",
}

// ---------------------------------------------------------------------------
// The fixtures
// ---------------------------------------------------------------------------

// proseBarFixture is one (screen, state) the sweep drives. The state matters as
// much as the screen: a bar changes shape with what the sheet is holding, and
// that is exactly where the honesty rule can break.
type proseBarFixture struct {
	name string
	// recv is the receiver type, so the coverage check can compare the fixtures
	// against the derived roster without a type switch per screen.
	recv  string
	build func() proseBarScreen
	// immobile is why NO movement key can move this fixture, where that is the
	// point of it rather than a defect — a one-row list has nowhere to go, and
	// a bar that named the movement keys there would be claiming what the arms
	// refuse.
	//
	// It is recorded rather than filtered, the shape jdeInertCases uses: the
	// movement sweep REQUIRES an immobile fixture to be immobile and requires a
	// mobile one to move, so absent and empty stay different states and a
	// reason that stops being true fails.
	immobile string
	// declines maps a key this fixture does NOT name to why pressing it changes
	// the pane anyway — a key that declines and says why has not ACTED. Read
	// proseBarReorderDeclines for how narrow the exemption is and where the real
	// check lives.
	declines map[string]string
	// load is the LOAD STATE this fixture is in — one of the proseLoad* states —
	// or "" for a fixture past its load. Only the load-state sweep reads it; every
	// other sweep in this file presses a load-state fixture exactly as it presses
	// any other, which is the point of building them into the same roster.
	load proseLoadState
	// typing is why a focused text box on this fixture takes the printable keys
	// and the box's editing keys (proseBarBoxKeys) — the search pickers, the
	// resolve notes, a manual badge entry. A letter typed into a query changes
	// the pane and is named by no bar, because no record can spell "every
	// printable key": the exemption TestPOSearchBoxes_TheBarNamesExactlyTheKeysThatWork
	// takes for the columnar search boxes.
	//
	// IT IS HELD IN BOTH DIRECTIONS RATHER THAN TRUSTED. A reason that has stopped
	// being true fails: a focused box takes EVERY printable key, so the sweep
	// requires every unnamed one to change the pane — asking for merely SOME let
	// `q` closing a read-only report satisfy it over a frame with no box at all,
	// which hid the very omission it was pointed at. And a letter the bar DOES
	// name is still pressed forward, and fails where all it does is type: it must
	// change something other than the rows an unbound character changes, or the
	// bar is claiming a key the box eats (`j/k` over the e-paper search).
	//
	// WHAT IT CANNOT SEE, said so nobody reads more into it: an UNNAMED letter
	// bound to an action on a surface whose box has the focus. Typing changes the
	// pane too, and comparing the rows a letter changes against a reference only
	// holds where the box does not filter live — on the SIG and badge filters a
	// different character draws a different list. `l/L/?` on the check-in entry
	// is the one such binding here, and it is named.
	typing string
	// digits narrows `typing` to a box that takes DIGITS alone — a counted
	// quantity, whose arm drops every other printable rune before the box sees it.
	// There a letter is not the box's key: it is an unbound key that changes
	// nothing, and the reverse half judges it like any other, so the exemption
	// covers exactly the runes the box really takes and a typed letter that did
	// land in it would fail.
	digits bool
	// otherEdge walks the fixture to the edge `end` does not reach, for a list
	// that binds no `end` and is too short to stand anywhere but at an edge. A
	// two-row picker is the case: from the top `k` clamps and from the bottom `j`
	// does, so no single resting position shows the pair acting, and a clamped key
	// is not a dead one (proseBarProbes).
	otherEdge []string
}

// proseBarProbes is where the key-effect sweeps press a key from: the resting
// position, the end of the body, and the fixture's other edge where it records
// one.
func proseBarProbes(f proseBarFixture) [][]string {
	probes := [][]string{nil, {"end"}}
	if len(f.otherEdge) > 0 {
		probes = append(probes, f.otherEdge)
	}
	return probes
}

// proseBarBoxKeys are the non-printable keys a focused bubbles textinput
// answers on its own: the caret and the deletions. On a typing fixture they are
// the box's, exactly as the printable keys are.
var proseBarBoxKeys = map[string]bool{
	"backspace": true, "delete": true, "left": true, "right": true, "home": true, "end": true,
	"ctrl+u": true, "ctrl+d": true, "ctrl+e": true,
}

// proseBarBoxTakes reports whether a key goes into the focused box on a typing
// fixture, and so is exempt from the reverse half where the bar does not name it.
func proseBarBoxTakes(f proseBarFixture, key string) bool {
	if f.typing == "" {
		return false
	}
	r := []rune(key)
	if f.digits && len(r) == 1 {
		return r[0] >= '0' && r[0] <= '9'
	}
	return (len(r) == 1 && r[0] >= 0x20 && r[0] <= 0x7e) || proseBarBoxKeys[key]
}

// proseBarFixtures builds every converted screen in every state that DRAWS a
// bar.
//
// A fixture must draw one: a screen left in its loading state renders a single
// muted line, answers no key, and would report coverage of a frame it never
// built — the vacuity TestJDEForm_EveryColumnarScreenIsSwept guards against on
// the columnar side, and TestProseBar_EveryConvertedScreenIsSwept guards here.
//
// THE BODIES ARE LONG ENOUGH TO MOVE, deliberately. proseNavScroll and
// proseNavList both drop the movement segments where nothing can move, which is
// correct and would also make every movement assertion in this file vacuous: a
// fixture whose body fits names no movement key, so a sweep over it proves
// nothing about the keystrokes this conversion exists for. Each one below either
// outruns the pane at the size the sweep drives, or records on the fixture WHY
// it cannot move (immobile), and TestProseBar_EveryMovementKeyIsNamedWhereItMoves
// fails a fixture in neither state.
func proseBarFixtures() []proseBarFixture {
	out := []proseBarFixture{
		// TWO STATES, for the one conditional arm on this bar: `i components` is
		// offered only where components are installed, and the handler answers a
		// toast and goes nowhere where they are not. A fixture with components
		// can never show the absence, and one without can never show the key.
		{
			name: "asset detail/no components", recv: "AssetDetailScreen",
			build: func() proseBarScreen { return proseBarAsset(0) },
		},
		{
			name: "asset detail/components", recv: "AssetDetailScreen",
			build: func() proseBarScreen { return proseBarAsset(3) },
		},
		{
			name: "analytics pulse", recv: "AnalyticsPulseScreen",
			build: func() proseBarScreen {
				s := NewAnalyticsPulseScreen(Deps{})
				next, _ := s.Update(analyticsPulseLoadedMsg{pulse: samplePulse()})
				return next.(*AnalyticsPulseScreen)
			},
		},
		{
			name: "notifications", recv: "NotificationsScreen",
			build: func() proseBarScreen {
				s := NewNotificationsScreen(Deps{})
				next, _ := s.Update(notificationsLoadedMsg{rows: proseBarNotifications(30)})
				return next.(*NotificationsScreen)
			},
		},
		// THE EMPTY STATE, which is the state a notification list spends most of
		// its life in and the one that used to draw the fact alone with no bar
		// under it. It is IMMOBILE by construction — there is nothing to scroll
		// — which is exactly why it has to be swept separately: the loaded
		// fixture can never show that the movement segments come OFF.
		{
			name: "notifications/empty", recv: "NotificationsScreen",
			build: func() proseBarScreen {
				s := NewNotificationsScreen(Deps{})
				next, _ := s.Update(notificationsLoadedMsg{})
				return next.(*NotificationsScreen)
			},
			immobile: "no notifications, so there is no body to scroll — which is the " +
				"point of this fixture: it is where the movement segments and `X` must " +
				"all be absent and the way out must still be named",
		},
		{
			name: "sig detail", recv: "SIGDetailScreen",
			build: func() proseBarScreen {
				s := NewSIGDetailScreen(Deps{}, "3")
				next, _ := s.Update(sigDetailLoadedMsg{
					sig:     &omsapi.SIG{ID: 3, Name: "Metal Fabrication SIG", MemberCount: 30},
					members: proseBarSIGMembers(30),
				})
				return next.(*SIGDetailScreen)
			},
		},
		{
			name: "project storage detail", recv: "ProjectStorageDetailScreen",
			build: func() proseBarScreen {
				s := NewProjectStorageDetailScreen(Deps{}, "PS-AB23CDFG")
				next, _ := s.Update(projectStorageDetailLoadedMsg{stint: &omsapi.ProjectStorageStint{
					StintID: "PS-AB23CDFG", Username: "alice", DisplayName: "Alice Smith",
					SlotCode: "1A1", LocationDisplay: "1A1", Status: "active",
					ProjectTitle: "Powder-coating rig for the metal shop",
				}})
				return next.(*ProjectStorageDetailScreen)
			},
		},
		// THREE STATES, because this sheet carries the most conditional bar of
		// the scroller sheets, and the conditions are what a sweep over one state
		// cannot see: a FREE slot offers `a assign`, an ASSIGNED one
		// offers `R release` instead, and a slot holding a project STINT offers
		// `enter open stint` on top of either.
		{
			name: "storage slot detail/free", recv: "StorageSlotDetailScreen",
			build: func() proseBarScreen {
				return proseBarSlot(omsapi.StorageSlot{
					ID: 1, Code: "1A1", Rack: 1, Level: "A", Position: 1, IsActive: true,
				})
			},
		},
		{
			name: "storage slot detail/assigned", recv: "StorageSlotDetailScreen",
			build: func() proseBarScreen {
				return proseBarSlot(omsapi.StorageSlot{
					ID: 2, Code: "1A2", Rack: 1, Level: "A", Position: 2, IsActive: true,
					IsOccupied: true, OccupancyType: "assignment",
					CurrentAssignment: &omsapi.StorageSlotAssignmentSummary{
						ID: 9, StorageType: "committee", TypeLetter: "C",
						OccupantDisplay: "Metal Fabrication SIG",
					},
				})
			},
		},
		{
			name: "storage slot detail/stint", recv: "StorageSlotDetailScreen",
			build: func() proseBarScreen {
				return proseBarSlot(omsapi.StorageSlot{
					ID: 3, Code: "1A3", Rack: 1, Level: "A", Position: 3, IsActive: true,
					IsOccupied: true, OccupancyType: "stint",
					CurrentStint: &omsapi.StorageSlotOccupant{
						ID: 4, StintID: "PS-AB23CDFG", Username: "alice",
						DisplayName: "Alice Smith", Status: "active",
					},
				})
			},
		},
		{
			name: "electrical panel detail", recv: "ElectricalPanelDetailScreen",
			build: func() proseBarScreen {
				s := NewElectricalPanelDetailScreen(Deps{}, 1)
				next, _ := s.Update(electricalPanelDetailLoadedMsg{
					topology: proseBarPanelTopology(),
				})
				return next.(*ElectricalPanelDetailScreen)
			},
		},
		{
			name: "serialized components/list", recv: "SerializedComponentsScreen",
			build: func() proseBarScreen {
				s := NewItemInstancesScreen(Deps{}, "item-1", "Safety relay", nil)
				next, _ := s.Update(serialComponentsLoadedMsg{rows: proseBarSerializedComponents(30)})
				return next.(*SerializedComponentsScreen)
			},
		},
		{
			name: "serialized components/empty", recv: "SerializedComponentsScreen",
			build: func() proseBarScreen {
				s := NewItemInstancesScreen(Deps{}, "item-1", "Safety relay", nil)
				next, _ := s.Update(serialComponentsLoadedMsg{})
				return next.(*SerializedComponentsScreen)
			},
			immobile: "there are no serialized components for a cursor to move across",
		},
		{
			name: "serialized components/history", recv: "SerializedComponentsScreen",
			build: func() proseBarScreen {
				s := NewItemInstancesScreen(Deps{}, "item-1", "Safety relay", nil)
				s.loading = false
				s.rows = proseBarSerializedComponents(1)
				s.showHistory = true
				s.historyFor = s.rows[0].ID
				s.historyScroller = NewTextScroller(defaultDetailHeight)
				s.historyScroller.Set(strings.Repeat(proseBarLongNote()+"\n", 3))
				return s
			},
		},
		{
			name: "maintenance item detail", recv: "MaintenanceItemDetailScreen",
			build: func() proseBarScreen {
				s := NewMaintenanceItemDetailScreen(Deps{}, "pm-1")
				tasks := make([]omsapi.MaintenanceTask, 30)
				for i := range tasks {
					tasks[i] = omsapi.MaintenanceTask{
						ID: fmt.Sprintf("task-%d", i+1), Order: i + 1,
						Title: fmt.Sprintf("Inspect station %d", i+1), IsRequired: true,
					}
				}
				next, _ := s.Update(mDetailLoadedMsg{item: &omsapi.MaintenanceItem{
					ID: "pm-1", Title: "Monthly machine inspection", IsActive: true, Tasks: tasks,
				}})
				return next.(*MaintenanceItemDetailScreen)
			},
		},
		// THE ONE CURSOR LIST IN THE CONVERTED SET, and it is here to prove the
		// record is not a scroller's private arrangement: this screen's bar
		// changes shape with the row's STATUS, with whether a write is out, and
		// with whether the list scrolls at all, so it exercises a conditional
		// bar the detail sheets cannot.
		//
		// FOUR STATES, each for a condition no other one reaches: the three
		// lifecycle views offer different keys on the same row (`a`/`x` on a
		// pending request, `o` on an approved one, `d` on an ordered one), and
		// the ONE-ROW list is where the movement segments must all be absent,
		// which a list of forty can never show.
		{
			name: "reorder queue/pending", recv: "ReorderQueueScreen",
			build:    func() proseBarScreen { return proseBarReorder(reorderViewPending, 40) },
			declines: proseBarReorderDeclines,
		},
		{
			name: "reorder queue/approved", recv: "ReorderQueueScreen",
			build:    func() proseBarScreen { return proseBarReorder(reorderViewApproved, 40) },
			declines: proseBarReorderDeclines,
		},
		{
			name: "reorder queue/ordered", recv: "ReorderQueueScreen",
			build:    func() proseBarScreen { return proseBarReorder(reorderViewOrdered, 40) },
			declines: proseBarReorderDeclines,
		},
		{
			name: "reorder queue/one row", recv: "ReorderQueueScreen",
			build: func() proseBarScreen { return proseBarReorder(reorderViewApproved, 1) },
			immobile: "one row, so there is nowhere for a cursor to go — which is the " +
				"whole point of this fixture: it is where the movement segments must be " +
				"ABSENT, and a list of forty can never show that",
			declines: proseBarReorderDeclines,
		},
		{
			name: "supplier detail", recv: "SupplierDetailScreen",
			build: func() proseBarScreen {
				s := NewSupplierDetailScreen(Deps{}, "4")
				next, _ := s.Update(supplierLoadedMsg{sup: &omsapi.Supplier{
					ID: 4, Name: "Grainger Industrial Supply", SupplierType: "distributor",
					Website: "https://www.grainger.com", AccountNumber: "ACCT-88213",
					Notes:      proseBarLongNote(),
					ItemCount:  212,
					TotalSpent: omsapi.DecimalString("18422.55"),
				}})
				return next.(*SupplierDetailScreen)
			},
		},
	}
	// The WINDOWED CURSOR LISTS, in their own file because they are one recipe
	// rather than one screen — see proseBarWindowedListFixtures.
	out = append(out, proseBarWindowedListFixtures()...)
	// The FLAT cursor lists, the second recipe — see proseBarFlatListFixtures.
	out = append(out, proseBarFlatListFixtures()...)
	// The screens that draw a SECOND SURFACE in place of their list, the third
	// — see proseBarSecondSurfaceFixtures.
	out = append(out, proseBarSecondSurfaceFixtures()...)
	// The lists binding MORE of the movement vocabulary than their bar's recipe
	// named, the fourth — see proseBarWiderVocabularyFixtures.
	out = append(out, proseBarWiderVocabularyFixtures()...)
	// The FIELD FORMS, whose up/down move a focus rather than a cursor, the
	// fifth — see proseBarFieldFormFixtures.
	out = append(out, proseBarFieldFormFixtures()...)
	// The lists whose rows are several lines and whose window already packed them
	// by line cost, the sixth — see proseBarRowPackedListFixtures.
	out = append(out, proseBarRowPackedListFixtures()...)
	// The flat lists sharing their pane with a second SECTION, the seventh — see
	// proseBarSectionFixtures.
	out = append(out, proseBarSectionFixtures()...)
	// The flat lists whose prompt, form or confirm is drawn under or in place of
	// the rows, the eighth — see proseBarFootPromptFixtures.
	out = append(out, proseBarFootPromptFixtures()...)
	// The two scroller sheets whose modes draw in place of their footer, the
	// ninth — see proseBarScrollerSheetFixtures.
	out = append(out, proseBarScrollerSheetFixtures()...)
	// The storage slot list, whose chrome constant counted the bar and
	// slotCardPrompt's overlay as one — see proseBarStorageSlotFixtures.
	out = append(out, proseBarStorageSlotFixtures()...)
	// The universal search palette, the tenth — see proseBarSearchPaletteFixtures.
	out = append(out, proseBarSearchPaletteFixtures()...)
	// The shared REPORT TABLE, on more than one report, the eleventh — see
	// proseBarReportTableFixtures.
	out = append(out, proseBarReportTableFixtures()...)
	// The storage overview, a rack grid gated per direction — see
	// proseBarStorageOverviewFixtures.
	out = append(out, proseBarStorageOverviewFixtures()...)
	// The parts list on an asset, the windowed recipe's last screen, whose parts
	// are several lines and whose pager is a page of them — see
	// proseBarAssetPartsFixtures.
	out = append(out, proseBarAssetPartsFixtures()...)
	// The item's stock history and usage logs, written on the record rather than
	// converted to it — see proseBarItemHistoryFixtures.
	out = append(out, proseBarItemHistoryFixtures()...)
	// The fixture refill screens, written on the record from the start — see
	// proseBarFixtureRefillFixtures.
	out = append(out, proseBarFixtureRefillFixtures()...)
	// Every screen above with a LOAD, in flight and failed, first time and on a
	// refresh — see proseBarLoadStateFixtures. They are built FROM the fixtures
	// above (a refresh starts from a loaded screen), which is why they are
	// appended last rather than listed.
	return append(out, proseBarLoadStateFixtures(out)...)
}

func proseBarSerializedComponents(n int) []omsapi.SerializedComponent {
	rows := make([]omsapi.SerializedComponent, n)
	for i := range rows {
		rows[i] = omsapi.SerializedComponent{
			ID:           fmt.Sprintf("unit-%d", i+1),
			SerialNumber: fmt.Sprintf("SR-%06d", i+1),
			Status:       omsapi.SerialStatusReceived,
			AvailableActions: []string{
				omsapi.SerialActionReceive,
				omsapi.SerialActionInstall,
				omsapi.SerialActionRemove,
				omsapi.SerialActionConsume,
				omsapi.SerialActionRetire,
				omsapi.SerialActionDispose,
			},
		}
	}
	return rows
}

// proseBarReorderDeclines are the reorder queue's four lifecycle keys, which
// change the pane on a row they are NOT named on — and correctly so.
//
// A KEY THAT DECLINES AND SAYS WHY HAS NOT ACTED (AGENTS.md), and this screen is
// where that distinction is load-bearing rather than academic. OMS gates none of
// approve / cancel / mark-ordered / mark-received on a status: `o` on a PENDING
// request would be obeyed and would skip approval, `d` on one would credit stock
// for goods nobody ordered. So the workflow is stated on the CLIENT, by which
// keys the bar offers — and the other half of stating it is that the keys it
// does not offer must SAY SO when they are pressed, or the operator reads a
// wedged program (standing rule: an arm that declines must say why, because
// `return s, nil` redraws a byte-identical pane).
//
// WHICH OF THE FOUR IS DECLINING IS DERIVED AND NOT LISTED: the same map is
// handed to every reorder fixture, and the sweep takes the exemption only where
// that fixture's bar does NOT name the key — so the pending view's `o` and `d`
// are declines while its `a` and `x` are acts, with nothing written down twice.
//
// THE EXEMPTION IS NARROW AND THE REAL CHECK IS ELSEWHERE. This sweep reads the
// PANE, and a decline and an act both change it, so it cannot tell them apart —
// asking for "no command" does not either, because a decline legitimately raises
// a Status toast to say why. What it does hold is that a recorded decline is not
// SILENT: pressing it must change the pane, so an entry cannot hide a key that
// has gone quiet. The biconditional against the WRITE is
// TestReorderQueue_EveryLifecycleKeyActsExactlyWhereItIsNamed, which drives a
// fake OMS and asks whether the request went out — the right instrument, and the
// reason not to re-derive a weaker one here.
var proseBarReorderDeclines = map[string]string{
	"a": "approve on a row that is not pending — the arm says which state the key needs",
	"x": "cancel on a row past cancelling",
	"o": "mark-ordered on a row that is not approved; obeying it would skip approval",
	"d": "mark-received on a row that is not ordered; obeying it would credit stock " +
		"for goods nobody ordered",
}

// proseBarAsset is an asset sheet past its load, with a body long enough to
// scroll and `n` serialized components installed.
func proseBarAsset(components int) *AssetDetailScreen {
	s := NewAssetDetailScreen(Deps{}, "a-1")
	msg := assetDetailLoadedMsg{asset: &omsapi.Asset{
		ID: "a-1", Name: "Haas VF-2SS vertical machining centre",
		AssetTag: "MACH-0042", Status: "operational",
		CategoryName: "Machining", LocationName: "Machine shop, north bay",
		DisplayManufacturer: "Haas Automation", Notes: proseBarLongNote(),
	}}
	for i := 0; i < components; i++ {
		msg.components = append(msg.components, omsapi.SerializedComponent{
			ID:           fmt.Sprint(i + 1),
			SerialNumber: fmt.Sprintf("SN-00%d", i+1),
			ItemName:     "Carbide insert holder",
		})
	}
	for i := 0; i < 12; i++ {
		msg.maintenance = append(msg.maintenance, omsapi.MaintenanceItem{
			ID:    fmt.Sprint(i + 1),
			Title: fmt.Sprintf("Way lube top-up, station %d", i+1),
		})
	}
	next, _ := s.Update(msg)
	return next.(*AssetDetailScreen)
}

// proseBarReorder is reorderPaneFixture at the sweep's own pane, kept separate
// so the size this file drives at is not buried in a call site.
func proseBarReorder(view reorderView, rows int) *ReorderQueueScreen {
	return reorderPaneFixture(view, rows, 80, 24)
}

func proseBarSlot(slot omsapi.StorageSlot) *StorageSlotDetailScreen {
	slot.Notes = proseBarLongNote()
	s := NewStorageSlotDetailScreen(Deps{}, slot.Code)
	next, _ := s.Update(storageSlotDetailLoadedMsg{slot: &slot})
	return next.(*StorageSlotDetailScreen)
}

func proseBarNotifications(n int) []omsapi.Notification {
	out := make([]omsapi.Notification, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, omsapi.Notification{
			ID: i + 1, Type: "reorder", Read: i%3 == 0,
			Title:   fmt.Sprintf("Reorder request #%d needs approval", i+1),
			Message: "Hex bolt M8x40 zinc fell below its minimum at the Grainger price.",
		})
	}
	return out
}

func proseBarSIGMembers(n int) []omsapi.SIGMember {
	out := make([]omsapi.SIGMember, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, omsapi.SIGMember{
			ID:       i + 1,
			Username: fmt.Sprintf("member%02d", i+1),
			Email:    fmt.Sprintf("member%02d@example.org", i+1),
		})
	}
	return out
}

func proseBarPanelTopology() *omsapi.PowerPanelTopology {
	t := &omsapi.PowerPanelTopology{
		ID: 1, Name: "Main distribution panel MDP-1", LocationName: "Machine shop",
		PhaseConfiguration: "3-phase", Voltage: 480, MainBreakerAmperage: 400,
	}
	for i := 0; i < 24; i++ {
		t.Breakers = append(t.Breakers, omsapi.PowerBreakerWithChain{
			PowerBreaker: omsapi.PowerBreaker{
				ID: i + 1, PanelID: 1, Position: fmt.Sprint(i + 1), Amperage: 20,
				Label: fmt.Sprintf("Bay %d receptacles", i+1),
			},
		})
	}
	return t
}

// proseBarLongNote is a body-lengthening note. It is prose rather than repeated
// filler because these bodies are also FOLDED, and a body of one repeated word
// folds differently from one an operator would actually read.
//
// NO TWO OF ITS LINES ARE ALIKE, and that is load-bearing: a scroll of one row
// through a run of identical lines draws the same pane before and after, so
// TestProseBar_EveryMovementKeyIsNamedWhereItMoves would call a working `k` dead.
// It did, on the item sheet, once a longer bar left the window inside the run.
func proseBarLongNote() string {
	var b strings.Builder
	for bay := 1; bay <= 12; bay++ {
		fmt.Fprintf(&b, "Bay %d: stored against the north wall behind the surface grinder; the "+
			"rack is bolted to the slab and the top level needs a pallet jack.\n", bay)
	}
	return strings.TrimSpace(b.String())
}

// ---------------------------------------------------------------------------
// Coverage
// ---------------------------------------------------------------------------

// TestProseBar_EveryConvertedScreenIsSwept: the fixtures and the converted
// screens agree, and every fixture supplies a non-empty bar. The executable
// rendered-footer check below is what makes this source-derived coverage roster
// safe: a dead proseBar method or one View does not draw fails there.
//
// Both halves earn their keep, and both have shipped as defects elsewhere in
// this package. Without the first, a screen converted tomorrow is absent from
// every assertion in this file and passes by not being looked at. Without the
// second, a fixture left in its loading state renders one muted line, answers no
// key, names none, and satisfies the biconditional vacuously.
func TestProseBar_EveryConvertedScreenIsSwept(t *testing.T) {
	want := proseBarReceivers(t)
	got := map[string]bool{}
	for _, f := range proseBarFixtures() {
		got[f.recv] = true
	}
	for recv := range want {
		if !got[recv] {
			t.Errorf("%s declares proseBar but no fixture builds it, so nothing in this "+
				"file ever presses a key at it. Add one in the state the operator reaches "+
				"it in — a converted bar is only honest where it is pressed", recv)
		}
	}
	for recv := range got {
		if !want[recv] {
			t.Errorf("proseBarFixtures builds %s, which no longer declares proseBar. A "+
				"stale fixture is a sweep spending its time on a screen whose bar it "+
				"cannot read, while the one that replaced it goes unchecked", recv)
		}
	}
	for _, f := range proseBarFixtures() {
		s := f.build()
		proseBarSize(s, 80, 40)
		if len(s.proseBar()) == 0 {
			t.Errorf("the %s fixture draws no bar at 80x40, so every assertion this file "+
				"makes about it is vacuous:\n%s", f.name, s.View())
		}
	}
}

// TestProseBar_EveryRecordIsTheRenderedFooter makes the coverage derivation
// safe by proving through each screen's View that proseBar is the bar the
// operator actually sees. It compares the complete rendered tail byte for byte,
// including the separator after AssetDetailScreen's optional action-banner row.
func TestProseBar_EveryRecordIsTheRenderedFooter(t *testing.T) {
	const width, height = 80, 40
	for _, f := range proseBarFixtures() {
		t.Run(f.name, func(t *testing.T) {
			s := proseBarSize(f.build(), width, height)
			if !proseBarFrameFits(s, height) {
				t.Fatalf("the frame does not fit at %dx%d, so its rendered footer tail cannot be checked", width, height)
			}
			want := "\n\n" + stripANSI(s.proseBar().render(proseBarCells(width)))
			got := stripANSI(s.View())
			if !strings.HasSuffix(got, want) {
				t.Errorf("View does not end with the bar its proseBar record renders.\nwant tail:\n%q\ngot:\n%s", want, got)
			}
		})
	}
}

// TestProseBar_EveryProseFooterScreenIsConvertedOrNamed: the REMAINDER is
// stated, not silent.
//
// listNavUnsweptReceivers is the derived roster of every receiver whose bar
// neither existing sweep can read. A receiver in it is now one of three things,
// and all three fail loudly when they are wrong: CONVERTED (it declares
// proseBar, so this file presses keys at it, and it must have been taken out of
// that roster); NAMED in proseBarUnconverted with the shape of the work left; or
// nothing, which is the unexamined remainder this whole area exists to prevent.
//
// It also fails a proseBarUnconverted entry for a screen that HAS been
// converted, for the reason jdeUnsizedDeclineCases gives: a roster is only worth
// keeping if being wrong about it is loud.
func TestProseBar_EveryProseFooterScreenIsConvertedOrNamed(t *testing.T) {
	converted := proseBarReceivers(t)

	var unnamed []string
	for recv := range listNavUnsweptReceivers {
		if converted[recv] {
			t.Errorf("%s declares proseBar — its bar IS a record now and this file presses "+
				"keys at it — but it is still recorded in listNavUnsweptReceivers as a "+
				"surface no sweep can read. A stale exclusion excuses a screen from the "+
				"sweep it passes", recv)
			continue
		}
		if _, named := proseBarUnconverted[recv]; !named {
			unnamed = append(unnamed, recv)
		}
	}
	sort.Strings(unnamed)
	for _, recv := range unnamed {
		t.Errorf("%s still writes its footer as a literal and nothing says what converting "+
			"it would take. Either convert it — a proseBar record, folded against the live "+
			"pane, with the row budget derived from the fold (prose_bar.go) — or record it "+
			"in proseBarUnconverted with the shape of the work. A named partial is a "+
			"decision; an unnamed one is nobody having looked", recv)
	}

	for recv, why := range proseBarUnconverted {
		if converted[recv] {
			t.Errorf("proseBarUnconverted says %s is still a literal (%q), but it declares "+
				"proseBar. The entry has outlived the screen it was written about", recv, why)
		}
		if _, listed := listNavUnsweptReceivers[recv]; !listed {
			t.Errorf("proseBarUnconverted names %s, which listNavUnsweptReceivers no longer "+
				"records as a surface with no readable bar — so this entry is describing a "+
				"screen that is not in the remainder any more", recv)
		}
	}
}

// proseBarKeySpace is the keystrokes the biconditional below presses: the list
// half's space, and the keystrokes a prose bar spells that no list binds.
//
// THE EXTRAS ARE RECORDED, NOT DERIVED FROM THE BARS, and the difference is the
// whole of the reverse half. A space built from what the bars name would press
// every NAMED key and no unnamed one, so a key bound in a handler and named
// nowhere would be pressed in neither direction — verbatim how `N` survived on
// the purchasing list. TestProseBar_EveryNamedKeyIsPressed holds the list in
// both directions: a bar naming a keystroke outside the space fails, and an
// extra no bar names any more fails as stale.
func proseBarKeySpace() []string {
	out := listKeySpace()
	extras := make([]string, 0, len(proseBarExtraKeys))
	for k := range proseBarExtraKeys {
		extras = append(extras, k)
	}
	sort.Strings(extras)
	return append(out, extras...)
}

// proseBarExtraKeys are the keystrokes beyond listKeySpace, each with the screen
// that binds it.
var proseBarExtraKeys = map[string]string{
	"ctrl+z": "BatchScanSerialsScreen undoes the last received unit — a chord a " +
		"scanner's burst of printable characters cannot produce",
}

// TestProseBar_EveryNamedKeyIsPressed: every keystroke a converted bar names is
// in the space the biconditional presses.
//
// A NAMED KEY OUTSIDE THE SPACE IS NEVER PRESSED, so the forward half — a key
// the bar names must act — cannot fail for it, however dead it is. The batch
// scan's `ctrl+z undo last` was the first segment to spell such a key, and it
// would have passed that half by not being looked at.
func TestProseBar_EveryNamedKeyIsPressed(t *testing.T) {
	space := map[string]bool{}
	for _, k := range proseBarKeySpace() {
		space[k] = true
	}
	named := map[string]bool{}
	for _, f := range proseBarFixtures() {
		for _, seg := range proseBarAt(f, 80, 24) {
			for _, k := range seg.Keys {
				named[k] = true
				if !space[k] {
					t.Errorf("%s names %q, which proseBarKeySpace does not contain, so the "+
						"sweep never presses it and a dead key there passes. Record it in "+
						"proseBarExtraKeys with the screen that binds it", f.name, k)
				}
			}
		}
	}
	for k, why := range proseBarExtraKeys {
		if !named[k] {
			t.Errorf("proseBarExtraKeys records %q (%q), but no fixture's bar names it any "+
				"more — a stale extra is a key pressed for a screen that no longer offers it", k, why)
		}
	}
}

// ---------------------------------------------------------------------------
// The rule
// ---------------------------------------------------------------------------

// TestProseBar_TheFooterNamesExactlyTheKeysThatWork is the biconditional, over
// the whole KEY SPACE rather than over the bar's own vocabulary.
//
// THE SPACE AND NOT A VOCABULARY, for the reason listKeySpace records: a roster
// of the tokens the bars happen to spell is safe in the FORWARD direction only,
// because a key bound in a handler and absent from the roster is pressed in
// NEITHER direction — untested rather than passing, which is verbatim how `N` on
// the purchasing list survived a sweep written to catch exactly it. Nothing here
// is curated, so a key bound tomorrow is pressed by this sweep today.
//
// TWO PROBE POSITIONS, because a scrolled body has two edges: `home` and `g` do
// nothing at the TOP and `end` and `G` nothing at the BOTTOM, so a key is dead
// only if it does nothing from either.
//
// THE FORWARD HALF ALLOWS THREE WAYS TO ACT, and the third is what makes `esc
// back` an honest claim rather than an exempted one. A key acts if the clipped
// pane CHANGES, or the screen ISSUES a command (an `E edit` that switches
// screens changes nothing on the pane it was pressed on), or — pressed through a
// real Root — it LEAVES the screen. On most of these sheets the app-wide
// back-step belongs to Root's dispatcher and the screen answers nothing at all,
// so the columnar sweep excuses `esc` outright; here it is PRESSED instead, the
// way TestList_ARefusedPaneNamesAKeyThatReallyLeaves presses the refusal's way
// out, and a bar naming a way off the screen that does not work fails.
//
// THE REVERSE HALF ASKS ONLY FOR THE PANE, not for a command, and that is the
// asymmetry the columnar sweep already carries: a key may legitimately answer a
// press with an explaining toast and change nothing, which is a decline that
// says why rather than a key that escaped the audit. What it may not do is
// change what the operator sees while no word on the bar claims it.
//
// BOTH DIRECTIONS WERE VERIFIED BY REVERTING rather than asserted. Restoring the
// SIG sheet's literal claim — j/k and pgup/pgdn, which is what the string said —
// reported the arrows, g, G, home and end as changing the pane unnamed, which is
// the defect as filed. Adding a `Z zap` segment to the same bar reported Z as
// named and doing nothing. A sweep nobody has watched fail is a sweep nobody
// knows can.
func TestProseBar_TheFooterNamesExactlyTheKeysThatWork(t *testing.T) {
	declined := 0
	for _, f := range proseBarFixtures() {
		t.Run(f.name, func(t *testing.T) {
			probes := proseBarProbes(f)
			bar := proseBarAt(f, 80, 24)
			if len(bar) == 0 {
				t.Fatalf("%s draws no bar at 80x24, so this sweep proves nothing", f.name)
			}
			// A recorded decline that this bar does NOT name has to be a real
			// one: pressing it must change the pane. A silent decline redraws
			// the frame the press before it left, which is the reported "it just
			// hangs" — and an entry for a key that has gone silent would hide
			// exactly that.
			for key, why := range f.declines {
				if bar.names(key) {
					continue
				}
				if c, _ := proseBarKeyEffect(f, 80, 24, nil, key); !c {
					t.Errorf("%s records %q as declining (%q), but pressing it changes "+
						"nothing at all. A silent decline is the wedged-program defect, "+
						"and a stale entry hides it", f.name, key, why)
				}
				declined++
			}
			// On a typing fixture, the rows an UNBOUND character changes when it is
			// typed: the box's own row, and whatever a live search redraws.
			var typedRows string
			if f.typing != "" {
				typedRows = proseBarTypedRows(f, bar)
			}
			var untyped []string
			for _, key := range proseBarKeySpace() {
				var changed, issued bool
				for _, probe := range probes {
					c, i := proseBarKeyEffect(f, 80, 24, probe, key)
					changed = changed || c
					issued = issued || i
				}
				named := bar.names(key)
				if !named && proseBarBoxTakes(f, key) {
					// The box's key, not the bar's — see proseBarFixture.typing.
					// A SPACE appended to a box already holding a value is the one
					// character a pane without colour cannot show: it lands past the
					// value's last glyph, where the box's own padding already was, and
					// only the caret — reverse video, stripped here — moves.
					if !changed && len([]rune(key)) == 1 && key != " " {
						untyped = append(untyped, key)
					}
					continue
				}
				if named && proseBarBoxTakes(f, key) && len([]rune(key)) == 1 &&
					proseBarChangedRows(f, 80, 24, key) == typedRows {
					// A letter the bar names that does nothing but go into the box
					// passes the forward half, because a typed character changes the
					// pane: it is the bar claiming a key the box eats.
					t.Errorf("%s names %q, but on this surface the key only types into the "+
						"box — it changes exactly the rows an unbound character does.\nbar: %s",
						f.name, key, bar.hint())
					continue
				}
				switch {
				case named && !changed && !issued:
					if proseBarLeaves(t, f, key) {
						continue
					}
					t.Errorf("%s names %q but the key does nothing there — it changes no "+
						"pixel of the pane, issues no command, and does not leave the "+
						"screen.\nbar: %s", f.name, key, bar.hint())
				case !named && changed:
					if f.declines[key] != "" {
						// A DECLINE, not an act. It is exempt HERE and checked
						// somewhere stronger — see proseBarReorderDeclines' doc for why
						// this sweep is the wrong instrument for the difference.
						continue
					}
					t.Errorf("%s does not name %q, but pressing it changes what the operator "+
						"sees.\nbar: %s", f.name, key, bar.hint())
				}
			}
			if f.typing != "" && len(untyped) > 0 {
				t.Errorf("the %s fixture is recorded as typing (%q), but %q changed nothing — a "+
					"focused box takes EVERY printable key, so the exemption was taken over a box "+
					"that is not there, or over a key that is not the box's", f.name, f.typing, untyped)
			}
		})
	}
	if declined == 0 {
		t.Error("no fixture exercised a recorded decline, so the exemption above was " +
			"never taken — either the entries are all stale or no fixture reaches the " +
			"state they are about, and both make this sweep weaker than it reads")
	}
}

// TestProseBar_EveryMovementKeyIsNamedWhereItMoves is the claim the conversion
// was FOR, stated on its own rather than left implicit in the biconditional
// above.
//
// The biconditional would pass a bar that named `j/k scroll` alone IF the rest
// of the vocabulary did nothing — and those keystrokes are not dead, they are
// bound by TextScroller.Handle (and, on a cursor list, by the screen's own
// switch), which is the whole report. So this presses each of them against a
// body that really moves and requires BOTH that it moves the pane and that the
// bar says so. It fails against every one of the literals this conversion
// replaced.
//
// It reads the vocabulary out of listNavSetVerb rather than writing the
// keystrokes down here: the vocabulary is one roster (list_nav.go) and a second
// copy in a test is the drift this package keeps paying for. The VERB it asks
// for is immaterial — "move" and "scroll" spell the same keystrokes, which is
// the whole point of that function — so asking for either covers both kinds of
// converted screen.
func TestProseBar_EveryMovementKeyIsNamedWhereItMoves(t *testing.T) {
	var keys []string
	for _, m := range listNavSetVerb("scroll") {
		keys = append(keys, m.Keys...)
	}
	if len(keys) == 0 {
		t.Fatal("the scroll vocabulary spells no keystroke at all, so this sweep would " +
			"assert nothing — listNavSetVerb is what it reads")
	}
	for _, f := range proseBarFixtures() {
		t.Run(f.name, func(t *testing.T) {
			bar := proseBarAt(f, 80, 24)
			moved := 0
			for _, key := range keys {
				// From the TOP for the forward keys, from the BOTTOM for the
				// backward ones: `k` at the top and `j` at the bottom clamp, and a
				// clamped key is not a dead one.
				if !bar.names(key) && proseBarBoxTakes(f, key) {
					// A letter or a caret key going into the focused box is not
					// movement — see proseBarFixture.typing.
					continue
				}
				var changed bool
				for _, probe := range proseBarProbes(f) {
					c, _ := proseBarKeyEffect(f, 80, 24, probe, key)
					changed = changed || c
				}
				if changed {
					moved++
				}
				if changed && !bar.names(key) {
					t.Errorf("%s scrolls on %q and its bar does not say so — the omission "+
						"this conversion exists to close.\nbar: %s", f.name, key, bar.hint())
				}
				if !changed && bar.names(key) {
					t.Errorf("%s names %q among its movement keys and it moves nothing "+
						"there.\nbar: %s", f.name, key, bar.hint())
				}
			}
			switch {
			case f.immobile != "" && moved > 0:
				t.Errorf("the %s fixture is recorded as immobile (%q) and a movement key "+
					"moved it. A reason that has stopped being true is worse than none",
					f.name, f.immobile)
			case f.immobile == "" && moved == 0:
				t.Errorf("no movement key moved the %s fixture at 80x24, so this sweep "+
					"asserted nothing about it — give it a body that outruns the pane, or "+
					"record on the fixture WHY nothing can move there", f.name)
			}
		})
	}
}

// TestProseBar_TheFooterSurvivesEveryDrawablePane: every segment of the bar
// reaches the operator WHOLE, at every pane Root draws.
//
// "A bar the operator cannot read is not honest, it is absent" (AGENTS.md), and
// that is the half the fold and the row budget buy. The literals this replaced
// failed it on both axes at once: they ran past the 51 cells an 80-column pane
// gives, so clampToBox took their tails, and they were budgeted at a flat
// detailFooterRows whatever they folded to, so a two-row footer ran a row past
// the pane and clampToBox — which drops from the BOTTOM — took the fold.
//
// IT IS SCOPED BY A MEASURED BOUNDARY AND COUNTS BOTH SIDES. Below a certain
// height the assembled frame is taller than the pane whatever the footer does:
// scrollerViewHeight floors the scrolled body at four rows, which is more than
// screenBodyRows gives at a terminal height of 9 or less, so the frame overruns
// and the bar is what clampToBox takes. That floor is pre-existing, it is the
// same lie screenBodyHeight tells, and removing it means giving these sheets the
// REFUSAL ListScreen has (listTooShort) — named in proseBarUnconverted's header
// as work left rather than papered over here. So the claim is scoped by
// proseBarFrameFits, which MEASURES what the screen handed over against the pane
// it was drawn into, and the sweep fails if either side of that boundary was
// never reached — a scoping that avoids the state is a way of asserting nothing.
//
// VERIFIED BY REVERTING: replacing proseBar.render's fold with a single
// StyleMuted.Render of the whole hint — which is exactly what the literals it
// replaced did — reports from 80x12 that the pane does not carry
// "g/G home/end top/bottom" whole.
func TestProseBar_TheFooterSurvivesEveryDrawablePane(t *testing.T) {
	widths, heights := jdeDrawableWidths(), jdePaneHeights()
	for _, f := range proseBarFixtures() {
		t.Run(f.name, func(t *testing.T) {
			fits, overruns := 0, 0
			for _, w := range widths {
				for _, h := range heights {
					s := f.build()
					proseBarSize(s, w, h)
					bar := s.proseBar()
					if len(bar) == 0 {
						continue
					}
					if !proseBarFrameFits(s, h) {
						overruns++
						continue
					}
					fits++
					pane := clampToBox(s.View(), screenBodyCells(w), screenBodyRows(h))
					for _, seg := range bar {
						if !strings.Contains(pane, seg.Hint) {
							t.Errorf("at %dx%d the %s bar claims %q and the pane does not "+
								"carry it whole — a key named past the cut is a key named "+
								"nowhere.\nbar: %s\npane:\n%s",
								w, h, f.name, seg.Hint, bar.hint(), pane)
						}
					}
				}
			}
			if fits == 0 {
				t.Errorf("the %s frame fitted no pane Root draws, so this sweep asserted "+
					"nothing", f.name)
			}
			if overruns == 0 {
				t.Errorf("the %s frame fitted EVERY pane Root draws, so the boundary this "+
					"sweep is scoped by was never reached — if the scrolled body's floor "+
					"has been fixed, take the scoping out rather than leaving a check that "+
					"cannot tell", f.name)
			}
		})
	}
}

// TestProseBar_EveryBarSegmentSpellsItsOwnKeys is the transcription rule, held
// structurally.
//
// A proseBarItem's Keys must be what its Hint SAYS and never a superset:
// crediting a segment with a synonym is the code making a claim on the bar's
// behalf, which is the defect the record exists to report. listBarKeyNames and
// poBarKeyNames keep the same rule from the other side, by transcribing a token
// rather than interpreting it; here the record carries both halves, so the check
// is that the keystroke appears in the words.
//
// The three spellings a terminal key has that its words do not are named
// individually with the reason, rather than skipped by a rule clever enough to
// swallow a real one.
//
// A BARE strings.Contains IS NOT ENOUGH, and that was this check's first
// version: every single-letter key is a letter of some English word, so
// `{Keys: ["s"], Hint: "a assign C/L/E"}` passed on the `s` in "assign" — the
// sweep making the bar's claim for it, which is the defect the check is about,
// sitting inside the check. A key has to be a WHOLE part of one of the segment's
// leading tokens (proseBarSpells), which is the shape every bar in this program
// writes: the keys lead the words, singly ("x delete"), slashed ("g/G", "n/esc")
// or as a glyph pair ("↑↓").
func TestProseBar_EveryBarSegmentSpellsItsOwnKeys(t *testing.T) {
	seen := 0
	for _, f := range proseBarFixtures() {
		for _, seg := range proseBarAt(f, 80, 24) {
			if len(seg.Keys) == 0 {
				t.Errorf("%s draws the segment %q, which claims no key at all — a word on "+
					"the bar that is not answerable is the literal this record replaced",
					f.name, seg.Hint)
			}
			for _, k := range seg.Keys {
				if !proseBarSpells(seg.Hint, k) {
					t.Errorf("%s: the segment %q claims %q, but it does not SAY it. A "+
						"segment credited with a key it does not spell is the sweep making "+
						"the bar's claim for it", f.name, seg.Hint, k)
				}
				seen++
			}
		}
	}
	if seen == 0 {
		t.Fatal("no bar segment was read, so this rule asserted nothing")
	}
}

// proseBarKeyGlyphs are the keystrokes a bar spells as a symbol rather than as
// their name, each recorded individually with what it is — the arrows because a
// legend has no room for the words, and pgdown because "pgdn" is what every bar
// in this program has always written.
//
// Recorded rather than absorbed into a fuzzy match, for the reason
// listNavUnspellableKeyTypes gives about its own exceptions: a rule loose enough
// to accept these silently would accept a segment that spells nothing.
var proseBarKeyGlyphs = map[string]string{
	"up":     "↑",
	"down":   "↓",
	"left":   "←",
	"right":  "→",
	"pgdown": "pgdn",
	// The space bar's keystroke is a blank, which no word on a bar can be.
	" ": "space",
}

// proseBarSpells reports whether a segment's words really say a keystroke.
//
// The key must be a WHOLE part of one of the segment's LEADING tokens — the
// tokens before the first plain English word — split on "/", which is how every
// bar in this program writes keys: "x delete", "g/G home/end top/bottom",
// "n/esc cancel". The arrow PAIR is the one token that spells two keys at once
// and carries no separator, so a glyph is matched inside its token.
func proseBarSpells(hint, key string) bool {
	want := key
	if glyph, ok := proseBarKeyGlyphs[key]; ok {
		want = glyph
	}
	for _, token := range strings.Fields(hint) {
		// A key that IS the separator — `/ filter` — splits into nothing, so it
		// is matched as the whole token before the token is split.
		if token == want {
			return true
		}
		if strings.ContainsAny(token, "↑↓←→") {
			if strings.Contains(token, want) {
				return true
			}
			continue
		}
		for _, part := range strings.Split(token, "/") {
			if part == want {
				return true
			}
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Driving
// ---------------------------------------------------------------------------

// proseBarSize puts a screen at a terminal size the way Root does.
func proseBarSize(s proseBarScreen, w, h int) proseBarScreen {
	next, _ := s.Update(tea.WindowSizeMsg{Width: w, Height: h})
	if out, ok := next.(proseBarScreen); ok {
		return out
	}
	return s
}

// proseBarAt builds a fixture, sizes it, and returns the bar it is drawing.
func proseBarAt(f proseBarFixture, w, h int) proseBar {
	return proseBarSize(f.build(), w, h).proseBar()
}

// proseBarKeyEffect presses one key from a fresh screen (after an optional
// probe run) and reports whether it changed the CLIPPED pane or issued a
// command.
//
// MEASURED ON THE CLIPPED PANE and not on a state fingerprint, which is the
// difference between standing rule 1 and a weaker cousin of it: the rule is that
// a keypress produces a distinguishable operator-VISIBLE change, and a
// fingerprint over the screen's fields reports a key that moved a number nothing
// draws as working. listKeyEffectAt makes the same choice for the same reason,
// and jdePlaceOf's blind spot on the slot-generate run report is what it cost
// when it was not made.
func proseBarKeyEffect(f proseBarFixture, w, h int, probe []string, key string) (changed, issued bool) {
	s := proseBarSize(f.build(), w, h)
	press := func(k string) tea.Cmd {
		next, cmd := s.Update(listRuneKey(k))
		if out, ok := next.(proseBarScreen); ok {
			s = out
		}
		return cmd
	}
	for _, p := range probe {
		press(p)
	}
	pane := func() string {
		return clampToBox(s.View(), screenBodyCells(w), screenBodyRows(h))
	}
	before := pane()
	cmd := press(key)
	return pane() != before, cmd != nil
}

// proseBarChangedRows presses one key from a fresh screen and reports WHICH rows
// of the clipped pane it changed, as a comparable string ("" when none did, and
// "height" when the pane changed height, which no typed character does).
func proseBarChangedRows(f proseBarFixture, w, h int, key string) string {
	s := proseBarSize(f.build(), w, h)
	pane := func() []string {
		return strings.Split(clampToBox(s.View(), screenBodyCells(w), screenBodyRows(h)), "\n")
	}
	before := pane()
	next, _ := s.Update(listRuneKey(key))
	if out, ok := next.(proseBarScreen); ok {
		s = out
	}
	after := pane()
	if len(before) != len(after) {
		return "height"
	}
	var rows []string
	for i := range before {
		if before[i] != after[i] {
			rows = append(rows, fmt.Sprint(i))
		}
	}
	return strings.Join(rows, ",")
}

// proseBarTypedRows is the rows a typing fixture's box changes for a character
// nothing binds — the reference a NAMED letter is compared against. It is the
// first printable key the bar does not name, which on every box is a character;
// the space is passed over because appended to a value it is invisible here (see
// the untyped guard).
func proseBarTypedRows(f proseBarFixture, bar proseBar) string {
	for _, key := range proseBarKeySpace() {
		if len([]rune(key)) == 1 && key != " " && !bar.names(key) && proseBarBoxTakes(f, key) {
			return proseBarChangedRows(f, 80, 24, key)
		}
	}
	return ""
}

// proseBarLeaves reports whether the key gets the operator off the screen, asked
// of a REAL Root rather than read off Root's switch.
//
// The back-stack is exercised both empty and loaded, for the reason
// TestList_ARefusedPaneNamesAKeyThatReallyLeaves gives: "esc worked" for the
// wrong one of pop-the-stack and fall-home is how a way out comes to be named on
// a frame it does not really work on.
func proseBarLeaves(t *testing.T, f proseBarFixture, key string) bool {
	t.Helper()
	for _, withHistory := range []bool{false, true} {
		screen := proseBarSize(f.build(), 80, 24)
		r := newTestRoot(screen)
		if withHistory {
			r.history = []navEntry{{screen: NewWelcomeScreen(), ws: WSScan}}
		}
		next, _ := r.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		r = next.(Root)
		after, _ := r.Update(listRuneKey(key))
		if after.(Root).screen == Screen(screen) {
			return false
		}
	}
	return true
}

// proseBarFrameFits reports whether what the screen handed over fits the pane it
// was drawn into.
//
// MEASURED, not named, and measured on what the screen HANDED OVER rather than
// on the clipped pane — after clampToBox no frame can be too big, because the
// truncation has already happened. It is the same question frameFits asks on the
// report table, for the same reason.
func proseBarFrameFits(s proseBarScreen, terminalHeight int) bool {
	return lipgloss.Height(s.View()) <= screenBodyRows(terminalHeight)
}
