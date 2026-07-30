package tui

import (
	"strings"
	"testing"

	"github.com/uid0/scantty/internal/omsapi"
)

// paperItem is the design's worked example: a case of 10 reams of 500 sheets,
// counted in reams.
func paperItem() *omsapi.Item {
	level := 9
	return &omsapi.Item{
		ID: "abc", Name: "Copy paper", Stock: 2500,
		// Thresholds are read in the COUNT unit for a pack-counting item, so these
		// are 2 reams and 4 reams — not base-unit sheets.
		MinimumStock: 2, ReorderQuantity: 4,
		BaseUnit: "sheet", CountMode: omsapi.CountModeByLevel, CountLevel: &level,
		PackagingLevels: []omsapi.PackagingLevel{
			{ID: 8, Name: "case", SortOrder: 0, BaseUnits: 5000},
			{ID: 9, Name: "ream", SortOrder: 1, BaseUnits: 500},
			{ID: 10, Name: "sheet", SortOrder: 2, BaseUnits: 1},
		},
		OnHandDisplay: &omsapi.OnHandDisplay{
			Mode: omsapi.CountModeByLevel, Level: "ream", LevelCount: 5, Text: "5 ream(s)",
		},
	}
}

// bagItem is the open/closed example: cases of 100 bags, 3 sealed and 1 open.
func bagItem() *omsapi.Item {
	level := 3
	return &omsapi.Item{
		ID: "def", Name: "Trash bags", Stock: 300, OpenContainerCount: 1,
		MinimumStock: 1, ReorderQuantity: 2,
		BaseUnit: "bag", CountMode: omsapi.CountModeOpenClosed, CountLevel: &level,
		PackagingLevels: []omsapi.PackagingLevel{
			{ID: 3, Name: "case", SortOrder: 0, BaseUnits: 100},
			{ID: 4, Name: "bag", SortOrder: 1, BaseUnits: 1},
		},
		OnHandDisplay: &omsapi.OnHandDisplay{
			Mode: omsapi.CountModeOpenClosed, Level: "case", Sealed: 3, Open: 1,
			Text: "3 sealed + 1 open",
		},
	}
}

func TestPluralizeUnit(t *testing.T) {
	cases := []struct {
		unit  string
		count int
		want  string
	}{
		{"case", 1, "case"},
		{"case", 2, "cases"},
		{"case", 0, "cases"},
		// A sibilant ending needs "es" — packaging levels are named by hand and
		// "box" is one of the commonest.
		{"box", 2, "boxes"},
		{"Box", 2, "Boxes"},
		{"dish", 2, "dishes"},
		{"batch", 3, "batches"},
		{"unit", 1, "unit"},
	}
	for _, tc := range cases {
		if got := pluralizeUnit(tc.unit, tc.count); got != tc.want {
			t.Errorf("pluralizeUnit(%q, %d) = %q, want %q", tc.unit, tc.count, got, tc.want)
		}
	}
	// A fractional ratio is plural unless it is exactly one.
	if got := pluralizeUnitF("ream", 2.5); got != "reams" {
		t.Errorf("pluralizeUnitF(ream, 2.5) = %q", got)
	}
	if got := pluralizeUnitF("ream", 1); got != "ream" {
		t.Errorf("pluralizeUnitF(ream, 1) = %q", got)
	}
}

