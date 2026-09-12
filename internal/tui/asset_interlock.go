// AssetInterlockScreen — taking the machine you are standing at out of service,
// and putting it back.
//
// THIS IS THE SCREEN THE TASK EXISTS FOR. ScanTTY could already NOTE that a
// machine was broken (asset_detail.go's `o` / `R` out-of-service records) and
// could not STOP IT BEING USED; the person who decides a machine is unsafe is
// standing in front of it, which is where the terminal is, and until now they
// had to walk to a browser. Everything here is in service of that one gesture.
//
// LOCK AND DISABLE ARE TWO DIFFERENT WORDS FOR STOPPING SOMETHING AND ONLY ONE
// OF THEM DOES. The distinction is not a nicety, it is the whole reason this
// screen is worded the way it is:
//
//   - LOCK creates a ForgeKey lockout and puts the asset in `locked_out`.
//     `forgekey.services.access_control.is_authorized` — which calls itself "the
//     single source of truth for 'can this user use this asset right now'" —
//     returns False while any active lockout exists, so the badge reader denies.
//     THIS IS THE ONE THAT STOPS THE MACHINE.
//   - DISABLE flips `is_active`, whose help_text is "Inactive assets are hidden
//     from most views". `is_authorized` never reads it. A disabled machine with a
//     valid authorization and no lockout IS STILL USABLE.
//
// So an operator who disables a dangerous machine and walks away has hidden a
// record and stopped nothing. That sentence is on the pane, in the essential
// header row and again in the confirm, because it is the one mistake this screen
// can let somebody make. internal/omsapi/asset_interlock.go carries the measured
// contract behind it.
//
// THE STATE IS THE POINT OF THE LANDING FRAME. "An operator must never be unsure
// whether the machine they are standing at is locked" is the requirement, so the
// interlock row is the header's ESSENTIAL one — wherever this frame is drawn at
// all, it says LOCKED or not — with the record axis and the asset's name beside it
// as context, and the state is read from every write's own reply rather than
// predicted from the status code.
//
// NOTHING HERE PRE-EMPTS THE SERVER. `can_unlock` and `can_enable` are SHOWN as
// facts and never gate a key: measured on a `report_only` asset both are false
// and the server accepts lock (201) and disable (200) anyway, so gating on them
// would refuse what the server allows — the defect class AGENTS.md records under
// the kit serialized-component ban. Every key sends its write and relays whatever
// comes back, in the server's own words (interlockRefusal).
//
// WHAT IS GATED IS THE STATE THE ACTION CHANGES, WITH ONE DELIBERATE EXCEPTION.
// `u` needs something locked to unlock (the server itself answers "Asset is not
// locked"), `d` needs an active asset and `e` an inactive one — those are facts
// the server just told us, not permission guesses. `l` is offered EVEN WHEN
// ALREADY LOCKED, because lock STACKS: a second lockout is a real operation and
// how lockout/tagout is meant to work (my lock protects me even when yours is
// cleared), so hiding the key would refuse something the server supports and that
// carries safety meaning. The bar says which it is — `Lock` or `Add lock`.
//
// KEYS, and why each is deliberate rather than a single press:
//
//	l        Lock — opens the REASON form (the server requires one), then a confirm
//	u        Unlock — opens a confirm that shows WHAT is being re-enabled
//	d        Disable — opens a confirm
//	e        Enable — opens a confirm
//	Ctrl-X   on any confirm, the write. NOT Enter — see below
//	Esc      back; on the lock confirm, back to the reason still in the box
//	r        refresh
//	UP/DN · PgUp/PgDn · Home/End   scroll the state sheet, where it scrolls
//
// UNLOCKING IS NOT MADE EASIER THAN LOCKING. It is the dangerous direction — it
// makes an unsafe machine usable again — so it costs the same two deliberate acts
// (a key that opens a frame, then Ctrl-X), and its confirm carries the lockout's
// OWN REASON, actor, level and timestamp. An operator clearing a lockout reads
// why it was set before they clear it. Enable is confirmed for the same reason,
// which is where this screen parts company with the web: the web confirms disable
// and NOT enable, guarding the safe direction and not the restoring one.
//
// CTRL-X CONFIRMS, NOT ENTER, on every one of the four. Enter is the key that
// opened the lock form and the letters are what opened the other three, so a
// reflexive second press must not be enough to stop or restart a machine — the
// same choice the PO line-delete confirm and the meter-reading confirm make.
//
// A 200 IS NOT A PROMISE THAT THE MACHINE IS USABLE. Unlock clears ONE lockout of
// a stack, so a success can come back still locked (measured; recorded in
// testdata/asset_unlock_still_locked.json). applyWrite reads IsLocked off the
// reply and says STILL LOCKED at warning level when it is, because reporting
// "unlocked" off the status code would tell somebody a machine was safe to start
// when the server still denies it.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// interlockLabelW is this screen's shared label column.
//
// renderJDEField TRUNCATES a label wider than the width it is handed with no
// mark, so the column is the widest label any row carries — "Interlock" and
// "Locked by" are both 9, the same as the asset meter and document screens'
// assetLabelW, which is why the frames read alike across the three sibling
// surfaces an asset now has.
const interlockLabelW = 9

// interlockAction is which of the four writes a confirm is standing in front of.
type interlockAction int

const (
	interlockNone interlockAction = iota
	interlockLock
	interlockUnlock
	interlockDisable
	interlockEnable
)

type interlockPhase int

const (
	interlockPhaseState interlockPhase = iota
	// interlockPhaseReason is lock's only typed field. It exists because the
	// SERVER requires a reason and refuses a blank one, so the form asks for it
	// rather than discovering it — and because the sentence is what the next
	// person to walk up to the machine reads off the lockout.
	interlockPhaseReason
	interlockPhaseConfirm
	// interlockPhaseCount is the sentinel the phase sweep walks to. It exists so a
	// phase added later is swept BY CONSTRUCTION rather than by somebody
	// remembering to extend a table — the derivation rule AGENTS.md records, and
	// the reason poPhaseCount exists one flow over.
	interlockPhaseCount
)

