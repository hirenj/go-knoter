# List available recipes
default:
    @just --list

# ── Build ──────────────────────────────────────────────────────────────────────

# Build knoter and knoter-auth for the current platform
build:
    make build

# Cross-compile + package release archives into dist/
dist:
    make dist-archives

# ── Version ───────────────────────────────────────────────────────────────────

# Show the current version (latest git tag)
version:
    @git describe --tags --always 2>/dev/null || echo "no tags yet"

# List all release tags
tags:
    @git tag --sort=-version:refname | grep -E '^v[0-9]' || echo "no tags yet"

# ── Tagging & release ─────────────────────────────────────────────────────────

# Tag and push a specific version  e.g. `just tag v1.2.3`
tag VERSION:
    #!/usr/bin/env bash
    set -euo pipefail
    if [[ "{{ VERSION }}" != v* ]]; then
        echo "error: version must start with 'v' (e.g. v1.2.3)" >&2
        exit 1
    fi
    if git rev-parse "{{ VERSION }}" >/dev/null 2>&1; then
        echo "error: tag {{ VERSION }} already exists" >&2
        exit 1
    fi
    git tag -a "{{ VERSION }}" -m "Release {{ VERSION }}"
    echo "Created tag {{ VERSION }}"
    echo "Push with:  just push-tag {{ VERSION }}"

# Push a tag to GitHub (triggers the release workflow)
push-tag VERSION:
    git push origin "{{ VERSION }}"
    @echo "Pushed {{ VERSION }} — GitHub Actions will build and publish the release."

# Bump patch version, tag, and push  (e.g. v1.2.3 → v1.2.4)
release-patch:
    #!/usr/bin/env bash
    set -euo pipefail
    current=$(git describe --tags --abbrev=0 2>/dev/null || echo "v0.0.0")
    next=$(just _bump "$current" patch)
    echo "Bumping $current → $next"
    just tag "$next"
    just push-tag "$next"

# Bump minor version, tag, and push  (e.g. v1.2.3 → v1.3.0)
release-minor:
    #!/usr/bin/env bash
    set -euo pipefail
    current=$(git describe --tags --abbrev=0 2>/dev/null || echo "v0.0.0")
    next=$(just _bump "$current" minor)
    echo "Bumping $current → $next"
    just tag "$next"
    just push-tag "$next"

# Bump major version, tag, and push  (e.g. v1.2.3 → v2.0.0)
release-major:
    #!/usr/bin/env bash
    set -euo pipefail
    current=$(git describe --tags --abbrev=0 2>/dev/null || echo "v0.0.0")
    next=$(just _bump "$current" major)
    echo "Bumping $current → $next"
    just tag "$next"
    just push-tag "$next"

# Delete a tag locally and on origin  e.g. `just delete-tag v1.2.3`
delete-tag VERSION:
    #!/usr/bin/env bash
    set -euo pipefail
    read -r -p "Delete tag {{ VERSION }} locally and on origin? [y/N] " confirm
    if [[ "$confirm" != [yY] ]]; then exit 0; fi
    git tag -d "{{ VERSION }}"
    git push origin ":refs/tags/{{ VERSION }}"
    echo "Deleted {{ VERSION }}"

# ── Internal helpers ──────────────────────────────────────────────────────────

# (internal) Compute next version given a bump level
_bump VERSION LEVEL:
    #!/usr/bin/env bash
    set -euo pipefail
    v="{{ VERSION }}"
    v="${v#v}"           # strip leading v
    major="${v%%.*}"; rest="${v#*.}"
    minor="${rest%%.*}"; patch="${rest#*.}"
    case "{{ LEVEL }}" in
        patch) patch=$((patch+1)) ;;
        minor) minor=$((minor+1)); patch=0 ;;
        major) major=$((major+1)); minor=0; patch=0 ;;
    esac
    echo "v${major}.${minor}.${patch}"