// TestCountsInPacks is the invariant's gate: only a fully-configured pack item
// reads true. Everything else keeps base-unit behaviour.
func TestCountsInPacks(t *testing.T) {
	if countsInPacks(nil) {
		t.Error("nil item must not count in packs")
	}
	// An each-mode item — every item until someone opts it in.
	if countsInPacks(&omsapi.Item{Stock: 12}) {
		t.Error("an item with no packaging fields must not count in packs")
	}
	// A chain alone is not enough: the mode has to say so too.
	if countsInPacks(&omsapi.Item{Stock: 12, PackagingLevels: paperItem().PackagingLevels}) {
		t.Error("a chain with count_mode each must not count in packs")
	}
	if !countsInPacks(paperItem()) {
		t.Error("a fully configured by_level item must count in packs")
	}
	if !countsInPacks(bagItem()) {
		t.Error("a fully configured open_closed item must count in packs")
	}
	// Half-configured: a pack mode whose count_level resolves to nothing. The
	// backend falls back to base units rather than erroring, and so must we.
	half := paperItem()
	missing := 999
	half.CountLevel = &missing
	if countsInPacks(half) {
		t.Error("a count_level that resolves to no rung must read as base-unit counting")
	}
	noLevel := paperItem()
	noLevel.CountLevel = nil
	if countsInPacks(noLevel) {
		t.Error("a pack mode with no count_level must read as base-unit counting")
	}
}

func TestCountUnitAndPackSize(t *testing.T) {
	if got := countUnitOf(paperItem()); got != "ream" {
		t.Errorf("countUnitOf(paper) = %q, want ream", got)
	}
	if got := countLevelBaseUnits(paperItem()); got != 500 {
		t.Errorf("countLevelBaseUnits(paper) = %d, want 500", got)
	}
	// An each-mode item reports its base unit and a pack size of 1, so a caller
	// can convert or price unconditionally.
	each := &omsapi.Item{Stock: 12}
	if got := countUnitOf(each); got != "unit" {
		t.Errorf("countUnitOf(each) = %q, want the default unit", got)
	}
	if got := countLevelBaseUnits(each); got != 1 {
		t.Errorf("countLevelBaseUnits(each) = %d, want 1", got)
	}
	if got := baseUnitOf(&omsapi.Item{BaseUnit: "  "}); got != "unit" {
		t.Errorf("baseUnitOf(blank) = %q, want the default", got)
	}
}

func TestCountAtLevelAndOpenCount(t *testing.T) {
	if got := countAtLevel(paperItem()); got != 5 {
		t.Errorf("countAtLevel(paper) = %d, want the 5 reams from on_hand_display", got)
	}
	// Under open/closed the countable stock is the SEALED packs.
	if got := countAtLevel(bagItem()); got != 3 {
		t.Errorf("countAtLevel(bags) = %d, want 3 sealed", got)
	}
	if got := openContainerCount(bagItem()); got != 1 {
		t.Errorf("openContainerCount(bags) = %d, want 1", got)
	}
	// With no display block, fall back to the raw columns.
	bare := bagItem()
	bare.OnHandDisplay = nil
	if got := countAtLevel(bare); got != 300 {
		t.Errorf("countAtLevel with no display = %d, want the base-unit stock", got)
	}
	if got := openContainerCount(bare); got != 1 {
		t.Errorf("openContainerCount with no display = %d, want the item column", got)
	}
	if got := countAtLevel(&omsapi.Item{Stock: 12}); got != 12 {
		t.Errorf("countAtLevel(each) = %d, want the base-unit stock", got)
	}
}

// TestOnHandLabel_EachModeUnchanged is the phase invariant: an item nobody opted
// in must read exactly as it did before the packaging matrix existed.
func TestOnHandLabel_EachModeUnchanged(t *testing.T) {
	if got := onHandLabel(&omsapi.Item{Stock: 12}); got != "12" {
		t.Errorf("an opted-out item must render the bare number, got %q", got)
	}
	// base_unit "unit" is the backend default every existing item carries, so it
	// counts as unnamed — "12 units" is a word this screen never printed.
	if got := onHandLabel(&omsapi.Item{Stock: 12, BaseUnit: "unit"}); got != "12" {
		t.Errorf("the default base unit must stay unnamed, got %q", got)
	}
	// An each-mode item with an explicitly named base unit does get the noun.
	if got := onHandLabel(&omsapi.Item{Stock: 12, BaseUnit: "glove"}); got != "12 gloves" {
		t.Errorf("named base unit = %q, want \"12 gloves\"", got)
	}
	if got := onHandLabel(&omsapi.Item{Stock: 1, BaseUnit: "glove"}); got != "1 glove" {
		t.Errorf("singular named base unit = %q", got)
	}
	// A server-side "each" display must not hijack the rendering either.
	each := &omsapi.Item{Stock: 12, OnHandDisplay: &omsapi.OnHandDisplay{
		Mode: omsapi.CountModeEach, Unit: "unit", BaseUnits: 12, Text: "12 unit"}}
	if got := onHandLabel(each); got != "12" {
		t.Errorf("an each-mode display must not be rendered verbatim, got %q", got)
	}
}