type AssetInterlockScreen struct {
	deps      Deps
	assetID   string
	assetName string

	asset   *omsapi.Asset
	loading bool
	loadErr string
	loadSeq int
	jdeScreen

	phase  interlockPhase
	scroll int

	// reason is lock's typed box.
	reason textinput.Model

	// errMsg is the last WRITE's failure — the server's own sentence. It rides
	// the status row, which no budget can trim.
	errMsg string
	// note is the screen's answer to the last keypress: what it DID, at a level.
	note      string
	noteLevel StatusLevel

	// pending is which write a confirm is about, and confirmScroll its caveat's
	// offset. saving is a write in flight.
	pending       interlockAction
	saving        bool
	confirmScroll int
}

type assetInterlockLoadedMsg struct {
	asset *omsapi.Asset
	err   error
	seq   int
}

type assetInterlockWrittenMsg struct {
	action interlockAction
	asset  *omsapi.Asset
	err    error
}

func NewAssetInterlockScreen(deps Deps, assetID, assetName string) *AssetInterlockScreen {
	s := &AssetInterlockScreen{
		deps:      deps,
		assetID:   assetID,
		assetName: assetName,
		loading:   true,
	}
	// No placeholder on a columnar sheet: a placeholder fills the fixed input
	// area and hides the underscores that say the field is empty, so what the
	// field wants rides beside it as a Hint.
	in := textinput.New()
	in.Prompt = ""
	// The server stores the reason in a TextField with no length cap, and this
	// is the sentence somebody reads at the machine — so the box is generous and
	// the fold, not the CharLimit, is what handles a long one.
	in.CharLimit = 500
	s.reason = in
	return s
}

func (s *AssetInterlockScreen) Title() string {
	if s.assetName != "" {
		return fmt.Sprintf("Interlock · %s", s.assetName)
	}
	return "Asset interlock"
}

// WantsRawInput claims every key on the reason form and on the confirm, so the
// typed box and the confirm's own keys land here rather than in the root's global
// layer. The state frame stays non-raw so workspace switching and the global back
// step keep working while reading it.
func (s *AssetInterlockScreen) WantsRawInput() bool { return s.phase != interlockPhaseState }

func (s *AssetInterlockScreen) Init() tea.Cmd { return s.beginLoad() }

func (s *AssetInterlockScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *AssetInterlockScreen) load() tea.Cmd {
	s.loadSeq++
	seq := s.loadSeq
	deps, id, ctx := s.deps, s.assetID, s.ctx()
	return func() tea.Msg {
		a, err := deps.OMS.GetAsset(ctx, id)
		return assetInterlockLoadedMsg{asset: a, err: err, seq: seq}
	}
}

func (s *AssetInterlockScreen) beginLoad() tea.Cmd {
	s.loading = true
	s.loadErr = ""
	return s.load()
}

// stateKnown is the one predicate behind "may this screen say what the machine's
// state IS". Every state row and every action gate reads it.
//
// COULD NOT TELL AND NOT LOCKED ARE DIFFERENT FACTS, and on this screen the
// difference is whether somebody starts a machine — so a screen that has never
// read the asset, or whose last read FAILED, says so and offers no action rather
// than showing a blank as an answer.
//
// A READ IN FLIGHT IS NOT THE SAME AS NOT KNOWING, which is why `loading` is
// deliberately NOT part of this. It was, and that was wrong in a way an operator
// would have felt: pressing `r` blanked all three state rows to "not known" and
// took every action off the bar for the length of the request, so `r` then `l`
// answered "the asset's state could not be read" about a state that had been read
// perfectly well a moment earlier. Keeping the last-known state while a refresh is
// out is the picker rule — a refresh must not destroy what the operator was
// looking at — and the working line on the status row is what says a read is
// outstanding. A read that FAILS sets loadErr and does withdraw everything, which
// is the case this exists for.
func (s *AssetInterlockScreen) stateKnown() bool {
	return s.asset != nil && s.loadErr == ""
}

func (s *AssetInterlockScreen) isLocked() bool { return s.stateKnown() && s.asset.IsLocked }
func (s *AssetInterlockScreen) isActive() bool { return s.stateKnown() && s.asset.IsActive }

func (s *AssetInterlockScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.setSize(m)
		return s, nil

	case assetInterlockLoadedMsg:
		if m.seq != s.loadSeq {
			return s, nil
		}
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
			return s, Status("load asset failed: "+m.err.Error(), StatusError)
		}
		s.loadErr = ""
		s.asset = m.asset
		if m.asset != nil && m.asset.Name != "" {
			s.assetName = m.asset.Name
		}
		return s, nil

	case assetInterlockWrittenMsg:
		return s.applyWrite(m)

	case tea.KeyMsg:
		switch s.phase {
		case interlockPhaseReason:
			return s.updateReason(m)
		case interlockPhaseConfirm:
			return s.updateConfirm(m)
		default:
			return s.updateState(m)
		}
	}

	// Anything else (a cursor blink) goes to the box if it holds the caret.
	if s.phase == interlockPhaseReason {
		var cmd tea.Cmd
		s.reason, cmd = s.reason.Update(msg)
		return s, cmd
	}
	return s, nil
}

// A NOTE ON WHAT IS *NOT* HERE: there is no reseat of an open confirm.
//
// po_edit.go's reseatLineIndexes and wo_tools_loto.go's reseatLotoConfirm exist
// because on those screens a reload really can land under an open confirm — every
// line action fires one. Here nothing does: `r` is bound only on the state frame,
// and applyWrite sets the phase back to the state frame BEFORE it fires its
// refusal re-read, so a loaded reply never arrives with a confirm up. A reseat
// written anyway would be a guard for a state no key reaches, and the only way to
// test it would be to fake the message in — a check that can pass over the defect
// it names.
//
// WHAT PROTECTS THE STALE CONFIRM INSTEAD IS THE SERVER. ScanTTY has no push, so
// an operator who opens the unlock confirm and stands there while somebody else
// clears the lockout is holding a frame this program cannot know is stale. Ctrl-X
// then sends the unlock, OMS answers 400 "Asset is not locked", and that sentence
// reaches the status row verbatim — which is the honest outcome rather than a
// local guess, and is what TestInterlock_AStaleConfirmRelaysTheServersRefusal
// drives.

