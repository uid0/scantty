// PurchaseOrderAttachmentsScreen — manage the files attached to a PO.
//
// TUI counterpart to the Attachments section of the web PurchaseOrderPage
// (frontend/src/pages/PurchaseOrderPage.tsx): list the attachments, upload a
// new one (file + optional description), and delete one. Uploads read a file
// off the local filesystem via a path input — the workstation-friendly analogue
// of the browser's file picker — and POST it multipart through
// UploadPurchaseOrderAttachment. Deletion is staff-gated server-side.
//
// Phases:
//
//	poAttachPhaseList    — the attachment list; j/k move, u upload, x delete
//	                       (with a y/n confirm), r refresh, esc back.
//	poAttachPhaseUpload  — file-path + description inputs; enter uploads.
package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

type poAttachPhase int

const (
	poAttachPhaseList poAttachPhase = iota
	poAttachPhaseUpload
)

const (
	poAttachFieldPath = iota
	poAttachFieldDesc
	poAttachFieldCount
)

type PurchaseOrderAttachmentsScreen struct {
	deps           Deps
	poID           string
	poNumber       string
	attachments    []omsapi.PurchaseOrderAttachment
	loading        bool
	loadErr        string
	terminalHeight int

	phase  poAttachPhase
	cursor int
	errMsg string

	// Upload form.
	uploadInputs []textinput.Model
	uploadFocus  int
	uploading    bool

	// Delete confirm.
	confirmingDelete bool
	deleting         bool
}

type poAttachLoadedMsg struct {
	po  *omsapi.PurchaseOrder
	err error
}

type poAttachUploadedMsg struct {
	att *omsapi.PurchaseOrderAttachment
	err error
}

type poAttachDeletedMsg struct {
	err error
}

// NewPurchaseOrderAttachmentsScreen seeds from the PO already loaded on the
// detail screen and reloads by id after each change so the list stays fresh.
func NewPurchaseOrderAttachmentsScreen(deps Deps, po *omsapi.PurchaseOrder) *PurchaseOrderAttachmentsScreen {
	s := &PurchaseOrderAttachmentsScreen{deps: deps}
	if po != nil {
		s.poID = fmt.Sprintf("%v", po.ID)
		s.poNumber = po.Number
		s.attachments = po.Attachments
	}

	s.uploadInputs = make([]textinput.Model, poAttachFieldCount)
	path := textinput.New()
	path.Prompt = ""
	path.Placeholder = "/path/to/file.pdf"
	path.CharLimit = 500
	s.uploadInputs[poAttachFieldPath] = path
	desc := textinput.New()
	desc.Prompt = ""
	desc.Placeholder = "description (optional)"
	desc.CharLimit = 300
	s.uploadInputs[poAttachFieldDesc] = desc
	return s
}

func (s *PurchaseOrderAttachmentsScreen) Title() string {
	if s.poNumber != "" {
		return fmt.Sprintf("Attachments · PO %s", s.poNumber)
	}
	return "Purchase order attachments"
}

// WantsRawInput claims keys during the upload form and the delete confirm so
// the path input and y/n land here; the plain list stays non-raw so global
// hotkeys keep working while browsing.
func (s *PurchaseOrderAttachmentsScreen) WantsRawInput() bool {
	return s.phase == poAttachPhaseUpload || s.confirmingDelete
}

func (s *PurchaseOrderAttachmentsScreen) Init() tea.Cmd { return textinput.Blink }

func (s *PurchaseOrderAttachmentsScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *PurchaseOrderAttachmentsScreen) load() tea.Cmd {
	deps := s.deps
	id := s.poID
	ctx := s.ctx()
	return func() tea.Msg {
		po, err := deps.OMS.GetPurchaseOrder(ctx, id)
		return poAttachLoadedMsg{po: po, err: err}
	}
}

func (s *PurchaseOrderAttachmentsScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		return s, nil

	case poAttachLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
			return s, Status("reload attachments failed: "+m.err.Error(), StatusError)
		}
		if m.po != nil {
			s.attachments = m.po.Attachments
			s.poNumber = m.po.Number
		}
		if s.cursor >= len(s.attachments) {
			s.cursor = len(s.attachments) - 1
		}
		if s.cursor < 0 {
			s.cursor = 0
		}
		return s, nil

	case poAttachUploadedMsg:
		s.uploading = false
		if m.err != nil {
			s.errMsg = m.err.Error()
			return s, Status("upload failed: "+m.err.Error(), StatusError)
		}
		s.errMsg = ""
		s.phase = poAttachPhaseList
		s.uploadInputs[poAttachFieldPath].SetValue("")
		s.uploadInputs[poAttachFieldDesc].SetValue("")
		s.loading = true
		return s, tea.Batch(Status("attachment uploaded", StatusOK), s.load())

	case poAttachDeletedMsg:
		s.deleting = false
		s.confirmingDelete = false
		if m.err != nil {
			return s, Status("delete failed: "+m.err.Error(), StatusError)
		}
		s.loading = true
		return s, tea.Batch(Status("attachment deleted", StatusOK), s.load())

	case tea.KeyMsg:
		switch {
		case s.confirmingDelete:
			return s.updateConfirmDelete(m)
		case s.phase == poAttachPhaseUpload:
			return s.updateUpload(m)
		default:
			return s.updateList(m)
		}
	}

	if s.phase == poAttachPhaseUpload {
		var cmd tea.Cmd
		s.uploadInputs[s.uploadFocus], cmd = s.uploadInputs[s.uploadFocus].Update(msg)
		return s, cmd
	}
	return s, nil
}

func (s *PurchaseOrderAttachmentsScreen) updateList(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		return s, SwitchTo(WSPurchasing, NewPurchaseOrderDetailScreen(s.deps, s.poID))
	case "j", "down":
		if s.cursor < len(s.attachments)-1 {
			s.cursor++
		}
	case "k", "up":
		if s.cursor > 0 {
			s.cursor--
		}
	case "r":
		s.loading = true
		s.loadErr = ""
		return s, s.load()
	case "u":
		s.phase = poAttachPhaseUpload
		s.uploadFocus = poAttachFieldPath
		s.errMsg = ""
		s.uploadInputs[poAttachFieldPath].Focus()
		s.uploadInputs[poAttachFieldDesc].Blur()
		return s, textinput.Blink
	case "x":
		if len(s.attachments) > 0 {
			s.confirmingDelete = true
		}
		return s, nil
	}
	return s, nil
}

func (s *PurchaseOrderAttachmentsScreen) updateConfirmDelete(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.deleting {
		return s, nil
	}
	switch m.String() {
	case "y", "Y":
		if s.cursor < 0 || s.cursor >= len(s.attachments) {
			s.confirmingDelete = false
			return s, nil
		}
		att := s.attachments[s.cursor]
		s.deleting = true
		deps := s.deps
		ctx := s.ctx()
		id := s.poID
		return s, func() tea.Msg {
			return poAttachDeletedMsg{err: deps.OMS.DeletePurchaseOrderAttachment(ctx, id, att.ID)}
		}
	case "n", "N", "esc":
		s.confirmingDelete = false
	}
	return s, nil
}

func (s *PurchaseOrderAttachmentsScreen) updateUpload(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		s.phase = poAttachPhaseList
		s.errMsg = ""
		return s, nil
	case "tab", "down":
		s.uploadInputs[s.uploadFocus].Blur()
		s.uploadFocus = (s.uploadFocus + 1) % poAttachFieldCount
		s.uploadInputs[s.uploadFocus].Focus()
		return s, textinput.Blink
	case "shift+tab", "up":
		s.uploadInputs[s.uploadFocus].Blur()
		s.uploadFocus = (s.uploadFocus - 1 + poAttachFieldCount) % poAttachFieldCount
		s.uploadInputs[s.uploadFocus].Focus()
		return s, textinput.Blink
	case "enter":
		if s.uploading {
			return s, nil
		}
		return s, s.submitUpload()
	}
	var cmd tea.Cmd
	s.uploadInputs[s.uploadFocus], cmd = s.uploadInputs[s.uploadFocus].Update(m)
	return s, cmd
}

