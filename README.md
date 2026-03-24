# go-knoter

A Go rewrite of [knoter](https://github.com/CopenhagenCenterForGlycomics/knoter) — upload HTML reports to Microsoft OneNote.

Two binaries:

| Binary | What it does |
|---|---|
| `knoter-auth` | Authenticates with Microsoft and outputs a `KNOTER_TOKEN` env var |
| `knoter` | Uploads an HTML file (+ attachments) to OneNote |

---

## Installation

### Homebrew

```sh
brew tap hirenj/go-knoter https://github.com/hirenj/go-knoter
brew install knoter
```

### Build from source

```sh
git clone https://github.com/hirenj/go-knoter
cd go-knoter
make build
```

This produces `knoter` and `knoter-auth` in the current directory.

---

## Authentication

Authentication is handled separately by `knoter-auth`. Run it once to get a
token; subsequent runs reuse the cached token (`~/.config/knoter/token.json`).

```sh
# Work / school account (tenant derived from email domain)
eval "$(knoter-auth --login-hint you@company.com)"

# Personal Microsoft account
eval "$(knoter-auth --tenant consumers)"

# If device-code is blocked by Conditional Access, use PKCE (opens browser)
eval "$(knoter-auth --login-hint you@company.com --flow pkce)"

# SharePoint notebooks (requests Sites.Read.All scope)
eval "$(knoter-auth --sharepoint https://contoso.sharepoint.com/sites/lab)"

# Clear cached token
knoter-auth --logout
```

`knoter-auth` prints `KNOTER_TOKEN=<token>` to stdout and all prompts to
stderr, so `eval "$(...)"` sets the variable in your shell.

### knoter-auth flags

| Flag | Default | Description |
|---|---|---|
| `--login-hint` | | Microsoft account email; derives tenant and pre-fills sign-in |
| `--flow` | `device-code` | Auth flow: `device-code` or `pkce` |
| `--tenant` | `common` | Azure AD tenant ID, `consumers`, or `organizations` |
| `--sharepoint` | | SharePoint site URL — requests `Sites.Read.All` scope |
| `--client-id` | built-in | Azure app client ID |
| `--client-secret` | | Client secret (confidential app registrations only) |
| `--env-var` | `KNOTER_TOKEN` | Name of the environment variable to output |
| `--logout` | | Clear the cached token and exit |

---

## Uploading

```sh
knoter upload --notebook "Lab Notes" --section "2024" report.html
```

By default a timestamp is appended to the page title (`report 2026-03-24 14:32`)
so each upload creates a new page. Use `--page` to set a fixed title.

### knoter upload flags

| Flag | Default | Description |
|---|---|---|
| `--notebook` | | OneNote notebook name **(required)** |
| `--section` | | OneNote section name **(required)** — created if it doesn't exist |
| `--page` | `<filename> YYYY-MM-DD HH:MM` | Page title |
| `--update` | | `replace` or `append` an existing page (requires `--page`) |
| `--attach` | | Comma-separated list of extra files to attach (PDF, xlsx, …) |
| `--sharepoint` | | SharePoint site URL |
| `--token-env` | `KNOTER_TOKEN` | Env var holding the Bearer token |
| `--embed-images` | false | Embed base64 data-URI images (increases request size) |

### Examples

```sh
# Authenticate once
eval "$(knoter-auth --login-hint you@company.com)"

# Upload (new page with timestamp title)
knoter upload --notebook "Lab Notes" --section "Results" analysis.html

# Upload with a fixed page title
knoter upload --notebook "Lab Notes" --section "Results" \
    --page "Glycan Analysis" analysis.html

# Replace an existing page
knoter upload --notebook "Lab Notes" --section "Results" \
    --page "Weekly Report" --update replace weekly.html

# Attach PDF figures (picked up automatically from <object> tags in the HTML,
# or passed explicitly)
knoter upload --notebook "Lab Notes" --section "Results" \
    --attach figures/fig1.pdf,figures/fig2.pdf analysis.html

# SharePoint notebook
eval "$(knoter-auth --sharepoint https://contoso.sharepoint.com/sites/lab)"
knoter upload --sharepoint https://contoso.sharepoint.com/sites/lab \
    --notebook "Lab Notes" --section "Results" analysis.html
```

---

## How it handles HTML

1. Strips `<style>`, `<script>`, and `<link>` tags (OneNote ignores them).
2. Rewrites `<img src="path/to/file.png">` → `<img src="name:partNNN">` and packs the image as a binary multipart part.
3. Rewrites `<object data="path/to/file.pdf">` → `<object data="name:partNNN">` and packs the file as a binary multipart part.
4. Base64 data-URI images (`<img src="data:...">`) are stripped by default to keep the request small; use `--embed-images` to attach them instead.
5. Posts a `multipart/form-data` body to the OneNote Pages API.

---

## Token notes

- The device-code flow requires no redirect URI or client secret — it works
  with the default public client registration.
- The cached token auto-refreshes from the stored refresh token; re-login is
  only needed when the refresh token expires (~90 days of inactivity).
- To use your own Azure app registration pass `--client-id <your-id>` to
  `knoter-auth`.