// actionApplies is the ONE predicate behind whether an action is on offer. The
// bar reads it to decide whether to name the key and updateState reads it to decide
// whether the key acts, so the legend and the key cannot come apart.
//
// It is about STATE and never about permission: see this file's header on why
// can_unlock / can_enable are shown and never gated on.
//
// A WRITE IN FLIGHT TAKES ALL FOUR OFF THE BAR, and that was a real defect until
// the key-space sweep reported it: openAction has always declined while saving,
// and the bar went on naming l / u / d / e — a legend advertising four keys that
// could only refuse, which is the honesty rule broken in the direction an operator
// cannot see to complain about. It is NOT a dead end, which is the other half of
// that rule: Esc still leaves, r still refreshes, and the write will answer on its
// own. The arm keeps its own saving check ahead of this one so the DECLINE can say
// which of the two reasons it is ("a write is already out" rather than "does not
// apply"), the shape removalOffered uses on a purchase-order line.
func (s *AssetInterlockScreen) actionApplies(a interlockAction) bool {
	if s.saving || !s.stateKnown() {
		return false
	}
	switch a {
	case interlockLock:
		// Always, even when already locked: lock STACKS and a second lockout is
		// a real operation.
		return true
	case interlockUnlock:
		return s.asset.IsLocked
	case interlockDisable:
		return s.asset.IsActive
	case interlockEnable:
		return !s.asset.IsActive
	}
	return false
}

func (s *AssetInterlockScreen) setNote(level StatusLevel, msg string) {
	s.note, s.noteLevel = msg, level
}

// ---------------------------------------------------------------------------
// The state frame — what the machine is, and the keys that change it
// ---------------------------------------------------------------------------

func (s *AssetInterlockScreen) updateState(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch key := m.String(); key {
	case "esc":
		return s, SwitchTo(WSAssets, NewAssetDetailScreen(s.deps, s.assetID))
	case "r":
		s.setNote(StatusInfo, "")
		s.errMsg = ""
		return s, s.beginLoad()
	case "l", "u", "d", "e":
		return s, s.openAction(interlockKeyAction(key), key)
	case "up", "down", "pgup", "pgdown", "home", "end":
		if !s.frameDrawn(len(s.stateHeader()), s.stateBar()) {
			// Refused pane: the sheet is not drawn, so moving the offset would
			// move the operator's place invisibly. An arm whose whole product is
			// a POSITION says nothing rather than declining out loud.
			return s, nil
		}
		if !s.stateScrolls() {
			s.setNote(StatusInfo, key+" moves nothing — the whole sheet is on the pane.")
			return s, nil
		}
		s.scroll = jdeScrollStep(key, s.scroll, s.stateBody().Len(),
			s.scrollRows(len(s.stateHeader()), s.stateBar()))
	default:
		// Every other key ANSWERS. This frame holds no cursor and no caret, so a
		// silent return redraws a pane that is a pure function of unchanged
		// state — byte for byte identical, which reads as a wedged program.
		s.setNote(StatusInfo, key+" does nothing here — the bar names the keys that do.")
	}
	return s, nil
}

// interlockKeyAction maps the four letters to their actions. One table, read by
// the handler and by nothing else — the BAR builds its own labels from the
// actions, so a letter cannot be named for one action and act as another.
func interlockKeyAction(key string) interlockAction {
	switch key {
	case "l":
		return interlockLock
	case "u":
		return interlockUnlock
	case "d":
		return interlockDisable
	case "e":
		return interlockEnable
	}
	return interlockNone
}

// openAction is the guarded entry to every one of the four. It DECLINES OUT LOUD
// where the action does not apply, because these four letters are the whole
// vocabulary of the screen and a silent refusal on a frame with no cursor and no
// caret redraws an identical pane.
func (s *AssetInterlockScreen) openAction(a interlockAction, key string) tea.Cmd {
	if s.saving {
		s.setNote(StatusWarn, key+" declined: a write is already out. "+
			"Wait for it to answer — the state it comes back with is what matters.")
		return nil
	}
	if !s.stateKnown() {
		s.setNote(StatusWarn, key+" declined: the asset's state could not be read, and "+
			"nothing here acts on a guess. Press r to try again.")
		return nil
	}
	if !s.actionApplies(a) {
		s.setNote(StatusInfo, key+" does not apply: "+s.stateSentence())
		return nil
	}
	s.pending = a
	s.confirmScroll = 0
	s.errMsg = ""
	s.setNote(StatusInfo, "")
	if a == interlockLock {
		// The reason is the server's requirement and the record's whole value,
		// so locking asks for it BEFORE the confirm, and the confirm then repeats
		// it back.
		s.phase = interlockPhaseReason
		s.reason.Focus()
		return textinput.Blink
	}
	s.phase = interlockPhaseConfirm
	return nil
}

// stateSentence is the one wording of "what the machine is right now", used by
// every decline and by the post-write report so the operator reads the same
// phrasing wherever the answer reaches them.
func (s *AssetInterlockScreen) stateSentence() string {
	if !s.stateKnown() {
		return "the state could not be read."
	}
	lock := "not locked"
	if s.asset.IsLocked {
		lock = "LOCKED"
	}
	rec := "record active"
	if !s.asset.IsActive {
		rec = "record disabled"
	}
	return lock + ", " + rec + "."
}

// interlockVerb names an action in the operator's words, for a sentence rather
// than for a bar label.
func interlockVerb(a interlockAction) string {
	switch a {
	case interlockLock:
		return "locking"
	case interlockUnlock:
		return "unlocking"
	case interlockDisable:
		return "disabling"
	case interlockEnable:
		return "enabling"
	}
	return "that"
}

// stateHeader pins what the operator came to find out.
//
// THE INTERLOCK ROW IS THE ESSENTIAL ONE, and that is the requirement rather
// than a preference: "an operator must never be unsure whether the machine they
// are standing at is locked", so wherever this frame is drawn at all that row is
// on the pane. jdeFitHeader may keep only ONE essential row (jdeMinBudget), and
// this is the one — the asset's NAME is in the screen title and on the sheet
// below, and a name without a state is the row that would leave the question open.
func (s *AssetInterlockScreen) stateHeader() jdeHeader {
	h := jdeHeader(nil).add(jdeHeadDecorative, StyleJDEHeading.Render("Interlock"), "")

	room := jdeStripWidth(s.bodyWidth(), interlockLabelW)
	h = h.add(jdeHeadEssential, renderJDEField(jdeField{
		Label: "Interlock", Kind: jdeValue,
		Value:   fitCellIf(s.interlockValue(), room),
		Focused: true,
	}, interlockLabelW, s.bodyWidth()))
	h = h.add(jdeHeadContext, renderJDEField(jdeField{
		Label: "Record", Kind: jdeValue,
		Value: fitCellIf(s.recordValue(), room),
	}, interlockLabelW, s.bodyWidth()))
	h = h.add(jdeHeadContext, renderJDEField(jdeField{
		Label: "Asset", Kind: jdeValue,
		Value: fitCellIf(s.assetName, room),
	}, interlockLabelW, s.bodyWidth()))
	return h.add(jdeHeadDecorative, "")
}

