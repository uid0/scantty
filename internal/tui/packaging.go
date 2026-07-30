package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/uid0/scantty/internal/omsapi"
)

// Unit-of-measure / packaging-chain helpers (OMS #979/#980/#981 backend, web
// #983).
//
// The terminal twin of frontend/src/utils/packaging.ts, which is itself the
// client twin of backend/inventory/services/packaging.py: the same chain rules,
// so the item form rejects an impossible chain before the request instead of
// round-tripping a 400, and the same "is this item counted in packs?" predicate
// every mode-aware surface branches on.
//
// The backend stays the authority — anything these helpers let through is still
// validated server-side, and on-hand text is rendered by the server. Nothing
// here converts stock: Item.Stock remains the canonical base-unit count.

// defaultBaseUnit is the backend's own default for InventoryItem.base_unit, and
// therefore what every item that has not opted into the packaging matrix
// carries. Rendering treats it as "unnamed" so an un-opted-in item reads exactly
// as it did before the matrix existed.
const defaultBaseUnit = "unit"

// countModeOptions is the count-mode picker, in the order an operator grows into
// it. Same set and wording as the web's COUNT_MODE_LABELS.
var countModeOptions = []selectOption{
	{omsapi.CountModeEach, "Each (count base units)"},
	{omsapi.CountModeByLevel, "By packaging level (count whole packs)"},
	{omsapi.CountModeOpenClosed, "Sealed + open (count sealed, track the open one)"},
}

// countModeIndex is the picker index for a wire count_mode, defaulting to
// "each" for the empty string a backend that predates the matrix returns.
func countModeIndex(mode string) int {
	for i, o := range countModeOptions {
		if o.value == mode {
			return i
		}
	}
	return 0
}

// pluralUnit is unit pluralised. Handles the sibilant ending the naive +"s"
// gets wrong ("box" → "boxes", not "boxs"), because packaging levels are named
// by hand and "box" is one of the commonest ones. Deliberately a shade better
// than the backend's own _plural, which only ever appends "s" — nothing
// compares the two strings, and no server-rendered text is re-pluralised here.
func pluralUnit(unit string) string {
	lower := strings.ToLower(unit)
	for _, suffix := range []string{"s", "x", "z", "ch", "sh"} {
		if strings.HasSuffix(lower, suffix) {
			return unit + "es"
		}
	}
	return unit + "s"
}

// pluralizeUnit is unit pluralised for count (the tui sibling of the naive
// plural() in maintenance_item_form.go, which existing callers keep using).
func pluralizeUnit(unit string, count int) string {
	if count == 1 {
		return unit
	}
	return pluralUnit(unit)
}

// pluralizeUnitF is pluralizeUnit for a possibly-fractional count — a chain is
// only required to shrink, not to divide evenly, so "1 case = 2.5 boxes".
func pluralizeUnitF(unit string, count float64) string {
	if count == 1 {
		return unit
	}
	return pluralUnit(unit)
}

// baseUnitOf is the item's base unit, falling back to the backend's own default
// (which is also what a backend predating the matrix implies).
func baseUnitOf(it *omsapi.Item) string {
	if it == nil {
		return defaultBaseUnit
	}
	if u := strings.TrimSpace(it.BaseUnit); u != "" {
		return u
	}
	return defaultBaseUnit
}

// countLevelOf is the rung the item is counted in, or nil.
func countLevelOf(it *omsapi.Item) *omsapi.PackagingLevel {
	if it == nil || it.CountLevel == nil {
		return nil
	}
	for i := range it.PackagingLevels {
		if it.PackagingLevels[i].ID == *it.CountLevel {
			return &it.PackagingLevels[i]
		}
	}
	return nil
}

// countsInPacks reports whether the item is counted in whole packs of a usable
// counting level — the twin of the backend's counts_in_packs, and deliberately
// just as conservative: a half-configured item (a pack mode with no resolvable
// count level) reads false and keeps today's base-unit behaviour instead of
// erroring. This is the ONE predicate every mode-aware surface branches on.
func countsInPacks(it *omsapi.Item) bool {
	if it == nil || it.CountMode == "" || it.CountMode == omsapi.CountModeEach {
		return false
	}
	level := countLevelOf(it)
	return level != nil && level.BaseUnits >= 1
}

// countUnitOf is the noun a quantity for this item is entered and reported in —
// the counting rung's name for a pack-counting item, the base unit otherwise.
// Mirrors the backend's count_unit, so a prompt labels its input with the unit
// the server will read it in.
func countUnitOf(it *omsapi.Item) string {
	if level := countLevelOf(it); countsInPacks(it) && level != nil {
		return level.Name
	}
	return baseUnitOf(it)
}

