package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/uid0/scantty/internal/omsapi"
)

// withColorProfile forces a colour profile for the duration of a test. lipgloss
// strips every sequence when stdout is not a TTY — which is always, in a test
// binary — so a test that wants to see what an operator sees has to ask for it
// (status_test.go's idiom).
func withColorProfile(t *testing.T, p termenv.Profile) {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(p)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}

// swatchColorCases are the strings a colour field can hold on the way to a
// value. `want` is whether a swatch is drawn — which is also, by the invariant
// the next test pins, whether the form would SAVE the value.
var swatchColorCases = []struct {
	in   string
	want bool
	why  string
}{
	{"", false, "an empty field has no colour to show"},
	{"#", false, "the operator has typed the hash and nothing else"},
	{"#F", false, "half-typed"},
	{"#FF5", true, "shorthand is a whole colour"},
	{"#FF57", false, "past shorthand, not yet full — the wrong colour if drawn"},
	{"#FF573", false, "still one digit short"},
	{"#FF5733", true, "the full form"},
	{"#ff5733", true, "lower case is the same colour"},
	{"#Ff5733", true, "mixed case is the same colour"},
	{"#FF57333", false, "one digit too many"},
	{"FF5733", false, "no hash — not a hex code"},
	{"#GG5733", false, "not hex digits"},
	{"#12 456", false, "a space is not a hex digit"},
	{"red", false, "a colour NAME is not what this field stores"},
	{"  #FF5733  ", true, "the form trims before saving, so the sample follows"},
}

// TestHexSwatch_DrawsExactlyWhatTheFormWouldSave is the invariant the shared
// helper exists for: the sample beside a field appears for every value a save
// would store and for no other, because both go through normalizeHexColor. A
// swatch beside a value the form is about to reject would be worse than none.
func TestHexSwatch_DrawsExactlyWhatTheFormWouldSave(t *testing.T) {
	for _, tc := range swatchColorCases {
		drawn := hexSwatch(tc.in) != ""
		if drawn != tc.want {
			t.Errorf("hexSwatch(%q) drawn = %v, want %v — %s", tc.in, drawn, tc.want, tc.why)
		}
		// The empty string is the one value the form accepts WITHOUT it being a
		// colour: "no colour set" is a legal category.
		saved := strings.TrimSpace(tc.in) != "" && validateHexColor(strings.TrimSpace(tc.in)) == nil
		if drawn != saved {
			t.Errorf("hexSwatch(%q) drawn = %v but validateHexColor accepts = %v — the sample and "+
				"the save rule have come apart", tc.in, drawn, saved)
		}
	}
}

// TestHexSwatch_ShorthandIsTheSameColourAsItsLongForm confirms what the bead
// asked to be confirmed: #RGB reaches the terminal as the colour it names.
// normalizeHexColor expands it here rather than trusting the vendored colour
// library to, and this is what proves the expansion is the identity.
func TestHexSwatch_ShorthandIsTheSameColourAsItsLongForm(t *testing.T) {
	withColorProfile(t, termenv.TrueColor)

	long := hexSwatch("#aabbcc")
	for _, in := range []string{"#abc", "#ABC", "#AABBCC", "#aaBBcc"} {
		if got := hexSwatch(in); got != long {
			t.Errorf("hexSwatch(%q) = %q, want the same sequence as #aabbcc (%q)", in, got, long)
		}
	}
	if !strings.Contains(long, "170;187;204") {
		t.Errorf("the swatch does not carry the RGB it names: %q", long)
	}
	if other := hexSwatch("#FF5733"); other == long {
		t.Errorf("two different colours rendered identically (%q) — nothing is being coloured", other)
	}
}

