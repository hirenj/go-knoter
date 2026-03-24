// knoter uploads HTML reports to Microsoft OneNote.
//
// Authentication is handled by knoter-auth, which prints a KNOTER_TOKEN
// environment variable.  Run knoter-auth once, then pass the token here:
//
//	eval "$(knoter-auth --login-hint you@company.com)"
//	knoter upload --notebook "My Notebook" --section "Results" report.html
//
// Usage:
//
//	knoter upload --notebook "My Notebook" --section "Results" report.html [attachment.pdf ...]
//	knoter upload --notebook "My Notebook" --section "Results" --page "Existing Page" --update replace report.html
//	knoter help
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hirenj/go-knoter/internal/auth"
	htmlpkg "github.com/hirenj/go-knoter/internal/html"
	"github.com/hirenj/go-knoter/internal/onenote"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "knoter:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}

	switch args[0] {
	case "upload":
		return runUpload(args[1:])
	case "help", "--help", "-h":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command %q — try 'knoter help'", args[0])
	}
}

func runUpload(args []string) error {
	fs := flag.NewFlagSet("upload", flag.ContinueOnError)

	notebook    := fs.String("notebook", "", "OneNote notebook name (required)")
	section     := fs.String("section", "", "OneNote section name (required)")
	page        := fs.String("page", "", "Page title; defaults to '<filename> YYYY-MM-DD HH:MM'")
	update      := fs.String("update", "", "Update mode: 'replace' or 'append' (requires --page)")
	tokenEnv    := fs.String("token-env", "KNOTER_TOKEN", "Env var holding the Bearer token (set by knoter-auth)")
	sharepoint  := fs.String("sharepoint", "", "SharePoint site URL (if notebooks are in SharePoint)")
	attachFlag  := fs.String("attach", "", "Comma-separated list of extra files to attach (PDF, xlsx, …)")
	embedImages := fs.Bool("embed-images", false, "Embed base64 data-URI images as attachments (increases request size)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *notebook == "" || *section == "" {
		return fmt.Errorf("--notebook and --section are required")
	}

	positional := fs.Args()
	if len(positional) == 0 {
		return fmt.Errorf("expected at least one HTML file argument")
	}
	htmlFile := positional[0]

	rawHTML, err := os.ReadFile(htmlFile)
	if err != nil {
		return fmt.Errorf("reading %s: %w", htmlFile, err)
	}

	// Resolve page title.
	// When --page is not set, append a timestamp so each upload creates a
	// distinct page rather than silently stacking identically-named pages.
	pageTitle := *page
	if pageTitle == "" {
		base := filepath.Base(htmlFile)
		pageTitle = strings.TrimSuffix(base, filepath.Ext(base)) +
			" " + time.Now().Format("2006-01-02 15:04")
	}

	// Build extra attachments list.
	var extraAttachments []onenote.AttachmentFile
	if *attachFlag != "" {
		for i, path := range strings.Split(*attachFlag, ",") {
			path = strings.TrimSpace(path)
			partName := fmt.Sprintf("attach%04d", i+1)
			extraAttachments = append(extraAttachments, onenote.AttachmentFile{
				PartName: partName,
				Path:     path,
			})
		}
	}
	// Positional args after the HTML file are also treated as attachments.
	for i, path := range positional[1:] {
		partName := fmt.Sprintf("extra%04d", i+1)
		extraAttachments = append(extraAttachments, onenote.AttachmentFile{
			PartName: partName,
			Path:     path,
		})
	}

	// Pre-process HTML.
	baseDir := filepath.Dir(htmlFile)
	result, err := htmlpkg.Process(string(rawHTML), htmlpkg.Options{
		BaseDir:          baseDir,
		Title:            pageTitle,
		ExtraAttachments: extraAttachments,
		EmbedDataImages:  *embedImages,
	})
	if err != nil {
		return fmt.Errorf("processing HTML: %w", err)
	}

	// Resolve access token from env var.
	accessToken := os.Getenv(*tokenEnv)
	if accessToken == "" {
		return fmt.Errorf(
			"env var %q is not set — run 'eval \"$(knoter-auth ...)\"' first",
			*tokenEnv,
		)
	}
	if err := auth.CheckTokenAudience(accessToken); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
	}

	var client *onenote.Client
	if *sharepoint != "" {
		client, err = onenote.NewForSharePoint(accessToken, *sharepoint)
		if err != nil {
			return fmt.Errorf("SharePoint: %w", err)
		}
	} else {
		client = onenote.New(accessToken)
	}

	// Resolve notebook ID.
	notebookID, err := client.NotebookID(*notebook)
	if err != nil {
		return err
	}

	// Resolve (or create) section ID.
	sectionID, err := client.SectionID(notebookID, *section)
	if err != nil {
		return err
	}

	req := &onenote.UploadRequest{
		SectionID:   sectionID,
		Title:       pageTitle,
		HTMLContent: result.HTML,
		Attachments: result.Attachments,
	}

	if *update != "" {
		existing, err := client.FindPage(sectionID, pageTitle)
		if err != nil {
			return err
		}
		if existing == nil {
			fmt.Fprintf(os.Stderr, "Warning: page %q not found; creating new page instead.\n", pageTitle)
		} else {
			req.UpdateMode = *update
			req.ExistingID = existing.ID
		}
	}

	fmt.Fprintf(os.Stderr, "Uploading %q → %s / %s ...\n", pageTitle, *notebook, *section)
	if err := client.Upload(req); err != nil {
		return fmt.Errorf("upload: %w", err)
	}

	fmt.Fprintln(os.Stderr, "Done.")
	return nil
}

func printUsage() {
	fmt.Print(`knoter — upload HTML reports to Microsoft OneNote

Authenticate first with knoter-auth:
  eval "$(knoter-auth --login-hint you@company.com)"

Commands:
  upload    Upload an HTML file as a OneNote page
  help      Show this help

Upload flags:
  --notebook     <name>        OneNote notebook name (required)
  --section      <name>        OneNote section name  (required)
  --page         <title>       Page title (default: '<filename> YYYY-MM-DD HH:MM')
  --update       replace|append  Update an existing page (requires --page)
  --attach       <file,...>    Extra files to attach (PDF, xlsx, …)
  --token-env    <var>         Env var holding the Bearer token (default: KNOTER_TOKEN)
  --sharepoint   <url>         SharePoint site URL
  --embed-images               Embed base64 data-URI images (increases request size)

Examples:
  eval "$(knoter-auth --login-hint you@company.com)"
  knoter upload --notebook "Lab Notes" --section "2024" results.html

  # SharePoint
  eval "$(knoter-auth --sharepoint https://contoso.sharepoint.com/sites/lab)"
  knoter upload --sharepoint https://contoso.sharepoint.com/sites/lab \
      --notebook "Lab Notes" --section "2024" results.html

  # Replace an existing page
  knoter upload --notebook "Lab Notes" --section "2024" \
      --page "Weekly Report" --update replace weekly.html
`)
}