// interlockValue is the lock axis in one row, and it names the CONSEQUENCE rather
// than only the flag: "LOCKED" alone does not tell somebody whether the machine
// will start.
func (s *AssetInterlockScreen) interlockValue() string {
	if !s.stateKnown() {
		return "not known — the asset could not be read"
	}
	if s.asset.IsLocked {
		return "LOCKED — the machine is denied"
	}
	return "not locked — the machine may be used"
}

// recordValue is the is_active axis, and it says what disabling actually buys —
// which is the half an operator gets wrong.
func (s *AssetInterlockScreen) recordValue() string {
	if !s.stateKnown() {
		return "not known"
	}
	if s.asset.IsActive {
		return "active"
	}
	return "disabled — hidden from lists, NOT stopped"
}

// stateBody is the scrolled sheet: who locked it and why, the whole id, the
// server's own read of the caller's standing, and the distinction this screen
// exists to keep straight.
//
// It owns no navigable row — there is nothing here to type into or pick — so the
// window over it is positioned by an OFFSET and the bar names the scroll keys
// exactly where the layer says they move.
func (s *AssetInterlockScreen) stateBody() *jdeLines {
	body := &jdeLines{}
	width := s.bodyWidth()
	room := jdeStripWidth(width, interlockLabelW)
	add := func(text string) {
		for _, line := range jdeCaveatLines(text, width) {
			body.Add(line)
		}
	}
	field := func(label, value string) {
		body.Add(renderJDEField(jdeField{
			Label: label, Kind: jdeValue, Value: fitCellIf(value, room),
		}, interlockLabelW, width))
	}

	if s.loadErr != "" {
		add("The asset could not be read, so nothing below is current and no key here " +
			"acts on it. Press r to try again.")
		body.Add("")
	}

	// THE LOCKOUT, which is WHAT an unlock would clear. Shown on the sheet as
	// well as on the unlock confirm, so the operator has read it before they
	// ever press u.
	if info := s.lockout(); info != nil {
		if info.Reason != "" {
			add("Locked because: " + info.Reason)
		} else {
			add("Locked, with no reason recorded on the lockout.")
		}
		if by := info.LockedBy; by != "" {
			field("Locked by", by)
		}
		if at := interlockStamp(info.LockedAt); at != "" {
			field("Locked at", at)
		}
		if lvl := info.LockoutLevel; lvl != "" {
			field("Level", lvl)
		}
		add(interlockStackNote)
		body.Add("")
	}

	// THE ID, WHOLE — and NOT through a labelled row, which is why it is the one
	// value on this sheet drawn as prose.
	//
	// A truncated UUID is not an identifier, and this is the screen somebody quotes
	// from when they phone about a machine they cannot start. A jdeValue row leaves
	// its value bodyWidth − (indent + label + leader) cells, which at the 51-cell
	// pane an 80-column terminal gives is 33 against a UUID's 36 — so a labelled
	// row CLIPPED it, marked, on every pane this program is modelled on. Drawn as a
	// folded line it has the whole pane, and pickerWrap BREAKS a token too wide for
	// one line rather than truncating it (pickerWords), so the digits survive at any
	// width instead of being cut at a narrow one.
	for _, line := range pickerWrap("Id "+s.assetID, width) {
		body.Add(jdeIndent + StyleMuted.Render(line))
	}
	if s.asset != nil {
		if tag := s.asset.AssetTag; tag != "" {
			field("Tag", tag)
		}
		if loc := s.asset.LocationName; loc != "" {
			field("Location", loc)
		}
	}
	body.Add("")

	// THE DISTINCTION. Last on the sheet but stated in the header row and in
	// every confirm too, because a sheet's tail is what a short pane drops.
	add(interlockAxesNote)

	// THE SERVER'S OWN READ OF THE CALLER, as a FACT and never as a gate.
	if s.stateKnown() {
		body.Add("")
		add(s.standingNote())
	}
	return body
}

// interlockStackNote and interlockAxesNote are constants so the tests assert the
// wording the screen really draws.
const (
	interlockStackNote = "Lockouts STACK, and one unlock clears one of them — the asset " +
		"payload names this one and cannot say how many there are. So an unlock that " +
		"succeeds may leave the machine locked, and this screen reports the state the " +
		"server comes back with rather than assuming."

	interlockAxesNote = "Lock and disable are different things. LOCK is the interlock: " +
		"ForgeKey denies the machine while a lockout is active. DISABLE only hides the " +
		"asset from lists — a disabled machine with a valid authorization and no lockout " +
		"can still be used. To stop a machine being run, LOCK it."
)

// standingNote reports can_unlock / can_enable as the server's OWN READ of this
// caller, and says in as many words that it is not what the endpoints enforce.
//
// It is worded as a prediction rather than a rule because that is what it is:
// measured on a report_only asset both flags are false and lock and disable are
// accepted anyway. Showing it lets an operator expect a refusal; gating on it
// would manufacture one.
func (s *AssetInterlockScreen) standingNote() string {
	says := func(ok bool) string {
		if ok {
			return "yes"
		}
		return "no"
	}
	note := fmt.Sprintf(
		"The server's own read of your standing: may lock or unlock — %s; may enable — %s. "+
			"It is a PREDICTION and not a gate: every key here sends its write and shows "+
			"you the answer in the server's words.",
		says(s.asset.CanUnlock), says(s.asset.CanEnable))
	if s.asset.ReportOnly {
		note += " This asset is report-only, which is why the web hides these buttons " +
			"entirely — the endpoints themselves still accept a lock."
	}
	return note
}

func (s *AssetInterlockScreen) lockout() *omsapi.AssetLockout {
	if !s.stateKnown() || !s.asset.IsLocked {
		return nil
	}
	return s.asset.LockoutInfo
}