// TestHexSwatch_KeepsOneColumnWithoutColour: on a terminal with no colour
// lipgloss drops the sequence and leaves the bare glyph. That is the expected
// degradation — but the row around it must not move, so the swatch has to stay
// exactly one column wide with the escapes and without them.
func TestHexSwatch_KeepsOneColumnWithoutColour(t *testing.T) {
	withColorProfile(t, termenv.Ascii)
	plain := hexSwatch("#FF5733")
	if plain != swatchGlyph {
		t.Errorf("with no colour profile the swatch = %q, want the bare glyph %q", plain, swatchGlyph)
	}

	withColorProfile(t, termenv.TrueColor)
	coloured := hexSwatch("#FF5733")
	if coloured == plain {
		t.Fatalf("the coloured swatch carries no sequence: %q", coloured)
	}
	if lipgloss.Width(coloured) != lipgloss.Width(plain) {
		t.Errorf("swatch width = %d coloured, %d plain — a row would shift between terminals",
			lipgloss.Width(coloured), lipgloss.Width(plain))
	}
	if w := lipgloss.Width(plain); w != 1 {
		t.Errorf("swatch is %d columns wide, want 1", w)
	}
}

// TestIndicatorSwatch_KeepsItsOwnContract: lifting the hex arm onto hexSwatch
// must not change what a device state renders. A firmware colour NAME outside
// the palette, and a malformed hex, both still show an uncoloured dot — the dot
// there is also saying "a colour was reported", which is why it differs from a
// form field, where a half-typed value must not look chosen.
func TestIndicatorSwatch_KeepsItsOwnContract(t *testing.T) {
	withColorProfile(t, termenv.TrueColor)

	if got := indicatorSwatch(""); got != "" {
		t.Errorf("no reported colour should draw nothing, got %q", got)
	}
	green := indicatorSwatch("green")
	if !strings.Contains(green, "47;158;68") {
		t.Errorf("a palette name should carry its hex, got %q", green)
	}
	if !strings.HasSuffix(green, swatchGlyph+" ") && !strings.HasSuffix(green, " ") {
		t.Errorf("the indicator swatch must keep its trailing separator, got %q", green)
	}
	for _, in := range []string{"chartreuse", "#zzzzzz", "#12"} {
		if got := indicatorSwatch(in); got != swatchGlyph+" " {
			t.Errorf("indicatorSwatch(%q) = %q, want a bare dot — an unfamiliar reading still "+
				"shows that something was reported", in, got)
		}
	}
	if got := indicatorSwatch("#ABC"); got != hexSwatch("#ABC")+" " {
		t.Errorf("an explicit hex should go through the shared helper, got %q", got)
	}
}

// TestJDEField_SwatchIsAppendedOutsideEverythingElse is the regression this
// design exists to dodge. jdeFieldArea draws a focused text row as the value
// followed by a reverse-video run, and StyleJDEHint wraps the hint; a colour
// sequence dropped into either one carries its own reset and would end that
// styling partway across the row. So the swatch may only ever be appended.
func TestJDEField_SwatchIsAppendedOutsideEverythingElse(t *testing.T) {
	withColorProfile(t, termenv.TrueColor)

	const labelWidth = 11
	bare := jdeField{Label: "Color", Kind: jdeText, Value: "#FF5733", Width: 8, Hint: "#RRGGBB", Focused: true}
	withSwatch := bare
	withSwatch.Swatch = hexSwatch("#FF5733")

	plain := renderJDEField(bare, labelWidth)
	row := renderJDEField(withSwatch, labelWidth)

	// Byte-for-byte the row without a swatch, plus the swatch. Nothing before it
	// can have changed, which is the whole claim.
	if want := plain + "  " + withSwatch.Swatch; row != want {
		t.Errorf("the swatch is not a pure append:\n got %q\nwant %q", row, want)
	}

	// And the input area survives whole: jdeFieldArea knows nothing about the
	// swatch, so finding its exact output inside the row proves nothing was
	// injected into the highlight.
	area := jdeFieldArea(withSwatch)
	if !strings.Contains(row, area) {
		t.Errorf("the focused field area is not contiguous in the row — something was drawn inside "+
			"the highlight:\n row  %q\n area %q", row, area)
	}
	if n := strings.Count(area, "\x1b[0m"); n != 1 {
		t.Errorf("the focused field area has %d resets, want 1 — the reverse-video run must be "+
			"unbroken: %q", n, area)
	}
	if w := lipgloss.Width(area); w != bare.Width {
		t.Errorf("the highlight covers %d columns, want the field's full %d", w, bare.Width)
	}

	// The hint's own span is untouched too.
	if !strings.Contains(row, StyleJDEHint.Render(bare.Hint)) {
		t.Errorf("the hint span is broken: %q", row)
	}
	if strings.Index(row, withSwatch.Swatch) < strings.Index(row, StyleJDEHint.Render(bare.Hint)) {
		t.Errorf("the swatch is drawn before the hint — it must trail, so a row that loses its "+
			"swatch mid-type does not slide its hint: %q", row)
	}
}