// countLevelBaseUnits is how many base units one counting pack holds — 1 for an
// item that is not counted in packs, so a caller can price or convert
// unconditionally.
func countLevelBaseUnits(it *omsapi.Item) int {
	if level := countLevelOf(it); countsInPacks(it) && level != nil {
		return level.BaseUnits
	}
	return 1
}

// countAtLevel is the item's whole count in the unit it is COUNTED in, which is
// what a cycle-count prompt seeds itself with. Prefers the server's
// OnHandDisplay (level_count, or sealed under open/closed) and falls back to the
// base-unit stock for an each-mode or half-configured item.
func countAtLevel(it *omsapi.Item) int {
	if it == nil {
		return 0
	}
	if d := it.OnHandDisplay; d != nil {
		switch d.Mode {
		case omsapi.CountModeByLevel:
			return d.LevelCount
		case omsapi.CountModeOpenClosed:
			return d.Sealed
		}
	}
	return it.Stock
}

// openContainerCount is how many packs are currently open. The server's
// OnHandDisplay is preferred (it is computed from the same field) with the raw
// item column as the fallback for a response that omits the display block.
func openContainerCount(it *omsapi.Item) int {
	if it == nil {
		return 0
	}
	if d := it.OnHandDisplay; d != nil && d.Mode == omsapi.CountModeOpenClosed {
		return d.Open
	}
	return it.OpenContainerCount
}

// onHandLabel is the on-hand quantity as a human reads it.
//
// A pack-counting item renders the server's OnHandDisplay.Text verbatim ("4
// case(s)" / "3 sealed + 1 open"), so scantty, the web and the index card all
// say the same thing. An each-mode item keeps the bare base-unit number the
// detail has always shown — the phase invariant is that an item nobody opted in
// reads exactly as it did before the packaging matrix existed — and gains the
// unit noun only once someone has actually NAMED a base unit (it is "unit" for
// every item until then, and "12 units" is a word this screen never printed).
func onHandLabel(it *omsapi.Item) string {
	if it == nil {
		return ""
	}
	if d := it.OnHandDisplay; d != nil && d.Mode != "" && d.Mode != omsapi.CountModeEach && d.Text != "" {
		return d.Text
	}
	unit := strings.TrimSpace(it.BaseUnit)
	if unit == "" || unit == defaultBaseUnit {
		return strconv.Itoa(it.Stock)
	}
	return fmt.Sprintf("%d %s", it.Stock, pluralizeUnit(unit, it.Stock))
}

// describePackChain is one "1 case = 10 reams" line per non-base rung, the
// innermost rung excluded (it contains nothing). Empty for an item with no chain
// or a single base rung, which describes nothing.
func describePackChain(levels []omsapi.PackagingLevel) []string {
	ordered := sortedPackagingLevels(levels)
	var lines []string
	for i, level := range ordered {
		if i+1 >= len(ordered) {
			break
		}
		below := ordered[i+1]
		if below.BaseUnits < 1 {
			continue
		}
		ratio := float64(level.BaseUnits) / float64(below.BaseUnits)
		lines = append(lines, fmt.Sprintf("1 %s = %s %s",
			level.Name, trimFloat(ratio), pluralizeUnitF(below.Name, ratio)))
	}
	return lines
}

// sortedPackagingLevels copies levels into sort_order order (outermost first)
// without mutating the caller's slice — the server already sends them ordered,
// but nothing in the contract promises it.
func sortedPackagingLevels(levels []omsapi.PackagingLevel) []omsapi.PackagingLevel {
	ordered := make([]omsapi.PackagingLevel, len(levels))
	copy(ordered, levels)
	for i := 1; i < len(ordered); i++ {
		for j := i; j > 0 && ordered[j].SortOrder < ordered[j-1].SortOrder; j-- {
			ordered[j], ordered[j-1] = ordered[j-1], ordered[j]
		}
	}
	return ordered
}

// ---------------------------------------------------------------------------
// The form's editable chain
// ---------------------------------------------------------------------------

// packagingRow is a chain rung as the item form edits it. sort_order is
// deliberately absent: the editor keeps rows largest-first and derives
// sort_order from the row's INDEX on save, which is exactly the backend's
// convention (0 = outermost).
//
// key is a client-only stable identity so the count-level selection survives
// reorders, inserts and deletes — the same reason the web tracks a row key
// rather than a pk. id is the server pk once the rung has been saved (0 until
// then), and baseUnits is 0 for a rung whose size has not been typed yet.
type packagingRow struct {
	key       int
	id        int
	name      string
	baseUnits int
}

