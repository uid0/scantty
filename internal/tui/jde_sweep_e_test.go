// The ADMIN & INTEGRATION family of forms on the columnar "JD Edwards" layer
// (sc-lmsi, sweep E of the sc-h412 redesign — the LAST batch): site settings,
// the webhook sheet, the SIG sheet and the ForgeKey access grant.
//
// These hold all four to the same contract sweeps A–D hold their families to,
// because the persistent action bar is now the only place an operator can learn
// what a key does:
//
//	the sheet is columnar   — every field row hangs off ONE leader column
//	the bar is persistent   — same two rows of the pane on every frame
//	the bar is honest       — a key on it works here, and a key that works
//	                          here is on it
//	no letter accelerators  — a stray letter on a row with no input does
//	                          NOTHING rather than firing something invisible
//	rows fit the pane       — clampToBox TRUNCATES an over-wide row, so a hint
//	                          that does not fit is a hint silently lost
//	                          (sc-ye0i's test, carried forward as instructed)
//
// Plus the three this batch owns:
//
//	four sheets, four columns — these are unrelated ADMIN surfaces, reached
//	                            from four different places and never seen
//	                            beside one another, so each computes its own
//	                            column (the other side of sweeps C and D's
//	                            shared-column judgement)
//	a masked field stays masked — the webhook secret is the first echo-mode
//	                            box on the layer, and a blurred row renders
//	                            without View() to apply the mask for it
//	a note rides on its row   — what is stored for an image, and what a blank
//	                            path does, belong under the row that replaces
//	                            it rather than in a header above the sheet
package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// longLogoURL is a media URL longer than the note column, because that is what
// a real one looks like — and an over-long token is exactly what would be cut
// at the pane edge with nothing on screen to say so.
const longLogoURL = "https://oms.example.org/media/customization/logo-2026-wide.png"

// jdeSweepEWidth is the terminal the family is measured at — the width sc-akwv
// eyeballed every screen at, and what screenBodyWidth turns into the pane.
const jdeSweepEWidth = 110

// jdeSweepECase is one converted form, loaded and ready to drive.
type jdeSweepECase struct {
	name      string
	screen    Screen
	setCursor func(row int)
	rowCount  int
	kinds     map[jdeRowKind]int // kind -> a row of that kind (absent = none)
	// saveVerb is what the bar calls Enter here: the grant sheet GRANTS rather
	// than saving, and the bar has to say what the key actually does.
	saveVerb string
	// pickerVerb is what the bar calls Ctrl-E on the picker row.
	pickerVerb string
	// labelWidth is the column this sheet computes for itself.
	labelWidth int
}

func jdeSweepECases(t *testing.T) []jdeSweepECase {
	t.Helper()
	size := tea.WindowSizeMsg{Width: jdeSweepEWidth, Height: jdeSweepHeight}

	ss := NewSiteSettingsFormScreen(Deps{})
	ss.loading = false
	ss.settings = &omsapi.SiteSettings{SiteName: "Acme", LogoURL: strptr(longLogoURL)}
	ss.hydrate()
	ss.buildFields()
	ss.Update(size)

	wh := NewWebhookFormScreen(Deps{}, 0)
	wh.loading = false
	wh.Update(size)

	sig := NewSIGFormScreen(Deps{}, "")
	sig.loading = false
	sig.Update(size)

	grant := NewAuthorizationGrantScreen(Deps{})
	grant.loading = false
	grant.assets = []omsapi.Asset{{ID: "a-1", Name: "Laser cutter"}, {ID: "a-2", Name: "Haas mill"}}
	grant.users = []omsapi.User{badgeUser(7, "Grace Hopper", "grace", nil), badgeUser(8, "Ada Lovelace", "ada", nil)}
	grant.Update(size)

	return []jdeSweepECase{
		{
			name:      "site settings",
			screen:    ss,
			setCursor: func(row int) { ss.cursor = row; ss.syncFocus() },
			rowCount:  len(ss.fields),
			kinds: map[jdeRowKind]int{
				jdeRowText:   elecRowOf(t, ss.fields, ssName),
				jdeRowChoice: elecRowOf(t, ss.fields, ssShowLogo),
			},
			saveVerb:   "Save",
			labelWidth: ssLabelWidth,
		},
		{
			name:      "webhook",
			screen:    wh,
			setCursor: func(row int) { wh.cursor = row; wh.syncFocus() },
			rowCount:  len(wh.fields),
			kinds: map[jdeRowKind]int{
				jdeRowText:   elecRowOf(t, wh.fields, whName),
				jdeRowChoice: elecRowOf(t, wh.fields, whEventType),
			},
			saveVerb:   "Save",
			labelWidth: webhookLabelWidth,
		},
		{
			name:      "sig",
			screen:    sig,
			setCursor: func(row int) { sig.cursor = row; sig.syncFocus() },
			rowCount:  len(sig.fields),
			kinds: map[jdeRowKind]int{
				jdeRowText: elecRowOf(t, sig.fields, sigfName),
			},
			saveVerb:   "Save",
			labelWidth: sigLabelWidth,
		},
		{
			name:      "access grant",
			screen:    grant,
			setCursor: func(row int) { grant.cursor = row; grant.syncFocus() },
			rowCount:  len(grant.fields),
			kinds: map[jdeRowKind]int{
				jdeRowText:   elecRowOf(t, grant.fields, agNotes),
				jdeRowPicker: elecRowOf(t, grant.fields, agAsset),
			},
			saveVerb:   "Grant",
			pickerVerb: "Pick",
			labelWidth: authGrantLabelWidth,
		},
	}
}

