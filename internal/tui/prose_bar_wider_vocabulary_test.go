package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/forgekeyapi"
	"github.com/uid0/scantty/internal/omsapi"
)

// prose_bar_wider_vocabulary_test.go — the fixtures for the FOURTH recipe taken
// out of proseBarUnconverted as a group rather than a screen at a time.
//
// THE RECIPE, and what makes these screens one group. Each is a list that binds
// MORE of the navigation vocabulary than the step pair and LESS than all of it —
// or all of it over a window that is not a cursor — so none of the three earlier
// recipes' movement helpers described it: proseNavStep names too little,
// proseNavCursor names a pager two of them do not bind, and the flat recipe's
// step pair leaves the jumps unnamed. Their footers said `j/k move` (or
// `j/k scroll`) over switches binding the arrows and some or all of g/G/home/end
// and pgup/pgdn, which is the defect as filed: the operator is told fewer keys
// than the screen works. What each binds, and so what each bar names:
//
//   - FacilitiesScreen: the step pair and g/G/home/end, no pager
//     (proseNavList(moves, false)), plus a letter per row.
//   - ReportsScreen: the step pair and g/home only — no G, no end
//     (proseNavTop), plus a letter per row.
//   - ElectricalPanelsScreen: the step pair and g/G/home/end, no pager, which is
//     what kept it off the windowed recipe beside PanelBreakersScreen.
//   - ForgeKeyCertificatesScreen: the whole vocabulary, over a scroll OFFSET
//     rather than a cursor (proseNavScroll), gated on the table outrunning its
//     window.
//
// THE MENUS DREW EVERY ROW WITH NO WINDOW, eleven and nine two-line rows under a
// legend and above a note, so on an ordinary pane the last rows and the note were
// what clampToBox took. proseFlatListFrame is their window now, and each has an
// OVERSIZED-ROW fixture below — a row taller than the tallest pane, which static
// menu data can never produce, injected into the items so the window's clip is
// exercised at all: a footer check whose fixtures cannot push the footer off is
// not checking the footer. The certificate sheet's window is its own (a scroll
// offset over two-line rows under a CA card), and its oversized fixture is a
// stored value with newlines in it, which used to make a counted line two.
//
// WHAT IS STILL nil IS WHAT WAS nil BEFORE: a load in flight or failed, and the
// y/n confirms (the panel delete, the root-CA rotation), which name their own
// keys — the states the earlier recipes left as literals, for the same reasons.

// proseBarWiderVocabularyFixtures is every screen on that recipe, in every state
// it draws a bar in.
func proseBarWiderVocabularyFixtures() []proseBarFixture {
	var out []proseBarFixture
	for _, m := range proseBarMenus() {
		m := m
		out = append(out,
			proseBarFixture{
				name: m.name, recv: m.recv,
				build: func() proseBarScreen {
					return proseBarPressAll(proseBarSize(m.build(), 80, 24), "j", "j", "j")
				},
			},
			proseBarFixture{
				name: m.name + "/oversized row", recv: m.recv,
				build: func() proseBarScreen { return m.oversized() },
			},
		)
	}
	for _, l := range proseBarWiderVocabularyWindowedLists() {
		out = append(out, proseBarListPair(l)...)
	}
	return append(out,
		proseBarFixture{
			name: "electrical panels/empty", recv: "ElectricalPanelsScreen",
			build: func() proseBarScreen { return proseBarElectricalPanels(0, proseBarSameName) },
			immobile: "no panels — which is the point of this fixture: the movement segments and " +
				"`enter`, `E` and `x` must be absent, and `n`, `r` and `esc` named",
		},
		proseBarFixture{
			name: "certificates", recv: "ForgeKeyCertificatesScreen",
			build: func() proseBarScreen {
				return proseBarPressAll(proseBarSize(proseBarCertificatesScreen(30, false), 80, 24), "pgdown")
			},
		},
		proseBarFixture{
			name: "certificates/fits", recv: "ForgeKeyCertificatesScreen",
			build: func() proseBarScreen { return proseBarCertificatesScreen(2, false) },
			immobile: "two certificates fit the window, so every movement key is clamped back " +
				"to the top — which is the point of this fixture: the scroll segments must be absent",
		},
		proseBarFixture{
			name: "certificates/empty", recv: "ForgeKeyCertificatesScreen",
			build:    func() proseBarScreen { return proseBarCertificatesScreen(0, false) },
			immobile: "no device certificates issued, so there is no table to scroll",
		},
		// NO ACTIVE CA, because the card then draws a FOLDED sentence instead of
		// four fixed lines, and caSectionLines has to count the fold for the window
		// to leave the footer its rows.
		proseBarFixture{
			name: "certificates/no active CA", recv: "ForgeKeyCertificatesScreen",
			build: func() proseBarScreen {
				s := proseBarCertificatesScreen(30, false).(*ForgeKeyCertificatesScreen)
				next, _ := s.Update(fkCertsLoadedMsg{
					cas:   []forgekeyapi.CertificateAuthority{{Name: "forgekey-root-2025"}},
					certs: s.certs,
				})
				return proseBarPressAll(proseBarSize(next.(*ForgeKeyCertificatesScreen), 80, 24), "pgdown")
			},
		},
		proseBarFixture{
			name: "certificates/multi-line values", recv: "ForgeKeyCertificatesScreen",
			build: func() proseBarScreen {
				return proseBarPressAll(proseBarSize(proseBarCertificatesScreen(30, true), 80, 24), "pgdown")
			},
		},
	)
}

