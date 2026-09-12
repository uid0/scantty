// An asset's document library: manuals, CAD sources, wiring diagrams,
// cut-sheets, and the cut-ready DXF/SVG/G-code/STL files that live WITH a
// machine.
//
// TUI counterpart to the web's asset document section. Three writes and a read:
// upload, supersede, delete, list. Uploads read a file off the local filesystem
// and POST it multipart — the workstation-friendly analogue of the browser's
// file picker, and the same shape UploadPurchaseOrderAttachment and
// UploadWorkOrderPdf already use.
//
// VERSIONING IS THE SERVER'S AND IS NEVER RE-DERIVED HERE. Uploading a document
// with `supersedes` set makes it `prior.version + 1` and flips the prior one's
// `is_current` to False, so it drops out of the current view; `version` and
// `is_current` are read-only on the serializer. Nothing on this side computes
// either, because a client that guessed would label a stale manual current —
// which is the exact failure the versioning exists to stop.
//
// SUPERSEDE IS NOT AN EDIT AND DELETE IS NOT A SUPERSEDE. Superseding keeps both
// rows: the old document stays retrievable, marked not-current, with the new one
// pointing back at it. Deleting destroys the row and its file outright and
// leaves nothing pointing anywhere — so a document another one supersedes can be
// deleted out from under that link (`supersedes` is `on_delete=SET_NULL`), and
// the version chain silently loses a rung. That asymmetry is why the terminal
// confirms a delete and does not confirm a supersede.
//
// Its refusals are OMS's STANDARD envelope, unlike the meter actions beside it:
// the viewset is a plain ModelViewSet, so a validation failure reaches the
// project's exception handler and arrives as
// `{"error": {"code": "validation_failed", "message": …, "details": {…}}}`,
// which parseError already understands. Measured against a real backend
// (2026-09-11, remote `main` tree 2d9c8f9c): a missing title and an invalid
// category both come back that way.
package omsapi

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"time"
)

// AssetDocument mirrors OMS's AssetDocumentSerializer.
//
// File and FileURL are BOTH absolute URLs when a request is in the serializer's
// context, which it is on every route this client drives — measured, not
// assumed: `file` came back as `http://…/media/assets/documents/2026/09/x.txt`
// rather than the relative storage path a FileField holds. They are kept as two
// fields because they are two serializer keys and an older or differently
// configured deployment can make them differ; a screen wanting something to show
// asks FileURL first.
//
// Supersedes is a *string because a UUID FK serializes as a string or null, and
// null here means "this is version 1", which is a different fact from a document
// whose predecessor has since been deleted (also null — see the note above about
// SET_NULL). SupersedesTitle is the server's own "Title (vN)" rendering and is
// what a screen shows rather than re-deriving it from an id it cannot resolve.
type AssetDocument struct {
	ID              string    `json:"id"`
	Asset           string    `json:"asset"`
	File            string    `json:"file,omitempty"`
	FileURL         string    `json:"file_url,omitempty"`
	Category        string    `json:"category"`
	CategoryDisplay string    `json:"category_display,omitempty"`
	Title           string    `json:"title"`
	Description     string    `json:"description,omitempty"`
	Version         int       `json:"version"`
	IsCurrent       bool      `json:"is_current"`
	Supersedes      *string   `json:"supersedes"`
	SupersedesTitle string    `json:"supersedes_title,omitempty"`
	UploadedBy      *int      `json:"uploaded_by"`
	UploadedByName  string    `json:"uploaded_by_name,omitempty"`
	UploadedAt      time.Time `json:"uploaded_at"`
}

// AssetDocumentCategory is one of OMS's AssetDocument.Category choices paired
// with the label the server itself displays for it.
//
// The pair is carried rather than just the value because a picker has to show
// something, and the server's label is the one the web shows — so an operator
// who has seen the web form recognises the row. The SET is transcribed from
// OMS's own TextChoices and a value outside it is a 400
// (`"nonsense" is not a valid choice`), measured.
type AssetDocumentCategory struct {
	Value string
	Label string
}