// TestJDEField_NoSwatchAddsNothing is the negative: a row without one must be
// exactly what it always was, including the two spaces the swatch would bring.
func TestJDEField_NoSwatchAddsNothing(t *testing.T) {
	withColorProfile(t, termenv.TrueColor)
	f := jdeField{Label: "Color", Kind: jdeText, Value: "#FF5", Width: 8, Hint: "#RRGGBB"}
	row := renderJDEField(f, 11)
	if strings.Contains(row, swatchGlyph) {
		t.Errorf("an unset Swatch drew a glyph anyway: %q", row)
	}
	if strings.HasSuffix(row, " ") {
		t.Errorf("an unset Swatch left its separator behind: %q", row)
	}
}

// --- Category form ---------------------------------------------------------

// categorySheetAt is a rendered category form at a known terminal size.
func categorySheetAt(t *testing.T, width int) *CategoryFormScreen {
	t.Helper()
	s := NewCategoryFormScreen(Deps{}, "")
	s.loading = false
	s.Update(tea.WindowSizeMsg{Width: width, Height: jdeSweepHeight})
	return s
}

// categoryColorField digs out the Color row as the sheet describes it.
func categoryColorField(t *testing.T, s *CategoryFormScreen) jdeField {
	t.Helper()
	return s.formFields()[elecRowOf(t, s.fields, cfColor)]
}

// TestCategoryForm_ColorSwatchTracksWhatIsTyped: the sample follows the input
// keystroke by keystroke and shows NOTHING until what is typed is a colour, so
// a half-finished "#FF5" never flashes a colour the operator did not choose.
//
// The table is walked through SetValue, which the field's CharLimit clamps —
// so every case is checked against what the field actually HOLDS, and the cases
// that cannot survive it are pinned separately below rather than skipped.
func TestCategoryForm_ColorSwatchTracksWhatIsTyped(t *testing.T) {
	withColorProfile(t, termenv.TrueColor)
	s := categorySheetAt(t, jdeSweepEWidth)

	for _, tc := range swatchColorCases {
		s.inputs[cfColor].SetValue(tc.in)
		if held := s.inputs[cfColor].Value(); held != tc.in {
			continue // clamped by CharLimit — see the next test
		}
		f := categoryColorField(t, s)
		if drawn := f.Swatch != ""; drawn != tc.want {
			t.Errorf("Color = %q: swatch drawn = %v, want %v — %s", tc.in, drawn, tc.want, tc.why)
		}
		// The WINDOWED View() would prove nothing about a row the cursor is not
		// near, so read the lines the sheet drew (sc-7wag).
		body := strings.Join(s.formLines().text, "\n")
		if got := strings.Contains(body, swatchGlyph); got != tc.want {
			t.Errorf("Color = %q: the rendered sheet %s a swatch, want the opposite",
				tc.in, map[bool]string{true: "shows", false: "hides"}[got])
		}
	}
}

// TestCategoryForm_ColorFieldCannotHoldAnOverlongValue: the reason the table
// above skips two of its cases. A 7-character CharLimit means "#FF57333" and a
// space-padded "  #FF5733  " can never be typed into this field in the first
// place — so the swatch does not have to defend against them, and a test that
// fed them in directly would be measuring something the operator cannot reach.
func TestCategoryForm_ColorFieldCannotHoldAnOverlongValue(t *testing.T) {
	s := categorySheetAt(t, jdeSweepEWidth)
	if got := categoryCharLimit(cfColor); got != 7 {
		t.Fatalf("colour CharLimit = %d, want 7 (#RRGGBB is the longest legal value)", got)
	}
	for _, in := range []string{"#FF57333", "  #FF5733  "} {
		s.inputs[cfColor].SetValue(in)
		if held := s.inputs[cfColor].Value(); held == in {
			t.Errorf("the field accepted %q whole — the CharLimit is not clamping", in)
		}
	}
}

