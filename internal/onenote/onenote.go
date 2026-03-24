// Package onenote wraps the Microsoft Graph OneNote REST API.
// It handles multipart page creation with inline image attachments,
// page updates (PATCH), and resolving notebook/section IDs by name.
package onenote

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const graphRoot = "https://graph.microsoft.com/v1.0"

// Client is a thin authenticated Graph API client.
type Client struct {
	HTTPClient  *http.Client
	AccessToken string
	onenoteBase string // e.g. ".../me/onenote" or ".../sites/{id}/onenote"
}

// New creates a Client for the signed-in user's personal notebooks.
func New(accessToken string) *Client {
	return &Client{
		HTTPClient:  &http.Client{Timeout: 60 * time.Second},
		AccessToken: accessToken,
		onenoteBase: graphRoot + "/me/onenote",
	}
}

// NewForSharePoint creates a Client whose notebooks are in the given SharePoint
// site.  sharepointURL should be the site root, e.g.
// "https://contoso.sharepoint.com" or "https://contoso.sharepoint.com/sites/mysite".
// Requires Sites.Read.All (or Sites.ReadWrite.All) permission on the token.
func NewForSharePoint(accessToken, sharepointURL string) (*Client, error) {
	c := New(accessToken)
	siteID, err := c.resolveSiteID(sharepointURL)
	if err != nil {
		return nil, fmt.Errorf("resolving SharePoint site: %w", err)
	}
	c.onenoteBase = graphRoot + "/sites/" + siteID + "/onenote"
	return c, nil
}

// resolveSiteID calls the Graph API to look up a SharePoint site ID from its URL.
// Requires Sites.Read.All or Sites.ReadWrite.All.
func (c *Client) resolveSiteID(sharepointURL string) (string, error) {
	u, err := url.Parse(sharepointURL)
	if err != nil {
		return "", fmt.Errorf("invalid SharePoint URL: %w", err)
	}
	// Graph endpoint: /sites/{hostname}:{path} (path may be empty → just /sites/{hostname})
	endpoint := graphRoot + "/sites/" + u.Hostname()
	if u.Path != "" && u.Path != "/" {
		endpoint += ":" + u.Path
	}
	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		var apiErr struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(body, &apiErr)
		if apiErr.Error.Code != "" {
			return "", fmt.Errorf("HTTP %d: %s: %s", resp.StatusCode, apiErr.Error.Code, apiErr.Error.Message)
		}
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var site struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &site); err != nil {
		return "", err
	}
	if site.ID == "" {
		return "", fmt.Errorf("no site ID in response")
	}
	return site.ID, nil
}

func (c *Client) do(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+c.AccessToken)
	return c.HTTPClient.Do(req)
}

// ---- Notebook / Section resolution ----------------------------------------

type onenoteItem struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Self        string `json:"self"`
}

type listResponse struct {
	Value []onenoteItem `json:"value"`
}

