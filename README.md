# knoter-go + knoter-py

A rewrite/extension of [knoter](https://github.com/CopenhagenCenterForGlycomics/knoter):

| Component | What it does |
|---|---|
| `knoter-go` | Go binary that uploads an HTML file (+ attachments) to OneNote |
| `knoter-py/knoter.py` | Single-import Python library for jupytext notebooks |
| `Makefile` | Ties jupytext → nbconvert → knoter upload together |

---

## knoter-go — OneNote uploader

### Build

```sh
cd knoter-go
go build -o knoter ./cmd/knoter
# or cross-compile:
GOOS=linux GOARCH=amd64 go build -o knoter-linux ./cmd/knoter
GOOS=darwin GOARCH=arm64 go build -o knoter-macos ./cmd/knoter
```

### First run — authentication

On first use, knoter opens the Microsoft device-code login flow:

```
$ knoter upload --notebook "Lab Notes" --section "2024" report.html

To sign in, use a web browser to open https://microsoft.com/devicelogin
and enter the code ABCD-1234 to authenticate.
```

The token is cached in `~/.config/knoter/token.json` (Linux/macOS) or
`%APPDATA%\knoter\token.json` (Windows) — subsequent calls are silent.

To log out:
```sh
knoter logout
```

### Usage

```
knoter upload [flags] <report.html> [extra-attachment ...]

Flags:
  --notebook   <name>     OneNote notebook name          (required)
  --section    <name>     OneNote section name           (required)
  --page       <title>    Page title (default: filename stem)
  --update     replace|append   Update existing page
  --attach     file,file,...    Extra attachments (PDF, xlsx, …)
  --client-id  <id>       Azure app client ID (override default)
```

**Create a new page:**
```sh
knoter upload --notebook "Lab Notes" --section "Results" analysis.html
```

**Attach the PDF figures:**
```sh
knoter upload --notebook "Lab Notes" --section "Results" \
    analysis.html --attach figures/fig_001.pdf,figures/fig_002.pdf
```

**Replace an existing page:**
```sh
knoter upload --notebook "Lab Notes" --section "Results" \
    --page "Weekly Report" --update replace weekly.html
```

### How it handles HTML

The Go uploader:
1. Strips `<style>`, `<script>`, `<link>` tags (OneNote ignores them).
2. Finds every `<img src="path/to/file.png">` and rewrites it as
   `<img src="name:partNNN">`, packing the PNG as a multipart attachment.
3. Appends `<object data-attachment>` tags for non-image attachments (PDF, xlsx).
4. Posts a `multipart/form-data` body to the OneNote Pages API.

---

## knoter-py — Python notebook helper

### Installation

Copy `knoter.py` onto your `PYTHONPATH`, or into the same directory as
your notebooks:

```sh
cp knoter-py/knoter.py ~/notebooks/
# or
pip install -e knoter-py/   # if you add a pyproject.toml
```

### Using it in a notebook

Add **one cell** at the top of any jupytext-managed Python notebook:

```python
import knoter
knoter.setup()
```

That's it.  Every subsequent cell that produces matplotlib figures will have
them automatically saved as both PDF and PNG in the `figures/` directory.

### Figure file naming

| Cell | Figures produced | File names |
|------|-----------------|------------|
| cell 2 | 1 figure | `chunk_002_fig_1.pdf`, `chunk_002_fig_1.png` |
| cell 3 | 3 figures | `chunk_003_fig_1.*`, `chunk_003_fig_2.*`, `chunk_003_fig_3.*` |
| cell with `knoter.label("foo")` | 1 figure | `foo_fig_1.pdf`, `foo_fig_1.png` |

### Per-cell controls

```python
# Change figure size for this cell only
knoter.figsize(12, 4)

# Give this cell's figures a meaningful name
knoter.label("glycan_composition")

# Revert to defaults mid-cell (rarely needed)
knoter.reset_figsize()
```

### setup() options

```python
knoter.setup(
    fig_dir="figures",      # output directory
    dpi=150,                # PNG DPI
    default_width=8,        # inches
    default_height=5,       # inches
    pdf=True,               # also save PDF
    close_after_save=True,  # call plt.close() after each figure
)
```

---

## Full workflow

```sh
# 1. Edit your jupytext .py notebook
vim analysis.py

# 2. Render + upload in one step
make upload \
    NOTEBOOK=analysis.py \
    NOTEBOOK_NAME="Lab Notes" \
    SECTION="2024" \
    PAGE="Glycan Analysis v3" \
    ATTACH_PDFS=1

# Or step by step:
make render NOTEBOOK=analysis.py          # → analysis.html + figures/
knoter upload \
    --notebook "Lab Notes" \
    --section "2024" \
    --attach "$(ls figures/*.pdf | tr '\n' ',')" \
    analysis.html
```

### Rmd / Rhtml support

The Makefile also handles R Markdown files via `rmarkdown::render()`.
The knoter R package can still be used for the upload step, or you can
use the Go binary once the HTML is rendered.

---

## Architecture

```
jupytext .py  ──jupytext──▶  .ipynb
                                │
                          nbconvert --execute
                                │
                           analysis.html
                           figures/
                             chunk_002_fig_1.png   ◀── knoter.py hook
                             chunk_002_fig_1.pdf
                             ...
                                │
                           knoter upload (Go)
                                │
                        ┌───────▼────────┐
                        │  OneNote API   │
                        │  (Graph v1)    │
                        └───────────────-┘
                         • page HTML (multipart)
                         • PNG images (inline)
                         • PDF attachments
```

---

## Token / credential notes

- The device-code flow requires no redirect URI or client secret — it works
  with the default public client registration.
- To use your own Azure app registration, pass `--client-id <your-id>` or
  set `KNOTER_CLIENT_ID=<your-id>` in the environment.
- The cached token auto-refreshes using the stored refresh token; re-login
  is only needed when the refresh token expires (~90 days of inactivity).