// TestCategoryForm_ColorRowTradesItsHintForTheSample: "#RRGGBB" and the ● answer
// one question at two moments, so the row carries exactly one of them. The hint
// is what an empty or half-typed field needs; once the value IS a colour, the
// sample says more than the format note does.
func TestCategoryForm_ColorRowTradesItsHintForTheSample(t *testing.T) {
	withColorProfile(t, termenv.TrueColor)
	s := categorySheetAt(t, jdeSweepEWidth)

	for _, tc := range []struct {
		value string
		hint  bool
	}{
		{"", true},
		{"#FF5", false},
		{"#FF57", true},
		{"#FF5733", false},
	} {
		s.inputs[cfColor].SetValue(tc.value)
		f := categoryColorField(t, s)
		if got := f.Hint != ""; got != tc.hint {
			t.Errorf("Color = %q: hint shown = %v, want %v", tc.value, got, tc.hint)
		}
		if (f.Hint != "") == (f.Swatch != "") {
			t.Errorf("Color = %q: hint and sample are both %s — a colour row carries one or the "+
				"other, never both", tc.value, map[bool]string{true: "shown", false: "hidden"}[f.Hint != ""])
		}
	}
	if categoryFieldHint[cfColor] == "" {
		t.Errorf("the Color row has no format hint left to trade away")
	}
}

// TestCategoryForm_ColorSwatchCarriesTheTypedColour: not just any dot — the one
// the value names. Typing a second colour moves it.
func TestCategoryForm_ColorSwatchCarriesTheTypedColour(t *testing.T) {
	withColorProfile(t, termenv.TrueColor)
	s := categorySheetAt(t, jdeSweepEWidth)

	s.inputs[cfColor].SetValue("#FF5733")
	first := categoryColorField(t, s).Swatch
	if !strings.Contains(first, "255;87;51") {
		t.Fatalf("swatch does not carry #FF5733: %q", first)
	}
	s.inputs[cfColor].SetValue("#0055AA")
	second := categoryColorField(t, s).Swatch
	if second == first {
		t.Errorf("the swatch did not follow the value: still %q", second)
	}
	if !strings.Contains(second, "0;85;170") {
		t.Errorf("swatch does not carry #0055AA: %q", second)
	}
}

// TestCategoryForm_OnlyTheColorRowSwatches: no other row on the sheet grew one.
func TestCategoryForm_OnlyTheColorRowSwatches(t *testing.T) {
	withColorProfile(t, termenv.TrueColor)
	s := categorySheetAt(t, jdeSweepEWidth)
	s.inputs[cfName].SetValue("#FF5733")
	s.inputs[cfDescription].SetValue("#FF5733")
	s.inputs[cfColor].SetValue("#FF5733")

	for i, f := range s.formFields() {
		want := s.fields[i] == cfColor
		if got := f.Swatch != ""; got != want {
			t.Errorf("row %d (field %d) swatch = %v, want %v — a hex-looking value in some other "+
				"field is not a colour", i, s.fields[i], got, want)
		}
	}
}

// TestCategoryForm_FocusedColorRowKeepsItsHighlight is the same regression as
// the jdeField test, driven through the real sheet with a real focused
// textinput rather than a synthetic field.
func TestCategoryForm_FocusedColorRowKeepsItsHighlight(t *testing.T) {
	withColorProfile(t, termenv.TrueColor)
	s := categorySheetAt(t, jdeSweepEWidth)
	s.inputs[cfColor].SetValue("#FF5733")
	s.cursor = elecRowOf(t, s.fields, cfColor)
	s.syncFocus()

	f := categoryColorField(t, s)
	if !f.Focused {
		t.Fatalf("the Color row should be the focused one")
	}
	if f.Swatch == "" {
		t.Fatalf("the focused Color row lost its swatch")
	}
	row := renderJDEField(f, jdeLabelWidth(s.formFields()))
	if !strings.Contains(row, jdeFieldArea(f)) {
		t.Errorf("the focused Color row's input area is not contiguous — the swatch got inside the "+
			"highlight: %q", row)
	}
	if !strings.HasSuffix(row, f.Swatch) {
		t.Errorf("the swatch is not the last thing on the row: %q", row)
	}
}

