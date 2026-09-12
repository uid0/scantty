package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

type ReorderFormScreen struct {
	deps      Deps
	item      *omsapi.Item
	suppliers []omsapi.ItemSupplier

	inputs    []textinput.Model
	labels    []string
	focused   int
	pending   bool
	resultMsg string
	resultLvl StatusLevel
}

type reorderSubmittedMsg struct {
	result *omsapi.ReorderRequestCreated
	err    error
}

const (
	reorderFieldQuantity = iota
	reorderFieldRequester
	reorderFieldPriority
	reorderFieldNotes
)

func NewReorderFormScreen(deps Deps, item *omsapi.Item, suppliers []omsapi.ItemSupplier) *ReorderFormScreen {
	s := &ReorderFormScreen{
		deps:      deps,
		item:      item,
		suppliers: suppliers,
		labels:    []string{"Quantity", "Requested by", "Priority", "Notes"},
	}
	for i, label := range s.labels {
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = 200
		ti.Placeholder = placeholderFor(label)
		if i == 0 {
			ti.Focus()
		}
		s.inputs = append(s.inputs, ti)
	}
	if def := defaultPackQty(suppliers); def > 0 {
		s.inputs[reorderFieldQuantity].SetValue(strconv.Itoa(def))
	}
	s.inputs[reorderFieldPriority].SetValue("normal")
	return s
}

func placeholderFor(label string) string {
	switch label {
	case "Quantity":
		return "integer"
	case "Priority":
		return "low | normal | high | urgent"
	case "Requested by":
		return "your name"
	case "Notes":
		return "optional"
	}
	return ""
}

func defaultPackQty(suppliers []omsapi.ItemSupplier) int {
	for _, s := range suppliers {
		if s.IsPreferred && s.PackQuantity > 0 {
			return s.PackQuantity
		}
	}
	for _, s := range suppliers {
		if s.PackQuantity > 0 {
			return s.PackQuantity
		}
	}
	return 0
}

func (s *ReorderFormScreen) Title() string { return "Request Reorder" }

func (s *ReorderFormScreen) WantsRawInput() bool { return true }

func (s *ReorderFormScreen) Init() tea.Cmd { return textinput.Blink }

func (s *ReorderFormScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case reorderSubmittedMsg:
		s.pending = false
		if m.err != nil {
			s.resultMsg = "submit failed: " + m.err.Error()
			s.resultLvl = StatusError
			return s, Status(s.resultMsg, StatusError)
		}
		s.resultMsg, s.resultLvl = reorderOutcome(m.result)
		return s, Status(s.resultMsg, s.resultLvl)
	case tea.KeyMsg:
		switch m.Type {
		case tea.KeyTab, tea.KeyShiftTab, tea.KeyDown, tea.KeyUp:
			s.focusNext(m.Type == tea.KeyShiftTab || m.Type == tea.KeyUp)
			return s, nil
		case tea.KeyEnter:
			if s.focused < len(s.inputs)-1 {
				s.focusNext(false)
				return s, nil
			}
			return s.submit()
		case tea.KeyEsc:
			return s, SwitchTo(WSInventory, NewInventoryDetailScreen(s.deps, s.item.ID))
		}
	}
	var cmd tea.Cmd
	s.inputs[s.focused], cmd = s.inputs[s.focused].Update(msg)
	return s, cmd
}

