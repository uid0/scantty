package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// prose_bar_scroller_sheets_test.go — the fixtures for the NINTH shape taken out
// of proseBarUnconverted: the two scroller sheets whose MODES draw in place of the
// footer, the item sheet (InventoryDetailScreen) and the work-order sheet
// (WorkOrderDetailScreen).
//
// THE SHAPE, and why the two were one tranche. Each is a TextScroller sheet — the
// group the first conversions came out of — with a literal footer naming `j/k
// scroll` while the whole movement vocabulary scrolled it; and each opens MODES
// that draw their own prompt where that footer was: three pick modals on the item
// sheet (cycle count, use/consume, packs) beside its delete confirm, and fourteen
// on the work-order sheet (four pick lists, the stock-item picker, eight forms,
// the lockout review, and the finalize confirm). Their recorded open question was
// whether each prompt already named every key its switch answers. The per-mode
// answer is written where each bar is built (inventory_detail.go's bar functions,
// wo_detail_bar.go's header); what this file holds is that the answer is TRUE, by
// pressing the whole key space at every surface in every state its bar changes
// shape in.
//
// TWO SURFACES STAY PROMPTS THAT NAME THEIR OWN KEYS and answer nil — the item
// sheet's delete confirm and the work-order sheet's finalize/cancel confirm, the
// y/n idiom every confirm in this package spells — and a modal whose write is out
// on the item sheet answers nil too, because every key there is ignored. Those
// are held on the pane by TestProseBarScrollerSheets_ThePromptsThatNameTheirOwnKeysReachThePane.
//
// EVERY WINDOW THE CONVERSION INTRODUCED HAS AN OVERSIZED FIXTURE — a row taller
// than the whole pane — because those lists drew every row with no window, and a
// footer check whose fixtures cannot push the footer off is not checking the
// footer (TestProseBarScrollerSheets_AnOversizedRowFitsAndMarksItsCut).

// proseBarScrollerSheetFixtures is both sheets, in every state their bars change
// shape in.
func proseBarScrollerSheetFixtures() []proseBarFixture {
	return append(proseBarItemSheetFixtures(), proseBarWOSheetFixtures()...)
}

const (
	proseBarDigitBox   = "a counted quantity: the box takes digits, and the arm drops every other rune"
	proseBarOneRowList = "one row, so there is nowhere for the cursor to go"
)

// ---------------------------------------------------------------------------
// The item sheet
// ---------------------------------------------------------------------------