// AssetDocumentCategories is that set, in the server's declaration order.
func AssetDocumentCategories() []AssetDocumentCategory {
	return []AssetDocumentCategory{
		{"manual", "Manual / Documentation"},
		{"cad_source", "CAD Source"},
		{"wiring_diagram", "Wiring Diagram"},
		{"cut_sheet_spec", "Cut Sheet / Spec"},
		{"cut_ready_template", "Cut-Ready Template"},
		{"photo", "Photo"},
		{"other", "Other"},
	}
}

// ListAssetDocuments returns every document on one asset, superseded versions
// included, walking `next` to the end.
//
// It does NOT send `?is_current=true`. The web keeps superseded versions behind
// a toggle; the terminal shows them inline and MARKS them, because the one thing
// a document library must never do is present a stale manual as current — and a
// row that says "v1, superseded" states that where a filtered-out row states
// nothing at all. The whole walk is the same argument ListAssetMeters makes: the
// grid numbers its rows, so a page left on the server is a document nobody can
// reach and nobody can see is missing.
func (c *Client) ListAssetDocuments(ctx context.Context, assetID string) ([]AssetDocument, error) {
	q := url.Values{"asset": {assetID}}
	var all []AssetDocument
	if err := IterPages[AssetDocument](ctx, c, "/api/inventory/asset-documents/", q,
		func(batch []AssetDocument) error {
			all = append(all, batch...)
			return nil
		}); err != nil {
		return nil, err
	}
	return all, nil
}

// UploadAssetDocument attaches a file to an asset via multipart POST
// /api/inventory/asset-documents/. The file part is named "file" and asset,
// title and category ride as text fields — the exact contract the web's
// assetDocumentsAPI.upload sends.
//
// Description is omitted when blank rather than sent empty. That matters on the
// SUPERSEDE path beside it, where an omitted key inherits the prior document's
// value and an empty one overwrites it, and the two are kept the same shape here
// so the difference is a property of the endpoint rather than of which helper
// was reached for.
func (c *Client) UploadAssetDocument(
	ctx context.Context, assetID, title, category, description, fileName string, file io.Reader,
) (*AssetDocument, error) {
	fields := map[string][]string{
		"asset":    {assetID},
		"title":    {title},
		"category": {category},
	}
	if description != "" {
		fields["description"] = []string{description}
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("oms: read document %s: %w", fileName, err)
	}
	files := []MultipartFile{{Field: "file", Filename: fileName, Data: data}}
	var out AssetDocument
	if err := c.PostMultipart(ctx, "/api/inventory/asset-documents/", fields, files, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SupersedeAssetDocument uploads a new version that replaces `docID` via
// multipart POST /api/inventory/asset-documents/{id}/supersede/.
//
// The new document INHERITS the prior one's asset, and its category and title
// too unless they are sent — `data.setdefault` on the server — so a caller that
// only has a replacement file sends only the file and the version chain keeps
// its identity. That is why title, category and description are OMITTED when
// blank rather than sent empty: sending `title=""` here would not mean "leave it
// alone", it would mean "this version is untitled".
func (c *Client) SupersedeAssetDocument(
	ctx context.Context, docID, title, category, description, fileName string, file io.Reader,
) (*AssetDocument, error) {
	fields := map[string][]string{}
	if title != "" {
		fields["title"] = []string{title}
	}
	if category != "" {
		fields["category"] = []string{category}
	}
	if description != "" {
		fields["description"] = []string{description}
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("oms: read document %s: %w", fileName, err)
	}
	files := []MultipartFile{{Field: "file", Filename: fileName, Data: data}}
	var out AssetDocument
	path := fmt.Sprintf("/api/inventory/asset-documents/%s/supersede/", docID)
	if err := c.PostMultipart(ctx, path, fields, files, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteAssetDocument destroys a document row and its file —
// DELETE /api/inventory/asset-documents/{id}/, 204 on success.
//
// There is no undo and no server-side soft delete. Any document that supersedes
// this one keeps its own row and loses the link (`SET_NULL`), so the chain it
// belonged to is left with a gap nothing records.
func (c *Client) DeleteAssetDocument(ctx context.Context, docID string) error {
	return c.Delete(ctx, fmt.Sprintf("/api/inventory/asset-documents/%s/", docID))
}