// proseBarPressAll presses keys in order and returns the screen they leave.
func proseBarPressAll(s proseBarScreen, keys ...string) proseBarScreen {
	for _, k := range keys {
		next, _ := s.Update(listRuneKey(k))
		s = next.(proseBarScreen)
	}
	return s
}

// ---------------------------------------------------------------------------
// The menus
// ---------------------------------------------------------------------------

// proseBarMenu is one of the two workspace menus.
type proseBarMenu struct {
	name, recv string
	build      func() proseBarScreen
	// withRow is the menu with a row INSERTED in the middle — so every movement
	// key has somewhere to go from it — whose subtitle spans `lines` lines, the
	// cursor standing on it.
	withRow func(lines int) proseBarScreen
}

// oversized is the menu carrying a row taller than any pane has body rows.
func (m proseBarMenu) oversized() proseBarScreen { return m.withRow(proseBarOversizedNameLines) }

// proseBarMenuInsertAt is where the injected row goes: the middle of the menu.
func proseBarMenuInsertAt(n int) int { return n / 2 }

// proseBarSubtitleLines is a subtitle of n lines.
func proseBarSubtitleLines(n int) string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("an injected subtitle line %02d", i+1)
	}
	return strings.Join(lines, "\n")
}

func proseBarMenus() []proseBarMenu {
	return []proseBarMenu{
		{
			name: "facilities menu", recv: "FacilitiesScreen",
			build: func() proseBarScreen { return NewFacilitiesScreen(Deps{}) },
			withRow: func(lines int) proseBarScreen {
				s := NewFacilitiesScreen(Deps{})
				at := proseBarMenuInsertAt(len(s.items))
				row := facilitiesItem{
					hotkey: 'Z', label: "Injected tall row",
					subtitle: proseBarSubtitleLines(lines),
				}
				s.items = append(s.items[:at], append([]facilitiesItem{row}, s.items[at:]...)...)
				s.cursor = at
				return s
			},
		},
		{
			name: "reports menu", recv: "ReportsScreen",
			build: func() proseBarScreen { return NewReportsScreen(Deps{}) },
			withRow: func(lines int) proseBarScreen {
				s := NewReportsScreen(Deps{})
				at := proseBarMenuInsertAt(len(s.items))
				row := reportsItem{
					hotkey: 'Z', label: "Injected tall row",
					subtitle: proseBarSubtitleLines(lines),
					build:    func(d Deps) Screen { return NewAnalyticsPulseScreen(d) },
				}
				s.items = append(s.items[:at], append([]reportsItem{row}, s.items[at:]...)...)
				s.cursor = at
				return s
			},
		},
	}
}