func (s *PurchaseOrderAttachmentsScreen) submitUpload() tea.Cmd {
	path := strings.TrimSpace(s.uploadInputs[poAttachFieldPath].Value())
	if path == "" {
		s.errMsg = "a file path is required"
		return Status(s.errMsg, StatusError)
	}
	info, err := os.Stat(path)
	if err != nil {
		s.errMsg = "cannot read file: " + err.Error()
		return Status(s.errMsg, StatusError)
	}
	if info.IsDir() {
		s.errMsg = "path is a directory, not a file"
		return Status(s.errMsg, StatusError)
	}
	desc := strings.TrimSpace(s.uploadInputs[poAttachFieldDesc].Value())
	name := filepath.Base(path)

	s.uploading = true
	s.errMsg = ""
	deps := s.deps
	ctx := s.ctx()
	id := s.poID
	return func() tea.Msg {
		f, err := os.Open(path)
		if err != nil {
			return poAttachUploadedMsg{err: err}
		}
		defer f.Close()
		att, err := deps.OMS.UploadPurchaseOrderAttachment(ctx, id, name, f, desc)
		return poAttachUploadedMsg{att: att, err: err}
	}
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

func (s *PurchaseOrderAttachmentsScreen) View() string {
	if s.phase == poAttachPhaseUpload {
		return s.viewUpload()
	}
	return s.viewList()
}

func (s *PurchaseOrderAttachmentsScreen) viewList() string {
	var b strings.Builder
	if s.loadErr != "" {
		b.WriteString(StyleStatusError.Render("Error: ") + s.loadErr + "\n\n")
	}
	if len(s.attachments) == 0 {
		b.WriteString(StyleMuted.Render("No attachments on this PO.") + "\n\n")
	} else {
		for i, att := range s.attachments {
			caret := "  "
			if i == s.cursor {
				caret = "▸ "
			}
			name := att.FileName
			if name == "" {
				name = att.File
			}
			line := caret + name
			if att.Description != "" {
				line += " — " + att.Description
			}
			if i == s.cursor {
				line = StyleSidebarItemActive.Render(line)
			}
			b.WriteString(line + "\n")
			meta := []string{}
			if !att.UploadedAt.IsZero() {
				meta = append(meta, att.UploadedAt.Format("2006-01-02"))
			}
			if att.UploadedByName != "" {
				meta = append(meta, "by "+att.UploadedByName)
			}
			if len(meta) > 0 {
				b.WriteString("    " + StyleMuted.Render(strings.Join(meta, " · ")) + "\n")
			}
		}
	}

	b.WriteString("\n")
	if s.confirmingDelete && s.cursor >= 0 && s.cursor < len(s.attachments) {
		name := s.attachments[s.cursor].FileName
		if s.deleting {
			b.WriteString(StyleMuted.Render("Deleting…"))
		} else {
			b.WriteString(StyleStatusWarn.Render(fmt.Sprintf("Delete %q? y delete · n/esc cancel", name)))
		}
		return b.String()
	}
	b.WriteString(StyleMuted.Render("j/k move · u upload · x delete · r refresh · esc back"))
	return b.String()
}

func (s *PurchaseOrderAttachmentsScreen) viewUpload() string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Upload attachment") + "\n\n")
	labels := []string{"File path", "Description"}
	for i := 0; i < poAttachFieldCount; i++ {
		caret := "  "
		if i == s.uploadFocus {
			caret = "▸ "
		}
		b.WriteString(caret + StyleTitle.Render(labels[i]+": ") + s.uploadInputs[i].View() + "\n")
	}
	b.WriteString("\n" + StyleMuted.Render("tab move · enter upload · esc cancel") + "\n")
	if s.uploading {
		b.WriteString("\n" + StyleMuted.Render("Uploading…"))
	} else if s.errMsg != "" {
		b.WriteString("\n" + StyleStatusError.Render("✗ "+s.errMsg))
	}
	return b.String()
}
