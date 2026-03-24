// Package html preprocesses HTML reports for OneNote compatibility.
//
// OneNote supports only a limited HTML subset (no CSS, limited tags).
// This package:
//   - Finds <img src="path/to/file.png"> tags and rewrites them as
//     <img src="name:partName"> while building the AttachmentFile list
//     needed for the multipart upload.
//   - Strips <style>, <script>, and <link> tags that OneNote ignores anyway.
//   - Optionally inlines the title as <h1>.
package html

import (
	"encoding/base64"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/CopenhagenCenterForGlycomics/knoter-go/internal/onenote"
)

// imgRe matches <img ...src="..."...> (non-greedy, handles single and double quotes).
var imgRe = regexp.MustCompile(`(?i)<img([^>]*?)\bsrc=["']([^"']+)["']([^>]*)>`)

// objectRe matches <object ...data="..."...> tags.
var objectRe = regexp.MustCompile(`(?i)<object([^>]*?)\bdata=["']([^"']+)["']([^>]*)>`)

// styleRe strips <style>...</style> blocks.
var styleRe = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)

// scriptRe strips <script>...</script> blocks.
var scriptRe = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)

// linkRe strips <link ...> tags.
var linkRe = regexp.MustCompile(`(?i)<link[^>]*/?>`)

// ProcessResult is returned by Process.
type ProcessResult struct {
	// HTML is the rewritten content ready to POST to OneNote.
	HTML string
	// Attachments is the ordered list of files referenced by the HTML.
	Attachments []onenote.AttachmentFile
}

// Options controls processing behaviour.
type Options struct {
	// BaseDir is used to resolve relative image paths. Defaults to CWD.
	BaseDir string
	// Title is prepended as <h1> if non-empty.
	Title string
	// ExtraAttachments are additional files (e.g. PDFs, xlsx) to attach.
	// They do not appear as <img> in the HTML; they are linked by PartName.
	ExtraAttachments []onenote.AttachmentFile
	// EmbedDataImages controls whether data: URI images are decoded and
	// uploaded as binary parts. When false (the default) they are stripped
	// from the HTML to keep the request size small.
	EmbedDataImages bool
}