// TestJDESweepE_FormsAreColumnar: every field row hangs off the same leader
// column, and the labels are RIGHT-aligned into it — that is what makes a block
// of fields read as one sheet rather than a ragged list.
func TestJDESweepE_FormsAreColumnar(t *testing.T) {
	for _, tc := range jdeSweepECases(t) {
		t.Run(tc.name, func(t *testing.T) {
			out := tc.screen.View()
			col, rows := jdeLeaderColumn(out)
			for _, line := range strings.Split(out, "\n") {
				at := strings.Index(line, jdeLeader)
				if at >= 0 && at != col {
					t.Errorf("a field row breaks the column (%d, want %d): %q", at, col, line)
				}
			}
			if rows == 0 {
				t.Errorf("no columnar rows rendered:\n%s", out)
			}
			padded := false
			for _, line := range strings.Split(out, "\n") {
				at := strings.Index(line, jdeLeader)
				if at < 0 || at != col {
					continue
				}
				label := strings.TrimSuffix(line[:at], " ")
				if len(label) > len(jdeIndent) && strings.HasPrefix(label, jdeIndent+" ") {
					padded = true
				}
			}
			if !padded {
				t.Errorf("no label is left-padded — are they right-aligned?\n%s", out)
			}
		})
	}
}

// TestJDESweepE_EachSheetKeepsItsOwnColumn is this batch's headline, and it is
// the OTHER side of sweeps C and D's shared column. The electrical forms share
// one because they are reached from each other, and the four storage screens
// share one because the operator walks the racking through them. These four are
// reached from four different places — Settings, Settings · Webhooks, the SIGs
// workspace, the Authorizations list — and none of them can be on screen beside
// another, so a shared column would only shove the narrow sheets' input areas
// right to line up with a sheet nobody sees next to them.
func TestJDESweepE_EachSheetKeepsItsOwnColumn(t *testing.T) {
	seen := map[int]string{}
	for _, tc := range jdeSweepECases(t) {
		col, rows := jdeLeaderColumn(tc.screen.View())
		if rows == 0 {
			t.Fatalf("%s rendered no columnar rows", tc.name)
		}
		if want := len(jdeIndent) + tc.labelWidth; col != want {
			t.Errorf("%s draws its leader at column %d but its own width implies %d",
				tc.name, col, want)
		}
		if other, dup := seen[col]; dup {
			t.Errorf("%s and %s draw the same column (%d) — these sheets each size "+
				"their own, so a match means one is borrowing the other's", tc.name, other, col)
		}
		seen[col] = tc.name
	}

	// And every width comes from that sheet's OWN label map, never pinned to a
	// number, so renaming a field can't silently break its alignment.
	for _, tc := range []struct {
		name   string
		got    int
		labels map[int]string
	}{
		{"site settings", ssLabelWidth, ssFieldLabel},
		{"webhook", webhookLabelWidth, webhookFieldLabel},
		{"sig", sigLabelWidth, sigFieldLabel},
		{"access grant", authGrantLabelWidth, authGrantFieldLabel},
	} {
		if want := jdeLabelWidth(jdeLabelFields(tc.labels)); tc.got != want {
			t.Errorf("%s label width = %d, want its own widest label %d", tc.name, tc.got, want)
		}
	}
}