// TestCategoryForm_ColorRowFitsTheBody: the swatch costs three columns, and
// clampToBox truncates rather than wraps — so a row that no longer fits would
// silently lose its tail (sc-ye0i). Measured at the family's 110 and again at
// 80, where sc-xxpa found other sheets already overrunning; this row must not
// be one of them.
func TestCategoryForm_ColorRowFitsTheBody(t *testing.T) {
	withColorProfile(t, termenv.TrueColor)
	for _, width := range []int{jdeSweepEWidth, 80} {
		s := categorySheetAt(t, width)
		s.inputs[cfColor].SetValue("#FF5733")
		budget := screenBodyWidth(width)
		row := elecRowOf(t, s.fields, cfColor)
		for _, cursor := range []int{row, 0} {
			s.cursor = cursor
			s.syncFocus()
			line := renderJDEField(categoryColorField(t, s), jdeLabelWidth(s.formFields()))
			if w := lipgloss.Width(line); w > budget {
				t.Errorf("at %d cols the Color row is %d wide but the pane is %d — it will be "+
					"clipped: %q", width, w, budget, line)
			}
		}
	}
}

// --- Category list ---------------------------------------------------------

// TestCategoryList_RowSwatchesItsColour mirrors the web list's per-row swatch.
// The row is built from two styled spans — the muted meta parens and, on the
// cursor row, the active highlight over the whole line — and a colour sequence
// inside either would strip the styling off everything after it. So the swatch
// trails, and both spans have to survive intact.
func TestCategoryList_RowSwatchesItsColour(t *testing.T) {
	withColorProfile(t, termenv.TrueColor)
	s := NewCategoryListScreen(Deps{})
	s.loading = false
	s.rows = []omsapi.Category{
		{ID: 1, Name: "Resistors", Color: "#FF5733", ItemCount: 4},
		{ID: 2, Name: "Capacitors"},
		{ID: 3, Name: "Diodes", Color: "#0aF"},
	}

	for _, cursor := range []int{0, 1, 2} {
		s.cursor = cursor
		for i, c := range s.rows {
			line := s.renderRow(i)
			want := c.Color != ""
			if got := strings.Contains(line, swatchGlyph); got != want {
				t.Errorf("cursor %d, row %d (%q, colour %q): swatch present = %v, want %v",
					cursor, i, c.Name, c.Color, got, want)
			}
			if !want {
				continue
			}
			if !strings.HasSuffix(line, hexSwatch(c.Color)) {
				t.Errorf("row %d: the swatch must trail the whole row, got %q", i, line)
			}
			if !strings.Contains(line, c.Color) {
				t.Errorf("row %d: the hex text is gone — the swatch annotates it, it does not "+
					"replace it: %q", i, line)
			}
		}
	}

	// The cursor row's highlight still spans the row it highlights.
	s.cursor = 0
	line := s.renderRow(0)
	inner := StyleSidebarItemActive.Render("▸ Resistors " + StyleMuted.Render("(4 items · #FF5733)"))
	if !strings.HasPrefix(line, inner) {
		t.Errorf("the cursor row's highlight was broken by the swatch:\n got %q\nwant prefix %q", line, inner)
	}
}

// --- Site settings ---------------------------------------------------------

func siteSettingsSheetAt(t *testing.T, width int) *SiteSettingsFormScreen {
	t.Helper()
	s := NewSiteSettingsFormScreen(Deps{})
	s.loading = false
	s.settings = &omsapi.SiteSettings{SiteName: "Acme"}
	s.hydrate()
	s.buildFields()
	s.Update(tea.WindowSizeMsg{Width: width, Height: jdeSweepHeight})
	return s
}

func ssFieldAt(t *testing.T, s *SiteSettingsFormScreen, id int) jdeField {
	t.Helper()
	return s.formFields()[elecRowOf(t, s.fields, id)]
}