// toPackagingRows converts a server chain into editor rows, outermost first.
// nextKey is the running row-key counter the form owns; the returned value is
// the counter after minting a key per row.
func toPackagingRows(levels []omsapi.PackagingLevel, nextKey int) ([]packagingRow, int) {
	ordered := sortedPackagingLevels(levels)
	rows := make([]packagingRow, 0, len(ordered))
	for _, level := range ordered {
		nextKey++
		rows = append(rows, packagingRow{
			key:       nextKey,
			id:        level.ID,
			name:      level.Name,
			baseUnits: level.BaseUnits,
		})
	}
	return rows, nextKey
}

// toPackagingPayload converts editor rows into the nested packaging_levels
// write payload. sort_order is the row index, so "largest first" in the UI is
// "sort_order 0 is outermost" on the wire. The pk is deliberately not sent: the
// serializer upserts on (item, sort_order), so a rung that keeps its position
// keeps its pk — and any count_level pointing at it survives the save.
func toPackagingPayload(rows []packagingRow) []omsapi.PackagingLevelWrite {
	out := make([]omsapi.PackagingLevelWrite, 0, len(rows))
	for i, row := range rows {
		out = append(out, omsapi.PackagingLevelWrite{
			Name:      strings.TrimSpace(row.name),
			SortOrder: i,
			BaseUnits: row.baseUnits,
		})
	}
	return out
}

// chainSignature is the chain as compared for dirtiness — position, name and
// size, nothing else. A save that changes none of it sends no chain at all, so
// an each-mode item with no packaging keeps writing exactly the request it
// always did (and a form that failed to hydrate can never wipe a stored chain).
func chainSignature(rows []packagingRow) string {
	var b strings.Builder
	for _, row := range rows {
		b.WriteString(strings.TrimSpace(row.name))
		b.WriteByte('\x1f')
		b.WriteString(strconv.Itoa(row.baseUnits))
		b.WriteByte('\x1e')
	}
	return b.String()
}

// validatePackagingChain reports every problem with the chain, the way the
// backend's validate_packaging_chain does (empty result = valid). An EMPTY chain
// is valid: an item with no packaging levels is simply counted in base units.
func validatePackagingChain(rows []packagingRow) []string {
	if len(rows) == 0 {
		return nil
	}

	var errs []string
	for _, row := range rows {
		if strings.TrimSpace(row.name) == "" {
			errs = append(errs, "Every packaging level needs a name.")
			break
		}
	}
	for _, row := range rows {
		if row.baseUnits < 1 {
			errs = append(errs, "Every packaging level must hold at least one base unit.")
			break
		}
	}
	// The ordering rules below read sizes as real numbers, so they only make
	// sense once every rung has one.
	if len(errs) > 0 {
		return errs
	}

	baseRungs := 0
	for _, row := range rows {
		if row.baseUnits == 1 {
			baseRungs++
		}
	}
	if baseRungs != 1 {
		errs = append(errs, fmt.Sprintf(
			"Exactly one packaging level must be the base unit (holding 1 base unit); found %d.",
			baseRungs))
	} else if rows[len(rows)-1].baseUnits != 1 {
		errs = append(errs, "The base packaging level must be the innermost (listed last).")
	}

	for i := 1; i < len(rows); i++ {
		if rows[i].baseUnits >= rows[i-1].baseUnits {
			errs = append(errs, fmt.Sprintf(
				"Packaging level %q must hold fewer base units than %q that contains it.",
				strings.TrimSpace(rows[i].name), strings.TrimSpace(rows[i-1].name)))
		}
	}
	return errs
}

// resolveCountLevelError says why countLevelKey is wrong for mode, or "" if the
// pair fits — the twin of the backend's resolve_count_level_error. A zero key
// means "nothing chosen".
func resolveCountLevelError(mode string, countLevelKey int, rows []packagingRow) string {
	if mode == omsapi.CountModeEach {
		return ""
	}
	if countLevelKey == 0 {
		return "Choose which packaging level this item is counted in."
	}
	for _, row := range rows {
		if row.key == countLevelKey {
			return ""
		}
	}
	return "The counting level must be one of the packaging levels above."
}

// packagingRowIndex is the position of the row with key, or -1. The position IS
// the rung's sort_order on the wire, which is how the saved chain's pk is
// resolved after a write.
func packagingRowIndex(rows []packagingRow, key int) int {
	for i, row := range rows {
		if row.key == key {
			return i
		}
	}
	return -1
}

// perParent is how many of the next rung down fit in this one — the "1 case =
// 10 reams" number — computed the way the serializer's get_per_parent does. ok
// is false for the base (last) rung, which has nothing below it, and for a
// half-typed neighbour.
func perParent(rows []packagingRow, index int) (float64, bool) {
	if index < 0 || index+1 >= len(rows) {
		return 0, false
	}
	own, below := rows[index].baseUnits, rows[index+1].baseUnits
	if own < 1 || below < 1 {
		return 0, false
	}
	return float64(own) / float64(below), true
}