// interlockStamp renders the lockout's timestamp, which arrives as the ISO string
// the serializer wrote rather than as a time.Time (AssetLockout.LockedAt is a
// string field, because the serializer builds that dict by hand).
//
// An unparseable value is shown AS IT ARRIVED rather than dropped: a timestamp
// this side cannot read is still the server's answer, and hiding it would lose
// the one fact saying how long the machine has been down.
func interlockStamp(raw string) string {
	if raw == "" {
		return ""
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.Local().Format("2006-01-02 15:04")
	}
	return raw
}

func (s *AssetInterlockScreen) stateScrolls() bool {
	return s.bodyScrollsForBar(s.stateBody(), len(s.stateHeader()),
		s.stateBarItems(true))
}

func (s *AssetInterlockScreen) stateBar() []actionBarItem {
	return s.stateBarItems(s.stateScrolls())
}

// stateBarItems names exactly the keys that act, which on this screen means
// asking actionApplies for each of the four rather than listing them.
//
// The LOCK label changes with the state — `Lock` on a machine that is not locked,
// `Add lock` on one that is — because pressing `l` on an already-locked asset
// stacks a second lockout, and a bar reading `Lock` there would name an operation
// the operator did not mean.
func (s *AssetInterlockScreen) stateBarItems(scroll bool) []actionBarItem {
	var items []actionBarItem
	if s.actionApplies(interlockLock) {
		label := "Lock"
		if s.asset.IsLocked {
			label = "Add lock"
		}
		items = append(items, actionBarItem{"l", label})
	}
	if s.actionApplies(interlockUnlock) {
		items = append(items, actionBarItem{"u", "Unlock"})
	}
	if s.actionApplies(interlockDisable) {
		items = append(items, actionBarItem{"d", "Disable"})
	}
	if s.actionApplies(interlockEnable) {
		items = append(items, actionBarItem{"e", "Enable"})
	}
	items = append(items, actionBarItem{"Esc", "Back"})
	if scroll {
		items = append(items,
			actionBarItem{"UP/DN", "Scroll"},
			actionBarItem{"PgUp/PgDn", "Page"},
			actionBarItem{"Home/End", "Top/End"})
	}
	return append(items, actionBarItem{"r", "Refresh"})
}

func (s *AssetInterlockScreen) stateStatus() string {
	if s.loading {
		// The working line WINS over the answer to the last keypress, which is the
		// layer's own precedence (statusRow's saving branch). The rows below go on
		// showing the last-known state; this row is what says it is being checked.
		return s.statusRow(true, "Reading "+interlockSubject(s.assetName)+"…", "")
	}
	if s.errMsg != "" {
		// A WRITE's failure is the fact on this row; the answer to a keypress
		// gives way to it, because the refusal is what the operator must read
		// and a note about a key is something they can reproduce by pressing it.
		return s.statusRow(false, "", s.errMsg)
	}
	if s.loadErr != "" {
		return s.statusRow(false, "", "could not read the asset: "+s.loadErr)
	}
	return s.statusAnswer(s.noteLevel, s.note)
}

// interlockSubject is the asset's name for a working line, or a stand-in while the
// screen does not have one yet.
//
// It BOUNDS NOTHING and must not claim to: the bound is fitStatus's, applied to the
// whole assembled row. What this composition decides is the ORDER, which is the
// half that matters — "Reading " leads, so what a clip takes is the NAME's tail
// rather than the words saying what is happening. A subject ahead of the fixed
// words inverts rule 6 inside one sentence, which is the defect the asset picker's
// working line had.
func interlockSubject(name string) string {
	if name == "" {
		return "the asset"
	}
	return name
}

func (s *AssetInterlockScreen) viewState() string {
	frame, offset := s.frameScrolled(s.stateHeader(), s.stateBody(), s.scroll,
		s.stateStatus(), s.stateBar())
	// Stored back so the offset this screen holds is the one that was DRAWN.
	s.scroll = offset
	return frame
}

// ---------------------------------------------------------------------------
// The reason form — lock's one field
// ---------------------------------------------------------------------------

var interlockReasonBar = []actionBarItem{
	{"Enter", "Check it"}, {"Esc", "Back"},
}

// updateReason drives lock's reason box. Enter does NOT write — it opens the
// confirm, which is where locking's consequence is stated. A blank is declined
// here rather than sent, because the server refuses it and a round trip to be
// told so is a round trip that loses the operator's place.
func (s *AssetInterlockScreen) updateReason(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		s.phase = interlockPhaseState
		s.pending = interlockNone
		s.reason.Blur()
		s.setNote(StatusInfo, "nothing locked — the reason was not sent.")
		return s, nil
	case "enter":
		if strings.TrimSpace(s.reason.Value()) == "" {
			s.errMsg = "nothing locked: a reason is required, and the server refuses a blank one."
			return s, nil
		}
		s.errMsg = ""
		s.phase = interlockPhaseConfirm
		s.confirmScroll = 0
		s.reason.Blur()
		return s, nil
	}
	var cmd tea.Cmd
	s.reason, cmd = s.reason.Update(m)
	return s, cmd
}

func (s *AssetInterlockScreen) reasonFields() []jdeField {
	return []jdeField{{
		Label: "Reason", Kind: jdeText, Input: &s.reason, Focused: true,
		Hint: "required — what is wrong, and what must happen before it runs",
	}}
}

// reasonHeader pins what is about to be locked, and the caveat that says what
// locking does. The ASSET row is the essential one here: the operator is typing a
// sentence about a specific machine and must be able to see which.
func (s *AssetInterlockScreen) reasonHeader() jdeHeader {
	h := jdeHeader(nil).add(jdeHeadDecorative, StyleStatusWarn.Render("Lock this machine"), "")
	h = h.add(jdeHeadEssential, renderJDEField(jdeField{
		Label: "Asset", Kind: jdeValue,
		Value:   fitCellIf(s.assetName, jdeStripWidth(s.bodyWidth(), interlockLabelW)),
		Focused: true,
	}, interlockLabelW, s.bodyWidth()))
	h = h.addBlock(jdeHeadContext, jdeCaveatLines(interlockReasonCaveat, s.bodyWidth()))
	return h.add(jdeHeadDecorative, "")
}

const interlockReasonCaveat = "This sentence is stored on the lockout and is what the " +
	"next person who walks up to the machine reads. Enter checks it before anything is sent."