func proseBarItemSheetFixtures() []proseBarFixture {
	const box = "a focused text box takes every printable key"
	const modalBox = "the focused box takes the movement keys, and no list is drawn to move"
	out := []proseBarFixture{
		// The ordinary item: the stock keys, no serial keys, no pack key.
		{
			name: "item detail", recv: "InventoryDetailScreen",
			build: func() proseBarScreen { return proseBarItemSheet(nil) },
		},
		// Every conditional segment ON: a serialized, sealed+open item, retired.
		{
			name: "item detail/serialized, open-closed, retired", recv: "InventoryDetailScreen",
			build: func() proseBarScreen {
				return proseBarItemSheet(func(it *omsapi.Item) {
					open := bagItem()
					it.CountMode, it.CountLevel, it.PackagingLevels = open.CountMode, open.CountLevel, open.PackagingLevels
					it.OnHandDisplay, it.OpenContainerCount = open.OnHandDisplay, open.OpenContainerCount
					it.IsSerialized, it.IsRetired = true, true
				})
			},
		},
		// The kit question unanswered: every stock and serial key comes off.
		{
			name: "item detail/kit question open", recv: "InventoryDetailScreen",
			build: func() proseBarScreen {
				s := NewInventoryDetailScreen(Deps{}, "itm-1")
				next, _ := s.Update(inventoryDetailLoadedMsg{item: proseBarItemRecord(func(it *omsapi.Item) {
					it.IsSerialized = true
				})})
				return next.(proseBarScreen)
			},
		},
		{
			name: "item detail/not found", recv: "InventoryDetailScreen",
			build: func() proseBarScreen {
				next, _ := NewInventoryDetailScreen(Deps{}, "itm-1").Update(inventoryDetailLoadedMsg{})
				return next.(proseBarScreen)
			},
			immobile: "no item, so there is no body to scroll — `r` and `esc` must still be named",
		},

		// --- cycle count ---------------------------------------------------------
		{
			name: "item detail/cycle count, quantity", recv: "InventoryDetailScreen",
			typing: proseBarDigitBox, digits: true, immobile: modalBox,
			build: func() proseBarScreen { return proseBarPress(proseBarItemSheet(nil), "c") },
		},
		{
			name: "item detail/cycle count, open containers", recv: "InventoryDetailScreen",
			typing: proseBarDigitBox, digits: true, immobile: modalBox,
			build: func() proseBarScreen {
				return proseBarPress(proseBarItemSheet(func(it *omsapi.Item) {
					open := bagItem()
					it.CountMode, it.CountLevel, it.PackagingLevels = open.CountMode, open.CountLevel, open.PackagingLevels
					it.OnHandDisplay, it.OpenContainerCount = open.OnHandDisplay, open.OpenContainerCount
				}), "c", "2", "enter")
			},
		},
		// The default reason is the third of seven, so both directions move.
		{
			name: "item detail/cycle count, reason", recv: "InventoryDetailScreen",
			build: func() proseBarScreen { return proseBarPress(proseBarItemSheet(nil), "c", "2", "enter") },
		},
		{
			name: "item detail/cycle count, note", recv: "InventoryDetailScreen",
			typing: box, immobile: modalBox,
			build: func() proseBarScreen { return proseBarPress(proseBarItemSheet(nil), "c", "2", "enter", "enter") },
		},

		// --- use / consume ---------------------------------------------------------
		{
			name: "item detail/consume, quantity", recv: "InventoryDetailScreen",
			typing: proseBarDigitBox, digits: true, immobile: modalBox,
			build: func() proseBarScreen { return proseBarPress(proseBarItemSheet(nil), "u") },
		},
		{
			name: "item detail/consume, committees", recv: "InventoryDetailScreen",
			build: func() proseBarScreen {
				s := proseBarConsumeSIGs(proseBarItemSheet(nil), proseBarCommittees(proseBarFlatLongRows))
				return proseBarWalk(s, "j")
			},
		},
		// The list still loading: the no-charge row alone, so nothing moves, and the
		// working line rides the foot the window is budgeted around.
		{
			name: "item detail/consume, committees loading", recv: "InventoryDetailScreen",
			build:    func() proseBarScreen { return proseBarPress(proseBarItemSheet(nil), "u", "2", "enter") },
			immobile: "the committee list has not loaded: one row, the no-charge one",
		},
		{
			name: "item detail/consume, committees unavailable", recv: "InventoryDetailScreen",
			build: func() proseBarScreen {
				s := proseBarPress(proseBarItemSheet(nil), "u", "2", "enter")
				next, _ := s.Update(consumeSIGsLoadedMsg{err: fmt.Errorf("%s", proseLoadGatewayPage)})
				return next.(proseBarScreen)
			},
			immobile: "the committee list failed: one row, the no-charge one",
		},
		{
			name: "item detail/consume, note", recv: "InventoryDetailScreen",
			typing: box, immobile: modalBox,
			build: func() proseBarScreen {
				s := proseBarConsumeSIGs(proseBarItemSheet(nil), proseBarCommittees(3))
				return proseBarPress(s, "enter")
			},
		},

		// --- packs -----------------------------------------------------------------
		// TWO options, so both positions are an edge: otherEdge walks to the second.
		{
			name: "item detail/packs", recv: "InventoryDetailScreen", otherEdge: []string{"j"},
			build: func() proseBarScreen {
				return proseBarPress(proseBarItemSheet(func(it *omsapi.Item) {
					open := bagItem()
					it.CountMode, it.CountLevel, it.PackagingLevels = open.CountMode, open.CountLevel, open.PackagingLevels
					it.OnHandDisplay, it.OpenContainerCount = open.OnHandDisplay, open.OpenContainerCount
				}), "p")
			},
		},
	}
	return append(out, proseBarOversizedItemSheet()...)
}