// Process takes raw HTML (e.g. from nbconvert or knitr) and makes it
// OneNote-ready, returning the modified HTML and the attachment list.
func Process(rawHTML string, opts Options) (*ProcessResult, error) {
	if opts.BaseDir == "" {
		var err error
		opts.BaseDir, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}

	html := rawHTML

	// Strip unsupported structural tags.
	html = styleRe.ReplaceAllString(html, "")
	html = scriptRe.ReplaceAllString(html, "")
	html = linkRe.ReplaceAllString(html, "")

	// Unwrap <html>/<head>/<body> if present; OneNote expects a fragment.
	html = stripOuterChrome(html)

	var attachments []onenote.AttachmentFile
	partCounter := 0

	html = imgRe.ReplaceAllStringFunc(html, func(match string) string {
		groups := imgRe.FindStringSubmatch(match)
		if groups == nil {
			return match
		}
		before, src, after := groups[1], groups[2], groups[3]

		// Skip http(s) URLs — OneNote can fetch those directly.
		if strings.HasPrefix(src, "http") {
			return match
		}

		// data: URI images are stripped by default to keep the request small.
		// Use EmbedDataImages to upload them as binary parts instead.
		if strings.HasPrefix(src, "data:") {
			if !opts.EmbedDataImages {
				return "" // strip the tag entirely
			}
			partCounter++
			partName := fmt.Sprintf("img%04d", partCounter)
			att, err := decodeDataURI(src, partName)
			if err != nil {
				partCounter--
				return match
			}
			attachments = append(attachments, *att)
			return fmt.Sprintf(`<img%s src="name:%s"%s>`, before, partName, after)
		}

		absPath := src
		if !filepath.IsAbs(src) {
			absPath = filepath.Join(opts.BaseDir, src)
		}

		if _, err := os.Stat(absPath); err != nil {
			// File not found; leave the tag as-is (will fail in OneNote but
			// shouldn't abort the whole upload).
			return match
		}

		partCounter++
		partName := fmt.Sprintf("img%04d", partCounter)
		attachments = append(attachments, onenote.AttachmentFile{
			PartName: partName,
			Path:     absPath,
		})

		return fmt.Sprintf(`<img%s src="name:%s"%s>`, before, partName, after)
	})

	// Rewrite <object data="local/path"> tags — same logic as R's
	// getNodeSet('//object[starts-with(@data, "file://")]').
	html = objectRe.ReplaceAllStringFunc(html, func(match string) string {
		groups := objectRe.FindStringSubmatch(match)
		if groups == nil {
			return match
		}
		before, src, after := groups[1], groups[2], groups[3]

		// Skip http(s) URLs.
		if strings.HasPrefix(src, "http") {
			return match
		}
		// Skip already-rewritten name: references.
		if strings.HasPrefix(src, "name:") {
			return match
		}

		// Strip file:// prefix (R uses file:// paths from knitr).
		filePath := strings.TrimPrefix(src, "file://")
		if !filepath.IsAbs(filePath) {
			filePath = filepath.Join(opts.BaseDir, filePath)
		}
		if _, err := os.Stat(filePath); err != nil {
			return match
		}

		partCounter++
		partName := fmt.Sprintf("obj%04d", partCounter)
		attachments = append(attachments, onenote.AttachmentFile{
			PartName: partName,
			Path:     filePath,
		})

		// Preserve existing attributes; rewrite data= and ensure
		// data-attachment= and type= are present for OneNote.
		name := filepath.Base(filePath)
		mt := mime.TypeByExtension(filepath.Ext(filePath))
		if mt == "" {
			mt = "application/octet-stream"
		}
		// Strip any existing data-attachment/type from before/after to avoid
		// duplicates, then rebuild cleanly.
		attrRe := regexp.MustCompile(`(?i)\s*(data-attachment|type)=["'][^"']*["']`)
		before = attrRe.ReplaceAllString(before, "")
		after = attrRe.ReplaceAllString(after, "")
		return fmt.Sprintf(`<object%s data-attachment=%q data="name:%s" type=%q%s />`,
			before, name, partName, mt, after)
	})

	// Prepend title.
	if opts.Title != "" {
		html = fmt.Sprintf("<h1>%s</h1>\n", escapeHTML(opts.Title)) + html
	}

	// Append extra attachments passed via --attach / positional args.
	for _, ea := range opts.ExtraAttachments {
		attachments = append(attachments, ea)
		name := filepath.Base(ea.Path)
		mt := mime.TypeByExtension(filepath.Ext(ea.Path))
		if mt == "" {
			mt = "application/octet-stream"
		}
		html += fmt.Sprintf(
			"\n<p><object data-attachment=%q data=\"name:%s\" type=%q /></p>",
			name, ea.PartName, mt,
		)
	}

	return &ProcessResult{
		HTML:        html,
		Attachments: attachments,
	}, nil
}

// stripOuterChrome removes the <html>, <head>, and <body> wrapper if present,
// returning only the inner content. OneNote wants a fragment, not a full page.
func stripOuterChrome(html string) string {
	// Pull out <body>...</body> content if it exists.
	bodyRe := regexp.MustCompile(`(?is)<body[^>]*>(.*)</body>`)
	if m := bodyRe.FindStringSubmatch(html); m != nil {
		return strings.TrimSpace(m[1])
	}
	return html
}

// decodeDataURI parses a data: URI of the form "data:<mime>;base64,<data>"
// and returns an AttachmentFile with the decoded bytes.
func decodeDataURI(uri, partName string) (*onenote.AttachmentFile, error) {
	// Strip the "data:" prefix.
	rest := strings.TrimPrefix(uri, "data:")
	// Split on the first comma to separate header from data.
	comma := strings.IndexByte(rest, ',')
	if comma < 0 {
		return nil, fmt.Errorf("malformed data URI: no comma")
	}
	header := rest[:comma]
	encoded := rest[comma+1:]

	// Header is "<mime>[;base64]".
	mimeType := header
	isBase64 := false
	if strings.HasSuffix(header, ";base64") {
		isBase64 = true
		mimeType = header[:len(header)-len(";base64")]
	}

	var data []byte
	var err error
	if isBase64 {
		data, err = base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			// Some generators use RawStdEncoding (no padding).
			data, err = base64.RawStdEncoding.DecodeString(encoded)
		}
		if err != nil {
			return nil, fmt.Errorf("decoding base64: %w", err)
		}
	} else {
		data = []byte(encoded)
	}

	return &onenote.AttachmentFile{
		PartName: partName,
		Data:     data,
		MimeType: mimeType,
	}, nil
}

func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&#34;")
	return s
}