// TestJDESweepE_NoLabelIsTruncatedIntoTheColumn: jdeLabelWidth CAPS the column,
// and renderJDEField truncates a label that overruns the cap — silently. "PM
// auto-bundle window (days)" was 28 columns and would have lost its tail; the
// unit belongs after the input area, where a long note cannot move anything.
func TestJDESweepE_NoLabelIsTruncatedIntoTheColumn(t *testing.T) {
	for name, labels := range map[string]map[int]string{
		"site settings": ssFieldLabel,
		"webhook":       webhookFieldLabel,
		"sig":           sigFieldLabel,
		"access grant":  authGrantFieldLabel,
	} {
		for id, label := range labels {
			if w := lipgloss.Width(label); w > jdeLabelMaxWidth {
				t.Errorf("%s field %d label %q is %d wide — past the %d cap, so it renders truncated",
					name, id, label, w, jdeLabelMaxWidth)
			}
		}
	}
}

// TestJDESweepE_RowsFitTheBody: clampToBox TRUNCATES an over-wide line rather
// than wrapping it, so a row that does not fit loses its tail with nothing on
// screen to say so — which for these forms means a silently missing HINT or
// note. Carried over from sc-ye0i; it caught the site-settings image hint and
// the webhook headers example while this batch was being written.
func TestJDESweepE_RowsFitTheBody(t *testing.T) {
	budget := screenBodyWidth(jdeSweepEWidth)
	for _, tc := range jdeSweepECases(t) {
		t.Run(tc.name, func(t *testing.T) {
			for row := 0; row < tc.rowCount; row++ {
				tc.setCursor(row)
				for _, line := range strings.Split(tc.screen.View(), "\n") {
					if w := lipgloss.Width(line); w > budget {
						t.Errorf("row %d: a line is %d wide but the pane is %d — it will be clipped: %q",
							row, w, budget, line)
					}
				}
			}
		})
	}
}

// TestJDESweepE_PickerRowsFitTheBody is the same check for the open picker,
// whose note and filter row are ours to keep inside the pane too.
func TestJDESweepE_PickerRowsFitTheBody(t *testing.T) {
	budget := screenBodyWidth(jdeSweepEWidth)
	for _, tc := range jdeSweepECases(t) {
		row, ok := tc.kinds[jdeRowPicker]
		if !ok {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			tc.setCursor(row)
			tc.screen.Update(ccCtrlEKey())
			for _, line := range strings.Split(tc.screen.View(), "\n") {
				if w := lipgloss.Width(line); w > budget {
					t.Errorf("a picker line is %d wide but the pane is %d: %q", w, budget, line)
				}
			}
			tc.screen.Update(ccEscKey())
		})
	}
}

// TestJDESweepE_BarIsPinnedToTheBottomOfThePane: "persistent" means the bar is
// on the same two rows on every frame — if the body were allowed to set the
// height, the bar would walk up and down as rows gained option strips and notes.
func TestJDESweepE_BarIsPinnedToTheBottomOfThePane(t *testing.T) {
	for _, tc := range jdeSweepECases(t) {
		t.Run(tc.name, func(t *testing.T) {
			for row := 0; row < tc.rowCount; row++ {
				tc.setCursor(row)
				lines := strings.Split(tc.screen.View(), "\n")
				if want := screenBodyHeight(jdeSweepHeight); len(lines) != want {
					t.Fatalf("row %d: view is %d rows, want the pane's budget of %d", row, len(lines), want)
				}
				if rule := lines[len(lines)-2]; strings.Trim(rule, "-") != "" || rule == "" {
					t.Errorf("row %d: the second-to-last row should be the bar's rule, got %q", row, rule)
				}
				if want := "Enter=" + tc.saveVerb; !strings.Contains(lines[len(lines)-1], want) {
					t.Errorf("row %d: the key line should offer %q, got %q", row, want, lines[len(lines)-1])
				}
			}
		})
	}
}