// proseBarOversizedItemSheet is the committee window with its cursor on a
// committee whose name is taller than the pane.
func proseBarOversizedItemSheet() []proseBarFixture {
	return []proseBarFixture{{
		name: "item detail/consume, committees, oversized row", recv: "InventoryDetailScreen",
		build: func() proseBarScreen {
			s := proseBarConsumeSIGs(proseBarItemSheet(nil), []omsapi.SIG{{ID: 1, Name: proseBarTallName()}})
			return proseBarPress(s, "j")
		},
		otherEdge: []string{"k"},
	}}
}

// proseBarItemRecord is an item with a description long enough to scroll.
func proseBarItemRecord(mod func(*omsapi.Item)) *omsapi.Item {
	it := &omsapi.Item{
		ID: "itm-1", Name: "Hex bolt M8x40 zinc-plated, grade 8.8, box of 100", SKU: "HB-M8X40-Z",
		Stock: 240, MinimumStock: 50, ReorderQuantity: 200, UnitCost: "0.18",
		CategoryName: "Fasteners", Location: "Aisle 4, bin 12",
		Description: proseBarLongNote(),
	}
	if mod != nil {
		mod(it)
	}
	return it
}

// proseBarItemSheet is the item sheet past every load it starts, with the kit
// question answered "an ordinary item".
func proseBarItemSheet(mod func(*omsapi.Item)) *InventoryDetailScreen {
	s := NewInventoryDetailScreen(Deps{}, "itm-1")
	for _, msg := range []any{
		inventoryDetailLoadedMsg{item: proseBarItemRecord(mod)},
		inventoryKitLoadedMsg{},
		inventoryUsedByLoadedMsg{},
		inventoryPurchaseHistoryLoadedMsg{history: &omsapi.ItemPurchaseHistory{}},
	} {
		next, _ := s.Update(msg)
		s = next.(*InventoryDetailScreen)
	}
	return s
}

// proseBarConsumeSIGs opens the use/consume prompt, types a quantity, reaches the
// committee step and delivers the committee list.
func proseBarConsumeSIGs(s *InventoryDetailScreen, sigs []omsapi.SIG) proseBarScreen {
	out := proseBarPress(proseBarSize(s, 80, 24), "u", "2", "enter")
	next, _ := out.Update(consumeSIGsLoadedMsg{sigs: sigs})
	return next.(proseBarScreen)
}

func proseBarCommittees(n int) []omsapi.SIG {
	out := make([]omsapi.SIG, n)
	for i := range out {
		out[i] = omsapi.SIG{ID: i + 1, Name: fmt.Sprintf("Committee %02d — metal fabrication and welding", i+1)}
	}
	return out
}

// ---------------------------------------------------------------------------
// The work-order sheet
// ---------------------------------------------------------------------------