// TestProseBarMenus_AnOversizedRowFitsThePaneAndMarksItsCut: a menu row taller
// than the pane is clipped to the window with the cut named, and the footer stays.
//
// At every drawable pane where the SAME menu carrying a row exactly as tall as the
// window's floor fits (cursor on it), the menu carrying the oversized row in that
// place must fit too, and must say the row was cut. The reference is a row of
// proseListWindowFloor lines rather than the menu's own two-line rows because
// below the height where the budget is floored, a two-line row spends less than
// the floor and fits by coincidence — the check would then report the floor,
// which is pre-existing and recorded on proseListWindow, rather than the window.
// It is a height derived from that constant rather than one written here, and the
// check fails if either side of the boundary was never reached.
//
// WATCHED FAILING: drawing the menus' rows straight into the frame, as they were
// drawn before proseFlatListFrame, fails the fit at every pane.
func TestProseBarMenus_AnOversizedRowFitsThePaneAndMarksItsCut(t *testing.T) {
	widths, heights := jdeDrawableWidths(), jdePaneHeights()
	for _, m := range proseBarMenus() {
		t.Run(m.name, func(t *testing.T) {
			fits, overruns := 0, 0
			for _, w := range widths {
				for _, h := range heights {
					// The row is a label line plus its subtitle.
					plain := proseBarSize(m.withRow(proseListWindowFloor-1), w, h)
					if !proseBarFrameFits(plain, h) {
						overruns++
						continue
					}
					fits++
					big := proseBarSize(m.oversized(), w, h)
					if !proseBarFrameFits(big, h) {
						t.Fatalf("at %dx%d the %s standing on a %d-line row hands over %d rows for "+
							"a %d-row pane, where the same menu without it fits — the footer is what "+
							"clampToBox takes:\n%s", w, h, m.name, proseBarOversizedNameLines+1,
							lipgloss.Height(big.View()), screenBodyRows(h), stripANSI(big.View()))
					}
					pane := stripANSI(clampToBox(big.View(), screenBodyCells(w), screenBodyRows(h)))
					if !strings.Contains(pane, "more lines") {
						t.Fatalf("at %dx%d the %s drew a %d-line row on a %d-row pane with no mark "+
							"saying it was cut:\n%s", w, h, m.name, proseBarOversizedNameLines+1,
							screenBodyRows(h), pane)
					}
				}
			}
			if fits == 0 || overruns == 0 {
				t.Errorf("the %s boundary was reached on %d fitting panes and %d overrunning "+
					"ones; a side never reached is a check that asserted nothing on it",
					m.name, fits, overruns)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The electrical panel list
// ---------------------------------------------------------------------------

// proseBarWiderVocabularyWindowedLists are the lists on this recipe whose window
// is the windowed-cursor one, swept the way proseBarListPair sweeps it.
//
// THE LONG FIXTURE IS SCROLLED WITH end AND k, not pgdn: the panel list binds no
// pager, so pgdn would leave it standing at the top with one marker — the state
// proseBarListPair's doc says a scrolled fixture exists to get past.
func proseBarWiderVocabularyWindowedLists() []proseBarWindowedList {
	return []proseBarWindowedList{
		{
			name: "electrical panels", recv: "ElectricalPanelsScreen",
			build:  proseBarElectricalPanels,
			scroll: []string{"end", "k", "k", "k"},
			// Every panel row already draws two lines — the name and a meta line —
			// so a two-line NAME is the reference that makes a row as tall as the
			// multi-line fixture's ordinary rows. See proseBarWindowedList.reference.
			reference: func(_ int, base string) string { return base + "\nsecond line of the stored name" },
			immobile:  "one panel, so there is nowhere for the cursor to go",
		},
	}
}

// TestProseBarWiderVocabulary_MultiLineNamesFitThePaneAndMarkTheirCut is the
// windowed recipe's multi-line check (proseBarAssertMultiLineWindows) over the
// lists on this one, whose window budget was a count of two-line ROWS until the
// conversion.
//
// WATCHED FAILING: restoring the panel list's row-counted window (every row
// drawn whole, windowSize rows of them) fails the fit on the multi-line fixture.
func TestProseBarWiderVocabulary_MultiLineNamesFitThePaneAndMarkTheirCut(t *testing.T) {
	proseBarAssertMultiLineWindows(t, proseBarWiderVocabularyWindowedLists())
}

func proseBarElectricalPanels(rows int, name proseBarRowName) proseBarScreen {
	panels := make([]omsapi.PowerPanel, 0, rows)
	for i := 0; i < rows; i++ {
		panels = append(panels, omsapi.PowerPanel{
			ID:                  i + 1,
			Name:                name(i, fmt.Sprintf("Main distribution panel MDP-%d", i+1)),
			LocationName:        "Machine shop, north bay",
			PhaseConfiguration:  "3-phase",
			Voltage:             480,
			MainBreakerAmperage: 400,
			BreakerCount:        24 + i,
			NeedsReview:         i%5 == 0,
		})
	}
	s := NewElectricalPanelsScreen(Deps{})
	next, _ := s.Update(electricalPanelsLoadedMsg{panels: panels})
	return next.(*ElectricalPanelsScreen)
}

// ---------------------------------------------------------------------------
// The certificate sheet
// ---------------------------------------------------------------------------

// proseBarCertificatesScreen is the certificate sheet holding an active CA, a
// retired one and `n` device certificates. With multiLine every value the CA card
// and a certificate row COUNT as one line carries newlines — the CA's common
// name spans more lines than any pane has rows — which is the shape the window's
// arithmetic cannot survive if one of them is drawn as stored.
func proseBarCertificatesScreen(n int, multiLine bool) proseBarScreen {
	active, revoked := 212, 4
	cn := "ForgeKey Internal Root CA — machine shop enrollment"
	if multiLine {
		lines := make([]string, proseBarOversizedNameLines)
		for i := range lines {
			lines[i] = fmt.Sprintf("ForgeKey Internal Root CA, line %02d", i+1)
		}
		cn = strings.Join(lines, "\n")
	}
	cas := []forgekeyapi.CertificateAuthority{
		{
			Name: "forgekey-root", CommonName: cn, IsActive: true,
			FingerprintSHA256: strings.Repeat("ab12cd34", 8),
			NotBefore:         time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			NotAfter:          time.Date(2036, 1, 1, 0, 0, 0, 0, time.UTC),
			ActiveCertCount:   &active, RevokedCertCount: &revoked,
		},
		{Name: "forgekey-root-2025", IsActive: false},
	}
	certs := make([]forgekeyapi.DeviceCertificate, 0, n)
	for i := 0; i < n; i++ {
		c := forgekeyapi.DeviceCertificate{
			DeviceChipID:      fmt.Sprintf("ESP32-S3-%012X", 0xA1B2C3000000+i),
			Serial:            fmt.Sprintf("7F3A%020X", i+1),
			FingerprintSHA256: strings.Repeat(fmt.Sprintf("%02x", i%256), 32),
			NotAfter:          time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
			Status:            "active",
		}
		if multiLine {
			c.DeviceChipID = "ESP32-S3\nsecond line of the chip id"
			c.Status = "active\nsecond line of the status"
		}
		certs = append(certs, c)
	}
	s := NewForgeKeyCertificatesScreen(Deps{})
	next, _ := s.Update(fkCertsLoadedMsg{cas: cas, certs: certs})
	return next.(*ForgeKeyCertificatesScreen)
}

// TestProseBarCertificates_MultiLineValuesCannotPushTheFooterOff: at every
// drawable pane where the sheet with one-line values fits, the sheet whose CA
// name, chip ids and statuses carry newlines fits too — scrolled part-way and
// scrolled to the bottom.
//
// The window is COUNTED: caSectionLines counts the CA card's lines and
// certRowLines each row's, and a stored newline in one of those values drew a
// line the count did not have, so the frame ran past the pane and clampToBox
// took the footer. The values are flattened now (fkCertOneLine), and flattening
// cuts nothing vertically, so what this asserts is the fit; a value cut for WIDTH
// is marked by pickerClip.
//
// WATCHED FAILING: drawing the common name as stored fails the fit at every pane.
func TestProseBarCertificates_MultiLineValuesCannotPushTheFooterOff(t *testing.T) {
	widths, heights := jdeDrawableWidths(), jdePaneHeights()
	fits, overruns := 0, 0
	for _, w := range widths {
		for _, h := range heights {
			plain := proseBarPressAll(proseBarSize(proseBarCertificatesScreen(30, false), w, h), "pgdown")
			if !proseBarFrameFits(plain, h) {
				overruns++
				continue
			}
			fits++
			for _, keys := range [][]string{{"pgdown"}, {"end"}} {
				big := proseBarPressAll(proseBarSize(proseBarCertificatesScreen(30, true), w, h), keys...)
				if !proseBarFrameFits(big, h) {
					t.Fatalf("at %dx%d after %v the certificate sheet with multi-line values hands "+
						"over %d rows for a %d-row pane, where the same sheet with one-line values "+
						"fits — the footer is what clampToBox takes:\n%s", w, h, keys,
						lipgloss.Height(big.View()), screenBodyRows(h), stripANSI(big.View()))
				}
			}
		}
	}
	if fits == 0 || overruns == 0 {
		t.Errorf("the one-line boundary was reached on %d fitting panes and %d overrunning ones; "+
			"a side never reached is a check that asserted nothing on it", fits, overruns)
	}
}
