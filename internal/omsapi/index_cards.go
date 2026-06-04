package omsapi

import "context"

// IndexCardResponse is the JSON envelope POST /generate/ and POST
// /test-sheet/ return — the PDF lives in storage; the response is
// just the path/URL plus a count + the rendered card type.
//
// Note: this isn't a binary download as the bead description
// originally implied. The backend writes the PDF to MEDIA_ROOT and
// hands back a link; consumers fetch the URL directly to download
// or hand it to the system PDF viewer.
type IndexCardResponse struct {
	FilePath     string `json:"file_path"`
	FileURL      string `json:"file_url"`
	AbsolutePath string `json:"absolute_path"`
	Count        int    `json:"count"`
	CardType     string `json:"card_type"`
}

// IndexCardBatchRequest is the POST body. item_ids are inventory item
// UUIDs (strings) — the backend rejects 404 if any are unknown and
// preserves the input order in the rendered PDF.
type IndexCardBatchRequest struct {
	ItemIDs    []string `json:"item_ids"`
	BlankCards bool     `json:"blank_cards,omitempty"`
	Filename   string   `json:"filename,omitempty"`
}

// GenerateIndexCards renders a 3-up Avery 5388 PDF for the given
// inventory items and returns the storage URL.
func (c *Client) GenerateIndexCards(ctx context.Context, req IndexCardBatchRequest) (*IndexCardResponse, error) {
	var out IndexCardResponse
	if err := c.Post(ctx, "/api/index-cards/generate/", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GenerateTestSheet renders an 8.5×11 test sheet PDF with images +
// names + QR codes for the given inventory items. Same item_id
// validation as the batch generator.
func (c *Client) GenerateTestSheet(ctx context.Context, req IndexCardBatchRequest) (*IndexCardResponse, error) {
	var out IndexCardResponse
	if err := c.Post(ctx, "/api/index-cards/test-sheet/", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PreviewIndexCard renders one card as a single-page preview PDF.
// Body shape is the same as the batch endpoint, but the caller is
// expected to pass exactly one item_id.
func (c *Client) PreviewIndexCard(ctx context.Context, req IndexCardBatchRequest) (*IndexCardResponse, error) {
	var out IndexCardResponse
	if err := c.Post(ctx, "/api/index-cards/preview/", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