func proseBarWOSheetFixtures() []proseBarFixture {
	const box = "a focused text box takes every printable key"
	const formBox = "a form whose box holds the focus: the movement keys go into it or do nothing"
	loaded := func() *WorkOrderDetailScreen { return proseBarWO(nil) }
	long := func() *WorkOrderDetailScreen { return proseBarWOLong(proseBarFlatLongRows) }
	out := []proseBarFixture{
		// Every conditional segment on: tasks, a running clock's `s`, lockout steps
		// and a parked scan.
		{
			name: "work order detail", recv: "WorkOrderDetailScreen",
			build: func() proseBarScreen { return long() },
		},
		// A finished job with nothing on it: the status keys stay (they still act),
		// the clock, the task list, the lockout list and the review come off — and
		// the answer row takes a place between the body and the bar.
		{
			name: "work order detail/completed, answer row", recv: "WorkOrderDetailScreen",
			build: func() proseBarScreen {
				s := proseBarWO(func(wo *omsapi.WorkOrder) { wo.Status = "completed" })
				s.setAction("completed OK", StatusOK)
				return s
			},
		},

		// --- tasks -----------------------------------------------------------------
		{
			name: "work order detail/tasks", recv: "WorkOrderDetailScreen",
			build: func() proseBarScreen { return proseBarWalk(proseBarPress(long(), "t"), "j") },
		},
		{
			name: "work order detail/tasks, updating", recv: "WorkOrderDetailScreen",
			build: func() proseBarScreen { return proseBarPress(proseBarWalk(proseBarPress(long(), "t"), "j"), " ") },
		},
		{
			name: "work order detail/tasks, one step", recv: "WorkOrderDetailScreen",
			build:    func() proseBarScreen { return proseBarPress(proseBarWOLong(1), "t") },
			immobile: proseBarOneRowList,
		},

		// --- materials -------------------------------------------------------------
		{
			name: "work order detail/materials", recv: "WorkOrderDetailScreen",
			build: func() proseBarScreen { return proseBarWalk(proseBarPress(long(), "M"), "j") },
		},
		{
			name: "work order detail/materials, updating", recv: "WorkOrderDetailScreen",
			build: func() proseBarScreen { return proseBarPress(proseBarWalk(proseBarPress(long(), "M"), "j"), "d") },
		},
		{
			name: "work order detail/materials, empty", recv: "WorkOrderDetailScreen",
			build: func() proseBarScreen { return proseBarPress(loaded(), "M") },
			immobile: "a corrective job with no lines: `a` and the way back must be named, " +
				"and nothing that acts on a row",
		},
		{
			name: "work order detail/add material", recv: "WorkOrderDetailScreen", typing: box,
			build: func() proseBarScreen { return proseBarPress(loaded(), "M", "a") },
		},
		{
			name: "work order detail/add material, stock item field", recv: "WorkOrderDetailScreen",
			build: func() proseBarScreen { return proseBarPress(loaded(), "M", "a", "tab", "tab", "tab", "tab") },
		},
		{
			name: "work order detail/add material, adding", recv: "WorkOrderDetailScreen", typing: box,
			build: func() proseBarScreen { return proseBarPress(loaded(), "M", "a", "x", "enter") },
		},
		{
			name: "work order detail/stock item picker", recv: "WorkOrderDetailScreen",
			build: func() proseBarScreen { return proseBarWalk(proseBarWOItemPicker(proseBarFlatLongRows), "j") },
		},
		{
			name: "work order detail/stock item picker, filtering", recv: "WorkOrderDetailScreen", typing: box,
			build:    func() proseBarScreen { return proseBarPress(proseBarWOItemPicker(proseBarFlatLongRows), "/") },
			immobile: formBox,
		},
		{
			name: "work order detail/material cost", recv: "WorkOrderDetailScreen", typing: box,
			build:    func() proseBarScreen { return proseBarPress(proseBarWalk(proseBarPress(long(), "M"), "j"), "c") },
			immobile: formBox,
		},

		// --- the forms off the sheet -------------------------------------------------
		{
			name: "work order detail/photo", recv: "WorkOrderDetailScreen", typing: box,
			build:    func() proseBarScreen { return proseBarPress(loaded(), "p") },
			immobile: formBox,
		},
		{
			name: "work order detail/upload pdf", recv: "WorkOrderDetailScreen", typing: box,
			build:    func() proseBarScreen { return proseBarPress(loaded(), "U") },
			immobile: formBox,
		},
		{
			name: "work order detail/checklist", recv: "WorkOrderDetailScreen",
			build: func() proseBarScreen { return proseBarPress(loaded(), "v") },
		},
		{
			name: "work order detail/checklist, notes", recv: "WorkOrderDetailScreen", typing: box,
			build: func() proseBarScreen { return proseBarPress(loaded(), "v", "shift+tab") },
		},
		{
			name: "work order detail/notes", recv: "WorkOrderDetailScreen", typing: box,
			build:    func() proseBarScreen { return proseBarPress(loaded(), "E") },
			immobile: formBox,
		},
		{
			name: "work order detail/notes, saving", recv: "WorkOrderDetailScreen", typing: box,
			build:    func() proseBarScreen { return proseBarPress(loaded(), "E", "enter") },
			immobile: formBox,
		},

		// --- tools -----------------------------------------------------------------
		{
			name: "work order detail/tools", recv: "WorkOrderDetailScreen",
			build: func() proseBarScreen { return proseBarWalk(proseBarPress(long(), "T"), "j") },
		},
		{
			name: "work order detail/tools, empty", recv: "WorkOrderDetailScreen",
			build:    func() proseBarScreen { return proseBarPress(loaded(), "T") },
			immobile: "no tool rows: `a` and the way back must be named, and nothing that acts on a row",
		},
		{
			name: "work order detail/add tool", recv: "WorkOrderDetailScreen", typing: box,
			build: func() proseBarScreen { return proseBarPress(loaded(), "T", "a") },
		},
		{
			name: "work order detail/add tool, required field", recv: "WorkOrderDetailScreen",
			build: func() proseBarScreen { return proseBarPress(loaded(), "T", "a", "tab", "tab", "tab") },
		},
		{
			name: "work order detail/tool location", recv: "WorkOrderDetailScreen", typing: box,
			build:    func() proseBarScreen { return proseBarPress(proseBarWalk(proseBarPress(long(), "T"), "j"), "l") },
			immobile: formBox,
		},

		// --- lockout ---------------------------------------------------------------
		{
			name: "work order detail/lockout", recv: "WorkOrderDetailScreen",
			build:    func() proseBarScreen { return proseBarWalk(proseBarPress(long(), "L"), "j") },
			declines: proseBarWOLotoSpace,
		},
		{
			name: "work order detail/lockout review", recv: "WorkOrderDetailScreen",
			build:    proseBarWOLotoReview,
			declines: proseBarWOLotoReviewDeclines(),
		},
	}
	return append(out, proseBarOversizedWOSheet()...)
}

