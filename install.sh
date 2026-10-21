#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
BIN_DIR="${HOME}/.local/bin"
mkdir -p "$BIN_DIR"
echo "Building maw (CGO_ENABLED=0 single binary)..."
rm -f "$BIN_DIR/maw"
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$BIN_DIR/maw" "$ROOT/cmd/maw"
echo "Installed: $BIN_DIR/maw"
echo "Ensure $BIN_DIR is on PATH"
"$BIN_DIR/maw" --version