// TestOnHandLabel_PackModesUseServerText keeps the wording identical across
// scantty, the web and the index card.
func TestOnHandLabel_PackModesUseServerText(t *testing.T) {
	if got := onHandLabel(paperItem()); got != "5 ream(s)" {
		t.Errorf("by_level = %q, want the server text", got)
	}
	if got := onHandLabel(bagItem()); got != "3 sealed + 1 open" {
		t.Errorf("open_closed = %q, want the server text", got)
	}
	// A pack mode whose display the server fell back to "each" for renders the
	// base-unit form — the same conservatism as countsInPacks.
	half := paperItem()
	half.OnHandDisplay = &omsapi.OnHandDisplay{Mode: omsapi.CountModeEach, Text: "2500 sheet"}
	if got := onHandLabel(half); got != "2500 sheets" {
		t.Errorf("half-configured = %q, want the base-unit form", got)
	}
}

func TestDescribePackChain(t *testing.T) {
	got := describePackChain(paperItem().PackagingLevels)
	want := []string{"1 case = 10 reams", "1 ream = 500 sheets"}
	if len(got) != len(want) {
		t.Fatalf("chain lines = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
	// The base rung describes nothing, so a single-rung chain yields no lines.
	if lines := describePackChain([]omsapi.PackagingLevel{{Name: "each", BaseUnits: 1}}); len(lines) != 0 {
		t.Errorf("single-rung chain = %v, want no lines", lines)
	}
	if lines := describePackChain(nil); len(lines) != 0 {
		t.Errorf("empty chain = %v, want no lines", lines)
	}
	// A chain is only required to SHRINK, not to divide evenly.
	uneven := describePackChain([]omsapi.PackagingLevel{
		{Name: "box", SortOrder: 0, BaseUnits: 10},
		{Name: "sleeve", SortOrder: 1, BaseUnits: 4},
		{Name: "pill", SortOrder: 2, BaseUnits: 1},
	})
	if uneven[0] != "1 box = 2.5 sleeves" {
		t.Errorf("uneven ratio = %q, want \"1 box = 2.5 sleeves\"", uneven[0])
	}
	// Rungs are ordered by sort_order regardless of arrival order.
	scrambled := describePackChain([]omsapi.PackagingLevel{
		{Name: "sheet", SortOrder: 2, BaseUnits: 1},
		{Name: "case", SortOrder: 0, BaseUnits: 5000},
		{Name: "ream", SortOrder: 1, BaseUnits: 500},
	})
	if scrambled[0] != "1 case = 10 reams" {
		t.Errorf("scrambled chain first line = %q", scrambled[0])
	}
}

// TestValidatePackagingChain covers every rule the backend enforces, so a bad
// chain never reaches the wire.
func TestValidatePackagingChain(t *testing.T) {
	// An EMPTY chain is valid: the item is simply counted in base units.
	if errs := validatePackagingChain(nil); len(errs) != 0 {
		t.Errorf("empty chain should be valid, got %v", errs)
	}

	good := []packagingRow{
		{key: 1, name: "case", baseUnits: 5000},
		{key: 2, name: "ream", baseUnits: 500},
		{key: 3, name: "sheet", baseUnits: 1},
	}
	if errs := validatePackagingChain(good); len(errs) != 0 {
		t.Errorf("valid chain rejected: %v", errs)
	}

	cases := []struct {
		name string
		rows []packagingRow
		want string
	}{
		{"unnamed rung", []packagingRow{{key: 1, name: " ", baseUnits: 1}}, "needs a name"},
		{"zero size", []packagingRow{{key: 1, name: "case", baseUnits: 0}}, "at least one base unit"},
		{"no base rung", []packagingRow{
			{key: 1, name: "case", baseUnits: 100},
			{key: 2, name: "ream", baseUnits: 10},
		}, "Exactly one packaging level must be the base unit"},
		{"two base rungs", []packagingRow{
			{key: 1, name: "case", baseUnits: 1},
			{key: 2, name: "each", baseUnits: 1},
		}, "Exactly one packaging level must be the base unit"},
		{"base rung not last", []packagingRow{
			{key: 1, name: "sheet", baseUnits: 1},
			{key: 2, name: "ream", baseUnits: 500},
		}, "must be the innermost"},
		{"not shrinking", []packagingRow{
			{key: 1, name: "ream", baseUnits: 500},
			{key: 2, name: "case", baseUnits: 5000},
			{key: 3, name: "sheet", baseUnits: 1},
		}, "must hold fewer base units"},
	}
	for _, tc := range cases {
		errs := validatePackagingChain(tc.rows)
		if len(errs) == 0 {
			t.Errorf("%s: expected an error", tc.name)
			continue
		}
		if !strings.Contains(strings.Join(errs, " "), tc.want) {
			t.Errorf("%s: errors %v do not mention %q", tc.name, errs, tc.want)
		}
	}

	// A half-typed row reports only the per-row problem: the ordering rules read
	// sizes as real numbers, so reporting them too would be noise.
	errs := validatePackagingChain([]packagingRow{
		{key: 1, name: "case", baseUnits: 0},
		{key: 2, name: "sheet", baseUnits: 1},
	})
	if len(errs) != 1 || !strings.Contains(errs[0], "at least one base unit") {
		t.Errorf("half-typed chain errors = %v, want just the size rule", errs)
	}
}

func TestResolveCountLevelError(t *testing.T) {
	rows := []packagingRow{
		{key: 1, name: "case", baseUnits: 100},
		{key: 2, name: "bag", baseUnits: 1},
	}
	// "each" needs no level, and must not complain about one.
	if msg := resolveCountLevelError(omsapi.CountModeEach, 0, rows); msg != "" {
		t.Errorf("each mode error = %q, want none", msg)
	}
	if msg := resolveCountLevelError(omsapi.CountModeByLevel, 0, rows); msg == "" {
		t.Error("a pack mode with no pick must be an error")
	}
	if msg := resolveCountLevelError(omsapi.CountModeByLevel, 1, rows); msg != "" {
		t.Errorf("valid pick error = %q, want none", msg)
	}
	// A key that is not in the chain — e.g. the rung was deleted.
	if msg := resolveCountLevelError(omsapi.CountModeOpenClosed, 99, rows); msg == "" {
		t.Error("a dangling count-level key must be an error")
	}
}

func TestPackagingRowsRoundTrip(t *testing.T) {
	rows, next := toPackagingRows(paperItem().PackagingLevels, 0)
	if len(rows) != 3 || next != 3 {
		t.Fatalf("rows = %d, nextKey = %d", len(rows), next)
	}
	// Outermost first, carrying the server pk.
	if rows[0].name != "case" || rows[0].baseUnits != 5000 || rows[0].id != 8 {
		t.Errorf("first row = %+v", rows[0])
	}
	// Keys are unique so the count-level pick can name a row.
	if rows[0].key == rows[1].key {
		t.Error("row keys must be distinct")
	}

	// sort_order is the row INDEX, and no pk is carried into the payload.
	payload := toPackagingPayload(rows)
	for i, rung := range payload {
		if rung.SortOrder != i {
			t.Errorf("payload rung %d sort_order = %d, want the index", i, rung.SortOrder)
		}
	}
	if payload[1].Name != "ream" || payload[1].BaseUnits != 500 {
		t.Errorf("payload rung 1 = %+v", payload[1])
	}
	// Names are trimmed on the way out.
	trimmed := toPackagingPayload([]packagingRow{{key: 1, name: "  case  ", baseUnits: 10}})
	if trimmed[0].Name != "case" {
		t.Errorf("payload name = %q, want trimmed", trimmed[0].Name)
	}
}

// TestChainSignature_DetectsOnlyRealChanges pins what counts as "dirty": a
// signature match is what keeps an untouched chain out of the payload entirely.
func TestChainSignature_DetectsOnlyRealChanges(t *testing.T) {
	rows, _ := toPackagingRows(paperItem().PackagingLevels, 0)
	base := chainSignature(rows)

	// Re-deriving the same chain (a fresh hydrate) must match.
	again, _ := toPackagingRows(paperItem().PackagingLevels, 100)
	if chainSignature(again) != base {
		t.Error("identical chains must have identical signatures regardless of row keys")
	}
	// Whitespace-only name edits are not changes.
	spaced := append([]packagingRow(nil), rows...)
	spaced[0].name = "  case  "
	if chainSignature(spaced) != base {
		t.Error("a whitespace-only edit must not read as dirty")
	}
	// A size change is.
	resized := append([]packagingRow(nil), rows...)
	resized[1].baseUnits = 250
	if chainSignature(resized) == base {
		t.Error("a size change must read as dirty")
	}
	// So is a reorder, since position is the rung's identity on the wire.
	swapped := append([]packagingRow(nil), rows...)
	swapped[0], swapped[1] = swapped[1], swapped[0]
	if chainSignature(swapped) == base {
		t.Error("a reorder must read as dirty")
	}
	// And so is a removal.
	if chainSignature(rows[:2]) == base {
		t.Error("a removed rung must read as dirty")
	}
	if chainSignature(nil) != "" {
		t.Errorf("empty signature = %q, want empty", chainSignature(nil))
	}
}

func TestPerParent(t *testing.T) {
	rows := []packagingRow{
		{key: 1, name: "case", baseUnits: 5000},
		{key: 2, name: "ream", baseUnits: 500},
		{key: 3, name: "sheet", baseUnits: 1},
	}
	if got, ok := perParent(rows, 0); !ok || got != 10 {
		t.Errorf("perParent(case) = %v/%v, want 10", got, ok)
	}
	if got, ok := perParent(rows, 1); !ok || got != 500 {
		t.Errorf("perParent(ream) = %v/%v, want 500", got, ok)
	}
	// The base rung has nothing below it.
	if _, ok := perParent(rows, 2); ok {
		t.Error("perParent of the base rung must report not-ok")
	}
	if _, ok := perParent(rows, -1); ok {
		t.Error("perParent out of range must report not-ok")
	}
	// A half-typed neighbour has no ratio yet.
	if _, ok := perParent([]packagingRow{{baseUnits: 10}, {baseUnits: 0}}, 0); ok {
		t.Error("perParent with an unsized rung below must report not-ok")
	}
}

func TestPackagingRowIndexAndCountModeIndex(t *testing.T) {
	rows := []packagingRow{{key: 4}, {key: 7}}
	if got := packagingRowIndex(rows, 7); got != 1 {
		t.Errorf("packagingRowIndex = %d, want 1", got)
	}
	if got := packagingRowIndex(rows, 9); got != -1 {
		t.Errorf("missing key index = %d, want -1", got)
	}
	if got := countModeIndex(omsapi.CountModeOpenClosed); countModeOptions[got].value != omsapi.CountModeOpenClosed {
		t.Errorf("countModeIndex(open_closed) = %d", got)
	}
	// An empty mode — what a backend predating the matrix returns — is "each".
	if got := countModeIndex(""); countModeOptions[got].value != omsapi.CountModeEach {
		t.Errorf("countModeIndex(\"\") must default to each, got %q", countModeOptions[got].value)
	}
}