// TestJDESweepE_BarNamesOnlyTheKeysThatApply is the contract that replaces the
// letter accelerators: a key on the bar works here, and a key that works here is
// on the bar.
func TestJDESweepE_BarNamesOnlyTheKeysThatApply(t *testing.T) {
	for _, tc := range jdeSweepECases(t) {
		t.Run(tc.name, func(t *testing.T) {
			for kind, row := range tc.kinds {
				tc.setCursor(row)
				bar := jdeBarLine(tc.screen.View())
				for _, want := range []string{"Enter=" + tc.saveVerb, "Esc=Cancel", "UP/DN=Fields"} {
					if !strings.Contains(bar, want) {
						t.Errorf("row %d: bar should always offer %q: %q", row, want, bar)
					}
				}
				switch kind {
				case jdeRowText:
					for _, deny := range []string{"Ctrl-E", "←→"} {
						if strings.Contains(bar, deny) {
							t.Errorf("a text row must not offer %q: %q", deny, bar)
						}
					}
				case jdeRowChoice:
					if !strings.Contains(bar, "←→=Change") {
						t.Errorf("a choice row should offer ←→=Change: %q", bar)
					}
					if strings.Contains(bar, "Ctrl-E") {
						t.Errorf("a choice row has nothing to open: %q", bar)
					}
				case jdeRowPicker:
					if !strings.Contains(bar, "Ctrl-E="+tc.pickerVerb) {
						t.Errorf("a picker row should offer Ctrl-E=%s: %q", tc.pickerVerb, bar)
					}
					if strings.Contains(bar, "←→") {
						t.Errorf("a picker row has nothing to cycle: %q", bar)
					}
				}
			}
		})
	}
}

// TestJDESweepE_ArrowsChangeAChoiceRow is the other half of the bar contract on
// a bounded set: the previous test proves ←→ is NAMED where it applies, this one
// proves it does something there. A toggle is a two-value choice set, so it
// takes the same arrows as the twelve-value one rather than keeping a checkbox
// the key scheme would have to explain separately.
func TestJDESweepE_ArrowsChangeAChoiceRow(t *testing.T) {
	for _, tc := range jdeSweepECases(t) {
		row, ok := tc.kinds[jdeRowChoice]
		if !ok {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			for _, key := range []tea.KeyType{tea.KeyRight, tea.KeyLeft} {
				tc.setCursor(row)
				before := tc.screen.View()
				tc.screen.Update(tea.KeyMsg{Type: key})
				if tc.screen.View() == before {
					t.Errorf("%v on a choice row should change it — the bar names it here:\n%s",
						key, tc.screen.View())
				}
			}
		})
	}
}

// TestJDESweepE_NoLetterAcceleratorsRemain is the guard for the whole point of
// the redesign: space-opens-the-picker and the picker's j/k and "/" are gone, so
// a stray letter on a row with no input must do NOTHING.
func TestJDESweepE_NoLetterAcceleratorsRemain(t *testing.T) {
	letters := []rune{'a', 'c', 'd', 'e', 'g', 'j', 'k', 'n', 'o', 'r', 't', 'v', 'w', 'x', 'E', 'G'}
	for _, tc := range jdeSweepECases(t) {
		t.Run(tc.name, func(t *testing.T) {
			for kind, row := range tc.kinds {
				if kind == jdeRowText {
					continue // a letter on a text row is text, not a command
				}
				tc.setCursor(row)
				before := tc.screen.View()
				for _, letter := range letters {
					tc.screen.Update(runeKey(letter))
					if got := tc.screen.View(); got != before {
						t.Fatalf("row %d: %q changed the screen — no letter is bound here:\n%s", row, letter, got)
					}
				}
			}
		})
	}
}