// reorderOutcome words what the submit actually DID, which is not the same
// question as whether it succeeded.
//
// THERE ARE THREE OUTCOMES AND THIS SCREEN KEEPS THEM APART BY COLOUR AS WELL
// AS BY WORDS, because an operator reads the colour first:
//
//   - FILED — StatusOK. A row was created.
//   - ALREADY REQUESTED — StatusWarn. OMS files no second ANONYMOUS request
//     while one for the same item is still pending, so nothing was created and
//     the id echoed back is the EXISTING request's. It is emphatically NOT an
//     error: the need IS on file and nothing more is asked of the operator, so
//     StatusError here would send them to scan again — the duplicate the server
//     rule exists to prevent. Nor is it StatusOK: told "created" twice, they can
//     reasonably believe two requests exist.
//   - COULD NOT TELL — StatusError, worded by the caller off the transport
//     error. Never reaches here.
//
// StatusInfo is the fourth level and is NOT the middle one: it renders in the
// muted colour every hint on this pane already uses, so the answer to a submit
// would be drawn as though it were decoration.
//
// AN OMS THAT DOES NOT SEND THE MARKER BEHAVES EXACTLY AS BEFORE, and it does so
// by construction rather than by a branch: the key is absent, AlreadyRequested
// decodes to false, and false is "filed" — which is the truth against such a
// server, because one without the rule creates a row on every success. That is
// the tolerance this screen shipped for, since ScanTTY lands before the server
// change; omsapi.ReorderRequestCreated carries why absent and false are one
// fact here.
//
// THE LOAD-BEARING CLAIM LEADS. An 80-column terminal leaves this pane 51 cells
// and the result line neither folds nor marks its cut, so what a clip takes has
// to be the part that can be lost: "already requested" first, then the id, then
// the reason. Cut anywhere, what stands cannot be read as a request that was
// filed. %v and not %d because ID is `any` — %d would print a %!d(...) mess for
// whatever concrete type the decoder chose, while %v is correct for all of them,
// and since omsapi's jsonDecoder sets UseNumber a numeric pk arrives as a
// json.Number and keeps the server's own digits.
func reorderOutcome(res *omsapi.ReorderRequestCreated) (string, StatusLevel) {
	if res.AlreadyRequested {
		return fmt.Sprintf("already requested — #%v is still pending", res.ID), StatusWarn
	}
	return fmt.Sprintf("reorder #%v created", res.ID), StatusOK
}

func (s *ReorderFormScreen) focusNext(reverse bool) {
	s.inputs[s.focused].Blur()
	if reverse {
		s.focused--
		if s.focused < 0 {
			s.focused = len(s.inputs) - 1
		}
	} else {
		s.focused = (s.focused + 1) % len(s.inputs)
	}
	s.inputs[s.focused].Focus()
}

func (s *ReorderFormScreen) submit() (Screen, tea.Cmd) {
	qty, err := strconv.Atoi(strings.TrimSpace(s.inputs[reorderFieldQuantity].Value()))
	if err != nil || qty <= 0 {
		s.resultMsg = "quantity must be a positive integer"
		s.resultLvl = StatusError
		return s, nil
	}
	requester := strings.TrimSpace(s.inputs[reorderFieldRequester].Value())
	priority := strings.TrimSpace(s.inputs[reorderFieldPriority].Value())
	notes := strings.TrimSpace(s.inputs[reorderFieldNotes].Value())

	req := omsapi.ReorderRequestCreate{
		Item:         s.item.ID,
		Quantity:     qty,
		RequestedBy:  requester,
		Priority:     priority,
		RequestNotes: notes,
	}
	s.pending = true
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return s, func() tea.Msg {
		out, err := deps.OMS.CreateReorderRequest(ctx, req)
		return reorderSubmittedMsg{result: out, err: err}
	}
}

func (s *ReorderFormScreen) View() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Reorder %s (SKU %s, stock %d)\n\n",
		StyleTitle.Render(s.item.Name), s.item.SKU, s.item.Stock))
	if len(s.suppliers) > 0 {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("%d supplier(s) available", len(s.suppliers))))
		b.WriteString("\n\n")
	}
	for i, ti := range s.inputs {
		caret := "  "
		if i == s.focused {
			caret = "▸ "
		}
		b.WriteString(caret + StyleMuted.Render(s.labels[i]) + "\n")
		b.WriteString("    " + ti.View() + "\n\n")
	}
	if s.pending {
		b.WriteString(StyleMuted.Render("Submitting…") + "\n")
	} else if s.resultMsg != "" {
		b.WriteString(RenderStatus(s.resultMsg, s.resultLvl) + "\n")
	}
	b.WriteString("\n" + StyleMuted.Render("tab move · enter submit · esc back"))
	return b.String()
}