// proseBarWOLotoSpace is `space` on the lockout list: bound only to say it
// records nothing (woLotoSpaceNote), which is a key that declines and says why.
var proseBarWOLotoSpace = map[string]string{
	" ": "space is bound only to say it records nothing (woLotoSpaceNote)",
}

// proseBarWOLotoReview is the lockout review frame on a step whose text outruns
// an 80x24 pane.
func proseBarWOLotoReview() proseBarScreen {
	return proseBarPress(proseBarWalk(proseBarPress(proseBarWOLong(proseBarFlatLongRows), "L"), "j"), "enter")
}

// proseBarWOLotoReviewDeclines is every key the review frame's bar does not
// name, each of which declines and says so on the frame.
//
// DERIVED FROM THE BAR, AND THE REASON IT MAY BE: the frame answers EVERY key it
// does not bind with "<key> does not record anything" (handleLotoConfirmKey) — a
// frame that binds a handful of keys and answered the rest with nil redrew
// itself byte for byte, and the press most likely to land there is a reflexive
// second `enter`. So the reverse half cannot fail here by construction, and the
// sweep's decline check is what holds instead: every one of these must still
// change the pane. The WRITE is held on the stronger instrument,
// TestWOLoto_NoKeyBUTyRecordsAnything, which asks the fake OMS.
func proseBarWOLotoReviewDeclines() map[string]string {
	bar := proseBarSize(proseBarWOLotoReview(), 80, 24).proseBar()
	out := map[string]string{}
	for _, k := range proseBarKeySpace() {
		if !bar.names(k) {
			out[k] = "the review frame answers every unbound key with what the key did not do"
		}
	}
	return out
}