func (c *Client) listItems(endpoint string) ([]onenoteItem, error) {
	var all []onenoteItem
	next := endpoint
	for next != "" {
		req, _ := http.NewRequest("GET", next, nil)
		resp, err := c.do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		var lr struct {
			Value    []onenoteItem `json:"value"`
			NextLink string        `json:"@odata.nextLink"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
			return nil, err
		}
		all = append(all, lr.Value...)
		next = lr.NextLink
	}
	return all, nil
}

// NotebookID resolves a notebook display-name to its Graph ID.
func (c *Client) NotebookID(name string) (string, error) {
	items, err := c.listItems(c.onenoteBase + "/notebooks")
	if err != nil {
		return "", err
	}
	for _, nb := range items {
		if strings.EqualFold(nb.DisplayName, name) {
			return nb.ID, nil
		}
	}
	return "", fmt.Errorf("notebook %q not found (available: %s)",
		name, joinNames(items))
}

// SectionID resolves a section display-name within a notebook to its Graph ID.
// If the section does not exist it is created automatically.
func (c *Client) SectionID(notebookID, sectionName string) (string, error) {
	endpoint := fmt.Sprintf("%s/notebooks/%s/sections", c.onenoteBase, notebookID)
	items, err := c.listItems(endpoint)
	if err != nil {
		return "", err
	}
	for _, s := range items {
		if strings.EqualFold(s.DisplayName, sectionName) {
			return s.ID, nil
		}
	}
	// Section not found — create it.
	return c.createSection(notebookID, sectionName)
}

// createSection creates a new section in the given notebook and returns its ID.
func (c *Client) createSection(notebookID, sectionName string) (string, error) {
	body, err := json.Marshal(map[string]string{"displayName": sectionName})
	if err != nil {
		return "", err
	}
	endpoint := fmt.Sprintf("%s/notebooks/%s/sections", c.onenoteBase, notebookID)
	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 300 {
		var apiErr struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &apiErr)
		if apiErr.Error.Code != "" {
			return "", fmt.Errorf("creating section %q: %s: %s", sectionName, apiErr.Error.Code, apiErr.Error.Message)
		}
		return "", fmt.Errorf("creating section %q: HTTP %d", sectionName, resp.StatusCode)
	}
	var section onenoteItem
	if err := json.Unmarshal(raw, &section); err != nil {
		return "", fmt.Errorf("decoding created section: %w", err)
	}
	if section.ID == "" {
		return "", fmt.Errorf("creating section %q: no ID in response", sectionName)
	}
	fmt.Fprintf(os.Stderr, "Created section %q.\n", sectionName)
	return section.ID, nil
}

// SectionIDForSharePoint resolves section via SharePoint site (future: can
// extend to look up via sites endpoint; stubbed for now).
func (c *Client) SectionIDForSharePoint(sharepointURL, notebookName, sectionName string) (string, error) {
	// SharePoint-hosted notebooks appear under /sites/{siteID}/onenote/...
	// For now we fall back to the personal path; extend as needed.
	return "", fmt.Errorf("SharePoint support not yet implemented in go-knoter")
}

// ---- Page operations -------------------------------------------------------

// PageRef points to an existing page.
type PageRef struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// ListPages returns all pages in a section, newest first.
func (c *Client) ListPages(sectionID string) ([]PageRef, error) {
	endpoint := fmt.Sprintf("%s/sections/%s/pages?$select=id,title&$orderby=lastModifiedDateTime desc",
		c.onenoteBase, sectionID)
	req, _ := http.NewRequest("GET", endpoint, nil)
	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var lr struct {
		Value []PageRef `json:"value"`
	}
	return lr.Value, json.NewDecoder(resp.Body).Decode(&lr)
}

// FindPage returns the first page whose title equals name (case-insensitive),
// or nil if none.
func (c *Client) FindPage(sectionID, name string) (*PageRef, error) {
	pages, err := c.ListPages(sectionID)
	if err != nil {
		return nil, err
	}
	for _, p := range pages {
		if strings.EqualFold(p.Title, name) {
			return &p, nil
		}
	}
	return nil, nil
}

// UploadRequest carries everything needed to create/update a page.
type UploadRequest struct {
	SectionID   string
	Title       string
	HTMLContent string            // full page HTML; img src="name:partName" for attachments
	Attachments []AttachmentFile  // files to attach as multipart parts
	UpdateMode  string            // "replace" | "append" | "" (create new)
	ExistingID  string            // required when UpdateMode != ""
}

// AttachmentFile represents a file to be included as a multipart attachment.
type AttachmentFile struct {
	// PartName is the name used in src="name:<PartName>" within the HTML.
	PartName string
	// Path is the filesystem path to read from. Ignored when Data is set.
	Path string
	// Data holds the raw bytes when the content is already in memory.
	// When set, Path is ignored and MimeType should be provided.
	Data []byte
	// MimeType overrides auto-detection when non-empty.
	MimeType string
}

// Upload creates or updates a OneNote page.
func (c *Client) Upload(r *UploadRequest) error {
	if r.UpdateMode != "" && r.ExistingID != "" {
		return c.updatePage(r)
	}
	return c.createPage(r)
}

func (c *Client) createPage(r *UploadRequest) error {
	body, contentType, err := buildMultipart(r.Title, r.HTMLContent, r.Attachments)
	if err != nil {
		return err
	}

	endpoint := fmt.Sprintf("%s/sections/%s/pages", c.onenoteBase, r.SectionID)
	req, err := http.NewRequest("POST", endpoint, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := c.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("OneNote create page failed (%d): %s", resp.StatusCode, b)
	}
	return nil
}

// updatePage uses PATCH on the page content endpoint.
// mode should be "replace" (default) or "append".
func (c *Client) updatePage(r *UploadRequest) error {
	action := "replace"
	if r.UpdateMode == "append" {
		action = "append"
	}

	patchBody := []map[string]interface{}{
		{
			"target":  "body",
			"action":  action,
			"content": r.HTMLContent,
		},
	}
	jsonBody, _ := json.Marshal(patchBody)

	endpoint := fmt.Sprintf("%s/pages/%s/content", c.onenoteBase, r.ExistingID)
	req, err := http.NewRequest("PATCH", endpoint, bytes.NewReader(jsonBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("OneNote update page failed (%d): %s", resp.StatusCode, b)
	}
	return nil
}

// ---- Multipart builder -----------------------------------------------------

// buildMultipart assembles the presentation/HTML + binary attachments into a
// multipart/form-data body as required by the OneNote Pages API.
func buildMultipart(title, htmlContent string, attachments []AttachmentFile) (io.Reader, string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	// Part 1: the HTML presentation
	presentationHeader := textproto.MIMEHeader{}
	presentationHeader.Set("Content-Disposition", `form-data; name="Presentation"`)
	presentationHeader.Set("Content-Type", "text/html")
	pw, err := mw.CreatePart(presentationHeader)
	if err != nil {
		return nil, "", err
	}
	if _, err := fmt.Fprintf(pw, htmlContent); err != nil {
		return nil, "", err
	}
	fmt.Fprintf(os.Stderr, "  [html]  Presentation (%s)\n", formatSize(len(htmlContent)))

	// Parts 2..N: binary attachments
	for _, att := range attachments {
		var data []byte
		if att.Data != nil {
			data = att.Data
		} else {
			var err error
			data, err = os.ReadFile(att.Path)
			if err != nil {
				return nil, "", fmt.Errorf("reading attachment %s: %w", att.Path, err)
			}
		}

		mt := att.MimeType
		if mt == "" {
			mt = mime.TypeByExtension(filepath.Ext(att.Path))
		}
		if mt == "" {
			mt = "application/octet-stream"
		}

		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"`, att.PartName))
		h.Set("Content-Type", mt)
		h.Set("Content-ID", att.PartName)
		part, err := mw.CreatePart(h)
		if err != nil {
			return nil, "", err
		}
		if _, err := part.Write(data); err != nil {
			return nil, "", err
		}

		label := att.Path
		if label == "" {
			label = att.PartName
		}
		fmt.Fprintf(os.Stderr, "  [%-4s]  %s (%s)\n", shortType(mt), label, formatSize(len(data)))
	}

	if err := mw.Close(); err != nil {
		return nil, "", err
	}

	contentType := "multipart/form-data; boundary=" + mw.Boundary()
	return &buf, contentType, nil
}

// ---- helpers ---------------------------------------------------------------

func shortType(mt string) string {
	switch {
	case strings.HasPrefix(mt, "image/"):
		return "img"
	case mt == "application/pdf":
		return "pdf"
	case strings.Contains(mt, "spreadsheet") || strings.Contains(mt, "excel"):
		return "xlsx"
	default:
		return "file"
	}
}

func formatSize(n int) string {
	switch {
	case n >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/1024/1024)
	case n >= 1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func joinNames(items []onenoteItem) string {
	names := make([]string, len(items))
	for i, it := range items {
		names[i] = it.DisplayName
	}
	return strings.Join(names, ", ")
}