func (s *AssetInterlockScreen) viewReason() string {
	fields := s.reasonFields()
	body := &jdeLines{}
	body.AddFittedFields(fields, jdeLabelWidth(fields), s.bodyWidth(), 0)
	return s.frameWrapped(s.reasonHeader(), body, 0,
		s.statusRow(false, "", s.errMsg), interlockReasonBar)
}

// ---------------------------------------------------------------------------
// The confirm — one frame, four actions, and what each one really does
// ---------------------------------------------------------------------------

func (s *AssetInterlockScreen) updateConfirm(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch key := m.String(); key {
	case "ctrl+x":
		if !s.confirmWrites() {
			return s, nil
		}
		s.setNote(StatusInfo, "")
		s.errMsg = ""
		return s, s.write(s.pending)
	case "esc":
		if s.saving {
			// Esc still LEAVES while a write is out — a frame with no way off it
			// is the worse defect — but it goes back to the STATE frame, where
			// the reply will land and be reported.
			s.phase = interlockPhaseState
			s.setNote(StatusWarn, "the write is still out; its answer will land here.")
			return s, nil
		}
		if s.pending == interlockLock {
			// Back to the operator's own sentence rather than throwing it away:
			// discarding typed input in answer to "are you sure" is the defect
			// the meter confirm's Esc exists to avoid.
			s.phase = interlockPhaseReason
			s.reason.Focus()
			return s, textinput.Blink
		}
		s.phase = interlockPhaseState
		s.pending = interlockNone
		s.setNote(StatusInfo, "nothing written.")
		return s, nil
	case "up", "down", "pgup", "pgdown", "home", "end":
		if !s.frameDrawn(len(s.confirmHeader()), s.confirmBar()) {
			return s, nil
		}
		if !s.confirmScrolls() {
			s.setNote(StatusInfo, key+" moves nothing — the whole warning is on the pane.")
			return s, nil
		}
		s.confirmScroll = jdeScrollStep(key, s.confirmScroll, s.confirmBody().Len(),
			s.scrollRows(len(s.confirmHeader()), s.confirmBar()))
	default:
		s.setNote(StatusInfo, key+" does nothing here — this frame only sends or goes back.")
	}
	return s, nil
}

// confirmWrites is the ONE expression behind "may Ctrl-X send". The bar reads it
// to decide whether to name the key and the arm reads it to decide whether to
// act, so the legend and the key cannot come apart.
//
// It re-asks actionApplies rather than trusting the phase, because the state is
// re-read on every refresh and a confirm can outlive the state it was opened
// against (reseatConfirm closes it, and this is the belt to that braces).
func (s *AssetInterlockScreen) confirmWrites() bool {
	if s.saving || s.pending == interlockNone || !s.actionApplies(s.pending) {
		return false
	}
	if s.pending == interlockLock {
		return strings.TrimSpace(s.reason.Value()) != ""
	}
	return true
}

// interlockHeadline is the one row that says what Ctrl-X is about to do,
// ASSEMBLED AND THEN BOUNDED — a bound applied to one part of a row that is
// afterwards added to is not a bound (the assetScopeRows rule, and the defect
// removalHeadline had on the row naming what an irreversible key would destroy).
//
// THE VERB NEVER GIVES AND THE NAME DOES. What the key does is the question being
// asked; which machine it is asked about is on three other surfaces (the screen
// title, the state sheet, the frame this was opened from), so the name abbreviates
// and then goes, and the verb is the last thing on the row.
func (s *AssetInterlockScreen) interlockHeadline() string {
	lead := interlockConfirmLead(s.pending, s.isLocked())
	room := s.bodyWidth() - len(jdeIndent)
	if room <= 0 {
		return lead + " " + s.assetName
	}
	if left := room - visibleCells(lead+" "); left >= interlockNameFloor {
		return lead + " " + fitCell(s.assetName, left)
	}
	// No room for a readable name beside it. The verb stays whole; where even
	// that will not fit, the pane is narrower than any wording of this row and
	// the clip is marked.
	if visibleCells(lead) <= room {
		return lead
	}
	return fitCell(lead, room)
}

// interlockNameFloor is the fewest cells worth spending on an asset name beside
// the verb. Below it the name is dropped instead: a machine name cut to three
// characters separates nothing, and the verb is what the row exists to say.
const interlockNameFloor = 8

// interlockConfirmLead names the action in the imperative, and it is the LOCK
// case that has to distinguish itself: pressing l on an already-locked asset adds
// a lockout rather than creating the first one.
func interlockConfirmLead(a interlockAction, locked bool) string {
	switch a {
	case interlockLock:
		if locked {
			return "Add a second lockout to"
		}
		return "Lock"
	case interlockUnlock:
		return "Unlock"
	case interlockDisable:
		return "Disable"
	case interlockEnable:
		return "Enable"
	}
	return "Act on"
}

// confirmHeader pins WHAT Ctrl-X will do. The headline is ESSENTIAL, so wherever
// this frame is drawn at all it names the action and (where the pane allows) the
// machine.
func (s *AssetInterlockScreen) confirmHeader() jdeHeader {
	h := jdeHeader(nil).add(jdeHeadDecorative,
		StyleStatusWarn.Render(interlockConfirmTitle(s.pending)), "")
	h = h.add(jdeHeadEssential, jdeIndent+s.interlockHeadline())
	return h.add(jdeHeadDecorative, "")
}

func interlockConfirmTitle(a interlockAction) string {
	switch a {
	case interlockLock:
		return "Stop this machine being used"
	case interlockUnlock:
		return "Let this machine be used again"
	case interlockDisable:
		return "Hide this asset from lists"
	case interlockEnable:
		return "Show this asset in lists again"
	}
	return "Confirm"
}