// proseBarOversizedWOSheet is every window the work-order sheet's conversion
// introduced, each with its cursor on a row taller than the pane.
func proseBarOversizedWOSheet() []proseBarFixture {
	return []proseBarFixture{
		{
			name: "work order detail/tasks, oversized row", recv: "WorkOrderDetailScreen",
			build: func() proseBarScreen {
				s := proseBarWOLong(1)
				s.wo.TaskCompletions[0].TaskTitle = proseBarTallName()
				return proseBarPress(s, "t")
			},
			immobile: proseBarOneRowList,
		},
		{
			name: "work order detail/materials, oversized row", recv: "WorkOrderDetailScreen",
			build: func() proseBarScreen {
				s := proseBarWOLong(1)
				s.wo.MaterialUsage[0].MaterialName = proseBarTallName()
				return proseBarPress(s, "M")
			},
			immobile: proseBarOneRowList,
		},
		{
			name: "work order detail/tools, oversized row", recv: "WorkOrderDetailScreen",
			build: func() proseBarScreen {
				s := proseBarWOLong(1)
				s.wo.ToolRows[0].Name = proseBarTallName()
				return proseBarPress(s, "T")
			},
			immobile: proseBarOneRowList,
		},
		{
			name: "work order detail/lockout, oversized row", recv: "WorkOrderDetailScreen",
			build: func() proseBarScreen {
				s := proseBarWOLong(1)
				s.wo.LotoCompletions[0].SourceLabel = proseBarTallName()
				return proseBarPress(s, "L")
			},
			immobile: proseBarOneRowList,
			declines: proseBarWOLotoSpace,
		},
		{
			name: "work order detail/stock item picker, oversized row", recv: "WorkOrderDetailScreen",
			build: func() proseBarScreen {
				s := proseBarWOItemPicker(1).(*WorkOrderDetailScreen)
				s.amItems[0].Name = proseBarTallName()
				s.applyItemFilter()
				return proseBarPress(s, "j")
			},
			otherEdge: []string{"k"},
		},
	}
}

// proseBarWO is a work order with a body long enough to scroll and nothing on
// it: no tasks, materials, tools or lockout steps.
func proseBarWO(mod func(*omsapi.WorkOrder)) *WorkOrderDetailScreen {
	wo := &omsapi.WorkOrder{
		ID: "wo1", DisplayTitle: "Quarterly spindle service, Haas VF-2SS", Status: "in_progress",
		AssetName: "Haas VF-2SS vertical machining centre", Description: proseBarLongNote(),
		Notes: proseBarLongNote(),
	}
	if mod != nil {
		mod(wo)
	}
	s := NewWorkOrderDetailScreen(Deps{}, "wo1")
	next, _ := s.Update(woDetailLoadedMsg{wo: wo})
	return next.(*WorkOrderDetailScreen)
}

