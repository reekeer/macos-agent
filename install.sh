#!/bin/sh
set -eu

PANEL=""
TOKEN=""
while [ $# -gt 0 ]; do
  case "$1" in
    --panel) PANEL="$2"; shift 2 ;;
    --token) TOKEN="$2"; shift 2 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done
if [ -z "$PANEL" ] || [ -z "$TOKEN" ]; then
  echo "usage: install.sh --panel URL --token TOKEN" >&2
  exit 2
fi
if [ "$(uname -s)" != Darwin ] || [ "$(uname -m)" != arm64 ]; then
  echo "an Apple Silicon Mac is required" >&2
  exit 1
fi

URL="https://github.com/reekeer/macos-agent/releases/latest/download"
BASE="$HOME/Library/Application Support/reekeer-agent"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

curl -fsSL -o "$TMP/reekeer-agent-darwin-arm64" "$URL/reekeer-agent-darwin-arm64"
curl -fsSL -o "$TMP/checksums.txt" "$URL/checksums.txt"
(cd "$TMP" && grep ' reekeer-agent-darwin-arm64$' checksums.txt | shasum -a 256 -c -)

mkdir -p "$BASE"
install -m 0755 "$TMP/reekeer-agent-darwin-arm64" "$BASE/reekeer-agent"
"$BASE/reekeer-agent" install --panel "$PANEL" --token "$TOKEN"