// confirmBody says what the write DOES, in an order that puts the consequence
// first and the mechanics after.
//
// It owns no navigable row, so the window over it is positioned by an OFFSET —
// and it is said ONCE, so the lines the bar is measured against are the lines the
// frame draws.
func (s *AssetInterlockScreen) confirmBody() *jdeLines {
	body := &jdeLines{}
	add := func(text string) {
		if text == "" {
			return
		}
		for _, line := range jdeCaveatLines(text, s.bodyWidth()) {
			body.Add(line)
		}
	}
	switch s.pending {
	case interlockLock:
		if s.isLocked() {
			add("This asset is ALREADY LOCKED. Ctrl-X adds a SECOND lockout beside the " +
				"first, which is how lockout/tagout is meant to work — yours holds even " +
				"when somebody clears theirs. Every lockout has to be cleared before the " +
				"machine runs again.")
		} else {
			add("Ctrl-X records a lockout and puts the asset in locked-out mode. ForgeKey " +
				"then DENIES the machine to everyone, and its indicators show locked out.")
		}
		add("It does not cut power to a machine that is already running — it denies the " +
			"next attempt to start one.")
		add("Your reason: " + strings.TrimSpace(s.reason.Value()))
		add("It does NOT disable the asset. " + interlockAxesNote)
	case interlockUnlock:
		// WHAT IS BEING RE-ENABLED, first and in the locker's own words. This is
		// the dangerous direction, and the operator reads why the machine was
		// stopped before they let it start.
		if info := s.lockout(); info != nil && info.Reason != "" {
			add("You are clearing this lockout: " + info.Reason)
		} else {
			add("You are clearing a lockout with no reason recorded on it.")
		}
		if info := s.lockout(); info != nil {
			if who := interlockLockedByPhrase(info); who != "" {
				add(who)
			}
		}
		add("Ctrl-X clears ONE lockout — the first you are entitled to clear. If another " +
			"remains, the machine STAYS DENIED and this screen will say so.")
		add("It does not enable a disabled asset, and it does not resolve any " +
			"out-of-service record or work order.")
	case interlockDisable:
		add("Ctrl-X hides this asset from most views. IT DOES NOT STOP THE MACHINE: " +
			"ForgeKey never reads this flag, so a disabled machine with a valid " +
			"authorization and no lockout can still be used.")
		if !s.isLocked() {
			add("This asset is NOT LOCKED. If the machine must not be run, press Esc and " +
				"lock it instead — or as well.")
		}
	case interlockEnable:
		add("Ctrl-X puts this asset back in the lists it was hidden from.")
		if s.isLocked() {
			add("It stays LOCKED: enabling does not clear a lockout, so the machine is " +
				"still denied afterwards.")
		} else {
			add("The asset is not locked, so after this the machine may be used by anyone " +
				"authorized for it.")
		}
	}
	add("The server decides. Whatever it answers, this screen shows the state that came " +
		"back rather than assuming the write's own meaning.")
	return body
}

// interlockLockedByPhrase names who set the lockout and when, as prose rather
// than as a field row, because the confirm's body folds and a field row does not.
func interlockLockedByPhrase(info *omsapi.AssetLockout) string {
	var parts []string
	if info.LockedBy != "" {
		parts = append(parts, "set by "+info.LockedBy)
	}
	if at := interlockStamp(info.LockedAt); at != "" {
		parts = append(parts, "at "+at)
	}
	if info.LockoutLevel != "" {
		parts = append(parts, "at level "+info.LockoutLevel)
	}
	if len(parts) == 0 {
		return ""
	}
	return "It was " + strings.Join(parts, ", ") + "."
}

func (s *AssetInterlockScreen) confirmScrolls() bool {
	return s.bodyScrollsForBar(s.confirmBody(), len(s.confirmHeader()),
		s.confirmBarItems(s.confirmWrites(), true))
}

func (s *AssetInterlockScreen) confirmBar() []actionBarItem {
	return s.confirmBarItems(s.confirmWrites(), s.confirmScrolls())
}

func (s *AssetInterlockScreen) confirmBarItems(writes, scroll bool) []actionBarItem {
	items := []actionBarItem{{"Ctrl-X", interlockConfirmVerb(s.pending, s.isLocked())}, {"Esc", "Back"}}
	if !writes {
		// Esc still leaves, so the bar keeps one key and drops the one that
		// would not act.
		items = []actionBarItem{{"Esc", "Back"}}
	}
	if scroll {
		items = append(items,
			actionBarItem{"UP/DN", "Scroll"},
			actionBarItem{"PgUp/PgDn", "Page"},
			actionBarItem{"Home/End", "Top/End"})
	}
	return items
}

func interlockConfirmVerb(a interlockAction, locked bool) string {
	switch a {
	case interlockLock:
		if locked {
			return "Add lock"
		}
		return "Lock it"
	case interlockUnlock:
		return "Unlock it"
	case interlockDisable:
		return "Disable it"
	case interlockEnable:
		return "Enable it"
	}
	return "Send"
}

func (s *AssetInterlockScreen) confirmStatus() string {
	if s.saving {
		verb := interlockWorkingLine(s.pending)
		switch room := s.bodyWidth(); {
		case s.note == "":
		case room > 0:
			verb = poLeadOnto(s.note, verb, room)
		default:
			verb = s.note + poLeadJoint + verb
		}
		return s.statusRow(true, verb, "")
	}
	if s.errMsg != "" {
		return s.statusRow(false, "", s.errMsg)
	}
	return s.statusAnswer(s.noteLevel, s.note)
}

// interlockWorkingLine names the work and its subject, per the working-line rule:
// "Locking the machine…", not "Saving…".
func interlockWorkingLine(a interlockAction) string {
	switch a {
	case interlockLock:
		return "Recording the lockout…"
	case interlockUnlock:
		return "Clearing the lockout…"
	case interlockDisable:
		return "Hiding the asset…"
	case interlockEnable:
		return "Restoring the asset…"
	}
	return "Working…"
}

func (s *AssetInterlockScreen) viewConfirm() string {
	frame, offset := s.frameScrolled(s.confirmHeader(), s.confirmBody(), s.confirmScroll,
		s.confirmStatus(), s.confirmBar())
	s.confirmScroll = offset
	return frame
}

// ---------------------------------------------------------------------------
// The writes, and reporting the state that came back
// ---------------------------------------------------------------------------

func (s *AssetInterlockScreen) write(a interlockAction) tea.Cmd {
	s.saving = true
	deps, id, ctx := s.deps, s.assetID, s.ctx()
	reason := strings.TrimSpace(s.reason.Value())
	return func() tea.Msg {
		var (
			asset *omsapi.Asset
			err   error
		)
		switch a {
		case interlockLock:
			asset, err = deps.OMS.LockAsset(ctx, id, reason)
		case interlockUnlock:
			asset, err = deps.OMS.UnlockAsset(ctx, id)
		case interlockDisable:
			asset, err = deps.OMS.DisableAsset(ctx, id)
		case interlockEnable:
			asset, err = deps.OMS.EnableAsset(ctx, id)
		}
		return assetInterlockWrittenMsg{action: a, asset: asset, err: err}
	}
}