// proseBarWOLong is a work order carrying `n` of every list — tasks, materials,
// tools and lockout steps — and a scanned sheet parked for review. Each row is
// its own ordinary length, and the rows differ AT THE FRONT so a moved cursor is
// a changed pane (AGENTS.md's fixture rule for movement).
func proseBarWOLong(n int) *WorkOrderDetailScreen {
	return proseBarWO(func(wo *omsapi.WorkOrder) {
		for i := 0; i < n; i++ {
			wo.TaskCompletions = append(wo.TaskCompletions, omsapi.WorkOrderTaskCompletion{
				ID: fmt.Sprintf("tc-%d", i+1), TaskTitle: fmt.Sprintf("%02d Inspect way covers and wipers", i+1),
				IsRequired: i%2 == 0, ElapsedSeconds: 60 * i,
			})
			wo.MaterialUsage = append(wo.MaterialUsage, omsapi.WorkOrderMaterialUsage{
				ID: fmt.Sprintf("mu-%d", i+1), MaterialName: fmt.Sprintf("%02d Way lube, Mobil Vactra 2", i+1),
				IsAdHoc: true, QuantityUsed: "1", Unit: "L", UnitCost: "14.50", ActualCost: "14.50",
			})
			wo.ToolRows = append(wo.ToolRows, omsapi.WorkOrderToolRow{
				ID: fmt.Sprintf("wt-%d", i+1), Name: fmt.Sprintf("%02d Torque wrench, 3/8 drive", i+1),
				IsAdHoc: true, Quantity: 1, ResolvedLocation: "Tool crib, drawer 3",
			})
			wo.LotoCompletions = append(wo.LotoCompletions, omsapi.WorkOrderLotoCompletion{
				ID: fmt.Sprintf("lc-%d", i+1), SourceLabel: fmt.Sprintf("%02d Electrical 480V main disconnect", i+1),
				IsolationPoint:  strings.Repeat("Panel MDP-1 breaker 14, north wall of the machine shop. ", 3),
				RequiredDevices: strings.Repeat("Red padlock and breaker lockout clamp. ", 5),
				SourceType:      "electrical",
			})
		}
		wo.Submissions = []omsapi.WorkOrderSubmission{{
			ID: "sub-1", Status: omsapi.WorkOrderSubmissionPendingReview, Source: "email",
		}}
	})
}

// proseBarWOItemPicker opens the add-material form, delivers a catalogue of `n`
// items, and opens the stock-item picker on it.
func proseBarWOItemPicker(n int) proseBarScreen {
	s := proseBarPress(proseBarSize(proseBarWO(nil), 80, 24), "M", "a")
	items := make([]omsapi.Item, n)
	for i := range items {
		items[i] = omsapi.Item{
			ID: fmt.Sprintf("itm-%d", i+1), Name: fmt.Sprintf("%02d Hex bolt M8x40 zinc", i+1),
			SKU: fmt.Sprintf("HB-%03d", i+1), UnitCost: "0.18",
		}
	}
	next, _ := s.Update(woItemsLoadedMsg{items: items})
	return proseBarPress(next.(proseBarScreen), "tab", "tab", "tab", "tab", " ")
}

// ---------------------------------------------------------------------------
// The checks this shape needs beyond the shared sweeps
// ---------------------------------------------------------------------------

// TestProseBarScrollerSheets_AnOversizedRowFitsAndMarksItsCut holds every window
// this conversion introduced to the bar the flat lists were held to: a row taller
// than the pane is clipped to what the foot leaves, the cut is MARKED, and the
// frame still fits — so the bar survives.
//
// WATCHED FAILING against HEAD before the conversion, where none of these lists
// had a window: measured at 80x24 with the same oversized rows, the task, tool,
// lockout, material and stock-picker frames assembled 49, 50, 50, 52 and 51 rows
// and the committee step 71, against the 18 the pane has, and not one clipped
// pane carried its way out (`esc back`, `esc cancel`). The cycle count's reason
// step came to 31 rows at the same size with no oversized value at all, and the
// delete confirm fitted its rows while its keys ran off the right edge.
func TestProseBarScrollerSheets_AnOversizedRowFitsAndMarksItsCut(t *testing.T) {
	const width, height = 80, 24
	fixtures := append(proseBarOversizedItemSheet(), proseBarOversizedWOSheet()...)
	for _, f := range fixtures {
		t.Run(f.name, func(t *testing.T) {
			s := proseBarSize(f.build(), width, height)
			if !proseBarFrameFits(s, height) {
				t.Fatalf("an oversized row pushed the frame past %dx%d, taking the bar with it:\n%s",
					width, height, stripANSI(s.View()))
			}
			got := stripANSI(s.View())
			if !strings.Contains(got, " more lines") {
				t.Fatalf("an oversized row was clipped with no mark saying so:\n%s", got)
			}
			if want := "\n\n" + stripANSI(s.proseBar().render(proseBarCells(width))); !strings.HasSuffix(got, want) {
				t.Fatalf("the bar is not the last thing on the pane:\n%s", got)
			}
		})
	}
}