// TestJDESweepE_LettersOnATextRowAreTyped is the other half: the reduced scheme
// took the accelerators away, not the ability to type.
func TestJDESweepE_LettersOnATextRowAreTyped(t *testing.T) {
	for _, tc := range jdeSweepECases(t) {
		row, ok := tc.kinds[jdeRowText]
		if !ok {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			tc.setCursor(row)
			before := tc.screen.View()
			tc.screen.Update(runeKey('w'))
			if tc.screen.View() == before {
				t.Errorf("a letter on a text row should be typed into it:\n%s", tc.screen.View())
			}
		})
	}
}

// TestJDESweepE_CtrlEOpensThePicker: EDIT opens whatever the highlighted row IS,
// and a text row has nothing to open — it must not fall through to something.
// Space used to be the opener on the grant sheet; it is filter text now.
func TestJDESweepE_CtrlEOpensThePicker(t *testing.T) {
	for _, tc := range jdeSweepECases(t) {
		row, ok := tc.kinds[jdeRowPicker]
		if !ok {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			tc.setCursor(tc.kinds[jdeRowText])
			before := tc.screen.View()
			tc.screen.Update(ccCtrlEKey())
			if tc.screen.View() != before {
				t.Errorf("ctrl+e on a text row should do nothing:\n%s", tc.screen.View())
			}

			// Space no longer opens anything — it is a character now.
			tc.setCursor(row)
			before = tc.screen.View()
			tc.screen.Update(spaceKey())
			if tc.screen.View() != before {
				t.Errorf("space on a picker row should no longer open it:\n%s", tc.screen.View())
			}

			tc.screen.Update(ccCtrlEKey())
			out := tc.screen.View()
			if !strings.Contains(out, "Filter") {
				t.Fatalf("ctrl+e on a picker row should open its picker:\n%s", out)
			}
			bar := jdeBarLine(out)
			for _, want := range []string{"Enter=Select", "Esc=Cancel", "UP/DN=Move"} {
				if !strings.Contains(bar, want) {
					t.Errorf("the picker bar should offer %q: %q", want, bar)
				}
			}
			tc.screen.Update(ccEscKey())
		})
	}
}

// TestJDESweepE_NoRetiredGestureIsAdvertised: the bar is the only place a key is
// taught, so nothing else on these sheets may name one — least of all a gesture
// this sweep RETIRED. Every string here was in one of the four screens' help
// lines or empty states before the conversion, and a key named where it no
// longer works is worse than a key named nowhere (sc-6qsk).
func TestJDESweepE_NoRetiredGestureIsAdvertised(t *testing.T) {
	retired := []string{
		"space to pick", "space to choose", "space toggle", "space/←→",
		"/ filter", "j/k", "tab/↑↓", "enter save", "esc cancel", "enter grant",
	}
	for _, tc := range jdeSweepECases(t) {
		t.Run(tc.name, func(t *testing.T) {
			for row := 0; row < tc.rowCount; row++ {
				tc.setCursor(row)
				body := tc.screen.View()
				if bar := jdeBarLine(body); bar != "" {
					body = strings.Replace(body, bar, "", 1)
				}
				for _, gesture := range retired {
					if strings.Contains(body, gesture) {
						t.Errorf("row %d still advertises %q outside the bar:\n%s", row, gesture, body)
					}
				}
			}
		})
	}
}

// TestJDESweepE_EmptyPickerRowIsDimNotPreStyled: a foreign key with nothing
// picked is an EMPTY STATE, and the row has to say so with the Dim flag rather
// than by pre-styling its own text — an inner reset ends the outer highlight
// partway through the field when the row is focused (sc-h412). The gesture that
// fills it belongs in the Hint, which is the part the bar can be checked against.
func TestJDESweepE_EmptyPickerRowIsDimNotPreStyled(t *testing.T) {
	s := NewAuthorizationGrantScreen(Deps{})
	s.loading = false
	s.assets = []omsapi.Asset{{ID: "a-1", Name: "Laser cutter"}}
	s.users = []omsapi.User{badgeUser(7, "Grace Hopper", "grace", nil)}
	s.Update(tea.WindowSizeMsg{Width: jdeSweepEWidth, Height: jdeSweepHeight})

	row := indexOfField(s.fields, agAsset)
	s.cursor = row
	s.syncFocus()
	f := s.formFields()[row]
	if f.Kind != jdeValue {
		t.Errorf("a picker row is a value some other gesture changes, got kind %v", f.Kind)
	}
	if !f.Dim {
		t.Errorf("an unset required FK is an empty state — it must carry Dim, got %+v", f)
	}
	if strings.Contains(f.Value, "pick") || strings.Contains(f.Value, "space") {
		t.Errorf("the value must not name the gesture that fills it (that is the Hint): %q", f.Value)
	}

	s.Update(ccCtrlEKey())
	s.Update(ccEnterKey())
	f = s.formFields()[row]
	if f.Dim {
		t.Errorf("a picked FK is a value, not an empty state: %+v", f)
	}
	if f.Value != thermostatAssetLabel(s.assets[0]) {
		t.Errorf("picked value = %q, want the asset's label", f.Value)
	}
}

