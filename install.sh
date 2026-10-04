#!/bin/sh
# jevx installer (macOS / Linux): downloads the release binary, then `jevx install` puts the agent skill in
# ~/.claude/skills, ~/.agents/skills (and ~/.codex/skills if present) and adds the Claude Code hook template (disabled).
#   curl -fsSL https://muthuishere.github.io/jevx/install.sh | sh
#   JEVX_VERSION=v0.1.0 JEVX_BIN=~/.local/bin sh install.sh     # pin a version / choose the dir
#   JEVX_NO_HOOK=1 ...                                            # skills only
set -eu
REPO=muthuishere/jevx
BIN=${JEVX_BIN:-$HOME/.local/bin}
VER=${JEVX_VERSION:-latest}
os=$(uname -s | tr '[:upper:]' '[:lower:]')
case $(uname -m) in x86_64|amd64) arch=amd64 ;; arm64|aarch64) arch=arm64 ;; *) echo "jevx: unsupported arch $(uname -m)" >&2; exit 1 ;; esac
case $os in darwin|linux) ;; *) echo "jevx: unsupported OS $os (Windows: install.cmd)" >&2; exit 1 ;; esac
asset=jevx_${os}_${arch}
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
if [ "$VER" = latest ]; then url=https://github.com/$REPO/releases/latest/download/$asset
else url=https://github.com/$REPO/releases/download/$VER/$asset; fi
echo "jevx: downloading $asset ($VER)"
if ! curl -fsSL "$url" -o "$tmp/jevx"; then
  # a private repo needs an authenticated download
  command -v gh >/dev/null || { echo "jevx: download failed: $url" >&2; exit 1; }
  if [ "$VER" = latest ]; then gh release download -R $REPO -p "$asset" -D "$tmp"; else gh release download "$VER" -R $REPO -p "$asset" -D "$tmp"; fi
  mv "$tmp/$asset" "$tmp/jevx"
fi
chmod +x "$tmp/jevx"
mkdir -p "$BIN"; mv "$tmp/jevx" "$BIN/jevx"
echo "jevx: installed $("$BIN/jevx" version) to $BIN/jevx"
if [ -n "${JEVX_NO_HOOK:-}" ]; then "$BIN/jevx" install --skills; else "$BIN/jevx" install; fi
case ":$PATH:" in *":$BIN:"*) ;; *) echo "jevx: add $BIN to your PATH" ;; esac
echo "jevx: next: export TYPESAFE_API_KEY=...   (your key from typesafe.ai; hosted Jev needs nothing else)"
echo "      then:  echo \"Prod is down\" | jevx is \"Is this urgent?\"     own endpoint: jevx profile add NAME URL --model M"