// TestProseBarScrollerSheets_ThePromptsThatNameTheirOwnKeysReachThePane holds the
// surfaces that answer nil from proseBar — whose keys are spelled by their own
// prompt, so no footer sweep reads them — to the claim a prompt is only honest
// if it is ON the pane.
//
// The item sheet's delete confirm is the one that was not: its question and its
// keys were ONE line, and the item's name took it past the 51 cells an 80-column
// pane gives, so clampToBox cut `y delete · n/esc cancel` off the end — a long
// name is the fixture here for that reason. The confirm sits UNDER the body, and
// the body's scroller floors at four rows, so the boundary is DERIVED from the
// frame the confirm draws at its least rather than asserted at every height, and
// both sides of it must be reached.
func TestProseBarScrollerSheets_ThePromptsThatNameTheirOwnKeysReachThePane(t *testing.T) {
	cases := []struct {
		name  string
		build func() proseBarScreen
		want  []string
	}{
		{
			name: "item detail/delete confirm",
			build: func() proseBarScreen {
				return proseBarPress(proseBarItemSheet(func(it *omsapi.Item) {
					it.Name = "Hex bolt M8x40 zinc-plated, grade 8.8, box of 100, from the Grainger contract"
				}), "x")
			},
			want: []string{"Delete ", "This can't be undone.", "y delete · n/esc cancel"},
		},
		{
			name:  "item detail/deleting",
			build: func() proseBarScreen { return proseBarPress(proseBarItemSheet(nil), "x", "y") },
			want:  []string{"Deleting…"},
		},
		{
			name: "item detail/cycle count, recording",
			build: func() proseBarScreen {
				return proseBarPress(proseBarItemSheet(nil), "c", "2", "enter", "enter", "enter")
			},
			want: []string{"Cycle count", "Recording count…"},
		},
		{
			name: "work order detail/confirm",
			build: func() proseBarScreen {
				return proseBarPress(proseBarWO(func(wo *omsapi.WorkOrder) {
					wo.DisplayTitle = strings.Repeat("Quarterly spindle service, Haas VF-2SS, north bay ", 4)
				}), "c")
			},
			want: []string{"Mark this work order COMPLETED?", "y confirm · n/esc cancel"},
		},
	}
	widths, heights := jdeDrawableWidths(), jdePaneHeights()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if bar := proseBarSize(c.build(), 80, 24).proseBar(); bar != nil {
				t.Fatalf("%s draws a bar record (%s), so it is the footer sweeps' to hold, not this test's",
					c.name, bar.hint())
			}
			held, short := 0, 0
			for _, w := range widths {
				for _, h := range heights {
					s := proseBarSize(c.build(), w, h)
					if !proseBarFrameFits(s, h) {
						short++
						continue
					}
					held++
					pane := stripANSI(clampToBox(s.View(), screenBodyCells(w), screenBodyRows(h)))
					for _, want := range c.want {
						if !strings.Contains(pane, want) {
							t.Errorf("at %dx%d the frame fits and the pane does not carry %q:\n%s", w, h, want, pane)
						}
					}
					// The prompt's own rows — the last three the frame draws — must fit
					// the pane as handed over: after clampToBox no line can be too wide,
					// because the cut has already been made with no mark. (The rows
					// above them are the sheet's body, whose width is not this
					// conversion's.)
					lines := strings.Split(s.View(), "\n")
					for i := len(lines) - 3; i < len(lines); i++ {
						if i < 0 {
							continue
						}
						line := lines[i]
						if lw := lipgloss.Width(line); lw > screenBodyCells(w) {
							t.Errorf("at %dx%d line %d is %d cells into a %d-cell pane: %q",
								w, h, i, lw, screenBodyCells(w), stripANSI(line))
						}
					}
				}
			}
			if held == 0 || short == 0 {
				t.Errorf("%s fitted %d panes and overran %d, so one side of the boundary was never tested",
					c.name, held, short)
			}
		})
	}
}