// TestSiteSettingsForm_BothColorRowsSwatch: the same feature on the same field
// type, so both get it — shipping one without the other leaves exactly the
// inconsistency this bead is closing. The hydrated defaults swatch normally.
func TestSiteSettingsForm_BothColorRowsSwatch(t *testing.T) {
	withColorProfile(t, termenv.TrueColor)
	s := siteSettingsSheetAt(t, jdeSweepEWidth)

	if got := s.inputs[ssPrimaryColor].Value(); got != "#007cba" {
		t.Fatalf("hydrate should have defaulted the primary colour, got %q", got)
	}
	primary := ssFieldAt(t, s, ssPrimaryColor)
	secondary := ssFieldAt(t, s, ssSecondaryColor)
	if !strings.Contains(primary.Swatch, "0;124;186") {
		t.Errorf("primary swatch does not carry #007cba: %q", primary.Swatch)
	}
	if !strings.Contains(secondary.Swatch, "65;118;144") {
		t.Errorf("secondary swatch does not carry #417690: %q", secondary.Swatch)
	}

	// Each row reads its OWN input: blanking one must not take the other's.
	s.inputs[ssPrimaryColor].SetValue("")
	if got := ssFieldAt(t, s, ssPrimaryColor).Swatch; got != "" {
		t.Errorf("a blank primary should show no swatch, got %q", got)
	}
	if got := ssFieldAt(t, s, ssSecondaryColor).Swatch; got == "" {
		t.Errorf("blanking the primary took the secondary's swatch with it")
	}
	s.inputs[ssSecondaryColor].SetValue("#FF5")
	if got := ssFieldAt(t, s, ssSecondaryColor).Swatch; got == "" {
		t.Errorf("shorthand should swatch on this sheet too")
	}
	s.inputs[ssSecondaryColor].SetValue("#FF57")
	if got := ssFieldAt(t, s, ssSecondaryColor).Swatch; got != "" {
		t.Errorf("a half-typed secondary should show nothing, got %q", got)
	}
	// And the hint/sample trade holds on this sheet too: the half-typed row is
	// back to naming the format, the blank one never stopped.
	for _, id := range []int{ssPrimaryColor, ssSecondaryColor} {
		f := ssFieldAt(t, s, id)
		if f.Hint == "" {
			t.Errorf("%q has no value and no format hint", ssFieldLabel[id])
		}
		if f.Swatch != "" {
			t.Errorf("%q shows a sample for a value that is not a colour", ssFieldLabel[id])
		}
	}
	s.inputs[ssPrimaryColor].SetValue("#007cba")
	if f := ssFieldAt(t, s, ssPrimaryColor); f.Hint != "" || f.Swatch == "" {
		t.Errorf("a valid primary should trade its hint for a sample; hint = %q, swatch = %q",
			f.Hint, f.Swatch)
	}
}

// TestSiteSettingsForm_OnlyTheColorRowsSwatch: sixteen rows, two samples.
func TestSiteSettingsForm_OnlyTheColorRowsSwatch(t *testing.T) {
	withColorProfile(t, termenv.TrueColor)
	s := siteSettingsSheetAt(t, jdeSweepEWidth)
	s.inputs[ssName].SetValue("#FF5733")
	s.inputs[ssFooterText].SetValue("#FF5733")

	for i, f := range s.formFields() {
		id := s.fields[i]
		want := id == ssPrimaryColor || id == ssSecondaryColor
		if got := f.Swatch != ""; got != want {
			t.Errorf("row %d (field %d) swatch = %v, want %v", i, id, got, want)
		}
	}
}

// TestSiteSettingsForm_ColorRowsFitTheBody is the category sheet's width check
// for the two colour rows here — same three columns, a wider label column.
func TestSiteSettingsForm_ColorRowsFitTheBody(t *testing.T) {
	withColorProfile(t, termenv.TrueColor)
	for _, width := range []int{jdeSweepEWidth, 80} {
		s := siteSettingsSheetAt(t, width)
		budget := screenBodyWidth(width)
		for _, id := range []int{ssPrimaryColor, ssSecondaryColor} {
			row := elecRowOf(t, s.fields, id)
			for _, cursor := range []int{row, 0} {
				s.cursor = cursor
				s.syncFocus()
				line := renderJDEField(ssFieldAt(t, s, id), ssLabelWidth)
				if w := lipgloss.Width(line); w > budget {
					t.Errorf("at %d cols the %q row is %d wide but the pane is %d — it will be "+
						"clipped: %q", width, ssFieldLabel[id], w, budget, line)
				}
			}
		}
	}
}