// TestJDESweepE_PickerFiltersAsYouType: the filter is always live, so there is
// no mode to enter — which is what let j/k and "/" leave. Esc returns to the
// form without choosing, and what was typed is cleared.
func TestJDESweepE_PickerFiltersAsYouType(t *testing.T) {
	s := NewAuthorizationGrantScreen(Deps{})
	s.loading = false
	s.assets = []omsapi.Asset{{ID: "a-1", Name: "Laser cutter"}, {ID: "a-2", Name: "Haas mill"}}
	s.users = []omsapi.User{badgeUser(7, "Grace Hopper", "grace", nil)}
	s.Update(tea.WindowSizeMsg{Width: jdeSweepEWidth, Height: jdeSweepHeight})
	s.cursor = indexOfField(s.fields, agAsset)
	s.syncFocus()
	s.Update(ccCtrlEKey())
	if s.phase != authGrantPhasePick {
		t.Fatalf("ctrl+e should open the picker, phase=%v", s.phase)
	}

	for _, r := range "haas" {
		s.Update(runeKey(r))
	}
	if len(s.pickOptions) != 1 || s.pickOptions[0].label != thermostatAssetLabel(s.assets[1]) {
		t.Fatalf("typing should narrow the list, got %+v", s.pickOptions)
	}
	if out := s.View(); !strings.Contains(out, "haas") {
		t.Errorf("the pinned filter row should show what was typed:\n%s", out)
	}

	// j and k are LETTERS again: they go into the filter like any other, rather
	// than moving the selection behind the operator's back.
	at := s.pickCursor
	for _, r := range "jk" {
		s.Update(runeKey(r))
	}
	if got := s.pickSearch.Value(); got != "haasjk" {
		t.Errorf("j/k should be typed into the filter, got %q", got)
	}
	if s.pickCursor != at {
		t.Errorf("j/k must not move the selection, cursor %d → %d", at, s.pickCursor)
	}

	// Esc leaves without choosing, and clears what was typed.
	s.Update(ccEscKey())
	if s.phase != authGrantPhaseForm || s.assetID != nil {
		t.Errorf("esc should return to the form without choosing (phase=%v, id=%v)", s.phase, s.assetID)
	}
	if s.pickSearch.Value() != "" {
		t.Errorf("the filter should be cleared on close, got %q", s.pickSearch.Value())
	}
}

