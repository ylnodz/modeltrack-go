#!/bin/bash
# ╔════════════════════════════════════════════════════════════════════╗
# ║  ModelTrack Go — Wispbyte startup script                           ║
# ║  No toolchain needed: pulls the prebuilt static binary from the    ║
# ║  latest GitHub release and runs it. Falls back to building from    ║
# ║  source if a Go toolchain happens to be present.                   ║
# ╚════════════════════════════════════════════════════════════════════╝
set -euo pipefail

REPO="ylnodz/modeltrack-go"
ASSET="modeltrack-linux-amd64"
HOME_DIR="/home/container"
BIN_DIR="$HOME_DIR/bin"
BIN="$BIN_DIR/modeltrack"
VERSION_FILE="$BIN_DIR/.version"
BASE_URL="https://github.com/$REPO/releases/latest/download"

cd "$HOME_DIR"

# ── .env (provider keys live here or in the panel env vars) ───────────
if [ -f "$HOME_DIR/.env" ]; then
  set -a
  # shellcheck disable=SC1091
  . "$HOME_DIR/.env"
  set +a
else
  echo "!! No .env found. Set DISCORD_BOT_TOKEN and DISCORD_CHANNEL_ID in the"
  echo "!! Wispbyte panel environment variables (or create $HOME_DIR/.env)."
fi

if [ -z "${DISCORD_BOT_TOKEN:-}" ] || [ -z "${DISCORD_CHANNEL_ID:-}" ]; then
  echo "!! DISCORD_BOT_TOKEN and DISCORD_CHANNEL_ID are required. Exiting."
  exit 1
fi

# ── fetch latest release version (one small API call) ─────────────────
LATEST=""
if command -v curl >/dev/null 2>&1; then
  LATEST=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null \
    | grep -m1 '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/') || true
fi

NEED_DOWNLOAD=1
if [ -x "$BIN" ] && [ -f "$VERSION_FILE" ] && [ -n "$LATEST" ]; then
  [ "$(cat "$VERSION_FILE" 2>/dev/null)" = "$LATEST" ] && NEED_DOWNLOAD=0
fi

mkdir -p "$BIN_DIR"

# ── download prebuilt static binary ───────────────────────────────────
if [ "$NEED_DOWNLOAD" -eq 1 ]; then
  echo ">> Downloading $ASSET ${LATEST:+($LATEST)} ..."
  if curl -fsSL --retry 3 --retry-delay 2 -o "$BIN.tmp" "$BASE_URL/$ASSET"; then
    if curl -fsSL --retry 3 -o "$BIN.tmp.sha256" "$BASE_URL/$ASSET.sha256"; then
      EXPECTED=$(awk '{print $1}' "$BIN.tmp.sha256")
      ACTUAL=$(sha256sum "$BIN.tmp" | awk '{print $1}')
      if [ "$EXPECTED" != "$ACTUAL" ]; then
        echo "!! Checksum mismatch (expected $EXPECTED, got $ACTUAL). Aborting."
        rm -f "$BIN.tmp" "$BIN.tmp.sha256"
        exit 1
      fi
      echo ">> Checksum OK"
    else
      echo ">> (no .sha256 published — skipping verification)"
    fi
    chmod +x "$BIN.tmp"
    mv "$BIN.tmp" "$BIN"
    [ -n "$LATEST" ] && echo "$LATEST" > "$VERSION_FILE"
    rm -f "$BIN.tmp.sha256"
    echo ">> Installed $BIN"
  else
    # ── fallback: build from source (needs Go) ────────────────────────
    echo ">> Release download failed — trying source build."
    if ! command -v go >/dev/null 2>&1; then
      echo "!! No Go toolchain and no downloadable release. Cannot continue."
      exit 1
    fi
    echo "!! This image has no Go compiler and the release download failed."
    echo "!! Check that a release exists: https://github.com/$REPO/releases"
    exit 1
  fi
fi

echo ">> Using $BIN"
"$BIN" --version 2>/dev/null || true

# ── run ───────────────────────────────────────────────────────────────
exec "$BIN"