// applyWrite reports the state THE SERVER CAME BACK WITH, which is the whole
// reason these client methods return an asset.
//
// The reply is the full asset serializer, so it is stored as the screen's state
// rather than re-fetched: one round trip, and no window in which the pane shows a
// state older than the write that just landed.
func (s *AssetInterlockScreen) applyWrite(m assetInterlockWrittenMsg) (Screen, tea.Cmd) {
	s.saving = false
	if m.err != nil {
		s.errMsg = interlockRefusal(m.err)
		s.phase = interlockPhaseState
		s.pending = interlockNone
		s.setNote(StatusInfo, "")
		// Nothing landed, so the state on screen is still whatever the last read
		// said. Re-read anyway: a refusal is often the server telling you somebody
		// else moved the asset ("Asset is not locked" because they unlocked it),
		// and the corrected state is what the operator acts on next.
		//
		// THE RE-READ DELIBERATELY DOES NOT SET `loading`, which is why it calls
		// load() rather than beginLoad(). Setting it would blank the state rows to
		// "not known" and put a working line on the status row — displacing the
		// REFUSAL, which is the answer to the key they actually pressed and the one
		// thing they have to read. The operator did not ask for this read, so it
		// stays quiet and simply corrects the rows when it lands. A read they DID
		// ask for (`r`) goes through beginLoad and says so.
		return s, tea.Batch(Status(interlockVerb(m.action)+" refused: "+s.errMsg, StatusError), s.load())
	}
	s.errMsg = ""
	s.phase = interlockPhaseState
	s.pending = interlockNone
	s.confirmScroll = 0
	if m.asset != nil {
		s.asset = m.asset
		s.loading = false
		s.loadErr = ""
		if m.asset.Name != "" {
			s.assetName = m.asset.Name
		}
	}
	if m.action == interlockLock {
		s.reason.SetValue("")
	}
	s.reason.Blur()
	level, note := s.wroteNote(m.action)
	s.setNote(level, note)
	return s, Status(note, level)
}

// wroteNote is what the screen says after a write landed, and it is derived from
// the state that CAME BACK rather than from the action that was sent.
//
// THE UNLOCK CASE IS WHY THIS FUNCTION EXISTS. Unlock clears one lockout of a
// stack, so a success can leave the machine denied — reporting "unlocked" off the
// status code would tell somebody a machine was safe to start when the server
// still refuses it. A still-locked unlock is a WARNING that names the state.
func (s *AssetInterlockScreen) wroteNote(a interlockAction) (StatusLevel, string) {
	if s.asset == nil {
		// The write succeeded and said nothing about the state. Do not invent
		// one: name the action and send the operator back to a read.
		return StatusWarn, interlockVerb(a) + " landed, but the server sent no state back. " +
			"Press r to read it."
	}
	switch a {
	case interlockLock:
		if !s.asset.IsLocked {
			return StatusWarn, "the lockout was recorded but the asset comes back NOT LOCKED. " +
				"Press r and check before trusting the machine is stopped."
		}
		return StatusOK, s.withRecordTail("LOCKED — the machine is denied.")
	case interlockUnlock:
		if s.asset.IsLocked {
			return StatusWarn, "one lockout cleared and the machine is STILL LOCKED — " +
				"another lockout remains. Press u again to clear the next one."
		}
		return StatusOK, s.withRecordTail("UNLOCKED — the machine may be used again.")
	case interlockDisable:
		if s.asset.IsLocked {
			return StatusOK, "disabled and hidden from lists. The machine is also LOCKED, " +
				"so it is denied."
		}
		return StatusWarn, "disabled and hidden from lists — but NOT stopped: the machine " +
			"can still be used. Press l to lock it."
	case interlockEnable:
		if s.asset.IsLocked {
			return StatusOK, "enabled and back in the lists — still LOCKED, so the machine " +
				"is denied until it is unlocked."
		}
		return StatusOK, "enabled and back in the lists. Not locked, so the machine may be used."
	}
	return StatusInfo, s.stateSentence()
}

// withRecordTail adds the OTHER axis to a lock-axis report, but only when it is
// the surprising value. A locked-and-active machine is the ordinary case, and
// saying "record active" every time would push the part that matters off a row
// fitStatus gives one line.
//
// It JOINS rather than being concatenated by each caller, because concatenating
// left a trailing space on every ordinary report — visible in the lab drive
// against a real backend as `"LOCKED — the machine is denied. "`.
func (s *AssetInterlockScreen) withRecordTail(head string) string {
	if s.asset != nil && !s.asset.IsActive {
		return head + " The record is also disabled."
	}
	return head
}

// interlockRefusal recovers the server's own sentence, and it needs BOTH
// recognisers because these four endpoints answer in two shapes.
//
// The validation refusals ("reason is required", "Asset is not locked") are OMS's
// standardized envelope, which parseError understands, so AsLineEntryError finds
// them on APIError.Code. The permission refusals ("You do not have permission to
// unlock this asset") are hand-built `Response({"error": "<prose>"})` bodies in
// the view, which defeat parseError entirely, so the whole payload sits in
// .Message and AsReceivingRefusal is the narrow recogniser for it.
//
// The two cannot be confused: a hand-built body never yields a Code, so it can
// only reach the second arm. Anything else — a gateway's HTML, a DRF field map —
// keeps the shape it arrived in rather than being dressed as a sentence the server
// never said.
func interlockRefusal(err error) string {
	if err == nil {
		return ""
	}
	if entry, ok := omsapi.AsLineEntryError(err); ok && strings.TrimSpace(entry.Message) != "" {
		return entry.Message
	}
	if prose, ok := omsapi.AsReceivingRefusal(err); ok {
		return prose
	}
	if prose, ok := omsapi.AsDetailRefusal(err); ok {
		return prose
	}
	return err.Error()
}

func (s *AssetInterlockScreen) View() string {
	switch s.phase {
	case interlockPhaseReason:
		return s.viewReason()
	case interlockPhaseConfirm:
		return s.viewConfirm()
	}
	return s.viewState()
}