// TestJDESweepE_PickerCursorClamps: a picker list is a set of choices, not a
// ring. Both of the grant's foreign keys are REQUIRED, so neither list has a
// "(none)" row — running off the bottom and reappearing at the top would still
// hand the operator a different asset than the one they were aiming at.
func TestJDESweepE_PickerCursorClamps(t *testing.T) {
	s := NewAuthorizationGrantScreen(Deps{})
	s.loading = false
	s.assets = []omsapi.Asset{{ID: "a-1", Name: "Laser cutter"}, {ID: "a-2", Name: "Haas mill"}}
	s.Update(tea.WindowSizeMsg{Width: jdeSweepEWidth, Height: jdeSweepHeight})
	s.cursor = indexOfField(s.fields, agAsset)
	s.syncFocus()
	s.Update(ccCtrlEKey())

	for i := 0; i < 6; i++ {
		s.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	if s.pickCursor != len(s.pickOptions)-1 {
		t.Errorf("down should stop at the last option, got %d of %d", s.pickCursor, len(s.pickOptions))
	}
	for i := 0; i < 6; i++ {
		s.Update(tea.KeyMsg{Type: tea.KeyUp})
	}
	if s.pickCursor != 0 {
		t.Errorf("up should stop at the first option, got %d", s.pickCursor)
	}
}

// TestJDESweepE_SecretStaysMaskedWhenBlurred is the batch's own layer contract.
// A columnar row renders a BLURRED box without calling View() — that is what
// stops a blurred textinput drawing a stray cursor cell — so the echo mode has
// to be applied on that path too, or moving the cursor off the secret prints it
// on screen.
func TestJDESweepE_SecretStaysMaskedWhenBlurred(t *testing.T) {
	const secret = "s3cr3t-value"
	s := NewWebhookFormScreen(Deps{}, 0)
	s.loading = false
	s.Update(tea.WindowSizeMsg{Width: jdeSweepEWidth, Height: jdeSweepHeight})
	s.inputs[whSecret].SetValue(secret)

	for _, focus := range []int{whSecret, whName} {
		s.cursor = indexOfField(s.fields, focus)
		s.syncFocus()
		out := s.View()
		if strings.Contains(out, secret) {
			t.Errorf("with the cursor on field %d the secret is on screen in clear text:\n%s", focus, out)
		}
		if !strings.Contains(out, strings.Repeat("•", len(secret))) {
			t.Errorf("with the cursor on field %d the secret row is not masked:\n%s", focus, out)
		}
	}

	// The mask is presentation only — what gets sent is still the real secret.
	s.inputs[whName].SetValue("Relay")
	s.inputs[whURL].SetValue("https://example.org/hook")
	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if w.Secret != secret {
		t.Errorf("payload secret = %q, want the typed value", w.Secret)
	}
}

// TestJDESweepE_ImageStateRidesOnItsRow: what is stored for an image, and what
// leaving the path blank does, used to be a header line above the whole sheet —
// the one place an operator deciding whether to replace the logo would not look.
// It is now a note under the row it is about, and it must not be navigable.
func TestJDESweepE_ImageStateRidesOnItsRow(t *testing.T) {
	s := NewSiteSettingsFormScreen(Deps{})
	s.loading = false
	s.settings = &omsapi.SiteSettings{SiteName: "Acme", LogoURL: strptr(longLogoURL)}
	s.hydrate()
	s.buildFields()
	s.Update(tea.WindowSizeMsg{Width: jdeSweepEWidth, Height: jdeSweepHeight})

	body := s.formLines()
	if got := body.rowsIn(0, body.Len()); got != len(s.fields) {
		t.Errorf("the sheet has %d navigable rows but %d fields — a note or heading became navigable",
			got, len(s.fields))
	}

	// The note belongs to the logo row's block, so scrolling can never separate
	// the two.
	logoRow := indexOfField(s.fields, ssLogoPath)
	first, last := body.block(logoRow)
	if last <= first {
		t.Fatalf("the logo row owns a single line — where did its note go?")
	}
	joined := strings.Join(body.text[first:last+1], "\n")
	if !strings.Contains(joined, "current logo") {
		t.Errorf("the logo row's block should carry what is stored:\n%s", joined)
	}

	// With nothing on file the note says so rather than claiming a blank path
	// keeps something that is not there.
	s.settings = &omsapi.SiteSettings{SiteName: "Acme"}
	s.buildFields()
	out := s.View()
	if !strings.Contains(out, "no logo set yet") {
		t.Errorf("with no logo the note should say so:\n%s", out)
	}
	if strings.Contains(out, "Remove logo") {
		t.Errorf("with no logo there is nothing to remove:\n%s", out)
	}
}

// TestJDESweepE_EventTypeStripListsTheSet: twelve event types is more than a
// "< value >" row can say on its own, so the FOCUSED row lists the set with the
// current one bracketed — and only the focused row, or the sheet would be a wall
// of options.
func TestJDESweepE_EventTypeStripListsTheSet(t *testing.T) {
	s := NewWebhookFormScreen(Deps{}, 0)
	s.loading = false
	s.Update(tea.WindowSizeMsg{Width: jdeSweepEWidth, Height: jdeSweepHeight})

	s.cursor = indexOfField(s.fields, whName)
	s.syncFocus()
	if strings.Contains(s.View(), "["+webhookEventTypeOptions[0].label+"]") {
		t.Errorf("the option strip should only be drawn under the FOCUSED row:\n%s", s.View())
	}

	s.cursor = indexOfField(s.fields, whEventType)
	s.syncFocus()
	out := s.View()
	if !strings.Contains(out, "["+webhookEventTypeOptions[0].label+"]") {
		t.Errorf("the focused event-type row should bracket the current option:\n%s", out)
	}
	// The value between the angle brackets is the BARE label — the renderer owns
	// the brackets, so a pre-wrapped value would double them up (sc-0zvi).
	if !strings.Contains(out, "< "+webhookEventTypeOptions[0].label+" >") {
		t.Errorf("the event-type row should read as one bounded set:\n%s", out)
	}

	// Cycling past the first few options keeps the bracketed entry on screen:
	// the strip WINDOWS around the selection rather than clipping its tail.
	for i := 0; i < 6; i++ {
		s.Update(tea.KeyMsg{Type: tea.KeyRight})
	}
	out = s.View()
	if !strings.Contains(out, "["+webhookEventTypeOptions[6].label+"]") {
		t.Errorf("the strip should window around the selection:\n%s", out)
	}
}

// TestJDESweepE_NoteWrapsInsteadOfBeingClipped: a note is the fold for guidance
// too long to ride as a Hint, so the one thing it must never do is overrun the
// pane — clampToBox would cut it with nothing on screen to say it had.
func TestJDESweepE_NoteWrapsInsteadOfBeingClipped(t *testing.T) {
	s := NewSIGFormScreen(Deps{}, "")
	s.loading = false
	s.Update(tea.WindowSizeMsg{Width: jdeSweepEWidth, Height: jdeSweepHeight})

	out := s.View()
	if !strings.Contains(out, "New reorder requests") || !strings.Contains(out, "notifies admins individually") {
		t.Errorf("the group-email note should be on screen whole:\n%s", out)
	}
	lines := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "New reorder requests") || strings.Contains(line, "notifies admins") {
			lines++
		}
	}
	if lines < 2 {
		t.Errorf("a note longer than the row should WRAP, not run past the pane:\n%s", out)
	}

	// A single token too long to wrap is ellipsised rather than left to be cut
	// at the pane edge, which is what an over-long media URL would be.
	long := strings.Repeat("x", 200)
	got := jdeWrapNote("keeps "+long, 30)
	if len(got) != 2 || !strings.HasSuffix(got[1], "…") {
		t.Errorf("an over-long word should be ellipsised, got %q", got)
	}
	for _, line := range got {
		if w := lipgloss.Width(line); w > 30 {
			t.Errorf("a wrapped line is %d wide, want ≤ 30: %q", w, line)
		}
	}
}

// TestJDESweepE_PagingClamps: a page is for covering ground in a sheet taller
// than the pane; one that wrapped would lose the operator's place. Site settings
// is the longest sheet in the batch at sixteen fields plus its bands.
func TestJDESweepE_PagingClamps(t *testing.T) {
	s := NewSiteSettingsFormScreen(Deps{})
	s.loading = false
	s.settings = &omsapi.SiteSettings{SiteName: "Acme"}
	s.hydrate()
	s.buildFields()
	s.Update(tea.WindowSizeMsg{Width: jdeSweepEWidth, Height: 18})
	if bar := jdeBarLine(s.View()); !strings.Contains(bar, "PgUp/PgDn=Page") {
		t.Fatalf("a sheet taller than the pane should offer paging: %q", bar)
	}
	for i := 0; i < 20; i++ {
		s.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	}
	if s.cursor != len(s.fields)-1 {
		t.Errorf("paging down should stop at the last row, got %d of %d", s.cursor, len(s.fields))
	}
	for i := 0; i < 20; i++ {
		s.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	}
	if s.cursor != 0 {
		t.Errorf("paging up should stop at the first row, got %d", s.cursor)
	}
}
